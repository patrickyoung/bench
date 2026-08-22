package plyexec

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestRefineKeepsSourceOnStdinAndFeedbackLiteral(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("test fixture is a POSIX program")
	}
	dir := t.TempDir()
	fixture := filepath.Join(dir, "fake-ply")
	script := `#!/bin/sh
set -eu
[ "$#" -eq 4 ]
[ "$1" = -sh ]
[ "$2" = -check ]
printf '%s' "$3" > check
printf '%s' "$4" > goal
printf '%s' "$BRIEF" > brief
printf '%s' "$PLY_DIR" > sessions
printf '%s' "$SOURCE_ROOT" > source-root
cat > source
printf 'refining' >&2
`
	if err := os.WriteFile(fixture, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	goal := "preserve this; $(literal feedback)"
	source := "pasted docs\nwith another line\n"
	sessions := filepath.Join(dir, "evidence")
	sourceRoot := filepath.Join(dir, "project root")
	var stderr strings.Builder
	var done Event
	for event := range (Runner{Path: fixture, BriefPath: "/opt/tools/brief"}).Refine(context.Background(), RefineRequest{
		Dir: dir, SourceRoot: sourceRoot, Goal: goal, Source: source, SessionDir: sessions,
	}) {
		if event.Stream == Stderr {
			stderr.WriteString(event.Text)
		}
		if event.Done {
			done = event
		}
	}
	if done.Err != nil || done.ExitCode != 0 || stderr.String() != "refining" {
		t.Fatalf("done=%#v stderr=%q", done, stderr.String())
	}
	for file, want := range map[string]string{
		"check": `"$BRIEF" lint -strict .`, "goal": goal, "brief": "/opt/tools/brief", "sessions": sessions,
		"source": source, "source-root": sourceRoot,
	} {
		got, err := os.ReadFile(filepath.Join(dir, file))
		if err != nil || string(got) != want {
			t.Errorf("%s=%q err=%v, want %q", file, got, err, want)
		}
	}
}

func TestWorkUsesReplayableSessionAndExplicitFullShell(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("test fixture is a POSIX program")
	}
	dir := t.TempDir()
	fixture := filepath.Join(dir, "fake-ply")
	script := `#!/bin/sh
set -eu
printf '%s\n' "$@" > args
printf '%s' "${ASK-}" > ask
printf '%s' "${BRIEF-}" > brief
printf '%s' "${ASK_MODEL-}" > ask-model
printf '%s' "${PLY_EFFORT-}" > ply-effort
printf '%s' "${PLY_DIR-}" > subagents-env
if [ -d "${PLY_DIR-}" ]; then
  printf present > subagents-state
else
  printf absent > subagents-state
fi
cat > input
printf 'ran rg and git\n' >&2
printf 'task answer\n'
`
	if err := os.WriteFile(fixture, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	session := filepath.Join(dir, "evidence", "task.jsonl")
	subagents := filepath.Join(dir, "subagents", "task-01234567")
	goal := "inspect this; $(still literal)"
	var stdout, stderr strings.Builder
	var done Event
	for event := range (Runner{Path: fixture, AskPath: "/opt/tools/ask", BriefPath: "/opt/tools/brief"}).Work(context.Background(), TaskRequest{
		Dir: dir, Goal: goal, Input: "piped evidence\n", Session: session, SubagentsDir: subagents,
		Model: "openai/test-model", Skills: []string{"go-review", "house-style"},
		Options: TaskOptions{Effort: "xhigh", Force: true},
	}) {
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
		t.Fatalf("done=%#v", done)
	}
	if stdout.String() != "task answer\n" || stderr.String() != "ran rg and git\n" {
		t.Fatalf("stdout=%q stderr=%q", stdout.String(), stderr.String())
	}
	wantArgs := strings.Join([]string{"-sh", "-B", "-effort", "xhigh", "-C", dir, "-f", session, "-m", "openai/test-model", "-s", "go-review", "-s", "house-style", "--", goal, ""}, "\n")
	for file, want := range map[string]string{
		"args": wantArgs, "ask": "/opt/tools/ask", "brief": "/opt/tools/brief",
		"ask-model": "openai/test-model", "ply-effort": "xhigh",
		"subagents-env": subagents, "subagents-state": "absent",
	} {
		got, err := os.ReadFile(filepath.Join(dir, file))
		if err != nil || string(got) != want {
			t.Errorf("%s=%q err=%v, want %q", file, got, err, want)
		}
	}
	input, err := os.ReadFile(filepath.Join(dir, "input"))
	if err != nil || string(input) != "piped evidence\n" {
		t.Fatalf("input=%q err=%v", input, err)
	}
	if _, err := os.Stat(filepath.Dir(session)); err != nil {
		t.Fatalf("session directory was not created: %v", err)
	}
	if _, err := os.Stat(subagents); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("subagent session directory was created before delegation: %v", err)
	}
}

