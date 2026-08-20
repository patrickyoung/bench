package draftexec

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestNewPassesRequirementsAsOneLiteralArgument(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("test fixture is a POSIX program")
	}
	workspace := t.TempDir()
	fixture := filepath.Join(workspace, "fake-draft")
	script := `#!/bin/sh
set -eu
[ "$#" -eq 3 ]
[ "$1" = new ]
printf '%s\n' "$2" > new.dir
printf '%s' "$3" > new.description
printf 'drafting' >&2
printf '%s/DESIGN.md\n' "$2"
`
	if err := os.WriteFile(fixture, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	description := "literal; $(not a shell)\nwith another line"
	var stdout, stderr strings.Builder
	var done Event
	for event := range (Runner{Path: fixture, WorkDir: workspace}).New(context.Background(), Request{
		Dir:         "agent-one",
		Description: description,
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
		t.Fatalf("done = %#v", done)
	}
	if stdout.String() != "agent-one/DESIGN.md\n" || stderr.String() != "drafting" {
		t.Fatalf("stdout=%q stderr=%q", stdout.String(), stderr.String())
	}
	got, err := os.ReadFile(filepath.Join(workspace, "new.description"))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != description {
		t.Fatalf("description = %q", got)
	}
}

func TestCheckPreservesNoAndBrokenExitCodes(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("test fixture is a POSIX program")
	}
	workspace := t.TempDir()
	fixture := filepath.Join(workspace, "fake-draft")
	script := `#!/bin/sh
case "$2" in
  incomplete) printf 'not buildable' >&2; exit 1 ;;
  broken) printf 'unreadable' >&2; exit 2 ;;
  *) printf './bin/check\n'; exit 0 ;;
esac
`
	if err := os.WriteFile(fixture, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		dir  string
		code int
	}{{"ready", 0}, {"incomplete", 1}, {"broken", 2}} {
		var done Event
		for event := range (Runner{Path: fixture, WorkDir: workspace}).Check(context.Background(), test.dir) {
			if event.Done {
				done = event
			}
		}
		if done.ExitCode != test.code {
			t.Errorf("%s exit = %d, want %d", test.dir, done.ExitCode, test.code)
		}
	}
}

func TestBuildKeepsPlyEvidenceWithProject(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("test fixture is a POSIX program")
	}
	workspace := t.TempDir()
	fixture := filepath.Join(workspace, "fake-draft")
	script := `#!/bin/sh
set -eu
[ "$1" = build ]
printf '%s' "$PLY_DIR" > ply-dir
printf '%s' "$2" > build-dir
printf 'typescript' >&2
printf 'finished'
`
	if err := os.WriteFile(fixture, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	project := filepath.Join(workspace, "agent")
	var done Event
	for event := range (Runner{Path: fixture, WorkDir: workspace}).Build(context.Background(), project) {
		if event.Done {
			done = event
		}
	}
	if done.Err != nil || done.ExitCode != 0 {
		t.Fatalf("done = %#v", done)
	}
	plyDir, err := os.ReadFile(filepath.Join(workspace, "ply-dir"))
	if err != nil {
		t.Fatal(err)
	}
	if got, want := string(plyDir), filepath.Join(project, ".draft", "build"); got != want {
		t.Fatalf("PLY_DIR = %q, want %q", got, want)
	}
}

func TestProvePreservesGapsAsExitOne(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("test fixture is a POSIX program")
	}
	dir := t.TempDir()
	fixture := filepath.Join(dir, "fake-draft")
	script := `#!/bin/sh
[ "$1" = prove ]
printf 'lib/agent.go:9: == -> != survived\n'
printf 'draft: killed 4 of 5, 1 survived\n' >&2
exit 1
`
	if err := os.WriteFile(fixture, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	var stdout strings.Builder
	var done Event
	for event := range (Runner{Path: fixture}).Prove(context.Background(), filepath.Join(dir, "agent")) {
		if event.Stream == Stdout {
			stdout.WriteString(event.Text)
		}
		if event.Done {
			done = event
		}
	}
	if done.ExitCode != 1 || done.Err == nil {
		t.Fatalf("done = %#v", done)
	}
	if !strings.Contains(stdout.String(), "survived") {
		t.Fatalf("stdout = %q", stdout.String())
	}
}
