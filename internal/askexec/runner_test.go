package askexec

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func mustRead(t *testing.T, path string) []byte {
	t.Helper()
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return body
}

func TestRunnerPreservesAskContractAndArguments(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("test fixture is a POSIX program")
	}
	dir := t.TempDir()
	fixture := filepath.Join(dir, "fake-ask")
	script := `#!/bin/sh
set -eu
[ "$1" = "-f" ]
session=$2
[ "$3" = "--" ]
printf '%s' "$4" > "$session.args"
printf 'thinking carefully' >&2
printf 'the answer'
`
	if err := os.WriteFile(fixture, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}

	session := filepath.Join(dir, "state", "run.jsonl")
	events := Runner{Path: fixture}.Start(context.Background(), Request{
		Message: "literal; $(not a shell)",
		Session: session,
	})
	var stdout, stderr strings.Builder
	var done Event
	for event := range events {
		switch event.Stream {
		case Stdout:
			stdout.WriteString(event.Text)
		case Stderr:
			stderr.WriteString(event.Text)
		}
		if event.Done {
			done = event
		}
	}
	if done.Err != nil || done.ExitCode != 0 {
		t.Fatalf("done = %#v", done)
	}
	if got := stdout.String(); got != "the answer" {
		t.Fatalf("stdout = %q", got)
	}
	if got := stderr.String(); got != "thinking carefully" {
		t.Fatalf("stderr = %q", got)
	}
	got, err := os.ReadFile(session + ".args")
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "literal; $(not a shell)" {
		t.Fatalf("argument = %q", got)
	}
}

func TestRunnerPassesModelAndPipeInputWithoutASecondProtocol(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("test fixture is a POSIX program")
	}
	dir := t.TempDir()
	fixture := filepath.Join(dir, "fake-ask")
	capture := filepath.Join(dir, "capture")
	t.Setenv("ASK_CAPTURE", capture)
	script := `#!/bin/sh
set -eu
printf '%s\n' "$@" > "$ASK_CAPTURE.args"
cat > "$ASK_CAPTURE.stdin"
printf answer
`
	if err := os.WriteFile(fixture, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	for range (Runner{Path: fixture}).Start(context.Background(), Request{
		Message: "explain", Input: "piped evidence\n", Session: filepath.Join(dir, "run.jsonl"), Model: "openai/test-model",
	}) {
	}
	args, err := os.ReadFile(capture + ".args")
	if err != nil {
		t.Fatal(err)
	}
	want := "-f\n" + filepath.Join(dir, "run.jsonl") + "\n-m\nopenai/test-model\n--\nexplain\n"
	if string(args) != want {
		t.Fatalf("args=%q, want %q", args, want)
	}
	input, err := os.ReadFile(capture + ".stdin")
	if err != nil || string(input) != "piped evidence\n" {
		t.Fatalf("stdin=%q err=%v", input, err)
	}
}

func TestRunnerPassesStructuredContractPolicyAndRemovesTemporarySchema(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("test fixture is a POSIX program")
	}
	dir := t.TempDir()
	fixture := filepath.Join(dir, "fake-ask")
	capture := filepath.Join(dir, "capture")
	t.Setenv("ASK_CAPTURE", capture)
	script := `#!/bin/sh
set -eu
printf '%s\n' "$@" > "$ASK_CAPTURE.args"
schema=
while [ "$#" -gt 0 ]; do
  case "$1" in
    -schema) schema=$2; shift 2 ;;
    *) shift ;;
  esac
done
cp "$schema" "$ASK_CAPTURE.schema"
cat > "$ASK_CAPTURE.stdin"
printf '{"outcome":"compiled"}'
`
	if err := os.WriteFile(fixture, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	session := filepath.Join(dir, "sessions", "run.jsonl")
	for range (Runner{Path: fixture}).Start(context.Background(), Request{
		Message: "compile this intent", Input: "workspace evidence", Session: session,
		Model: "openai/test", Effort: "xhigh", System: "contract compiler", Schema: `{"type":"object"}`,
	}) {
	}
	args := string(mustRead(t, capture+".args"))
	for _, want := range []string{"-effort\nxhigh\n", "-S\ncontract compiler\n", "-schema\n"} {
		if !strings.Contains(args, want) {
			t.Errorf("args missing %q:\n%s", want, args)
		}
	}
	if got := string(mustRead(t, capture+".schema")); got != `{"type":"object"}` {
		t.Fatalf("schema=%q", got)
	}
	if got := string(mustRead(t, capture+".stdin")); got != "workspace evidence" {
		t.Fatalf("stdin=%q", got)
	}
	fields := strings.Split(args, "\n")
	for i, field := range fields {
		if field == "-schema" && i+1 < len(fields) {
			if _, err := os.Stat(fields[i+1]); !os.IsNotExist(err) {
				t.Fatalf("temporary schema remains: %v", err)
			}
		}
	}
}

