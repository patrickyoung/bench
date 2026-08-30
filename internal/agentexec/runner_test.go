package agentexec

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestRunPreservesArgvStreamsEnvironmentAndExit(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("test fixture is a POSIX program")
	}
	dir := t.TempDir()
	fixture := filepath.Join(dir, "fake-agent")
	script := `#!/bin/sh
set -eu
[ "$#" -eq 3 ]
[ "$1" = run ]
printf '%s\n%s\n' "$2" "$3" > argv
printf '%s\n%s\n%s\n%s\n%s\n%s\n%s\n%s\n' "$AGENT_PLY" "$AGENT_BRIEF" "$AGENT_CAGE" "$AGENT_HONE" "$AGENT_TRAIL" "$AGENT_ASK" "$AGENT_MAY" "$AGENT_ACTION" > tools
cat > stdin
printf 'answer'
printf 'evidence' >&2
exit 7
`
	if err := os.WriteFile(fixture, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr strings.Builder
	runner := Runner{
		Path: fixture, WorkDir: dir,
		PlyPath: "/suite/ply", BriefPath: "/suite/brief",
		CagePath: "/suite/cage", HonePath: "/suite/hone",
		TrailPath: "/suite/trail", AskPath: "/suite/ask", MayPath: "/suite/may", ActionPath: "/suite/action",
	}
	outcome := runner.Run(context.Background(), []string{"run", "home with spaces", "; $(literal)"}, strings.NewReader("input bytes\n"), &stdout, &stderr)
	if outcome.Err != nil || outcome.ExitCode != 7 {
		t.Fatalf("outcome = %#v", outcome)
	}
	if stdout.String() != "answer" || stderr.String() != "evidence" {
		t.Fatalf("stdout=%q stderr=%q", stdout.String(), stderr.String())
	}
	argv, err := os.ReadFile(filepath.Join(dir, "argv"))
	if err != nil || string(argv) != "home with spaces\n; $(literal)\n" {
		t.Fatalf("argv=%q err=%v", argv, err)
	}
	input, err := os.ReadFile(filepath.Join(dir, "stdin"))
	if err != nil || string(input) != "input bytes\n" {
		t.Fatalf("stdin=%q err=%v", input, err)
	}
	tools, err := os.ReadFile(filepath.Join(dir, "tools"))
	if err != nil || string(tools) != "/suite/ply\n/suite/brief\n/suite/cage\n/suite/hone\n/suite/trail\n/suite/ask\n/suite/may\n/suite/action\n" {
		t.Fatalf("tools=%q err=%v", tools, err)
	}
}

func TestRunReportsMissingExecutableAsBoundaryError(t *testing.T) {
	outcome := (Runner{Path: filepath.Join(t.TempDir(), "missing")}).Run(
		context.Background(), []string{"show", "."}, strings.NewReader(""), &strings.Builder{}, &strings.Builder{},
	)
	if outcome.ExitCode != 2 || outcome.Err == nil {
		t.Fatalf("outcome = %#v", outcome)
	}
}

func TestStartStreamsInteractiveAgentEventsWithoutShell(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("test fixture is a POSIX program")
	}
	dir := t.TempDir()
	fixture := filepath.Join(dir, "fake-agent")
	script := `#!/bin/sh
set -eu
[ "$#" -eq 2 ]
printf '%s\n%s\n' "$1" "$2" > start-argv
cat > start-stdin
printf 'compiled home'
printf 'validation evidence' >&2
exit 1
`
	if err := os.WriteFile(fixture, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr strings.Builder
	var done Event
	for event := range (Runner{Path: fixture, WorkDir: dir}).Start(context.Background(), []string{"show", "home; $(literal)"}, "source bytes") {
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
	if done.ExitCode != 1 || done.Err == nil || stdout.String() != "compiled home" || stderr.String() != "validation evidence" {
		t.Fatalf("done=%#v stdout=%q stderr=%q", done, stdout.String(), stderr.String())
	}
	argv, err := os.ReadFile(filepath.Join(dir, "start-argv"))
	if err != nil || string(argv) != "show\nhome; $(literal)\n" {
		t.Fatalf("argv=%q err=%v", argv, err)
	}
	input, err := os.ReadFile(filepath.Join(dir, "start-stdin"))
	if err != nil || string(input) != "source bytes" {
		t.Fatalf("stdin=%q err=%v", input, err)
	}
}
