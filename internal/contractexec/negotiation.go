package contractexec

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/patrickyoung/bench/internal/askexec"
	"github.com/patrickyoung/bench/internal/filterexec"
	"github.com/patrickyoung/bench/internal/plyexec"
)

// Draft is a proposed contract plus the controller inputs that give its
// eventual envelope ID meaning. Contract remains ordinary canonical JSON so a
// person can edit it with any editor before admission.
type Draft struct {
	Schema                 int             `json:"schema"`
	OutcomeID              string          `json:"outcome_id"`
	Generation             int             `json:"generation"`
	ParentRevisionID       string          `json:"parent_revision_id,omitempty"`
	RevisionID             string          `json:"revision_id,omitempty"`
	DraftSHA256            string          `json:"draft_sha256,omitempty"`
	RecordedDraftSHA256    string          `json:"recorded_draft_sha256,omitempty"`
	Intent                 string          `json:"intent"`
	Workspace              string          `json:"workspace"`
	Toolbox                string          `json:"toolbox,omitempty"`
	Contract               json.RawMessage `json:"contract"`
	ContractID             string          `json:"contract_id"`
	ContractSHA256         string          `json:"contract_sha256"`
	CompilerEvidenceSHA256 string          `json:"compiler_evidence_sha256"`
	Check                  string          `json:"check,omitempty"`
	CheckSHA256            string          `json:"check_sha256"`
	CheckAll               bool            `json:"check_all"`
	Skills                 []string        `json:"skills"`
}

// DraftRequest compiles an initial proposal or revises Current according to
// Instruction. Task.Goal always remains the original user intent.
type DraftRequest struct {
	Task        plyexec.TaskRequest
	Current     *Draft
	Instruction string
	Store       DraftStore
}

// DraftEvent is the compiler's ordinary process stream followed by exactly
// one terminal event. A successful terminal event carries the durable draft.
type DraftEvent struct {
	Stream   filterexec.Stream
	Text     string
	Draft    *Draft
	Done     bool
	ExitCode int
	Err      error
}

// Negotiator is the controller boundary used by the interactive workbench.
// Compile never invokes Ply; Admit never asks a model to rewrite the contract.
type Negotiator interface {
	Compile(context.Context, DraftRequest) <-chan DraftEvent
	Import(context.Context, ImportRequest) (Draft, error)
	Admit(context.Context, AdmitRequest) <-chan plyexec.Event
	Run(context.Context, RunRequest) <-chan plyexec.Event
}

type ImportRequest struct {
	Session string
	Store   DraftStore
}

type AdmitRequest struct {
	Task                plyexec.TaskRequest
	Draft               Draft
	Store               DraftStore
	ExpectedDraftSHA256 string
}

// RunRequest explicitly starts another attempt under an already admitted
// immutable revision. Guidance may steer implementation, but cannot amend the
// admitted outcome contract.
type RunRequest struct {
	Task     plyexec.TaskRequest
	Draft    Draft
	Store    DraftStore
	Guidance string
}

func (r Runner) Compile(ctx context.Context, req DraftRequest) <-chan DraftEvent {
	events := make(chan DraftEvent, 16)
	go r.compile(ctx, req, events)
	return events
}

// Import validates the controller-owned editable file, normalizes it through
// the same Contract parser as model proposals, and seals a manual proposal
// record. It never has a Ply dependency.
func (r Runner) Import(ctx context.Context, req ImportRequest) (Draft, error) {
	if r.Ask == nil {
		return Draft{}, errors.New("manual contract import needs ask")
	}
	if req.Store == nil {
		return Draft{}, errors.New("manual contract import needs a durable store")
	}
	draft, status, err := req.Store.Load()
	if err != nil {
		return Draft{}, err
	}
	if status != "draft" {
		return Draft{}, errors.New("contract is not in draft state")
	}
	draft, err = req.Store.SaveDraftCAS(draft, draft.DraftSHA256)
	if err != nil {
		return Draft{}, err
	}
	body, err := json.Marshal(struct {
		Status string `json:"status"`
		Source string `json:"source"`
		Path   string `json:"path"`
		Draft
	}{Status: "proposed", Source: "manual", Path: req.Store.DraftPath(), Draft: draft})
	if err != nil {
		return Draft{}, err
	}
	if err := r.Ask.Record(ctx, askexec.RecordRequest{
		Session: req.Session, Source: "bench-user", Kind: "bench.contract-proposal/v1", JSON: string(body),
	}); err != nil {
		return Draft{}, fmt.Errorf("record manual contract proposal: %w", err)
	}
	draft, err = req.Store.MarkDraftRecorded(draft)
	if err != nil {
		return Draft{}, fmt.Errorf("mark manual contract proposal durable: %w", err)
	}
	return draft, nil
}