func TestRunnerWritesStructuredRecordOnStdinWithSealFlags(t *testing.T) {
	dir := t.TempDir()
	fixture := filepath.Join(dir, "fake-ask")
	capture := filepath.Join(dir, "capture")
	t.Setenv("ASK_CAPTURE", capture)
	script := `#!/bin/sh
set -eu
printf '%s\n' "$@" > "$ASK_CAPTURE.args"
cat > "$ASK_CAPTURE.stdin"
`
	if err := os.WriteFile(fixture, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	req := RecordRequest{Session: filepath.Join(dir, "run.jsonl"), Source: "bench", Kind: "bench.contract/v1", JSON: `{"status":"admitted"}`}
	if err := (Runner{Path: fixture}).Record(context.Background(), req); err != nil {
		t.Fatal(err)
	}
	args := string(mustRead(t, capture+".args"))
	for _, want := range []string{"note\n", "-s\nbench\n", "-k\nbench.contract/v1\n", "-json\n-\n", "-seal\n"} {
		if !strings.Contains(args, want) {
			t.Errorf("args missing %q:\n%s", want, args)
		}
	}
	if got := string(mustRead(t, capture+".stdin")); got != req.JSON {
		t.Fatalf("stdin = %q", got)
	}
	if err := (Runner{Path: fixture}).Record(context.Background(), RecordRequest{Session: req.Session, Source: "bench", Kind: req.Kind, JSON: `{bad`}); err == nil {
		t.Fatal("invalid record JSON was executed")
	}
}

func TestRunnerComposesSelectedBriefSkillIntoSystemPrompt(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("test fixture is a POSIX program")
	}
	dir := t.TempDir()
	ask := filepath.Join(dir, "fake-ask")
	brief := filepath.Join(dir, "fake-brief")
	capture := filepath.Join(dir, "system")
	t.Setenv("ASK_CAPTURE", capture)
	askScript := `#!/bin/sh
set -eu
if [ "$1" = system ]; then
  printf 'base system\n'
  exit 0
fi
[ "$1" = -f ]
[ "$3" = -S ]
printf '%s' "$4" > "$ASK_CAPTURE"
[ "$5" = -- ]
printf 'skilled answer'
`
	briefScript := `#!/bin/sh
set -eu
[ "$1" = cat ]
[ "$2" = go-review ]
printf 'Run the repository fixture before reporting a finding.\n'
`
	for path, body := range map[string]string{ask: askScript, brief: briefScript} {
		if err := os.WriteFile(path, []byte(body), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	var stdout strings.Builder
	var done Event
	for event := range (Runner{Path: ask, BriefPath: brief}).Start(context.Background(), Request{
		Message: "review this", Session: filepath.Join(dir, "run.jsonl"), Skills: []string{"go-review"},
	}) {
		if event.Stream == Stdout {
			stdout.WriteString(event.Text)
		}
		if event.Done {
			done = event
		}
	}
	if done.Err != nil || done.ExitCode != 0 || stdout.String() != "skilled answer" {
		t.Fatalf("done=%#v stdout=%q", done, stdout.String())
	}
	system, err := os.ReadFile(capture)
	if err != nil {
		t.Fatal(err)
	}
	if got := string(system); got != "base system\n\nRun the repository fixture before reporting a finding." {
		t.Fatalf("system = %q", got)
	}
}

func TestRunnerAppendsSkillsToExplicitSystemWithoutAskingForDefault(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("test fixture is a POSIX program")
	}
	dir := t.TempDir()
	ask := filepath.Join(dir, "fake-ask")
	brief := filepath.Join(dir, "fake-brief")
	capture := filepath.Join(dir, "system")
	t.Setenv("ASK_CAPTURE", capture)
	askScript := `#!/bin/sh
set -eu
[ "${1-}" != system ]
[ "$1" = -f ]
[ "$3" = -S ]
printf '%s' "$4" > "$ASK_CAPTURE"
printf answer
`
	briefScript := `#!/bin/sh
set -eu
[ "$1" = cat ]
case "$2" in
  web) printf 'Render wide and narrow views.\n' ;;
  house) printf 'Preserve the source material.\n' ;;
  *) exit 1 ;;
esac
`
	for path, body := range map[string]string{ask: askScript, brief: briefScript} {
		if err := os.WriteFile(path, []byte(body), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	var done Event
	for event := range (Runner{Path: ask, BriefPath: brief}).Start(context.Background(), Request{
		Message: "compile", Session: filepath.Join(dir, "run.jsonl"), System: "contract policy",
		Skills: []string{"web", "house"},
	}) {
		if event.Done {
			done = event
		}
	}
	if done.Err != nil || done.ExitCode != 0 {
		t.Fatalf("done=%#v", done)
	}
	got := string(mustRead(t, capture))
	want := "contract policy\n\nRender wide and narrow views.\n\nPreserve the source material."
	if got != want {
		t.Fatalf("system=%q, want %q", got, want)
	}
}

func TestRunnerReportsExitCode(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("test fixture is a POSIX program")
	}
	dir := t.TempDir()
	fixture := filepath.Join(dir, "fake-ask")
	if err := os.WriteFile(fixture, []byte("#!/bin/sh\nprintf 'full' >&2\nexit 2\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	var done Event
	for event := range (Runner{Path: fixture}).Start(context.Background(), Request{
		Message: "continue",
		Session: filepath.Join(dir, "run.jsonl"),
	}) {
		if event.Done {
			done = event
		}
	}
	if done.ExitCode != 2 {
		t.Fatalf("exit code = %d, want 2", done.ExitCode)
	}
}

func TestRunnerInterruptsAskBeforeForcingItClosed(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("test fixture is a POSIX program")
	}
	dir := t.TempDir()
	fixture := filepath.Join(dir, "fake-ask")
	script := `#!/bin/sh
trap 'exit 130' INT TERM
printf ready >&2
while :; do sleep 1; done
`
	if err := os.WriteFile(fixture, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	events := Runner{Path: fixture}.Start(ctx, Request{
		Message: "a long turn",
		Session: filepath.Join(dir, "run.jsonl"),
	})
	select {
	case event := <-events:
		if event.Stream != Stderr || event.Text != "ready" {
			t.Fatalf("first event = %#v", event)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("ask did not start")
	}
	cancel()
	select {
	case event := <-events:
		if !event.Done || event.Err != context.Canceled {
			t.Fatalf("done = %#v", event)
		}
	case <-time.After(4 * time.Second):
		t.Fatal("ask ignored cancellation")
	}
}

func TestReplayChecksBeforeRendering(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("test fixture is a POSIX program")
	}
	dir := t.TempDir()
	fixture := filepath.Join(dir, "fake-ask")
	calls := filepath.Join(dir, "calls")
	t.Setenv("FAKE_CALLS", calls)
	script := `#!/bin/sh
set -eu
printf '%s\n' "$*" >> "$FAKE_CALLS"
if [ "$1" = replay ] && [ "$2" = -check ]; then
  printf 'ok: verified\n'
  exit 0
fi
if [ "$1" = replay ]; then
  printf 'session fixture\n» requirement\nanswer\n'
  exit 0
fi
exit 9
`
	if err := os.WriteFile(fixture, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}

	session := filepath.Join(dir, "literal name.jsonl")
	var stdout, activity strings.Builder
	var done Event
	for event := range (Runner{Path: fixture}).Replay(context.Background(), session) {
		if event.Stream == Stdout {
			stdout.WriteString(event.Text)
		}
		if event.Stream == Stderr {
			activity.WriteString(event.Text)
		}
		if event.Done {
			done = event
		}
	}
	if done.Err != nil || done.ExitCode != 0 {
		t.Fatalf("done = %#v", done)
	}
	if !strings.Contains(activity.String(), "verified") {
		t.Fatalf("activity = %q", activity.String())
	}
	if got := stdout.String(); got != "session fixture\n» requirement\nanswer\n" {
		t.Fatalf("stdout = %q", got)
	}
	gotCalls, err := os.ReadFile(calls)
	if err != nil {
		t.Fatal(err)
	}
	want := "replay -check " + session + "\nreplay " + session + "\n"
	if string(gotCalls) != want {
		t.Fatalf("calls = %q, want %q", gotCalls, want)
	}
}

func TestReplayRefusesUnverifiedSession(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("test fixture is a POSIX program")
	}
	dir := t.TempDir()
	fixture := filepath.Join(dir, "fake-ask")
	calls := filepath.Join(dir, "calls")
	t.Setenv("FAKE_CALLS", calls)
	script := `#!/bin/sh
printf '%s\n' "$*" >> "$FAKE_CALLS"
printf 'replay divergence' >&2
exit 1
`
	if err := os.WriteFile(fixture, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	var done Event
	for event := range (Runner{Path: fixture}).Replay(context.Background(), filepath.Join(dir, "bad.jsonl")) {
		if event.Done {
			done = event
		}
	}
	if done.ExitCode != 1 || done.Err == nil {
		t.Fatalf("done = %#v", done)
	}
	got, err := os.ReadFile(calls)
	if err != nil {
		t.Fatal(err)
	}
	if lines := strings.Count(string(got), "\n"); lines != 1 {
		t.Fatalf("render ran after failed check: %q", got)
	}
}
