package main

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/patrickyoung/bench/internal/contractexec"
	"github.com/patrickyoung/bench/internal/plyexec"
	"github.com/patrickyoung/bench/internal/session"
	"github.com/patrickyoung/bench/internal/suite"
)

func TestHeadlessRunCompilesIntentBeforeWorkByDefault(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("test fixtures are POSIX programs")
	}
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "poem.md"), []byte("verse"), 0o644); err != nil {
		t.Fatal(err)
	}
	ask := filepath.Join(dir, "fake-ask")
	ply := filepath.Join(dir, "fake-ply")
	capture := filepath.Join(dir, "capture")
	t.Setenv("BENCH_ASK", ask)
	t.Setenv("BENCH_PLY", ply)
	t.Setenv("CAPTURE", capture)
	askScript := `#!/bin/sh
set -eu
if [ "${1-}" = note ]; then
  n=1
  if [ -f "$CAPTURE.record.count" ]; then n=$(( $(cat "$CAPTURE.record.count") + 1 )); fi
  printf '%s' "$n" > "$CAPTURE.record.count"
  printf '%s\n' "$@" > "$CAPTURE.record.$n.args"
  cat > "$CAPTURE.record.$n.stdin"
  exit 0
fi
printf '%s\n' "$@" > "$CAPTURE.ask.args"
cat > "$CAPTURE.ask.stdin"
printf '%s' "$CONTRACT_FIXTURE"
`
	plyScript := `#!/bin/sh
set -eu
printf '%s\n' "$@" > "$CAPTURE.ply.args"
session_out=
session=
while [ "$#" -gt 0 ]; do
  case "$1" in
    -session-out) session_out=$2; shift 2 ;;
    -f) session=$2; shift 2 ;;
    *) shift ;;
  esac
done
printf '%s\n' "$session" > "$session_out"
printf 'worked answer\n'
`
	for path, body := range map[string]string{ask: askScript, ply: plyScript} {
		if err := os.WriteFile(path, []byte(body), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("CONTRACT_FIXTURE", `{"version":2,"outcome":"A complete poem gallery exists.","deliverables":["gallery.html"],"invariants":["poem.md remains unchanged"],"criteria":[{"id":"exists","requirement":"gallery exists","evidence":"the configured check exits zero","judge":"check"}],"approvals":[],"assumptions":[],"open_questions":[],"limits":[]}`)
	var stdout, stderr strings.Builder
	session := filepath.Join(dir, "sessions", "run.jsonl")
	code := run([]string{"run", "-C", dir, "-f", session, "-m", "openai/luna", "-check", "test -s gallery.html", "make art"}, strings.NewReader("source evidence"), &stdout, &stderr)
	if code != 2 || stdout.String() != "worked answer\n" || !strings.Contains(stderr.String(), "Ready for review") {
		t.Fatalf("code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
	if !strings.Contains(stderr.String(), "OUTCOME CONTRACT v2") {
		t.Fatalf("contract was not visible on stderr:\n%s", stderr.String())
	}
	askArgs := string(mustRead(t, capture+".ask.args"))
	for _, want := range []string{"-S\n" + contractexec.System + "\n", "-schema\n", "--\nCompile this user intent"} {
		if !strings.Contains(askArgs, want) {
			t.Errorf("ask args missing %q:\n%s", want, askArgs)
		}
	}
	askInput := string(mustRead(t, capture+".ask.stdin"))
	if !strings.Contains(askInput, "poem.md") || !strings.Contains(askInput, "source evidence") {
		t.Fatalf("contract evidence:\n%s", askInput)
	}
	recordArgs := string(mustRead(t, capture+".record.1.args"))
	if !strings.Contains(recordArgs, "-k\nbench.contract/v2\n") || !strings.Contains(recordArgs, "-seal\n") {
		t.Fatalf("contract was not sealed: %s", recordArgs)
	}
	if record := string(mustRead(t, capture+".record.1.stdin")); !strings.Contains(record, `"status":"compiled"`) {
		t.Fatalf("compiled contract record: %s", record)
	}
	if result := string(mustRead(t, capture+".record.2.stdin")); !strings.Contains(result, `"status":"review_required"`) || !strings.Contains(result, `"proposed_check_coverage":["exists"]`) || strings.Contains(result, `"status":"complete"`) {
		t.Fatalf("contract result record: %s", result)
	}
	plyArgs := string(mustRead(t, capture+".ply.args"))
	for _, want := range []string{"-f\n" + session + "\n", "-contract-id\nsha256:", "ORIGINAL INTENT\nmake art", "OUTCOME CONTRACT v2", "sha256"} {
		if !strings.Contains(plyArgs, want) {
			t.Errorf("ply args missing %q:\n%s", want, plyArgs)
		}
	}
}

func TestHeadlessContractReviewPreservesReportAndReturnsNotDone(t *testing.T) {
	events := make(chan plyexec.Event, 2)
	events <- plyexec.Event{Stream: plyexec.Stdout, Text: "worker report\n"}
	events <- plyexec.Event{
		Done: true, ExitCode: 2, Stream: plyexec.Stderr,
		Text: "Ready for review · configured check passed 1/3 criteria · 2 require inspection/human review · session is replayable\n",
		ContractResult: &plyexec.ContractResult{
			Status: "review_required", CheckConfigured: true, CheckPassed: true, ProposedCheckCoverage: []string{"exists"},
			Outstanding: []plyexec.ContractCriterion{{ID: "layout", Judge: "inspection"}, {ID: "quality", Judge: "human"}},
		},
	}
	close(events)
	var stdout, stderr strings.Builder
	code := streamPlyEvents(context.Background(), events, &stdout, &stderr)
	if code != 2 || stdout.String() != "worker report\n" || !strings.Contains(stderr.String(), "Ready for review") || strings.Contains(strings.ToLower(stderr.String()), "outcome complete") {
		t.Fatalf("code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
}

func TestSuiteToolUsesOnlyACompleteMarkedBundle(t *testing.T) {
	root := t.TempDir()
	bin := filepath.Join(root, "bin")
	if err := os.Mkdir(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	ask := filepath.Join(bin, "ask")
	if err := os.WriteFile(ask, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if got := suiteTool(bin, "ask"); got != "" {
		t.Fatalf("unmarked suite resolved %q", got)
	}
	m, err := suite.Current()
	if err != nil {
		t.Fatal(err)
	}
	data, err := suite.JSON(m)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "suite.json"), data, 0o644); err != nil {
		t.Fatal(err)
	}
	if got := suiteTool(bin, "ask"); got != ask {
		t.Fatalf("suite ask = %q, want %q", got, ask)
	}
	if got := suiteTool(bin, "missing"); got != "" {
		t.Fatalf("missing tool resolved %q", got)
	}
}

func TestVersionMatchesSuiteVersion(t *testing.T) {
	m, err := suite.Current()
	if err != nil {
		t.Fatal(err)
	}
	if version != m.Version {
		t.Fatalf("bench version %q does not match suite %q", version, m.Version)
	}
}

func TestHeadlessRunPreservesUnixStreamsAndArguments(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("test fixture is a POSIX program")
	}
	dir := t.TempDir()
	fixture := filepath.Join(dir, "fake-ply")
	capture := filepath.Join(dir, "capture")
	script := `#!/bin/sh
set -eu
printf '%s\n' "$@" > "$CAPTURE.args"
pwd > "$CAPTURE.cwd"
cat > "$CAPTURE.stdin"
printf 'tool transcript\n' >&2
printf 'answer only\n'
`
	if err := os.WriteFile(fixture, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("BENCH_PLY", fixture)
	t.Setenv("BENCH_ASK", "/opt/bin/ask")
	t.Setenv("BENCH_BRIEF", "/opt/bin/brief")
	t.Setenv("BENCH_TOOLS", "")
	t.Setenv("CAPTURE", capture)
	var stdout, stderr strings.Builder
	session := filepath.Join(dir, "sessions", "work.jsonl")
	code := run([]string{"run", "-contract=false", "-C", dir, "-f", session, "-m", "openai/test", "-s", "go-review", "inspect; $(literal)"},
		strings.NewReader("piped evidence\n"), &stdout, &stderr)
	if code != 0 || stdout.String() != "answer only\n" || stderr.String() != "tool transcript\n" {
		t.Fatalf("code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
	args, err := os.ReadFile(capture + ".args")
	if err != nil {
		t.Fatal(err)
	}
	want := strings.Join([]string{"-sh", "-C", dir, "-f", session, "-m", "openai/test", "-s", "go-review", "--", "inspect; $(literal)", ""}, "\n")
	if string(args) != want {
		t.Fatalf("args=%q, want %q", args, want)
	}
	input, err := os.ReadFile(capture + ".stdin")
	if err != nil || string(input) != "piped evidence\n" {
		t.Fatalf("stdin=%q err=%v", input, err)
	}
	cwd, err := os.ReadFile(capture + ".cwd")
	gotCWD, resolveErr := filepath.EvalSymlinks(strings.TrimSpace(string(cwd)))
	wantCWD, wantErr := filepath.EvalSymlinks(dir)
	if err != nil || resolveErr != nil || wantErr != nil || gotCWD != wantCWD {
		t.Fatalf("cwd=%q err=%v", cwd, err)
	}
}

func TestHeadlessRunRoutesFreshSubagentSessionsBesideBenchEvidence(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("test fixture is a POSIX program")
	}
	dir := t.TempDir()
	fixture := filepath.Join(dir, "fake-ply")
	capture := filepath.Join(dir, "subagent-env")
	script := `#!/bin/sh
set -eu
printf '%s\n%s\n' "${PLY_DIR-}" "${ASK_MODEL-}" > "$CAPTURE"
printf answer
`
	if err := os.WriteFile(fixture, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("BENCH_PLY", fixture)
	t.Setenv("BENCH_DIR", filepath.Join(dir, "bench-data"))
	t.Setenv("CAPTURE", capture)
	parent := filepath.Join(dir, "elsewhere", "parent.jsonl")
	var stdout, stderr strings.Builder
	code := run([]string{"run", "-contract=false", "-C", dir, "-f", parent, "-m", "openai/selected", "delegate it"}, strings.NewReader(""), &stdout, &stderr)
	if code != 0 || stdout.String() != "answer" || stderr.Len() != 0 {
		t.Fatalf("code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
	wantDir := session.SubagentsDir(filepath.Join(dir, "bench-data"), parent)
	if got := strings.Split(strings.TrimSpace(string(mustRead(t, capture))), "\n"); len(got) != 2 || got[0] != wantDir || got[1] != "openai/selected" {
		t.Fatalf("delegation env=%q, want %q and selected model", got, wantDir)
	}
	if _, err := os.Stat(wantDir); !os.IsNotExist(err) {
		t.Fatalf("subagent directory was created without delegation: %v", err)
	}
}

func mustRead(t *testing.T, path string) []byte {
	t.Helper()
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return body
}

func TestHeadlessAskUsesTheSameModelSessionAndPipeContract(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("test fixture is a POSIX program")
	}
	dir := t.TempDir()
	fixture := filepath.Join(dir, "fake-ask")
	capture := filepath.Join(dir, "ask")
	script := `#!/bin/sh
set -eu
printf '%s\n' "$@" > "$CAPTURE.args"
cat > "$CAPTURE.stdin"
printf thinking >&2
printf explained
`
	if err := os.WriteFile(fixture, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("BENCH_ASK", fixture)
	t.Setenv("CAPTURE", capture)
	var stdout, stderr strings.Builder
	session := filepath.Join(dir, "ask.jsonl")
	code := run([]string{"ask", "-C", dir, "-f", session, "-m", "anthropic/test", "explain"},
		strings.NewReader("evidence"), &stdout, &stderr)
	if code != 0 || stdout.String() != "explained" || stderr.String() != "thinking" {
		t.Fatalf("code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
	args, err := os.ReadFile(capture + ".args")
	if err != nil {
		t.Fatal(err)
	}
	want := strings.Join([]string{"-f", session, "-m", "anthropic/test", "--", "explain", ""}, "\n")
	if string(args) != want {
		t.Fatalf("args=%q, want %q", args, want)
	}
}

func TestHeadlessExitStatusPassesThrough(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("test fixture is a POSIX program")
	}
	dir := t.TempDir()
	fixture := filepath.Join(dir, "fake-ply")
	if err := os.WriteFile(fixture, []byte("#!/bin/sh\nprintf 'not done\\n' >&2\nexit 2\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("BENCH_PLY", fixture)
	var stdout, stderr strings.Builder
	code := run([]string{"run", "-contract=false", "-C", dir, "unfinished"}, strings.NewReader(""), &stdout, &stderr)
	if code != 2 || stdout.Len() != 0 || stderr.String() != "not done\n" {
		t.Fatalf("code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
}

func TestHeadlessRunPassesOnlyExplicitPlyPolicyFlags(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("test fixture is a POSIX program")
	}
	dir := t.TempDir()
	fixture := filepath.Join(dir, "fake-ply")
	capture := filepath.Join(dir, "args")
	script := `#!/bin/sh
set -eu
printf '%s\n' "$@" > "$CAPTURE"
session_out=
session=
while [ "$#" -gt 0 ]; do
  case "$1" in
    -session-out) session_out=$2; shift 2 ;;
    -f) session=$2; shift 2 ;;
    *) shift ;;
  esac
done
printf '%s\n' "$session" > "$session_out"
printf checked-answer
`
	if err := os.WriteFile(fixture, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("BENCH_PLY", fixture)
	t.Setenv("CAPTURE", capture)
	check := `go test ./...; printf '$(literal)'`
	var stdout, stderr strings.Builder
	code := run([]string{
		"run", "-contract=false", "-C", dir, "-check", check, "-effort", "xhigh", "-cycles", "0", "-turns", "9",
		"-timeout", "35s", "-compact", "-compactions", "2", "finish it",
	}, strings.NewReader(""), &stdout, &stderr)
	if code != 0 || stdout.String() != "checked-answer" || stderr.Len() != 0 {
		t.Fatalf("code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
	args, err := os.ReadFile(capture)
	if err != nil {
		t.Fatal(err)
	}
	got := string(args)
	for _, want := range []string{
		"-check\n" + check + "\n", "-effort\nxhigh\n", "-cycles\n0\n", "-turns\n9\n",
		"-timeout\n35s\n", "-compact\n", "-compactions\n2\n",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("args missing %q:\n%s", want, got)
		}
	}
}

func TestHeadlessPolicyFlagsRejectInvalidValuesBeforePlyStarts(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("test fixture is a POSIX program")
	}
	dir := t.TempDir()
	fixture := filepath.Join(dir, "fake-ply")
	started := filepath.Join(dir, "started")
	if err := os.WriteFile(fixture, []byte("#!/bin/sh\ntouch \"$STARTED\"\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("BENCH_PLY", fixture)
	t.Setenv("STARTED", started)
	for _, flagAndValue := range [][2]string{{"-turns", "-1"}, {"-cycles", "-1"}, {"-compactions", "-1"}, {"-timeout", "0s"}} {
		var stdout, stderr strings.Builder
		code := run([]string{"run", "-C", dir, flagAndValue[0], flagAndValue[1], "goal"}, strings.NewReader(""), &stdout, &stderr)
		if code != 2 || stderr.Len() == 0 {
			t.Fatalf("%v: code=%d stderr=%q", flagAndValue, code, stderr.String())
		}
	}
	for _, args := range [][]string{
		{"run", "-C", dir, "-check-all", "goal"},
		{"run", "-C", dir, "-contract=false", "-check", "true", "-check-all", "goal"},
	} {
		var stdout, stderr strings.Builder
		code := run(args, strings.NewReader(""), &stdout, &stderr)
		if code != 2 || !strings.Contains(stderr.String(), "check-all needs") {
			t.Fatalf("%v: code=%d stderr=%q", args, code, stderr.String())
		}
	}
	if _, err := os.Stat(started); !os.IsNotExist(err) {
		t.Fatalf("Ply started for invalid policy: %v", err)
	}
}

func TestPlainBenchAutomaticallyComposesWhenPiped(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("test fixture is a POSIX program")
	}
	dir := t.TempDir()
	fixture := filepath.Join(dir, "fake-ply")
	capture := filepath.Join(dir, "stdin")
	script := "#!/bin/sh\ncat > \"$CAPTURE\"\nprintf piped-answer\n"
	if err := os.WriteFile(fixture, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("BENCH_PLY", fixture)
	t.Setenv("CAPTURE", capture)
	var stdout, stderr strings.Builder
	code := run([]string{"-contract=false", "-C", dir, "review this"}, strings.NewReader("diff bytes"), &stdout, &stderr)
	if code != 0 || stdout.String() != "piped-answer" || stderr.Len() != 0 {
		t.Fatalf("code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
	input, err := os.ReadFile(capture)
	if err != nil || string(input) != "diff bytes" {
		t.Fatalf("stdin=%q err=%v", input, err)
	}
}

func TestCLIHelpAndUsageErrorsAreConventional(t *testing.T) {
	var stdout, stderr strings.Builder
	if code := run([]string{"help"}, strings.NewReader(""), &stdout, &stderr); code != 0 || !strings.Contains(stdout.String(), "bench run") || stderr.Len() != 0 {
		t.Fatalf("help code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
	stdout.Reset()
	stderr.Reset()
	if code := run([]string{"run", "-h"}, strings.NewReader(""), &stdout, &stderr); code != 0 || !strings.Contains(stderr.String(), "usage: bench run") {
		t.Fatalf("run help code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
	stdout.Reset()
	stderr.Reset()
	if code := run([]string{"run"}, strings.NewReader("evidence"), &stdout, &stderr); code != 2 || !strings.Contains(stderr.String(), "goal is required") {
		t.Fatalf("usage code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
	stdout.Reset()
	stderr.Reset()
	if code := run([]string{"run", "-t", "tools", "-sh", "goal"}, strings.NewReader(""), &stdout, &stderr); code != 2 || !strings.Contains(stderr.String(), "mutually exclusive") {
		t.Fatalf("conflict code=%d stderr=%q", code, stderr.String())
	}
	stdout.Reset()
	stderr.Reset()
	if code := run([]string{"run", "-t", "", "goal"}, strings.NewReader(""), &stdout, &stderr); code != 2 || !strings.Contains(stderr.String(), "toolbox directory is empty") {
		t.Fatalf("empty toolbox code=%d stderr=%q", code, stderr.String())
	}
	stdout.Reset()
	stderr.Reset()
	if code := run([]string{"-version"}, strings.NewReader(""), &stdout, &stderr); code != 0 || strings.TrimSpace(stdout.String()) != "bench "+version || stderr.Len() != 0 {
		t.Fatalf("version code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
}

func TestPipeInputLimitFailsBeforeStartingAFilter(t *testing.T) {
	var stdout, stderr strings.Builder
	tooLarge := strings.NewReader(strings.Repeat("x", maxPipeInput+1))
	code := run([]string{"ask", "message"}, tooLarge, &stdout, &stderr)
	if code != 1 || !strings.Contains(stderr.String(), "stdin exceeds 16 MB") {
		t.Fatalf("code=%d stderr=%q", code, stderr.String())
	}
}

func TestWorkspaceMustExistAndBeADirectory(t *testing.T) {
	var stdout, stderr strings.Builder
	missing := filepath.Join(t.TempDir(), "missing")
	code := run([]string{"run", "-C", missing, "goal"}, strings.NewReader(""), &stdout, &stderr)
	if code != 1 || !strings.Contains(stderr.String(), "workspace") || !strings.Contains(stderr.String(), "no such file") {
		t.Fatalf("missing code=%d stderr=%q", code, stderr.String())
	}

	stdout.Reset()
	stderr.Reset()
	file := filepath.Join(t.TempDir(), "not-a-directory")
	if err := os.WriteFile(file, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	code = run([]string{"ask", "-C", file, "message"}, strings.NewReader(""), &stdout, &stderr)
	if code != 1 || !strings.Contains(stderr.String(), "not a directory") {
		t.Fatalf("file code=%d stderr=%q", code, stderr.String())
	}
}
