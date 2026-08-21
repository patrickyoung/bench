package main

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

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
	code := run([]string{"run", "-C", dir, "-f", session, "-m", "openai/test", "-s", "go-review", "inspect; $(literal)"},
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
	code := run([]string{"run", "-C", dir, "unfinished"}, strings.NewReader(""), &stdout, &stderr)
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
		"run", "-C", dir, "-check", check, "-cycles", "0", "-turns", "9",
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
		"-check\n" + check + "\n", "-cycles\n0\n", "-turns\n9\n",
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
	code := run([]string{"-C", dir, "review this"}, strings.NewReader("diff bytes"), &stdout, &stderr)
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
