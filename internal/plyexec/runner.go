// Package plyexec owns the process boundary between bench and ply.
package plyexec

import (
	"context"
	"errors"
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

type Client interface {
	Refine(context.Context, RefineRequest) <-chan Event
}

type Runner struct {
	Path      string
	BriefPath string
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
