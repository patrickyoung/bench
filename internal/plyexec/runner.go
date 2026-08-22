// Package plyexec owns the process boundary between bench and ply.
package plyexec

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/patrickyoung/bench/internal/filterexec"
)

// Event preserves Ply's ordinary streams and exit status. Session is set on
// the terminal event when Ply reports the Ask session it actually finished
// in through its explicit -session-out artifact.
type Event struct {
	Stream filterexec.Stream
	Text   string
	// Contract is set by Bench's intent compiler before Ply starts. Digest is
	// the envelope ID binding those canonical bytes to the intent, compiler
	// evidence, check, and selected skill references.
	Contract       string
	ContractDigest string
	// ContractResult is set by Bench's contract supervisor on the terminal
	// event. Ply itself remains the authority only for its literal verifier;
	// this record says which contract criteria that narrow result covered and
	// which still require another judge.
	ContractResult *ContractResult
	Done           bool
	ExitCode       int
	Err            error
	Session        string
}

type ContractCriterion struct {
	ID    string `json:"id"`
	Judge string `json:"judge"`
}

type VerifierReceiptRef struct {
	Seq             int    `json:"seq"`
	BodySHA256      string `json:"body_sha256"`
	SealSHA256      string `json:"seal_sha256"`
	Phase           string `json:"phase"`
	CandidateSHA256 string `json:"candidate_sha256"`
	VerifierSHA256  string `json:"verifier_sha256"`
}

type ContractResult struct {
	ContractID            string              `json:"contract_id"`
	Status                string              `json:"status"`
	CheckConfigured       bool                `json:"check_configured"`
	CheckPassed           bool                `json:"check_passed"`
	WorkerExitCode        int                 `json:"worker_exit_code"`
	ProposedCheckCoverage []string            `json:"proposed_check_coverage"`
	AdmittedCheckCoverage []string            `json:"admitted_check_coverage"`
	Outstanding           []ContractCriterion `json:"outstanding"`
	JudgeMapSHA256        string              `json:"judge_map_sha256,omitempty"`
	VerifierReceipt       *VerifierReceiptRef `json:"verifier_receipt,omitempty"`
	OpenQuestions         []string            `json:"open_questions"`
	PendingApprovals      []string            `json:"pending_approvals"`
}

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
	Model      string
}

// TaskRequest is one open-ended workspace task. An empty Toolbox deliberately
// means ply's full-shell mode; callers must make that grant visible rather than
// presenting it as a sandbox.
type TaskRequest struct {
	Dir          string
	Goal         string
	Input        string
	Session      string
	SubagentsDir string
	Skills       []string
	Toolbox      string
	Model        string
	Options      TaskOptions
}

// TaskOptions are optional Ply policy controls. The Has fields preserve the
// distinction between an omitted option (Ply owns its default) and an
// explicit zero (Ply documents zero as unbounded).
type TaskOptions struct {
	IntentContract   bool
	ContractID       string
	Check            string
	CheckAllCriteria bool
	Force            bool
	Effort           string
	Cycles           int
	HasCycles        bool
	Turns            int
	HasTurns         bool
	Timeout          time.Duration
	HasTimeout       bool
	Compact          bool
	Compactions      int
	HasCompactions   bool
}

type Client interface {
	Refine(context.Context, RefineRequest) <-chan Event
}

type Worker interface {
	Work(context.Context, TaskRequest) <-chan Event
}

type Runner struct {
	Path      string
	AskPath   string
	BriefPath string
}

