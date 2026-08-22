// Package askexec owns the process boundary between bench and ask.
package askexec

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
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
	Effort  string
	System  string
	Schema  string
}

// Event is either a stream chunk or the final process outcome. Exactly one
// event has Done set.
type Event = filterexec.Event

// Starter begins an ask turn and returns its ordered event stream.
type Starter interface {
	Start(context.Context, Request) <-chan Event
}

type RecordRequest struct {
	Session string
	Source  string
	Kind    string
	JSON    string
}

// Recorder appends a typed, sealed program record without changing the model
// conversation. Contract admission and verifier evidence use this boundary.
type Recorder interface {
	Record(context.Context, RecordRequest) error
}

type Client interface {
	Starter
	Recorder
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

func (r Runner) Record(ctx context.Context, req RecordRequest) error {
	if strings.TrimSpace(req.Session) == "" || strings.TrimSpace(req.Source) == "" || strings.TrimSpace(req.Kind) == "" {
		return errors.New("sealed record needs session, source, and kind")
	}
	if !json.Valid([]byte(req.JSON)) {
		return errors.New("sealed record body is not valid JSON")
	}
	path := r.Path
	if path == "" {
		path = "ask"
	}
	var diagnostic strings.Builder
	outcome := filterexec.Execute(ctx, filterexec.Spec{
		Path: path,
		Args: []string{"note", "-q", "-s", req.Source, "-f", req.Session,
			"-k", req.Kind, "-json", "-", "-seal"},
		Stdin: req.JSON,
	}, func(stream Stream, text string) {
		if stream == Stderr {
			diagnostic.WriteString(text)
		}
	})
	if outcome.Err != nil {
		if detail := strings.TrimSpace(diagnostic.String()); detail != "" {
			return fmt.Errorf("record %s: %w: %s", req.Kind, outcome.Err, detail)
		}
		return fmt.Errorf("record %s: %w", req.Kind, outcome.Err)
	}
	return nil
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
	if effort := strings.TrimSpace(req.Effort); effort != "" {
		args = append(args, "-effort", effort)
	}
	if len(req.Skills) > 0 {
		system, outcome := r.skilledSystem(ctx, path, req.System, req.Skills, events)
		if outcome.Err != nil || outcome.ExitCode != 0 {
			emitFinal(ctx, events, Event{Done: true, ExitCode: outcome.ExitCode, Err: outcome.Err})
			return
		}
		args = append(args, "-S", system)
	} else if req.System != "" {
		args = append(args, "-S", req.System)
	}
	if req.Schema != "" {
		schema, err := writeSchema(filepath.Dir(req.Session), req.Schema)
		if err != nil {
			emitFinal(ctx, events, Event{Done: true, ExitCode: 1, Err: err})
			return
		}
		defer os.Remove(schema)
		args = append(args, "-schema", schema)
	}
	if message != "" {
		args = append(args, "--", message)
	}
	outcome := filterexec.Execute(ctx, filterexec.Spec{Path: path, Args: args, Stdin: req.Input}, func(stream Stream, text string) {
		emit(ctx, events, Event{Stream: stream, Text: text})
	})
	emitFinal(ctx, events, Event{Done: true, ExitCode: outcome.ExitCode, Err: outcome.Err})
}

func writeSchema(dir, schema string) (string, error) {
	f, err := os.CreateTemp(dir, ".bench-schema-*.json")
	if err != nil {
		return "", fmt.Errorf("create Ask schema: %w", err)
	}
	path := f.Name()
	ok := false
	defer func() {
		if !ok {
			_ = os.Remove(path)
		}
	}()
	if err := f.Chmod(0o600); err != nil {
		_ = f.Close()
		return "", fmt.Errorf("protect Ask schema: %w", err)
	}
	if _, err := f.WriteString(schema); err != nil {
		_ = f.Close()
		return "", fmt.Errorf("write Ask schema: %w", err)
	}
	if err := f.Close(); err != nil {
		return "", fmt.Errorf("close Ask schema: %w", err)
	}
	ok = true
	return path, nil
}

func (r Runner) skilledSystem(ctx context.Context, askPath, base string, skills []string, events chan<- Event) (string, filterexec.Outcome) {
	var parts []string
	var outcome filterexec.Outcome
	if value := strings.TrimSpace(base); value != "" {
		parts = append(parts, value)
	} else {
		var system strings.Builder
		outcome = filterexec.Execute(ctx, filterexec.Spec{Path: askPath, Args: []string{"system"}}, func(stream Stream, text string) {
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
