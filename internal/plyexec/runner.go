// Package plyexec owns the process boundary between bench and ply.
package plyexec

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"

	"github.com/patrickyoung/bench/internal/filterexec"
)

type Event = filterexec.Event

const (
	Stdout = filterexec.Stdout
	Stderr = filterexec.Stderr
)

type RefineRequest struct {
	Dir        string
	SourceRoot string
	Goal       string
	Source     string
	SessionDir string
}

// TaskRequest is one open-ended workspace task. An empty Toolbox deliberately
// means ply's full-shell mode; callers must make that grant visible rather than
// presenting it as a sandbox.
type TaskRequest struct {
	Dir     string
	Goal    string
	Session string
	Skills  []string
	Toolbox string
}

type Client interface {
	Refine(context.Context, RefineRequest) <-chan Event
}

type Worker interface {
	Work(context.Context, TaskRequest) <-chan Event
}

type Runner struct {
	Path      string
	AskPath   string
	BriefPath string
}

// Work lets ply compose Ask with ordinary programs in the workspace. The
// goal and paths are literal argv values; no user text is evaluated by bench.
// With no explicit toolbox, -sh is intentional: ply documents that this is a
// full shell grant and the TUI labels it as such.
func (r Runner) Work(ctx context.Context, req TaskRequest) <-chan Event {
	dir := strings.TrimSpace(req.Dir)
	goal := strings.TrimSpace(req.Goal)
	session := strings.TrimSpace(req.Session)
	if dir == "" {
		return failed("task workspace is empty")
	}
	if goal == "" {
		return failed("task goal is empty")
	}
	if session == "" {
		return failed("task session is empty")
	}
	if err := os.MkdirAll(filepath.Dir(session), 0o700); err != nil {
		return failed(err.Error())
	}
	path := r.Path
	if path == "" {
		path = "ply"
	}
	args := []string{}
	if toolbox := strings.TrimSpace(req.Toolbox); toolbox != "" {
		args = append(args, "-t", toolbox)
	} else {
		args = append(args, "-sh")
	}
	args = append(args, "-C", dir, "-f", session)
	for _, skill := range req.Skills {
		skill = strings.TrimSpace(skill)
		if skill == "" {
			return failed("skill name is empty")
		}
		args = append(args, "-s", skill)
	}
	args = append(args, "--", goal)
	env := []string{}
	if ask := strings.TrimSpace(r.AskPath); ask != "" {
		env = append(env, "ASK="+ask)
	}
	if brief := strings.TrimSpace(r.BriefPath); brief != "" {
		env = append(env, "BRIEF="+brief)
	}
	return filterexec.Start(ctx, filterexec.Spec{Path: path, Args: args, Dir: dir, Env: env})
}

// Refine lets ply edit the ordinary skill directory. The fixed check invokes
// the configured brief through a quoted environment expansion; user source
// stays on stdin and user feedback stays one literal argv value.
func (r Runner) Refine(ctx context.Context, req RefineRequest) <-chan Event {
	dir := strings.TrimSpace(req.Dir)
	goal := strings.TrimSpace(req.Goal)
	source := strings.TrimSpace(req.Source)
	sourceRoot := strings.TrimSpace(req.SourceRoot)
	sessions := strings.TrimSpace(req.SessionDir)
	if dir == "" {
		return failed("skill directory is empty")
	}
	if goal == "" {
		return failed("refinement goal is empty")
	}
	if source == "" {
		return failed("source material is empty")
	}
	if sourceRoot == "" {
		sourceRoot = dir
	}
	if sessions == "" {
		return failed("refinement session directory is empty")
	}
	path := r.Path
	if path == "" {
		path = "ply"
	}
	briefPath := r.BriefPath
	if briefPath == "" {
		briefPath = "brief"
	}
	return filterexec.Start(ctx, filterexec.Spec{
		Path:  path,
		Args:  []string{"-sh", "-check", `"$BRIEF" lint -strict .`, goal},
		Dir:   dir,
		Env:   []string{"BRIEF=" + briefPath, "PLY_DIR=" + sessions, "SOURCE_ROOT=" + sourceRoot},
		Stdin: req.Source,
	})
}

func failed(message string) <-chan Event {
	events := make(chan Event, 1)
	events <- Event{Done: true, ExitCode: 2, Err: errors.New(message)}
	close(events)
	return events
}