// Work lets ply compose Ask with ordinary programs in the workspace. The
// goal and paths are literal argv values; no user text is evaluated by bench.
// With no explicit toolbox, -sh is intentional: ply documents that this is a
// full shell grant and the TUI labels it as such.
func (r Runner) Work(ctx context.Context, req TaskRequest) <-chan Event {
	if err := Validate(req); err != nil {
		return failed(err.Error())
	}
	dir := strings.TrimSpace(req.Dir)
	goal := strings.TrimSpace(req.Goal)
	session := strings.TrimSpace(req.Session)
	skills := make([]string, 0, len(req.Skills))
	for _, skill := range req.Skills {
		skill = strings.TrimSpace(skill)
		if skill == "" {
			return failed("skill name is empty")
		}
		skills = append(skills, skill)
	}
	if err := os.MkdirAll(filepath.Dir(session), 0o700); err != nil {
		return failed(err.Error())
	}
	path := r.Path
	if path == "" {
		path = "ply"
	}
	args := []string{}
	if toolbox := strings.TrimSpace(req.Toolbox); toolbox != "" {
		args = append(args, "-t", toolbox)
	} else {
		args = append(args, "-sh")
	}
	// Work mode promises tool-mediated pursuit. Ask-only requests take a
	// different path; a Ply worker that emits only prose has not done this job.
	args = append(args, "-require-action")
	if req.Options.Check != "" {
		args = append(args, "-check", req.Options.Check)
	}
	if req.Options.Force {
		args = append(args, "-B")
	}
	if contractID := strings.TrimSpace(req.Options.ContractID); contractID != "" {
		args = append(args, "-contract-id", contractID)
	}
	if effort := strings.TrimSpace(req.Options.Effort); effort != "" {
		args = append(args, "-effort", effort)
	}
	if req.Options.HasCycles {
		args = append(args, "-cycles", strconv.Itoa(req.Options.Cycles))
	}
	if req.Options.HasTurns {
		args = append(args, "-turns", strconv.Itoa(req.Options.Turns))
	}
	if req.Options.HasTimeout {
		args = append(args, "-timeout", req.Options.Timeout.String())
	}
	if req.Options.Compact {
		args = append(args, "-compact")
	}
	if req.Options.HasCompactions {
		args = append(args, "-compactions", strconv.Itoa(req.Options.Compactions))
	}

	// A compacting Ply run can move to a new Ask session. Keep that control
	// result out of the human stdout/stderr contract: Ply writes one path to
	// a caller-owned file and Work attaches it to the terminal event.
	sessionOut := ""
	if req.Options.Compact || req.Options.Check != "" {
		f, err := os.CreateTemp(filepath.Dir(session), ".bench-ply-session-*")
		if err != nil {
			return failed(fmt.Sprintf("create Ply session result: %v", err))
		}
		sessionOut = f.Name()
		if err := f.Close(); err != nil {
			_ = os.Remove(sessionOut)
			return failed(fmt.Sprintf("close Ply session result: %v", err))
		}
		if err := os.Remove(sessionOut); err != nil {
			return failed(fmt.Sprintf("prepare Ply session result: %v", err))
		}
		args = append(args, "-session-out", sessionOut)
	}
	args = append(args, "-C", dir, "-f", session)
	if model := strings.TrimSpace(req.Model); model != "" {
		args = append(args, "-m", model)
	}
	for _, skill := range skills {
		args = append(args, "-s", skill)
	}
	args = append(args, "--", goal)
	env := []string{}
	if ask := strings.TrimSpace(r.AskPath); ask != "" {
		env = append(env, "ASK="+ask)
	}
	if brief := strings.TrimSpace(r.BriefPath); brief != "" {
		env = append(env, "BRIEF="+brief)
	}
	// The parent has an explicit session, but nested Ply processes normally
	// choose their own. Give those subagents a durable, parent-scoped home
	// without changing either process's stdout/stderr contract.
	if dir := strings.TrimSpace(req.SubagentsDir); dir != "" {
		env = append(env, "PLY_DIR="+dir)
	}
	// A child Ply inherits Ask's ordinary model default. Mirror an explicit
	// parent selection so delegation does not silently switch models.
	if model := strings.TrimSpace(req.Model); model != "" {
		env = append(env, "ASK_MODEL="+model)
	}
	// Keep nested Ply workers on the parent's explicit reasoning policy. Ask
	// remains the authority for which literal effort names are supported.
	if effort := strings.TrimSpace(req.Options.Effort); effort != "" {
		env = append(env, "PLY_EFFORT="+effort)
	}
	processEvents := filterexec.Start(ctx, filterexec.Spec{Path: path, Args: args, Dir: dir, Env: env, Stdin: req.Input})
	if sessionOut == "" {
		return adapt(ctx, processEvents)
	}
	return withSessionResult(ctx, processEvents, sessionOut, req.Options.Check != "")
}

