package plyexec

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
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
printf 'ran rg and git\n' >&2
printf 'task answer\n'
`
	if err := os.WriteFile(fixture, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	session := filepath.Join(dir, "evidence", "task.jsonl")
	goal := "inspect this; $(still literal)"
	var stdout, stderr strings.Builder
	var done Event
	for event := range (Runner{Path: fixture, AskPath: "/opt/tools/ask", BriefPath: "/opt/tools/brief"}).Work(context.Background(), TaskRequest{
		Dir: dir, Goal: goal, Session: session, Skills: []string{"go-review", "house-style"},
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
	wantArgs := strings.Join([]string{"-sh", "-C", dir, "-f", session, "-s", "go-review", "-s", "house-style", "--", goal, ""}, "\n")
	for file, want := range map[string]string{"args": wantArgs, "ask": "/opt/tools/ask", "brief": "/opt/tools/brief"} {
		got, err := os.ReadFile(filepath.Join(dir, file))
		if err != nil || string(got) != want {
			t.Errorf("%s=%q err=%v, want %q", file, got, err, want)
		}
	}
	if _, err := os.Stat(filepath.Dir(session)); err != nil {
		t.Fatalf("session directory was not created: %v", err)
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
