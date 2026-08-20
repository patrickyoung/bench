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
