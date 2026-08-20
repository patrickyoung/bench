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
