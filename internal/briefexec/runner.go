// Package briefexec owns the process boundary between bench and brief.
package briefexec

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

type NewRequest struct {
	Directory string
	Name      string
}

// Client is exactly the public brief surface the Skills UI composes.
type Client interface {
	List(context.Context) <-chan Event
	Path(context.Context, string) <-chan Event
	Cat(context.Context, string) <-chan Event
	Files(context.Context, string) <-chan Event
	Lint(context.Context, string) <-chan Event
	New(context.Context, NewRequest) <-chan Event
}

type Runner struct {
	Binary  string
	WorkDir string
}

func (r Runner) List(ctx context.Context) <-chan Event {
	return r.start(ctx, "ls")
}

func (r Runner) Path(ctx context.Context, name string) <-chan Event {
	if name = strings.TrimSpace(name); name == "" {
		return failed("skill name is empty")
	}
	return r.start(ctx, "path", name)
}

func (r Runner) Cat(ctx context.Context, ref string) <-chan Event {
	if ref = strings.TrimSpace(ref); ref == "" {
		return failed("skill reference is empty")
	}
	return r.start(ctx, "cat", ref)
}

func (r Runner) Files(ctx context.Context, name string) <-chan Event {
	if name = strings.TrimSpace(name); name == "" {
		return failed("skill name is empty")
	}
	return r.start(ctx, "ls", name)
}

func (r Runner) Lint(ctx context.Context, target string) <-chan Event {
	if target = strings.TrimSpace(target); target == "" {
		return failed("skill target is empty")
	}
	return r.start(ctx, "lint", "-strict", target)
}

func (r Runner) New(ctx context.Context, req NewRequest) <-chan Event {
	dir := strings.TrimSpace(req.Directory)
	name := strings.TrimSpace(req.Name)
	if dir == "" {
		return failed("skill directory is empty")
	}
	if name == "" {
		return failed("skill name is empty")
	}
	return r.start(ctx, "new", "-d", dir, name)
}

func (r Runner) start(ctx context.Context, args ...string) <-chan Event {
	path := r.Binary
	if path == "" {
		path = "brief"
	}
	return filterexec.Start(ctx, filterexec.Spec{Path: path, Args: args, Dir: r.WorkDir})
}

func failed(message string) <-chan Event {
	events := make(chan Event, 1)
	events <- Event{Done: true, ExitCode: 2, Err: errors.New(message)}
	close(events)
	return events
}
