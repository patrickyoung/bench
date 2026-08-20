package briefexec

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestNewKeepsFlagsBeforeLiteralName(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("test fixture is a POSIX program")
	}
	dir := t.TempDir()
	fixture := filepath.Join(dir, "fake-brief")
	script := `#!/bin/sh
set -eu
[ "$#" -eq 4 ]
[ "$1" = new ]
[ "$2" = -d ]
printf '%s' "$3" > directory
printf '%s' "$4" > name
printf '%s/%s/SKILL.md\n' "$3" "$4"
`
	if err := os.WriteFile(fixture, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	wantDir := filepath.Join(dir, "skills with spaces")
	var stdout strings.Builder
	var done Event
	for event := range (Runner{Binary: fixture, WorkDir: dir}).New(context.Background(), NewRequest{Directory: wantDir, Name: "patch-review"}) {
		if event.Stream == Stdout {
			stdout.WriteString(event.Text)
		}
		if event.Done {
			done = event
		}
	}
	if done.Err != nil || done.ExitCode != 0 || !strings.Contains(stdout.String(), "SKILL.md") {
		t.Fatalf("done=%#v stdout=%q", done, stdout.String())
	}
	got, err := os.ReadFile(filepath.Join(dir, "directory"))
	if err != nil || string(got) != wantDir {
		t.Fatalf("directory=%q err=%v", got, err)
	}
}

func TestLintPreservesOrdinaryNegativeExitOne(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("test fixture is a POSIX program")
	}
	dir := t.TempDir()
	fixture := filepath.Join(dir, "fake-brief")
	script := "#!/bin/sh\n[ \"$1\" = lint ]\nprintf 'SKILL.md: warning: too long\\n'\nexit 1\n"
	if err := os.WriteFile(fixture, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	var done Event
	for event := range (Runner{Binary: fixture}).Lint(context.Background(), "house") {
		if event.Done {
			done = event
		}
	}
	if done.ExitCode != 1 || done.Err == nil {
		t.Fatalf("done = %#v", done)
	}
}