// Validate checks one task request without starting Ask, Ply, or touching the
// workspace. Supervisors that add a phase before Ply use the same gate so a
// bad work policy never spends a model turn.
func Validate(req TaskRequest) error {
	if strings.TrimSpace(req.Dir) == "" {
		return errors.New("task workspace is empty")
	}
	if strings.TrimSpace(req.Goal) == "" {
		return errors.New("task goal is empty")
	}
	if strings.TrimSpace(req.Session) == "" {
		return errors.New("task session is empty")
	}
	if req.Options.Check != "" && strings.TrimSpace(req.Options.Check) == "" {
		return errors.New("task check is empty")
	}
	if req.Options.CheckAllCriteria && req.Options.Check == "" {
		return errors.New("check-all needs a configured check")
	}
	if req.Options.CheckAllCriteria && !req.Options.IntentContract {
		return errors.New("check-all needs an outcome contract")
	}
	for _, skill := range req.Skills {
		if strings.TrimSpace(skill) == "" {
			return errors.New("skill name is empty")
		}
	}
	if req.Options.HasCycles && req.Options.Cycles < 0 {
		return errors.New("task cycles cannot be negative")
	}
	if req.Options.HasTurns && req.Options.Turns < 0 {
		return errors.New("task turns cannot be negative")
	}
	if req.Options.HasTimeout && req.Options.Timeout <= 0 {
		return errors.New("task timeout must be positive")
	}
	if req.Options.HasCompactions && req.Options.Compactions < 0 {
		return errors.New("task compactions cannot be negative")
	}
	if req.Options.IntentContract && req.Options.Compact {
		return errors.New("contracted compaction needs verified session lineage; use -contract=false or omit -compact")
	}
	return nil
}

func withSessionResult(ctx context.Context, source <-chan filterexec.Event, resultPath string, checked bool) <-chan Event {
	events := make(chan Event, 16)
	go func() {
		defer close(events)
		defer os.Remove(resultPath)
		for processEvent := range source {
			event := eventFrom(processEvent)
			if !event.Done {
				emit(ctx, events, event)
				continue
			}
			session, err := readSessionResult(resultPath)
			switch {
			case err == nil:
				event.Session = session
			case errors.Is(event.Err, context.Canceled):
				// An interrupt may arrive before Ply establishes a session.
			case checked && event.Err == nil && event.ExitCode == 0 && errors.Is(err, os.ErrNotExist):
				// Ply's passing pre-check intentionally creates no Ask session.
			case event.Err != nil || event.ExitCode != 0:
				// Preserve Ply's actual failure. A missing control artifact is
				// expected when it could not start or rejected the invocation.
			default:
				event.ExitCode = 1
				event.Err = fmt.Errorf("read Ply session result: %w", err)
			}
			emitFinal(ctx, events, event)
		}
	}()
	return events
}

func adapt(ctx context.Context, source <-chan filterexec.Event) <-chan Event {
	events := make(chan Event, 16)
	go func() {
		defer close(events)
		for processEvent := range source {
			event := eventFrom(processEvent)
			if event.Done {
				emitFinal(ctx, events, event)
				continue
			}
			emit(ctx, events, event)
		}
	}()
	return events
}

func eventFrom(event filterexec.Event) Event {
	return Event{Stream: event.Stream, Text: event.Text, Done: event.Done, ExitCode: event.ExitCode, Err: event.Err}
}

func readSessionResult(path string) (string, error) {
	body, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	text := string(body)
	if strings.HasSuffix(text, "\n") {
		text = strings.TrimSuffix(text, "\n")
	}
	if text == "" || strings.ContainsAny(text, "\r\n\x00") {
		return "", errors.New("invalid session path record")
	}
	if !filepath.IsAbs(text) {
		return "", fmt.Errorf("session path is not absolute: %q", text)
	}
	return text, nil
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
	args := []string{"-sh", "-check", `"$BRIEF" lint -strict .`}
	if model := strings.TrimSpace(req.Model); model != "" {
		args = append(args, "-m", model)
	}
	args = append(args, goal)
	return adapt(ctx, filterexec.Start(ctx, filterexec.Spec{
		Path:  path,
		Args:  args,
		Dir:   dir,
		Env:   []string{"BRIEF=" + briefPath, "PLY_DIR=" + sessions, "SOURCE_ROOT=" + sourceRoot},
		Stdin: req.Source,
	}))
}

func failed(message string) <-chan Event {
	events := make(chan Event, 1)
	events <- Event{Done: true, ExitCode: 2, Err: errors.New(message)}
	close(events)
	return events
}
