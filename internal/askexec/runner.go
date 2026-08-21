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
	Input   string
	Session string
	Skills  []string
	Model   string
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
	Path      string
	BriefPath string
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
	if message == "" && req.Input == "" {
		emitFinal(ctx, events, Event{Done: true, ExitCode: 1, Err: errors.New("message and stdin are empty")})
		return
	}
	if req.Session == "" {
		emitFinal(ctx, events, Event{Done: true, ExitCode: 1, Err: errors.New("session path is empty")})
		return
	}
	if err := os.MkdirAll(filepath.Dir(req.Session), 0o700); err != nil {
		emitFinal(ctx, events, Event{Done: true, ExitCode: 1, Err: err})
		return
	}

	path := r.Path
	if path == "" {
		path = "ask"
	}
	args := []string{"-f", req.Session}
	if model := strings.TrimSpace(req.Model); model != "" {
		args = append(args, "-m", model)
	}
	if len(req.Skills) > 0 {
		system, outcome := r.skilledSystem(ctx, path, req.Skills, events)
		if outcome.Err != nil || outcome.ExitCode != 0 {
			emitFinal(ctx, events, Event{Done: true, ExitCode: outcome.ExitCode, Err: outcome.Err})
			return
		}
		args = append(args, "-S", system)
	}
	if message != "" {
		args = append(args, "--", message)
	}
	outcome := filterexec.Execute(ctx, filterexec.Spec{Path: path, Args: args, Stdin: req.Input}, func(stream Stream, text string) {
		emit(ctx, events, Event{Stream: stream, Text: text})
	})
	emitFinal(ctx, events, Event{Done: true, ExitCode: outcome.ExitCode, Err: outcome.Err})
}

func (r Runner) skilledSystem(ctx context.Context, askPath string, skills []string, events chan<- Event) (string, filterexec.Outcome) {
	var parts []string
	var system strings.Builder
	outcome := filterexec.Execute(ctx, filterexec.Spec{Path: askPath, Args: []string{"system"}}, func(stream Stream, text string) {
		if stream == Stdout {
			system.WriteString(text)
		} else {
			emit(ctx, events, Event{Stream: Stderr, Text: text})
		}
	})
	if outcome.Err != nil || outcome.ExitCode != 0 {
		return "", outcome
	}
	if value := strings.TrimSpace(system.String()); value != "" {
		parts = append(parts, value)
	}
	briefPath := r.BriefPath
	if briefPath == "" {
		briefPath = "brief"
	}
	for _, skill := range skills {
		skill = strings.TrimSpace(skill)
		if skill == "" {
			return "", filterexec.Outcome{ExitCode: 2, Err: errors.New("skill name is empty")}
		}
		var body strings.Builder
		outcome = filterexec.Execute(ctx, filterexec.Spec{Path: briefPath, Args: []string{"cat", skill}}, func(stream Stream, text string) {
			if stream == Stdout {
				body.WriteString(text)
			} else {
				emit(ctx, events, Event{Stream: Stderr, Text: text})
			}
		})
		if outcome.Err != nil || outcome.ExitCode != 0 {
			return "", outcome
		}
		if value := strings.TrimSpace(body.String()); value != "" {
			parts = append(parts, value)
		}
	}
	return strings.Join(parts, "\n\n"), filterexec.Outcome{ExitCode: 0}
}

// Replay first proves the append-only log, then asks ask to render it. The
// check's stdout is progress; only the renderer's stdout is transcript data.
func (r Runner) Replay(ctx context.Context, session string) <-chan Event {
	events := make(chan Event, 16)
	go func() {
		defer close(events)
		if strings.TrimSpace(session) == "" {
			emitFinal(ctx, events, Event{Done: true, ExitCode: 1, Err: errors.New("session path is empty")})
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
			emitFinal(ctx, events, Event{Done: true, ExitCode: checked.ExitCode, Err: checked.Err})
			return
		}
		rendered := filterexec.Execute(ctx, filterexec.Spec{Path: path, Args: []string{"replay", session}}, func(stream Stream, text string) {
			emit(ctx, events, Event{Stream: stream, Text: text})
		})
		emitFinal(ctx, events, Event{Done: true, ExitCode: rendered.ExitCode, Err: rendered.Err})
	}()
	return events
}

func emit(ctx context.Context, dst chan<- Event, event Event) {
	select {
	case dst <- event:
	case <-ctx.Done():
	}
}

func emitFinal(ctx context.Context, dst chan<- Event, event Event) {
	select {
	case dst <- event:
		return
	default:
	}
	select {
	case dst <- event:
	case <-ctx.Done():
		select {
		case dst <- event:
		default:
		}
	}
}
