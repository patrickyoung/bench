// Package honeexec owns the process boundary between bench and hone.
package honeexec

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

// Request names the verified build session and the ordinary brief skill that
// should receive any admitted lesson.
type Request struct {
	Session string
	Skill   string
}

type Client interface {
	Learn(context.Context, Request) <-chan Event
}

// Runner invokes hone directly. Flags precede the session because that is
// part of hone's public CLI contract.
type Runner struct {
	Path      string
	AskPath   string
	BriefPath string
	WorkDir   string
}

func (r Runner) Learn(ctx context.Context, req Request) <-chan Event {
	session := strings.TrimSpace(req.Session)
	skill := strings.TrimSpace(req.Skill)
	if session == "" {
		return failed("build session is empty")
	}
	if skill == "" {
		return failed("skill name is empty")
	}
	return filterexec.Start(ctx, filterexec.Spec{
		Path: r.path(),
		Args: []string{"-into", skill, session},
		Dir:  r.WorkDir,
		Env:  r.toolEnv(),
	})
}

func (r Runner) toolEnv() []string {
	var env []string
	if path := strings.TrimSpace(r.AskPath); path != "" {
		env = append(env, "ASK="+path)
	}
	if path := strings.TrimSpace(r.BriefPath); path != "" {
		env = append(env, "BRIEF="+path)
	}
	return env
}

func (r Runner) path() string {
	if r.Path != "" {
		return r.Path
	}
	return "hone"
}

func failed(message string) <-chan Event {
	events := make(chan Event, 1)
	events <- Event{Done: true, ExitCode: 2, Err: errors.New(message)}
	close(events)
	return events
}