func (r Runner) compile(ctx context.Context, req DraftRequest, events chan<- DraftEvent) {
	defer close(events)
	if r.Ask == nil {
		emitDraftFinal(ctx, events, DraftEvent{Done: true, ExitCode: 1, Err: errors.New("intent compiler needs ask")})
		return
	}
	if err := plyexec.Validate(req.Task); err != nil {
		emitDraftFinal(ctx, events, DraftEvent{Done: true, ExitCode: 1, Err: err})
		return
	}
	if !req.Task.Options.IntentContract {
		emitDraftFinal(ctx, events, DraftEvent{Done: true, ExitCode: 1, Err: errors.New("contract negotiation is off")})
		return
	}
	if req.Store == nil {
		emitDraftFinal(ctx, events, DraftEvent{Done: true, ExitCode: 1, Err: errors.New("contract negotiation needs a durable store")})
		return
	}
	message := compilerMessage(req.Task)
	generation := 1
	outcomeID := ""
	parent := ""
	if req.Current != nil {
		current, err := UpdateDraft(*req.Current, string(req.Current.Contract))
		if err != nil {
			emitDraftFinal(ctx, events, DraftEvent{Done: true, ExitCode: 1, Err: fmt.Errorf("read current contract draft: %w", err)})
			return
		}
		if strings.TrimSpace(req.Instruction) == "" {
			emitDraftFinal(ctx, events, DraftEvent{Done: true, ExitCode: 1, Err: errors.New("contract revision instruction is empty")})
			return
		}
		generation = current.Generation + 1
		outcomeID = current.OutcomeID
		parent = current.ParentRevisionID
		message = revisionMessage(current, req.Instruction, req.Task.Options.Check, req.Task.Options.CheckAllCriteria)
	} else {
		id, err := newOutcomeID()
		if err != nil {
			emitDraftFinal(ctx, events, DraftEvent{Done: true, ExitCode: 1, Err: err})
			return
		}
		outcomeID = id
	}
	evidence := Evidence(req.Task.Dir, req.Task.Input)
	var answer strings.Builder
	var outcome askexec.Event
	for event := range r.Ask.Start(ctx, askexec.Request{
		Message: message, Input: evidence, Session: req.Task.Session, Model: req.Task.Model,
		Effort: req.Task.Options.Effort, System: System, Schema: Schema,
		Skills: append([]string(nil), req.Task.Skills...),
	}) {
		if event.Done {
			outcome = event
			continue
		}
		if event.Stream == askexec.Stdout {
			answer.WriteString(event.Text)
		} else {
			emitDraft(ctx, events, DraftEvent{Stream: filterexec.Stderr, Text: event.Text})
		}
	}
	if outcome.Err != nil || outcome.ExitCode != 0 {
		emitDraftFinal(ctx, events, DraftEvent{Done: true, ExitCode: outcome.ExitCode, Err: outcome.Err})
		return
	}
	_, canonical, contractDigest, err := Parse(answer.String())
	if err != nil {
		emitDraftFinal(ctx, events, DraftEvent{Done: true, ExitCode: 1, Err: err})
		return
	}
	draft := Draft{
		Schema: 1, OutcomeID: outcomeID, Generation: generation, ParentRevisionID: parent,
		Intent: req.Task.Goal, Workspace: req.Task.Dir, Toolbox: req.Task.Toolbox, Contract: json.RawMessage(canonical),
		ContractSHA256:         "sha256:" + contractDigest,
		CompilerEvidenceSHA256: sha256Text(evidence), Check: req.Task.Options.Check, CheckSHA256: sha256Text(req.Task.Options.Check),
		CheckAll: req.Task.Options.CheckAllCriteria, Skills: append([]string{}, req.Task.Skills...),
	}
	expected := ""
	if req.Current != nil {
		expected = req.Current.DraftSHA256
	}
	draft, err = req.Store.SaveDraftCAS(draft, expected)
	if err != nil {
		emitDraftFinal(ctx, events, DraftEvent{Done: true, ExitCode: 1, Err: err})
		return
	}
	body, err := json.Marshal(struct {
		Status            string `json:"status"`
		InstructionSHA256 string `json:"instruction_sha256"`
		Draft
	}{Status: "proposed", InstructionSHA256: sha256Text(req.Instruction), Draft: draft})
	if err != nil {
		emitDraftFinal(ctx, events, DraftEvent{Done: true, ExitCode: 1, Err: err})
		return
	}
	if err := r.Ask.Record(ctx, askexec.RecordRequest{
		Session: req.Task.Session, Source: "bench", Kind: "bench.contract-proposal/v1", JSON: string(body),
	}); err != nil {
		emitDraftFinal(ctx, events, DraftEvent{Done: true, ExitCode: 1, Err: fmt.Errorf("record contract proposal: %w", err)})
		return
	}
	draft, err = req.Store.MarkDraftRecorded(draft)
	if err != nil {
		emitDraftFinal(ctx, events, DraftEvent{Done: true, ExitCode: 1, Err: fmt.Errorf("mark contract proposal durable: %w", err)})
		return
	}
	emitDraftFinal(ctx, events, DraftEvent{Done: true, ExitCode: 0, Draft: &draft})
}

