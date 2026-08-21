// Package draftexec owns the process boundary between bench and draft.
package draftexec

import (
	"context"
	"errors"
	"path/filepath"
	"strings"

	"github.com/patrickyoung/bench/internal/filterexec"
)

type Event = filterexec.Event

const (
	Stdout = filterexec.Stdout
	Stderr = filterexec.Stderr
)

// Request is the complete input to draft new. Dir is a path, not a project
// name in a private registry; Description stays one literal argv value.
type Request struct {
	Dir         string
	Description string
	Model       string
}

type BuildRequest struct {
	Dir   string
	Model string
}

// Client is the part of draft the current TUI slice composes.
type Client interface {
	New(context.Context, Request) <-chan Event
	Check(context.Context, string) <-chan Event
	Build(context.Context, BuildRequest) <-chan Event
	Prove(context.Context, string) <-chan Event
}

// Runner invokes an installed draft program directly, without a shell.
type Runner struct {
	Path      string
	AskPath   string
	BriefPath string
	PlyPath   string
	HonePath  string
	WorkDir   string
}

func (r Runner) New(ctx context.Context, req Request) <-chan Event {
	dir := strings.TrimSpace(req.Dir)
	description := strings.TrimSpace(req.Description)
	if dir == "" {
		return failed("project directory is empty")
	}
	if description == "" {
		return failed("requirements are empty")
	}
	env := append(r.toolEnv(), modelEnv(req.Model)...)
	return filterexec.Start(ctx, filterexec.Spec{
		Path: r.path(),
		Args: []string{"new", dir, description},
		Dir:  r.WorkDir,
		Env:  env,
	})
}

func (r Runner) Check(ctx context.Context, dir string) <-chan Event {
	dir = strings.TrimSpace(dir)
	if dir == "" {
		return failed("project directory is empty")
	}
	return filterexec.Start(ctx, filterexec.Spec{
		Path: r.path(),
		Args: []string{"check", dir},
		Dir:  r.WorkDir,
		Env:  r.toolEnv(),
	})
}

// Build delegates the complete loop to draft/ply. PLY_DIR is placed beside
// DESIGN.md so the agent's replayable evidence travels with its project.
func (r Runner) Build(ctx context.Context, req BuildRequest) <-chan Event {
	dir := strings.TrimSpace(req.Dir)
	if dir == "" {
		return failed("project directory is empty")
	}
	env := append(r.toolEnv(), "PLY_DIR="+filepath.Join(dir, ".draft", "build"))
	env = append(env, modelEnv(req.Model)...)
	return filterexec.Start(ctx, filterexec.Spec{
		Path: r.path(),
		Args: []string{"build", dir},
		Dir:  r.WorkDir,
		Env:  env,
	})
}

func modelEnv(model string) []string {
	if model = strings.TrimSpace(model); model != "" {
		return []string{"ASK_MODEL=" + model}
	}
	return nil
}

// Prove mechanically mutates the project and lets its check detect the
// changes. Exit 1 is a valid negative evaluation, not a process failure.
func (r Runner) Prove(ctx context.Context, dir string) <-chan Event {
	dir = strings.TrimSpace(dir)
	if dir == "" {
		return failed("project directory is empty")
	}
	return filterexec.Start(ctx, filterexec.Spec{
		Path: r.path(),
		Args: []string{"prove", dir},
		Dir:  r.WorkDir,
		Env:  r.toolEnv(),
	})
}

func (r Runner) path() string {
	if r.Path != "" {
		return r.Path
	}
	return "draft"
}

func (r Runner) toolEnv() []string {
	var env []string
	for _, tool := range []struct{ name, path string }{
		{"ASK", r.AskPath}, {"BRIEF", r.BriefPath}, {"PLY", r.PlyPath}, {"HONE", r.HonePath},
	} {
		if path := strings.TrimSpace(tool.path); path != "" {
			env = append(env, tool.name+"="+path)
		}
	}
	return env
}

func failed(message string) <-chan Event {
	events := make(chan Event, 1)
	events <- Event{Done: true, ExitCode: 2, Err: errors.New(message)}
	close(events)
	return events
}
