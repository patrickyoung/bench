package filterexec

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
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

func TestTerminalEventDoesNotBlockOnCanceledFullChannel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	events := make(chan Event, 1)
	events <- Event{Stream: Stdout, Text: "unread"}
	cancel()

	done := make(chan struct{})
	go func() {
		emitFinal(ctx, events, Event{Done: true, ExitCode: 130, Err: context.Canceled})
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("terminal send blocked behind an abandoned full channel")
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

func TestExecuteDrainsShortLivedStreamsBeforeReturning(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("test fixture is a POSIX program")
	}
	dir := t.TempDir()
	fixture := filepath.Join(dir, "short-output")
	script := "#!/bin/sh\nprintf 'answer\\n'\nprintf 'evidence\\n' >&2\nexit 1\n"
	if err := os.WriteFile(fixture, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	for attempt := 0; attempt < 100; attempt++ {
		var stdout, stderr strings.Builder
		outcome := Execute(context.Background(), Spec{Path: fixture}, func(stream Stream, text string) {
			switch stream {
			case Stdout:
				stdout.WriteString(text)
			case Stderr:
				stderr.WriteString(text)
			}
		})
		if outcome.ExitCode != 1 || outcome.Err == nil || stdout.String() != "answer\n" || stderr.String() != "evidence\n" {
			t.Fatalf("attempt %d: outcome=%#v stdout=%q stderr=%q", attempt, outcome, stdout.String(), stderr.String())
		}
	}
}