// UpdateDraft validates manually edited contract JSON and recomputes every
// identity derived from those bytes without changing the frozen controller
// policy that surrounded the proposal.
func UpdateDraft(draft Draft, body string) (Draft, error) {
	_, canonical, digest, err := Parse(body)
	if err != nil {
		return Draft{}, err
	}
	draft.Schema = 1
	draft.Contract = json.RawMessage(canonical)
	draft.ContractSHA256 = "sha256:" + digest
	draft.ContractID = ""
	draft.RevisionID = ""
	return draft, nil
}

func (r Runner) Admit(ctx context.Context, request AdmitRequest) <-chan plyexec.Event {
	events := make(chan plyexec.Event, 16)
	go func() {
		defer close(events)
		req, supplied := request.Task, request.Draft
		if r.Ask == nil {
			emitFinal(ctx, events, plyexec.Event{Done: true, ExitCode: 1, Err: errors.New("contract admission needs ask")})
			return
		}
		if r.Ply == nil {
			emitFinal(ctx, events, plyexec.Event{Done: true, ExitCode: 1, Err: errors.New("contract admission needs ply")})
			return
		}
		if err := plyexec.Validate(req); err != nil {
			emitFinal(ctx, events, plyexec.Event{Done: true, ExitCode: 1, Err: err})
			return
		}
		if request.Store == nil {
			emitFinal(ctx, events, plyexec.Event{Done: true, ExitCode: 1, Err: errors.New("contract admission needs a durable store")})
			return
		}
		current, status, err := request.Store.Load()
		if err != nil || status != "draft" {
			if err == nil {
				err = errors.New("no editable contract draft is awaiting admission")
			}
			emitFinal(ctx, events, plyexec.Event{Done: true, ExitCode: 1, Err: err, Session: req.Session})
			return
		}
		if current.DraftSHA256 != request.ExpectedDraftSHA256 {
			emitFinal(ctx, events, plyexec.Event{Done: true, ExitCode: 1, Err: errors.New("contract draft changed since review"), Session: req.Session})
			return
		}
		if current.RecordedDraftSHA256 != current.DraftSHA256 {
			emitFinal(ctx, events, plyexec.Event{Done: true, ExitCode: 1, Err: errors.New("contract draft has unsealed changes; revise or import it before admission"), Session: req.Session})
			return
		}
		proposed, _, _, err := Parse(string(current.Contract))
		if err != nil {
			emitFinal(ctx, events, plyexec.Event{Done: true, ExitCode: 1, Err: err, Session: req.Session})
			return
		}
		if len(proposed.OpenQuestions) > 0 || len(proposed.Approvals) > 0 {
			emitFinal(ctx, events, plyexec.Event{Done: true, ExitCode: 2, Err: errors.New("contract still contains open questions or ungranted approvals"), Session: req.Session})
			return
		}
		if err := validateAdmission(req, current); err != nil {
			emitFinal(ctx, events, plyexec.Event{Done: true, ExitCode: 1, Err: err, Session: req.Session})
			return
		}
		draft, err := request.Store.PublishRevision(supplied, request.ExpectedDraftSHA256)
		if err != nil {
			emitFinal(ctx, events, plyexec.Event{Done: true, ExitCode: 1, Err: fmt.Errorf("admit contract draft: %w", err), Session: req.Session})
			return
		}
		contract, canonical, contractDigest, _ := Parse(string(draft.Contract))
		body, err := json.Marshal(struct {
			Status          string          `json:"status"`
			AdmittedBy      string          `json:"admitted_by"`
			ContractID      string          `json:"contract_id"`
			ContractSHA     string          `json:"contract_sha256"`
			ContractBodySHA string          `json:"contract_body_sha256"`
			IntentSHA       string          `json:"intent_sha256"`
			EvidenceSHA     string          `json:"compiler_evidence_sha256"`
			CheckSHA        string          `json:"check_sha256"`
			CheckAll        bool            `json:"check_all"`
			Workspace       string          `json:"workspace"`
			Toolbox         string          `json:"toolbox,omitempty"`
			Skills          []string        `json:"skills"`
			OutcomeID       string          `json:"outcome_id"`
			RevisionID      string          `json:"revision_id"`
			DraftSHA        string          `json:"draft_sha256"`
			Generation      int             `json:"generation"`
			Parent          string          `json:"parent_revision_id,omitempty"`
			Contract        json.RawMessage `json:"contract"`
		}{
			Status: "admitted", AdmittedBy: "interactive-user", ContractID: draft.ContractID,
			ContractSHA: draft.ContractSHA256, ContractBodySHA: compactJSONSHA(draft.Contract), IntentSHA: sha256Text(draft.Intent), EvidenceSHA: draft.CompilerEvidenceSHA256,
			CheckSHA: draft.CheckSHA256, CheckAll: draft.CheckAll, Workspace: draft.Workspace, Toolbox: draft.Toolbox, Skills: append([]string{}, draft.Skills...),
			OutcomeID: draft.OutcomeID, RevisionID: draft.RevisionID, DraftSHA: draft.DraftSHA256,
			Generation: draft.Generation, Parent: draft.ParentRevisionID, Contract: json.RawMessage(canonical),
		})
		if err != nil {
			emitFinal(ctx, events, plyexec.Event{Done: true, ExitCode: 1, Err: err, Session: req.Session})
			return
		}
		if err := r.Ask.Record(ctx, askexec.RecordRequest{
			Session: req.Session, Source: "bench-user", Kind: "bench.contract/v3", JSON: string(body),
		}); err != nil {
			emitFinal(ctx, events, plyexec.Event{Done: true, ExitCode: 1, Err: fmt.Errorf("record admitted contract: %w", err), Session: req.Session})
			return
		}
		if err := r.Ask.AdmittedContract(ctx, req.Session, admissionExpectation(draft)); err != nil {
			emitFinal(ctx, events, plyexec.Event{Done: true, ExitCode: 1, Err: fmt.Errorf("verify admitted contract record: %w", err), Session: req.Session})
			return
		}
		if err := request.Store.MarkAdmitted(draft); err != nil {
			emitFinal(ctx, events, plyexec.Event{Done: true, ExitCode: 1, Err: fmt.Errorf("mark admitted contract durable: %w", err), Session: req.Session})
			return
		}
		run := req
		run.Goal = draft.Intent
		r.runAccepted(ctx, run, contract, canonical, contractDigest, draft.ContractID, events)
	}()
	return events
}

