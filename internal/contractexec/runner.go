package contractexec

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/patrickyoung/bench/internal/askexec"
	"github.com/patrickyoung/bench/internal/plyexec"
)

type Runner struct {
	Ask askexec.Client
	Ply plyexec.Worker
}

func (r Runner) Work(ctx context.Context, req plyexec.TaskRequest) <-chan plyexec.Event {
	if err := plyexec.Validate(req); err != nil {
		events := make(chan plyexec.Event, 1)
		events <- plyexec.Event{Done: true, ExitCode: 1, Err: err}
		close(events)
		return events
	}
	if !req.Options.IntentContract {
		return r.Ply.Work(ctx, req)
	}
	events := make(chan plyexec.Event, 16)
	go r.run(ctx, req, events)
	return events
}

func (r Runner) run(ctx context.Context, req plyexec.TaskRequest, events chan<- plyexec.Event) {
	defer close(events)
	if r.Ask == nil {
		emitFinal(ctx, events, plyexec.Event{Done: true, ExitCode: 1, Err: errors.New("intent compiler needs ask")})
		return
	}
	if r.Ply == nil {
		emitFinal(ctx, events, plyexec.Event{Done: true, ExitCode: 1, Err: errors.New("intent compiler needs ply")})
		return
	}
	message := compilerMessage(req)
	var answer strings.Builder
	var outcome askexec.Event
	for event := range r.Ask.Start(ctx, askexec.Request{
		Message: message,
		Input:   Evidence(req.Dir, req.Input),
		Session: req.Session,
		Model:   req.Model,
		Effort:  req.Options.Effort,
		System:  System,
		Schema:  Schema,
	}) {
		if event.Done {
			outcome = event
			continue
		}
		if event.Stream == askexec.Stdout {
			answer.WriteString(event.Text)
		} else {
			emit(ctx, events, plyexec.Event{Stream: plyexec.Stderr, Text: event.Text})
		}
	}
	if outcome.Err != nil || outcome.ExitCode != 0 {
		emitFinal(ctx, events, plyexec.Event{Done: true, ExitCode: outcome.ExitCode, Err: outcome.Err, Session: req.Session})
		return
	}
	contract, canonical, digest, err := Parse(answer.String())
	if err != nil {
		emitFinal(ctx, events, plyexec.Event{Done: true, ExitCode: 1, Err: err, Session: req.Session})
		return
	}
	recordBody, err := json.Marshal(struct {
		Status       string          `json:"status"`
		Admission    string          `json:"admission"`
		ContractID   string          `json:"contract_id"`
		IntentSHA256 string          `json:"intent_sha256"`
		Contract     json.RawMessage `json:"contract"`
	}{
		Status: "admitted", Admission: "bench-default",
		ContractID: "sha256:" + digest, IntentSHA256: sha256Text(req.Goal),
		Contract: json.RawMessage(canonical),
	})
	if err != nil {
		emitFinal(ctx, events, plyexec.Event{Done: true, ExitCode: 1, Err: err, Session: req.Session})
		return
	}
	if err := r.Ask.Record(ctx, askexec.RecordRequest{
		Session: req.Session, Source: "bench", Kind: "bench.contract/v1", JSON: string(recordBody),
	}); err != nil {
		emitFinal(ctx, events, plyexec.Event{Done: true, ExitCode: 1, Err: fmt.Errorf("record outcome contract: %w", err), Session: req.Session})
		return
	}
	emit(ctx, events, plyexec.Event{Contract: Render(contract, digest), ContractDigest: digest})

	work := req
	work.Options.ContractID = "sha256:" + digest
	work.Goal = workGoal(req.Goal, canonical, digest, req.Options.Check)
	for event := range r.Ply.Work(ctx, work) {
		// The contract turn already created the session. Ply intentionally does
		// not create one when a pre-check passes, so keep the real session
		// visible instead of claiming that no model turn occurred.
		if event.Done && event.Session == "" {
			event.Session = req.Session
		}
		emit(ctx, events, event)
	}
}

func sha256Text(s string) string {
	sum := sha256.Sum256([]byte(s))
	return "sha256:" + hex.EncodeToString(sum[:])
}

func compilerMessage(req plyexec.TaskRequest) string {
	verifier := "No executable verifier is configured. Distinguish evidence the worker can gather from claims only a person can judge."
	if req.Options.Check != "" {
		verifier = "The operator configured this exact executable verifier; align relevant criteria with what it actually observes, without treating it as broader proof:\n" + req.Options.Check
	}
	return "Compile this user intent into an outcome contract.\n\nUSER INTENT\n" + req.Goal + "\n\nVERIFIER BOUNDARY\n" + verifier
}

func workGoal(intent, contract, digest, check string) string {
	verifier := "No executable verifier is configured. You may stop after reporting evidence, but must not describe the outcome as externally verified."
	if check != "" {
		verifier = "The operator's fixed verifier still decides executable completion. Its exact command is:\n" + check
	}
	return fmt.Sprintf(`Pursue the user's outcome under this admitted contract.

ORIGINAL INTENT
%s

OUTCOME CONTRACT v%d
sha256 %s
%s

WORKING RULES
- Address every deliverable, invariant, and acceptance criterion.
- Gather the named evidence; do not replace evidence with a claim that work is done.
- Approval boundaries remain boundaries: do not cross one without the required approval.
- Open questions block only the affected irreversible or materially ambiguous step; continue safe reversible work where possible.
- Do not silently weaken or amend this contract. If evidence makes it wrong or impossible, report the exact proposed amendment and why.
- %s
`, intent, Version, digest, contract, verifier)
}

func emit(ctx context.Context, dst chan<- plyexec.Event, event plyexec.Event) {
	select {
	case dst <- event:
	case <-ctx.Done():
	}
}

func emitFinal(ctx context.Context, dst chan<- plyexec.Event, event plyexec.Event) {
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
