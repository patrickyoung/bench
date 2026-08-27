// Package agentexec owns Bench's transparent process boundary to the public
// agent executable. Agent-home parsing, policy, evidence, and execution stay
// entirely inside that standalone program.
package agentexec

import (
	"context"
	"errors"
	"io"
	"os/exec"
	"strings"

	"github.com/patrickyoung/bench/internal/filterexec"
)

type Event = filterexec.Event

const (
	Stdout = filterexec.Stdout
	Stderr = filterexec.Stderr
)

type Outcome struct {
	ExitCode int
	Err      error
}

// Start runs one public agent command asynchronously for an interactive
// controller. Arguments remain literal and the standalone executable keeps
// ownership of every command's meaning.
func (r Runner) Start(ctx context.Context, args []string, stdin string) <-chan Event {
	if len(args) == 0 {
		events := make(chan Event, 1)
		events <- Event{Done: true, ExitCode: 2, Err: errors.New("agent command is empty")}
		close(events)
		return events
	}
	return filterexec.Start(ctx, filterexec.Spec{
		Path:  r.path(),
		Args:  append([]string(nil), args...),
		Dir:   r.WorkDir,
		Env:   r.toolEnv(),
		Stdin: stdin,
	})
}

// Runner invokes an installed agent program directly, without a shell.
type Runner struct {
	Path      string
	PlyPath   string
	BriefPath string
	CagePath  string
	HonePath  string
	TrailPath string
	AskPath   string
	MayPath   string
	WorkDir   string
}

func (r Runner) Run(ctx context.Context, args []string, stdin io.Reader, stdout, stderr io.Writer) Outcome {
	if len(args) == 0 {
		return Outcome{ExitCode: 2, Err: errors.New("agent command is empty")}
	}
	outcome := filterexec.RunAttached(ctx, filterexec.AttachedSpec{
		Path:   r.path(),
		Args:   append([]string(nil), args...),
		Dir:    r.WorkDir,
		Env:    r.toolEnv(),
		Stdin:  stdin,
		Stdout: stdout,
		Stderr: stderr,
	})
	if ctx.Err() != nil {
		return Outcome{ExitCode: 130}
	}
	if outcome.Err != nil {
		var exitErr *exec.ExitError
		if !errors.As(outcome.Err, &exitErr) {
			return Outcome{ExitCode: 2, Err: outcome.Err}
		}
	}
	return Outcome{ExitCode: outcome.ExitCode}
}

func (r Runner) path() string {
	if path := strings.TrimSpace(r.Path); path != "" {
		return path
	}
	return "agent"
}

func (r Runner) toolEnv() []string {
	var env []string
	for _, tool := range []struct{ name, path string }{
		{"AGENT_PLY", r.PlyPath},
		{"AGENT_BRIEF", r.BriefPath},
		{"AGENT_CAGE", r.CagePath},
		{"AGENT_HONE", r.HonePath},
		{"AGENT_TRAIL", r.TrailPath},
		{"AGENT_ASK", r.AskPath},
		{"AGENT_MAY", r.MayPath},
	} {
		if path := strings.TrimSpace(tool.path); path != "" {
			env = append(env, tool.name+"="+path)
		}
	}
	return env
}