func (r Runner) Run(ctx context.Context, request RunRequest) <-chan plyexec.Event {
	events := make(chan plyexec.Event, 16)
	go func() {
		defer close(events)
		if r.Ask == nil || r.Ply == nil {
			emitFinal(ctx, events, plyexec.Event{Done: true, ExitCode: 1, Err: errors.New("admitted contract run needs ask and ply")})
			return
		}
		if request.Store == nil {
			emitFinal(ctx, events, plyexec.Event{Done: true, ExitCode: 1, Err: errors.New("admitted contract run needs a durable store")})
			return
		}
		loaded, status, err := request.Store.Load()
		if err != nil || status != "admitted" {
			if err == nil {
				err = errors.New("contract is not admitted")
			}
			emitFinal(ctx, events, plyexec.Event{Done: true, ExitCode: 1, Err: err, Session: request.Task.Session})
			return
		}
		if loaded.ContractID != request.Draft.ContractID || loaded.RevisionID != request.Draft.RevisionID {
			emitFinal(ctx, events, plyexec.Event{Done: true, ExitCode: 1, Err: errors.New("admitted contract revision changed"), Session: request.Task.Session})
			return
		}
		if err := r.Ask.AdmittedContract(ctx, request.Task.Session, admissionExpectation(loaded)); err != nil {
			emitFinal(ctx, events, plyexec.Event{Done: true, ExitCode: 1, Err: fmt.Errorf("verify admitted contract record: %w", err), Session: request.Task.Session})
			return
		}
		if err := validateAdmission(request.Task, loaded); err != nil {
			emitFinal(ctx, events, plyexec.Event{Done: true, ExitCode: 1, Err: err, Session: request.Task.Session})
			return
		}
		contract, canonical, digest, err := Parse(string(loaded.Contract))
		if err != nil {
			emitFinal(ctx, events, plyexec.Event{Done: true, ExitCode: 1, Err: err, Session: request.Task.Session})
			return
		}
		req := request.Task
		req.Goal = loaded.Intent
		if guidance := strings.TrimSpace(request.Guidance); guidance != "" {
			req.Goal += "\n\nIMPLEMENTATION GUIDANCE FOR THIS ATTEMPT\n" + guidance + "\n\nThis guidance does not amend the admitted outcome contract."
		}
		r.runAccepted(ctx, req, contract, canonical, digest, loaded.ContractID, events)
	}()
	return events
}

