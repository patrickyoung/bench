// Package filterexec runs one Unix filter while preserving its three-part
// contract: stdout, stderr, and exit status.
package filterexec

import (
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"
)

type Stream uint8

const (
	Stdout Stream = iota + 1
	Stderr
)

// Event is either one stream chunk or the final process outcome.
type Event struct {
	Stream   Stream
	Text     string
	Done     bool
	ExitCode int
	Err      error
}

// Spec names one program invocation. Args are passed directly, never through
// a shell. Dir is optional.
type Spec struct {
	Path  string
	Args  []string
	Dir   string
	Env   []string
	Stdin string
}

// Outcome is the terminal process result.
type Outcome struct {
	ExitCode int
	Err      error
}

// Start executes one filter asynchronously.
func Start(ctx context.Context, spec Spec) <-chan Event {
	events := make(chan Event, 16)
	go func() {
		defer close(events)
		outcome := Execute(ctx, spec, func(stream Stream, text string) {
			emit(ctx, events, Event{Stream: stream, Text: text})
		})
		emitFinal(ctx, events, Event{Done: true, ExitCode: outcome.ExitCode, Err: outcome.Err})
	}()
	return events
}

// Execute runs one filter synchronously and calls onChunk from reader
// goroutines. Callers that mutate state in onChunk must synchronize it.
func Execute(ctx context.Context, spec Spec, onChunk func(Stream, string)) Outcome {
	cmd := exec.CommandContext(ctx, spec.Path, spec.Args...)
	cmd.Dir = spec.Dir
	if spec.Stdin != "" {
		cmd.Stdin = strings.NewReader(spec.Stdin)
	}
	if len(spec.Env) > 0 {
		cmd.Env = overlayEnv(os.Environ(), spec.Env)
	}
	configureProcess(cmd)
	// Give a cooperative filter time to record interruption before Go closes
	// its pipes and forces it down.
	cmd.Cancel = func() error {
		if cmd.Process == nil {
			return nil
		}
		return interruptProcess(cmd)
	}
	cmd.WaitDelay = 2 * time.Second

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return Outcome{ExitCode: 1, Err: err}
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return Outcome{ExitCode: 1, Err: err}
	}
	if err := cmd.Start(); err != nil {
		return Outcome{ExitCode: 1, Err: err}
	}

	var readers sync.WaitGroup
	readers.Add(2)
	go copyChunks(&readers, stdout, Stdout, onChunk)
	go copyChunks(&readers, stderr, Stderr, onChunk)
	waitErr := cmd.Wait()
	readers.Wait()

	code := 0
	if waitErr != nil {
		code = 1
		var exitErr *exec.ExitError
		if errors.As(waitErr, &exitErr) {
			code = exitErr.ExitCode()
		}
	}
	if ctx.Err() != nil {
		waitErr = ctx.Err()
	}
	return Outcome{ExitCode: code, Err: waitErr}
}

func overlayEnv(base, overrides []string) []string {
	for _, override := range overrides {
		key, _, ok := strings.Cut(override, "=")
		if !ok || key == "" {
			continue
		}
		prefix := key + "="
		kept := base[:0]
		for _, item := range base {
			if !strings.HasPrefix(item, prefix) {
				kept = append(kept, item)
			}
		}
		base = append(kept, override)
	}
	return base
}

func copyChunks(wg *sync.WaitGroup, src io.Reader, stream Stream, onChunk func(Stream, string)) {
	defer wg.Done()
	buf := make([]byte, 4096)
	for {
		n, err := src.Read(buf)
		if n > 0 {
			onChunk(stream, string(buf[:n]))
		}
		if err != nil {
			return
		}
	}
}

func emit(ctx context.Context, dst chan<- Event, event Event) {
	select {
	case dst <- event:
	case <-ctx.Done():
	}
}

// emitFinal delivers the outcome when an active consumer has room, including
// after cancellation, but never strands the process goroutine behind an
// abandoned full event channel. Stream chunks are already best-effort after
// cancellation; the terminal event must follow the same ownership rule.
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
