package filterexec

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestOverlayEnvReplacesRatherThanDuplicates(t *testing.T) {
	got := overlayEnv([]string{"A=old", "B=keep", "A=stale"}, []string{"A=new", "INVALID", "C=added"})
	want := []string{"B=keep", "A=new", "C=added"}
	if len(got) != len(want) {
		t.Fatalf("env = %#v", got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("env = %#v, want %#v", got, want)
		}
	}
}

func TestStdinReachesFilterWithoutBecomingAnArgument(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("test fixture is a POSIX program")
	}
	dir := t.TempDir()
	fixture := filepath.Join(dir, "read-input")
	script := "#!/bin/sh\n[ \"$#\" -eq 0 ] || exit 2\ncat\n"
	if err := os.WriteFile(fixture, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	input := "literal source; $(never a shell)\nsecond line\n"
	var stdout strings.Builder
	outcome := Execute(context.Background(), Spec{Path: fixture, Stdin: input}, func(stream Stream, text string) {
		if stream == Stdout {
			stdout.WriteString(text)
		}
	})
	if outcome.Err != nil || outcome.ExitCode != 0 || stdout.String() != input {
		t.Fatalf("outcome=%#v stdout=%q", outcome, stdout.String())
	}
}