func admissionExpectation(draft Draft) askexec.AdmissionExpectation {
	return askexec.AdmissionExpectation{
		ContractID: draft.ContractID, ContractSHA256: draft.ContractSHA256, ContractBodySHA256: compactJSONSHA(draft.Contract), IntentSHA256: sha256Text(draft.Intent),
		CompilerEvidenceSHA256: draft.CompilerEvidenceSHA256, CheckSHA256: draft.CheckSHA256, CheckAll: draft.CheckAll,
		Workspace: draft.Workspace, Toolbox: draft.Toolbox, Skills: append([]string{}, draft.Skills...),
		OutcomeID: draft.OutcomeID, RevisionID: draft.RevisionID, DraftSHA256: draft.DraftSHA256,
		Generation: draft.Generation, ParentRevisionID: draft.ParentRevisionID,
	}
}

func compactJSONSHA(body []byte) string {
	var compact bytes.Buffer
	_ = json.Compact(&compact, body)
	return sha256Text(compact.String())
}

func validateAdmission(req plyexec.TaskRequest, draft Draft) error {
	if !req.Options.IntentContract {
		return errors.New("cannot admit a contract while contract mode is off")
	}
	if req.Dir != draft.Workspace || req.Goal != draft.Intent {
		return errors.New("contract workspace or intent changed; revise the draft before admission")
	}
	if req.Toolbox != draft.Toolbox {
		return errors.New("contract tool grant changed; revise the draft before admission")
	}
	if sha256Text(req.Options.Check) != draft.CheckSHA256 || req.Options.CheckAllCriteria != draft.CheckAll {
		return errors.New("contract check policy changed; revise the draft before admission")
	}
	if !slices.Equal(req.Skills, draft.Skills) {
		return errors.New("contract skills changed; revise the draft before admission")
	}
	return nil
}

func revisionMessage(current Draft, instruction, check string, checkAll bool) string {
	verifier := "No executable verifier is configured."
	if check != "" {
		verifier = "The operator configured this exact verifier:\n" + check
		if checkAll {
			verifier = "The operator explicitly admits this exact verifier as judge of every criterion:\n" + check
		}
	}
	return "Revise the proposed outcome contract according to the user's change request. Return the complete replacement contract, not a patch. Do not solve the task or emit shell commands. Preserve sound parts of the current contract. Remove an open question or approval only when the user's change request explicitly resolves or grants it.\n\nORIGINAL USER INTENT\n" + current.Intent + "\n\nCURRENT PROPOSED CONTRACT\n" + string(current.Contract) + "\n\nUSER CHANGE REQUEST\n" + instruction + "\n\nVERIFIER BOUNDARY\n" + verifier
}

func newOutcomeID() (string, error) {
	var id [16]byte
	if _, err := rand.Read(id[:]); err != nil {
		return "", fmt.Errorf("create outcome id: %w", err)
	}
	return fmt.Sprintf("%x", id[:]), nil
}

func emitDraft(ctx context.Context, dst chan<- DraftEvent, event DraftEvent) {
	select {
	case dst <- event:
	case <-ctx.Done():
	}
}

func emitDraftFinal(ctx context.Context, dst chan<- DraftEvent, event DraftEvent) {
	if ctx.Err() != nil && event.Err == nil {
		event.Err = ctx.Err()
		event.ExitCode = 130
	}
	emitDraft(context.WithoutCancel(ctx), dst, event)
}
