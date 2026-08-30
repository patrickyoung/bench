package main

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/patrickyoung/bench/internal/autonomy"
	"github.com/patrickyoung/bench/internal/autoroute"
	"github.com/patrickyoung/bench/internal/contractexec"
	"github.com/patrickyoung/bench/internal/plyexec"
	"github.com/patrickyoung/bench/internal/session"
	"github.com/patrickyoung/bench/internal/suite"
)

func TestHeadlessRunDraftsContractAndDoesNotStartPlyByDefault(t *testing.T) {
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
if [ "${1-}" = replay ]; then
  n=$(cat "$CAPTURE.record.count")
  printf '{"seq":1,"type":"note","data":{"source":"bench-user","kind":"bench.contract/v3","body":'
  cat "$CAPTURE.record.$n.stdin"
  printf '}}\n{"seq":2,"type":"seal","data":{"through":1,"sha256":"sha256:seal"}}\n'
  exit 0
fi
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
n=1
if [ -f "$CAPTURE.ply.count" ]; then n=$(( $(cat "$CAPTURE.ply.count") + 1 )); fi
printf '%s' "$n" > "$CAPTURE.ply.count"
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
	code := run([]string{"run", "-C", dir, "-f", session, "-m", "openai/luna", "-mode", "loop", "-check", "test -s gallery.html", "make art"}, strings.NewReader("source evidence"), &stdout, &stderr)
	if code != 2 || !strings.HasSuffix(strings.TrimSpace(stdout.String()), "draft.json") || !strings.Contains(stderr.String(), "Ply has not started") || !strings.Contains(stderr.String(), "-mode loop") {
		t.Fatalf("code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
	if _, err := os.Stat(strings.TrimSpace(stdout.String())); err != nil {
		t.Fatalf("editable draft was not created: %v", err)
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
	if !strings.Contains(recordArgs, "-k\nbench.contract-proposal/v1\n") || !strings.Contains(recordArgs, "-seal\n") {
		t.Fatalf("contract was not sealed: %s", recordArgs)
	}
	if record := string(mustRead(t, capture+".record.1.stdin")); !strings.Contains(record, `"status":"proposed"`) {
		t.Fatalf("proposed contract record: %s", record)
	}
	if _, err := os.Stat(capture + ".ply.args"); !os.IsNotExist(err) {
		t.Fatalf("Ply ran before contract admission: %v", err)
	}
	stdout.Reset()
	stderr.Reset()
	code = run([]string{"contract", "revise", "-C", dir, "-f", session, "make mobile inspection explicit"}, strings.NewReader(""), &stdout, &stderr)
	if code != 0 || !strings.HasSuffix(strings.TrimSpace(stdout.String()), "draft.json") || !strings.Contains(stderr.String(), "Ply has not started") {
		t.Fatalf("revise code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
	if args := string(mustRead(t, capture+".ask.args")); !strings.Contains(args, "USER CHANGE REQUEST\nmake mobile inspection explicit") {
		t.Fatalf("revision did not use Ask contract turn:\n%s", args)
	}
	if _, err := os.Stat(capture + ".ply.args"); !os.IsNotExist(err) {
		t.Fatalf("Ply ran during contract revision: %v", err)
	}
	stdout.Reset()
	stderr.Reset()
	code = run([]string{"contract", "show", "-C", dir, "-f", session}, strings.NewReader(""), &stdout, &stderr)
	showStore := contractexec.FileStore{Dir: sessionpkgContractDirForTest(dir, session)}
	if code != 0 || stdout.String() != string(mustRead(t, showStore.DraftPath())) || !strings.Contains(stdout.String(), `"workspace": "`+dir+`"`) || !strings.Contains(stdout.String(), `"check": "test -s gallery.html"`) || !strings.Contains(stderr.String(), "generation 2") || !strings.Contains(stderr.String(), "exact draft.json sha256:") {
		t.Fatalf("show code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
	editor := filepath.Join(dir, "fake-editor")
	if err := os.WriteFile(editor, []byte("#!/bin/sh\nprintf '%s' \"$1\" > \"$CAPTURE.editor.path\"\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("EDITOR", editor)
	stdout.Reset()
	stderr.Reset()
	code = run([]string{"contract", "edit", "-C", dir, "-f", session}, strings.NewReader(""), &stdout, &stderr)
	if code != 0 || !strings.Contains(stderr.String(), "manual draft") || !strings.HasSuffix(string(mustRead(t, capture+".editor.path")), "draft.json") {
		t.Fatalf("edit code=%d stdout=%q stderr=%q editor=%q", code, stdout.String(), stderr.String(), mustRead(t, capture+".editor.path"))
	}
	if _, err := os.Stat(capture + ".ply.args"); !os.IsNotExist(err) {
		t.Fatalf("Ply ran during manual contract edit: %v", err)
	}
	store := contractexec.FileStore{Dir: sessionpkgContractDirForTest(dir, session)}
	raw, err := os.ReadFile(store.DraftPath())
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(store.DraftPath(), append(raw, '\n'), 0o600); err != nil {
		t.Fatal(err)
	}
	stdout.Reset()
	stderr.Reset()
	code = run([]string{"contract", "import", "-C", dir, "-f", session}, strings.NewReader(""), &stdout, &stderr)
	if code != 0 || !strings.Contains(stderr.String(), "manual draft") {
		t.Fatalf("import code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
	if _, err := os.Stat(capture + ".ply.args"); !os.IsNotExist(err) {
		t.Fatalf("Ply ran during external contract import: %v", err)
	}
	draft, status, err := store.Load()
	if err != nil || status != "draft" {
		t.Fatalf("load draft status=%q err=%v", status, err)
	}
	stdout.Reset()
	stderr.Reset()
	code = run([]string{"contract", "accept", "-C", dir, "-f", session, "-expect", draft.DraftSHA256, "-m", "openai/luna", "-mode", "loop"}, strings.NewReader(""), &stdout, &stderr)
	if code != 2 || stdout.String() != "worked answer\n" {
		t.Fatalf("accepted code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
	if !strings.Contains(stderr.String(), "LOOP · this invocation · cycles=unbounded · turns=50") {
		t.Fatalf("loop policy was not visible before work: %q", stderr.String())
	}
	plyArgs := string(mustRead(t, capture+".ply.args"))
	for _, want := range []string{"-f\n" + session + "\n", "-contract-id\nsha256:", "-cycles\n0\n", "-turns\n50\n", "ORIGINAL INTENT\nmake art", "OUTCOME CONTRACT v2"} {
		if !strings.Contains(plyArgs, want) {
			t.Errorf("admitted Ply args missing %q:\n%s", want, plyArgs)
		}
	}
	if _, status, err := store.Load(); err != nil || status != "admitted" {
		t.Fatalf("admitted status=%q err=%v", status, err)
	}
	admitted, _, err := store.Load()
	if err != nil {
		t.Fatal(err)
	}
	stdout.Reset()
	stderr.Reset()
	code = run([]string{"contract", "edit", "-C", dir, "-f", session}, strings.NewReader(""), &stdout, &stderr)
	if code != 0 {
		t.Fatalf("amend edit code=%d stderr=%q", code, stderr.String())
	}
	amendment, status, err := store.Load()
	if err != nil || status != "draft" || amendment.ParentRevisionID != admitted.RevisionID || amendment.RevisionID != "" || string(mustRead(t, capture+".ply.count")) != "1" {
		t.Fatalf("amendment=%#v status=%q err=%v ply-count=%q", amendment, status, err, mustRead(t, capture+".ply.count"))
	}
}

func TestCageRequiresAbsoluteExternalBenchDirBeforeCompiler(t *testing.T) {
	workspace := t.TempDir()
	ask := filepath.Join(workspace, "ask")
	marker := filepath.Join(workspace, "started")
	if err := os.WriteFile(ask, []byte("#!/bin/sh\ntouch \"$MARKER\"\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("BENCH_ASK", ask)
	t.Setenv("MARKER", marker)
	t.Setenv("BENCH_DIR", "")
	var out, errout strings.Builder
	code := run([]string{"run", "-C", workspace, "-cage", "do it"}, strings.NewReader(""), &out, &errout)
	if code != 1 || !strings.Contains(errout.String(), "absolute external BENCH_DIR") {
		t.Fatalf("code=%d stderr=%q", code, errout.String())
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatalf("compiler started: %v", err)
	}
	external := t.TempDir()
	t.Setenv("BENCH_DIR", external)
	if err := validateCageControllerRoot(workspace, external); err != nil {
		t.Fatalf("external rejected: %v", err)
	}
	if err := validateCageControllerPath(workspace, filepath.Join(workspace, "session.jsonl"), "Ask session"); err == nil {
		t.Fatal("workspace session accepted")
	}
}

func TestCageImpliesEveryActionApproval(t *testing.T) {
	flags := taskFlags{contract: true, cage: true, approval: plyexec.ApprovalOff}
	if err := validateTaskPolicy(flags); err != nil {
		t.Fatal(err)
	}
	if got := flags.options().ApprovalPolicy; got != plyexec.ApprovalEveryAction {
		t.Fatalf("approval=%q", got)
	}
}

func TestHeadlessAutoQuickRoutesAndSealsBeforePly(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("test fixture is a POSIX program")
	}
	workspace := t.TempDir()
	toolbox := filepath.Join(workspace, "tools")
	if err := os.Mkdir(toolbox, 0o755); err != nil {
		t.Fatal(err)
	}
	ask := filepath.Join(workspace, "ask")
	ply := filepath.Join(workspace, "ply")
	order := filepath.Join(workspace, "order")
	askScript := `#!/bin/sh
case "${1-}" in
  note) printf 'route-record\n' >> "$ORDER"; exit 0 ;;
esac
printf 'route-turn\n' >> "$ORDER"
printf '{"version":1,"route":"quick","reason":"routine-local","risk_tags":[]}'
`
	plyScript := `#!/bin/sh
printf 'ply\n' >> "$ORDER"
printf 'changed\n'
`
	if err := os.WriteFile(ask, []byte(askScript), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(ply, []byte(plyScript), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("BENCH_ASK", ask)
	t.Setenv("BENCH_PLY", ply)
	t.Setenv("ORDER", order)
	var stdout, stderr strings.Builder
	code := run([]string{"run", "-C", workspace, "-t", toolbox, "-mode", "auto", "fix the typo"}, strings.NewReader(""), &stdout, &stderr)
	if code != 0 || stdout.String() != "changed\n" || !strings.Contains(stderr.String(), "bench: AUTO -> QUICK · reason=routine-local") {
		t.Fatalf("code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
	if got := string(mustRead(t, order)); got != "route-turn\nroute-record\nply\n" {
		t.Fatalf("order=%q", got)
	}
}

type fixedAutoRouter struct{ events []autoroute.Event }

func (f fixedAutoRouter) Route(context.Context, autoroute.Request) <-chan autoroute.Event {
	events := make(chan autoroute.Event, len(f.events))
	for _, event := range f.events {
		events <- event
	}
	close(events)
	return events
}

func TestResolveAutoPreservesInterrupt(t *testing.T) {
	router := fixedAutoRouter{events: []autoroute.Event{{Done: true, ExitCode: 130, Err: context.Canceled}}}
	var stderr strings.Builder
	_, code := resolveAuto(context.Background(), router, plyexec.TaskRequest{}, &stderr)
	if code != 130 || !strings.Contains(stderr.String(), "context canceled") {
		t.Fatalf("code=%d stderr=%q", code, stderr.String())
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	router = fixedAutoRouter{events: []autoroute.Event{{Done: true, ExitCode: 0, Decision: &autoroute.Decision{Effective: autonomy.Quick, Reason: "routine-local"}}}}
	stderr.Reset()
	_, code = resolveAuto(ctx, router, plyexec.TaskRequest{}, &stderr)
	if code != 130 || !strings.Contains(stderr.String(), "context canceled") {
		t.Fatalf("queued success code=%d stderr=%q", code, stderr.String())
	}
}

func TestHeadlessAutoFullShellClampsQuickAndDraftsReview(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("test fixture is a POSIX program")
	}
	workspace := t.TempDir()
	ask := filepath.Join(workspace, "ask")
	ply := filepath.Join(workspace, "ply")
	order := filepath.Join(workspace, "order")
	count := filepath.Join(workspace, "ask-count")
	askScript := `#!/bin/sh
case "${1-}" in
  note) printf 'record\n' >> "$ORDER"; exit 0 ;;
esac
n=0
[ ! -f "$COUNT" ] || n=$(cat "$COUNT")
n=$((n + 1))
printf '%s' "$n" > "$COUNT"
if [ "$n" -eq 1 ]; then
  printf 'route-turn\n' >> "$ORDER"
  printf '{"version":1,"route":"quick","reason":"routine-local","risk_tags":[]}'
else
  printf 'compiler\n' >> "$ORDER"
  printf '{"version":2,"outcome":"Update the local fixture","deliverables":["updated fixture"],"invariants":[],"criteria":[{"id":"updated","requirement":"fixture is updated","evidence":"inspect the fixture","judge":"inspection"}],"approvals":[],"assumptions":[],"open_questions":[],"limits":[]}'
fi
`
	if err := os.WriteFile(ask, []byte(askScript), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(ply, []byte("#!/bin/sh\nprintf 'ply\n' >> \"$ORDER\"\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("BENCH_ASK", ask)
	t.Setenv("BENCH_PLY", ply)
	t.Setenv("ORDER", order)
	t.Setenv("COUNT", count)
	var stdout, stderr strings.Builder
	code := run([]string{"run", "-C", workspace, "-mode", "auto", "update the fixture"}, strings.NewReader(""), &stdout, &stderr)
	if code != 2 || !strings.Contains(stderr.String(), "bench: AUTO -> REVIEW · reason=broad-authority") {
		t.Fatalf("code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
	if got := string(mustRead(t, order)); strings.Contains(got, "ply") || got != "route-turn\nrecord\ncompiler\nrecord\n" {
		t.Fatalf("order=%q", got)
	}
}

func TestContractAcceptAndRunRejectUnreviewedPolicyFlags(t *testing.T) {
	for _, args := range [][]string{
		{"accept", "-f", "/tmp/session.jsonl", "-expect", "sha256:draft", "-check", "true"},
		{"run", "-f", "/tmp/session.jsonl", "-check-all"},
		{"accept", "-f", "/tmp/session.jsonl", "-expect", "sha256:draft", "-approval", "off"},
		{"run", "-f", "/tmp/session.jsonl", "-approval", "off"},
	} {
		var stdout, stderr strings.Builder
		if code := runContractCLI(args, strings.NewReader(""), &stdout, &stderr); code != 2 || !strings.Contains(stderr.String(), "flag provided but not defined") {
			t.Fatalf("args=%v code=%d stdout=%q stderr=%q", args, code, stdout.String(), stderr.String())
		}
	}
}

func TestContractRunRejectsInvalidEffectivePolicyBeforeAskOrPly(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("test fixtures are POSIX programs")
	}
	dir := t.TempDir()
	sessionPath := filepath.Join(dir, "sessions", "run.jsonl")
	store := contractexec.FileStore{Dir: sessionpkgContractDirForTest(dir, sessionPath)}
	draft, err := store.SaveDraft(contractexec.Draft{
		Schema: 1, OutcomeID: "outcome", Generation: 1, Intent: "work", Workspace: dir,
		Contract:               []byte(`{"version":2,"outcome":"Work is complete.","deliverables":["result"],"invariants":[],"criteria":[{"id":"quality","requirement":"result is useful","evidence":"review","judge":"human"}],"approvals":[],"assumptions":[],"open_questions":[],"limits":[]}`),
		CompilerEvidenceSHA256: "sha256:evidence", CheckSHA256: "sha256:empty", Skills: []string{},
	})
	if err != nil {
		t.Fatal(err)
	}
	draft, err = store.MarkDraftRecorded(draft)
	if err != nil {
		t.Fatal(err)
	}
	draft, err = store.PublishRevision(draft, draft.DraftSHA256)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.MarkAdmitted(draft); err != nil {
		t.Fatal(err)
	}
	started := filepath.Join(dir, "started")
	fixture := filepath.Join(dir, "must-not-start")
	if err := os.WriteFile(fixture, []byte("#!/bin/sh\ntouch \"$STARTED\"\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("BENCH_ASK", fixture)
	t.Setenv("BENCH_PLY", fixture)
	t.Setenv("STARTED", started)
	for _, args := range [][]string{
		{"contract", "run", "-C", dir, "-f", sessionPath, "-mode", "loop"},
		{"contract", "run", "-C", dir, "-f", sessionPath, "-compact"},
	} {
		var stdout, stderr strings.Builder
		if code := run(args, strings.NewReader(""), &stdout, &stderr); code != 2 || stderr.Len() == 0 {
			t.Fatalf("args=%v code=%d stdout=%q stderr=%q", args, code, stdout.String(), stderr.String())
		}
	}
	if _, err := os.Stat(started); !os.IsNotExist(err) {
		t.Fatalf("invalid contract run started Ask or Ply: %v", err)
	}
}

func sessionpkgContractDirForTest(root, file string) string {
	return session.ContractsDir(benchDir(root), file)
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

func TestVersionMatchesBenchComponentVersion(t *testing.T) {
	m, err := suite.Current()
	if err != nil {
		t.Fatal(err)
	}
	for _, component := range m.Components {
		if component.Name != "bench" {
			continue
		}
		if version != component.Version {
			t.Fatalf("bench version %q does not match component %q", version, component.Version)
		}
		return
	}
	t.Fatal("suite manifest has no Bench component")
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
	code := run([]string{"run", "-mode", "quick", "-C", dir, "-f", session, "-m", "openai/test", "-s", "go-review", "inspect; $(literal)"},
		strings.NewReader("piped evidence\n"), &stdout, &stderr)
	if code != 0 || stdout.String() != "answer only\n" || stderr.String() != "tool transcript\n" {
		t.Fatalf("code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
	args, err := os.ReadFile(capture + ".args")
	if err != nil {
		t.Fatal(err)
	}
	want := strings.Join([]string{"-sh", "-require-action", "-C", dir, "-f", session, "-m", "openai/test", "-s", "go-review", "--", "inspect; $(literal)", ""}, "\n")
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
		{"run", "-C", dir, "-mode", "quick", "-check", "true", "-check-all", "goal"},
		{"run", "-C", dir, "-mode", "quick", "-approval", "every-action", "goal"},
		{"run", "-C", dir, "-approval", "sometimes", "goal"},
	} {
		var stdout, stderr strings.Builder
		code := run(args, strings.NewReader(""), &stdout, &stderr)
		if code != 2 || (!strings.Contains(stderr.String(), "check-all needs") && !strings.Contains(stderr.String(), "approval")) {
			t.Fatalf("%v: code=%d stderr=%q", args, code, stderr.String())
		}
	}
	var stdout, stderr strings.Builder
	if code := run([]string{"run", "-C", dir, "-mode", "unknown", "goal"}, strings.NewReader(""), &stdout, &stderr); code != 2 || !strings.Contains(stderr.String(), "not supported") {
		t.Fatalf("invalid mode: code=%d stderr=%q", code, stderr.String())
	}
	stdout.Reset()
	stderr.Reset()
	if code := run([]string{"contract", "draft", "-C", dir, "-mode", "quick", "goal"}, strings.NewReader(""), &stdout, &stderr); code != 2 || !strings.Contains(stderr.String(), "flag provided but not defined") {
		t.Fatalf("contract draft accepted contradictory mode: code=%d stderr=%q", code, stderr.String())
	}
	for _, args := range [][]string{
		{"run", "-C", dir, "-mode", "loop", "goal"},
		{"run", "-C", dir, "-mode", "loop", "-check", "true", "-turns", "0", "goal"},
	} {
		stdout.Reset()
		stderr.Reset()
		if code := run(args, strings.NewReader(""), &stdout, &stderr); code != 2 || !strings.Contains(stderr.String(), "loop autonomy needs") {
			t.Fatalf("loop policy %v: code=%d stderr=%q", args, code, stderr.String())
		}
	}
	stdout.Reset()
	stderr.Reset()
	if code := run([]string{"contract", "draft", "-C", dir, "-mode", "loop", "-check", "true", "goal"}, strings.NewReader(""), &stdout, &stderr); code != 2 || !strings.Contains(stderr.String(), "flag provided but not defined") {
		t.Fatalf("contract draft accepted runtime mode: code=%d stderr=%q", code, stderr.String())
	}
	if _, err := os.Stat(started); !os.IsNotExist(err) {
		t.Fatalf("Ply started for invalid policy: %v", err)
	}
}

func TestModeFlagExplicitlyOverridesLegacyContractAlias(t *testing.T) {
	for _, tc := range []struct {
		flags taskFlags
		want  string
	}{
		{taskFlags{contract: false, mode: "review"}, "review"},
		{taskFlags{contract: true, mode: "quick"}, "quick"},
	} {
		got, err := tc.flags.autonomy()
		if err != nil || string(got) != tc.want {
			t.Fatalf("autonomy() = %q, %v; want %q", got, err, tc.want)
		}
	}
}

func TestLoopPolicyDisplaysExplicitZeroCyclesAsUnbounded(t *testing.T) {
	var stderr strings.Builder
	printLoopPolicy(&stderr, plyexec.TaskOptions{Loop: true, Cycles: 0, HasCycles: true})
	if got := stderr.String(); !strings.Contains(got, "cycles=unbounded") || strings.Contains(got, "cycles=0") || !strings.Contains(got, "turns=50") {
		t.Fatalf("policy=%q", got)
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

func TestHomeCLIIsTransparentAgentProcessBoundary(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("test fixture is a POSIX program")
	}
	dir := t.TempDir()
	fixture := filepath.Join(dir, "fake-agent")
	script := `#!/bin/sh
set -eu
printf '%s\n' "$@" > args
printf '%s\n%s\n%s\n%s\n%s\n%s\n%s\n%s\n' "$AGENT_PLY" "$AGENT_BRIEF" "$AGENT_CAGE" "$AGENT_HONE" "$AGENT_TRAIL" "$AGENT_ASK" "$AGENT_MAY" "$AGENT_ACTION" > tools
cat > input
printf 'agent-answer'
printf 'agent-evidence' >&2
exit 9
`
	if err := os.WriteFile(fixture, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("BENCH_AGENT", fixture)
	t.Setenv("BENCH_PLY", "/suite/ply")
	t.Setenv("BENCH_BRIEF", "/suite/brief")
	t.Setenv("BENCH_CAGE", "/suite/cage")
	t.Setenv("BENCH_HONE", "/suite/hone")
	t.Setenv("BENCH_TRAIL", "/suite/trail")
	t.Setenv("BENCH_ASK", "/suite/ask")
	t.Setenv("BENCH_MAY", "/suite/may")
	t.Setenv("BENCH_ACTION", "/suite/action")
	var stdout, stderr strings.Builder
	code := run([]string{"home", "-C", dir, "run", "-q", "home with spaces", "--", "; $(literal)"}, strings.NewReader("piped bytes\n"), &stdout, &stderr)
	if code != 9 || stdout.String() != "agent-answer" || stderr.String() != "agent-evidence" {
		t.Fatalf("code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
	args, err := os.ReadFile(filepath.Join(dir, "args"))
	if err != nil || string(args) != "run\n-q\nhome with spaces\n--\n; $(literal)\n" {
		t.Fatalf("args=%q err=%v", args, err)
	}
	input, err := os.ReadFile(filepath.Join(dir, "input"))
	if err != nil || string(input) != "piped bytes\n" {
		t.Fatalf("input=%q err=%v", input, err)
	}
	tools, err := os.ReadFile(filepath.Join(dir, "tools"))
	if err != nil || string(tools) != "/suite/ply\n/suite/brief\n/suite/cage\n/suite/hone\n/suite/trail\n/suite/ask\n/suite/may\n/suite/action\n" {
		t.Fatalf("tools=%q err=%v", tools, err)
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
	if code := run([]string{"home", "-h"}, strings.NewReader(""), &stdout, &stderr); code != 0 || !strings.Contains(stderr.String(), "usage: bench home") {
		t.Fatalf("home help code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
	stdout.Reset()
	stderr.Reset()
	if code := run([]string{"home"}, strings.NewReader(""), &stdout, &stderr); code != 2 || !strings.Contains(stderr.String(), "agent command is required") {
		t.Fatalf("home usage code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
	stdout.Reset()
	stderr.Reset()
	if code := run([]string{"tui", "-home", "worker", "-project", "design"}, strings.NewReader(""), &stdout, &stderr); code != 2 || !strings.Contains(stderr.String(), "cannot be used together") {
		t.Fatalf("home/project conflict code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
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
