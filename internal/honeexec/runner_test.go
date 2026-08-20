package honeexec

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestLearnPassesSkillBeforeLiteralSession(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("test fixture is a POSIX program")
	}
	dir := t.TempDir()
	fixture := filepath.Join(dir, "fake-hone")
	script := `#!/bin/sh
set -eu
[ "$#" -eq 3 ]
[ "$1" = -into ]
printf '%s' "$2" > skill
printf '%s' "$3" > session
printf 'lesson admitted' >&2
`
	if err := os.WriteFile(fixture, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	session := filepath.Join(dir, "session with spaces.jsonl")
	var stderr strings.Builder
	var done Event
	for event := range (Runner{Path: fixture, WorkDir: dir}).Learn(context.Background(), Request{
		Session: session,
		Skill:   "review-house",
	}) {
		if event.Stream == Stderr {
			stderr.WriteString(event.Text)
		}
		if event.Done {
			done = event
		}
	}
	if done.Err != nil || done.ExitCode != 0 || stderr.String() != "lesson admitted" {
		t.Fatalf("done=%#v stderr=%q", done, stderr.String())
	}
	got, err := os.ReadFile(filepath.Join(dir, "session"))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != session {
		t.Fatalf("session = %q", got)
	}
}

func TestLearnRejectsMissingEvidenceBeforeStartingProcess(t *testing.T) {
	var done Event
	for event := range (Runner{}).Learn(context.Background(), Request{Skill: "house"}) {
		if event.Done {
			done = event
		}
	}
	if done.ExitCode != 2 || done.Err == nil {
		t.Fatalf("done = %#v", done)
	}
}
