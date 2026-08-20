// Package askexec owns the process boundary between bench and ask.
package askexec

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"

	"github.com/patrickyoung/bench/internal/filterexec"
)

// Stream identifies which part of ask's Unix contract produced a chunk.
type Stream = filterexec.Stream

const (
	Stdout = filterexec.Stdout
	Stderr = filterexec.Stderr
)

// Request is one ask turn. Session is always explicit so bench never moves or
// guesses ask's global current conversation.
type Request struct {
	Message string
	Session string
}

// Event is either a stream chunk or the final process outcome. Exactly one
// event has Done set.
type Event = filterexec.Event

// Starter begins an ask turn and returns its ordered event stream.
type Starter interface {
	Start(context.Context, Request) <-chan Event
}

// Replayer verifies and then renders an existing ask session. Verification is
// deliberately part of this boundary: bench never resumes a log ask cannot
// prove.
type Replayer interface {
	Replay(context.Context, string) <-chan Event
}

// Runner invokes an installed ask executable directly, without a shell.
type Runner struct {
	Path string
}

// Start starts ask. It never blocks the caller.
func (r Runner) Start(ctx context.Context, req Request) <-chan Event {
	events := make(chan Event, 16)
	go r.runTurn(ctx, req, events)
	return events
}

func (r Runner) runTurn(ctx context.Context, req Request, events chan<- Event) {
	defer close(events)

	message := strings.TrimSpace(req.Message)
	if message == "" {
		events <- Event{Done: true, ExitCode: 1, Err: errors.New("message is empty")}
		return
	}
	if req.Session == "" {
		events <- Event{Done: true, ExitCode: 1, Err: errors.New("session path is empty")}
		return
	}
	if err := os.MkdirAll(filepath.Dir(req.Session), 0o700); err != nil {
		events <- Event{Done: true, ExitCode: 1, Err: err}
		return
	}

	path := r.Path
	if path == "" {
		path = "ask"
	}
	outcome := filterexec.Execute(ctx, filterexec.Spec{Path: path, Args: []string{"-f", req.Session, "--", message}}, func(stream Stream, text string) {
		emit(ctx, events, Event{Stream: stream, Text: text})
	})
	events <- Event{Done: true, ExitCode: outcome.ExitCode, Err: outcome.Err}
}

// Replay first proves the append-only log, then asks ask to render it. The
// check's stdout is progress; only the renderer's stdout is transcript data.
func (r Runner) Replay(ctx context.Context, session string) <-chan Event {
	events := make(chan Event, 16)
	go func() {
		defer close(events)
		if strings.TrimSpace(session) == "" {
			events <- Event{Done: true, ExitCode: 1, Err: errors.New("session path is empty")}
			return
		}
		path := r.Path
		if path == "" {
			path = "ask"
		}
		checked := filterexec.Execute(ctx, filterexec.Spec{Path: path, Args: []string{"replay", "-check", session}}, func(_ Stream, text string) {
			emit(ctx, events, Event{Stream: Stderr, Text: text})
		})
		if checked.Err != nil {
			events <- Event{Done: true, ExitCode: checked.ExitCode, Err: checked.Err}
			return
		}
		rendered := filterexec.Execute(ctx, filterexec.Spec{Path: path, Args: []string{"replay", session}}, func(stream Stream, text string) {
			emit(ctx, events, Event{Stream: stream, Text: text})
		})
		events <- Event{Done: true, ExitCode: rendered.ExitCode, Err: rendered.Err}
	}()
	return events
}

func emit(ctx context.Context, dst chan<- Event, event Event) {
	select {
	case dst <- event:
	case <-ctx.Done():
	}
}