func TestWorkKeepsAmbientAskModelWithoutExplicitSelection(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("test fixture is a POSIX program")
	}
	dir := t.TempDir()
	fixture := filepath.Join(dir, "fake-ply")
	if err := os.WriteFile(fixture, []byte("#!/bin/sh\nprintf '%s' \"${ASK_MODEL-}\" > ask-model\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("ASK_MODEL", "ambient/model")
	for range (Runner{Path: fixture}).Work(context.Background(), TaskRequest{
		Dir: dir, Goal: "inspect", Session: filepath.Join(dir, "task.jsonl"),
	}) {
	}
	got, err := os.ReadFile(filepath.Join(dir, "ask-model"))
	if err != nil || string(got) != "ambient/model" {
		t.Fatalf("ASK_MODEL=%q err=%v, want ambient model unchanged", got, err)
	}
}

func TestWorkUsesOnlyAnExplicitToolbox(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("test fixture is a POSIX program")
	}
	dir := t.TempDir()
	fixture := filepath.Join(dir, "fake-ply")
	if err := os.WriteFile(fixture, []byte("#!/bin/sh\nprintf '%s\\n' \"$@\" > args\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	toolbox := filepath.Join(dir, "tools")
	for range (Runner{Path: fixture}).Work(context.Background(), TaskRequest{
		Dir: dir, Goal: "inspect", Session: filepath.Join(dir, "task.jsonl"), Toolbox: toolbox,
	}) {
	}
	got, err := os.ReadFile(filepath.Join(dir, "args"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(got), "-sh") || !strings.Contains(string(got), "-t\n"+toolbox+"\n") {
		t.Fatalf("args=%q", got)
	}
}

func TestWorkPassesTrackedPolicyLiterallyAndFollowsCompactedSession(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("test fixture is a POSIX program")
	}
	dir := t.TempDir()
	fixture := filepath.Join(dir, "fake-ply")
	script := `#!/bin/sh
set -eu
printf '%s\n' "$@" > args
session_out=
while [ "$#" -gt 0 ]; do
  if [ "$1" = -session-out ]; then
    session_out=$2
    shift 2
    continue
  fi
  shift
done
printf '%s\n' "$NEXT_SESSION" > "$session_out"
printf answer
`
	if err := os.WriteFile(fixture, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	session := filepath.Join(dir, "sessions", "source.jsonl")
	next := filepath.Join(dir, "sessions", "compacted.jsonl")
	t.Setenv("NEXT_SESSION", next)
	check := `test "$(printf literal)" = literal; # remains one argv value`
	var done Event
	for event := range (Runner{Path: fixture}).Work(context.Background(), TaskRequest{
		Dir: dir, Goal: "finish", Session: session,
		Options: TaskOptions{
			Check:  check,
			Effort: "xhigh",
			Cycles: 0, HasCycles: true,
			Turns: 12, HasTurns: true,
			Timeout: 45 * time.Second, HasTimeout: true,
			Compact:     true,
			Compactions: 0, HasCompactions: true,
		},
	}) {
		if event.Done {
			done = event
		}
	}
	if done.Err != nil || done.ExitCode != 0 || done.Session != next {
		t.Fatalf("done=%#v, want successor %q", done, next)
	}
	args, err := os.ReadFile(filepath.Join(dir, "args"))
	if err != nil {
		t.Fatal(err)
	}
	got := string(args)
	for _, want := range []string{
		"-check\n" + check + "\n",
		"-effort\nxhigh\n",
		"-cycles\n0\n",
		"-turns\n12\n",
		"-timeout\n45s\n",
		"-compact\n",
		"-compactions\n0\n",
		"-session-out\n",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("args missing %q:\n%s", want, got)
		}
	}
	if matches, err := filepath.Glob(filepath.Join(filepath.Dir(session), ".bench-ply-session-*")); err != nil || len(matches) != 0 {
		t.Fatalf("session result was not cleaned up: matches=%v err=%v", matches, err)
	}
}

func TestWorkRecognizesPassingPrecheckWithoutASession(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("test fixture is a POSIX program")
	}
	dir := t.TempDir()
	fixture := filepath.Join(dir, "fake-ply")
	if err := os.WriteFile(fixture, []byte("#!/bin/sh\nexit 0\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	var done Event
	for event := range (Runner{Path: fixture}).Work(context.Background(), TaskRequest{
		Dir: dir, Goal: "already true", Session: filepath.Join(dir, "sessions", "unused.jsonl"),
		Options: TaskOptions{Check: "test -f result"},
	}) {
		if event.Done {
			done = event
		}
	}
	if done.Err != nil || done.ExitCode != 0 || done.Session != "" {
		t.Fatalf("precheck outcome=%#v", done)
	}
}

func TestWorkPreservesPlyFailureWhenSessionResultWasNeverWritten(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("test fixture is a POSIX program")
	}
	dir := t.TempDir()
	fixture := filepath.Join(dir, "fake-ply")
	if err := os.WriteFile(fixture, []byte("#!/bin/sh\nprintf 'ply diagnostic\\n' >&2\nexit 2\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	var done Event
	for event := range (Runner{Path: fixture}).Work(context.Background(), TaskRequest{
		Dir: dir, Goal: "stop", Session: filepath.Join(dir, "sessions", "unused.jsonl"),
		Options: TaskOptions{Compact: true},
	}) {
		if event.Done {
			done = event
		}
	}
	if done.ExitCode != 2 || done.Err == nil || strings.Contains(done.Err.Error(), "session result") {
		t.Fatalf("Ply failure was replaced: %#v", done)
	}
}

func TestWorkRejectsInvalidPolicyBeforeStartingPly(t *testing.T) {
	dir := t.TempDir()
	started := filepath.Join(dir, "started")
	fixture := filepath.Join(dir, "fake-ply")
	if err := os.WriteFile(fixture, []byte("#!/bin/sh\ntouch \"$STARTED\"\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("STARTED", started)
	for _, options := range []TaskOptions{
		{Cycles: -1, HasCycles: true},
		{Turns: -1, HasTurns: true},
		{Timeout: 0, HasTimeout: true},
		{Compactions: -1, HasCompactions: true},
		{IntentContract: true, Compact: true},
		{IntentContract: true, CheckAllCriteria: true},
		{Check: "true", CheckAllCriteria: true},
	} {
		var done Event
		for event := range (Runner{Path: fixture}).Work(context.Background(), TaskRequest{
			Dir: dir, Goal: "invalid", Session: filepath.Join(dir, "task.jsonl"), Options: options,
		}) {
			if event.Done {
				done = event
			}
		}
		if done.ExitCode != 2 || done.Err == nil {
			t.Fatalf("invalid options outcome=%#v", done)
		}
	}
	if _, err := os.Stat(started); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("Ply started for invalid options: %v", err)
	}
}
