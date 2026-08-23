package askexec

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"path/filepath"
	"regexp"
	"strings"
	"sync"

	"github.com/patrickyoung/bench/internal/filterexec"
	"github.com/patrickyoung/bench/internal/plyexec"
)

type VerifierReceipt struct {
	Seq             int
	BodySHA256      string
	SealSHA256      string
	ContractID      string
	Phase           string
	CandidateSHA256 string
	Verifier        string
	VerifierSHA256  string
	Outcome         string
	ExitCode        int
}

type VerifierReader interface {
	AcceptedVerifier(context.Context, string, string, string, string, string, string) (VerifierReceipt, error)
}

type ApprovalReceipt struct {
	Seq          int
	BodySHA256   string
	SealSHA256   string
	ContractID   string
	Job          string
	Digest       string
	Verdict      string
	Action       string
	ActionSHA256 string
	MaySHA256    string
}

type ApprovalReader interface {
	TerminalApproval(context.Context, string, string, string, string, string, string) (ApprovalReceipt, error)
}

type CagedApprovalReader interface {
	TerminalCagedApproval(context.Context, string, string, string, string, string, string, string, string) (ApprovalReceipt, error)
}

type ApprovalResultReader interface {
	LatestApprovalResult(context.Context, string, string, string, string) (plyexec.ContractResult, bool, error)
}

type CagedApprovalResultReader interface {
	LatestCagedApprovalResult(context.Context, string, string, string, string, string, string) (plyexec.ContractResult, bool, error)
}

type ConfinementReceipt struct {
	Seq, ExitCode                                int
	BodySHA256, SealSHA256, ActionSHA256, Detail string
	MayHaveRun                                   bool
}

type ConfinementReader interface {
	TerminalConfinement(context.Context, string, string, string, string, string, string, string, string) (ConfinementReceipt, error)
}

// AcceptedVerifier reads one verified Ask snapshot, then selects the terminal
// sealed accepted Ply receipt matching the controller's exact contract,
// command, and candidate digest. It composes Ask's public replay interface;
// Bench never reads Ask's log format from disk.
func (r Runner) AcceptedVerifier(ctx context.Context, session, judgeMapSHA256, contractID, verifier, candidateSHA256, directory string) (VerifierReceipt, error) {
	data, err := r.verifiedReplay(ctx, session, "verifier receipts")
	if err != nil {
		return VerifierReceipt{}, err
	}
	return selectAcceptedVerifier(data, judgeMapSHA256, contractID, verifier, candidateSHA256, directory)
}

func (r Runner) TerminalApproval(ctx context.Context, session, contractID, job, directory, mayPath, maySHA256 string) (ApprovalReceipt, error) {
	data, err := r.verifiedReplay(ctx, session, "approval receipts")
	if err != nil {
		return ApprovalReceipt{}, err
	}
	return selectTerminalApproval(data, contractID, job, directory, mayPath, maySHA256)
}

func (r Runner) TerminalCagedApproval(ctx context.Context, session, contractID, job, directory, mayPath, maySHA256, cagePath, cageSHA256 string) (ApprovalReceipt, error) {
	data, err := r.verifiedReplay(ctx, session, "caged approval receipts")
	if err != nil {
		return ApprovalReceipt{}, err
	}
	return selectTerminalApproval(data, contractID, job, directory, mayPath, maySHA256, cagePath, cageSHA256)
}

// LatestApprovalResult restores only a terminal, controller-sealed v4 result
// from one verified Ask snapshot. A parked/declined result must immediately
// follow the exact Ply approval receipt it references.
func (r Runner) LatestApprovalResult(ctx context.Context, session, contractID, directory, mayPath string) (plyexec.ContractResult, bool, error) {
	resolvedMay, err := plyexec.ResolveMayPath(mayPath)
	if err != nil {
		return plyexec.ContractResult{}, false, err
	}
	maySHA256, err := plyexec.ExecutableSHA256(resolvedMay)
	if err != nil {
		return plyexec.ContractResult{}, false, err
	}
	abs, err := filepath.Abs(directory)
	if err != nil {
		return plyexec.ContractResult{}, false, fmt.Errorf("resolve approval workspace: %w", err)
	}
	resolvedDir, err := filepath.EvalSymlinks(abs)
	if err != nil {
		return plyexec.ContractResult{}, false, fmt.Errorf("resolve approval workspace: %w", err)
	}
	data, err := r.verifiedReplay(ctx, session, "contract approval results")
	if err != nil {
		return plyexec.ContractResult{}, false, err
	}
	return selectLatestApprovalResult(data, contractID, plyexec.MayJob(contractID), resolvedDir, resolvedMay, maySHA256)
}

func (r Runner) LatestCagedApprovalResult(ctx context.Context, session, contractID, directory, mayPath, cagePath, cageSHA256 string) (plyexec.ContractResult, bool, error) {
	resolvedMay, err := plyexec.ResolveMayPath(mayPath)
	if err != nil {
		return plyexec.ContractResult{}, false, err
	}
	maySHA256, err := plyexec.ExecutableSHA256(resolvedMay)
	if err != nil {
		return plyexec.ContractResult{}, false, err
	}
	abs, err := filepath.Abs(directory)
	if err != nil {
		return plyexec.ContractResult{}, false, err
	}
	resolvedDir, err := filepath.EvalSymlinks(abs)
	if err != nil {
		return plyexec.ContractResult{}, false, err
	}
	data, err := r.verifiedReplay(ctx, session, "caged contract approval results")
	if err != nil {
		return plyexec.ContractResult{}, false, err
	}
	return selectLatestApprovalResult(data, contractID, plyexec.MayJob(contractID), resolvedDir, resolvedMay, maySHA256, cagePath, cageSHA256)
}

func (r Runner) TerminalConfinement(ctx context.Context, session, contractID, job, directory, mayPath, maySHA256, cagePath, cageSHA256 string) (ConfinementReceipt, error) {
	data, err := r.verifiedReplay(ctx, session, "confinement receipts")
	if err != nil {
		return ConfinementReceipt{}, err
	}
	return selectTerminalConfinement(data, contractID, job, directory, mayPath, maySHA256, cagePath, cageSHA256)
}

func (r Runner) verifiedReplay(ctx context.Context, session, label string) ([]byte, error) {
	path := r.Path
	if path == "" {
		path = "ask"
	}
	replay := lockedBuffer{limit: 64 << 20}
	outcome := filterexec.Execute(ctx, filterexec.Spec{Path: path, Args: []string{"replay", "-check", "-json", session}}, replay.write)
	if outcome.Err != nil || outcome.ExitCode != 0 {
		return nil, fmt.Errorf("read verified %s: %w: %s", label, outcome.Err, strings.TrimSpace(replay.stderr.String()))
	}
	if replay.exceeded() {
		return nil, fmt.Errorf("read %s: replay exceeds 64 MiB", label)
	}
	return append([]byte(nil), replay.stdout.Bytes()...), nil
}

type lockedBuffer struct {
	mu             sync.Mutex
	stdout, stderr bytes.Buffer
	limit          int
	tooLarge       bool
}

func (b *lockedBuffer) write(stream Stream, text string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.limit > 0 {
		buffer := &b.stderr
		if stream == Stdout {
			buffer = &b.stdout
		}
		remaining := b.limit - buffer.Len()
		if remaining <= 0 {
			b.tooLarge = true
			return
		}
		if len(text) > remaining {
			text = text[:remaining]
			b.tooLarge = true
		}
	}
	if stream == Stdout {
		b.stdout.WriteString(text)
	} else {
		b.stderr.WriteString(text)
	}
}

func (b *lockedBuffer) exceeded() bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.tooLarge
}

type replayEvent struct {
	Seq  int             `json:"seq"`
	Type string          `json:"type"`
	Data json.RawMessage `json:"data"`
}

type replayNote struct {
	Source string          `json:"source"`
	Kind   string          `json:"kind"`
	Body   json.RawMessage `json:"body"`
}

type replaySeal struct {
	Through int    `json:"through"`
	SHA256  string `json:"sha256"`
}

type verifierBody struct {
	ContractID      string `json:"contract_id"`
	Phase           string `json:"phase"`
	CandidateSHA256 string `json:"candidate_sha256"`
	Verifier        string `json:"verifier"`
	VerifierSHA256  string `json:"verifier_sha256"`
	Shell           string `json:"shell"`
	Directory       string `json:"directory"`
	Outcome         string `json:"outcome"`
	ExitCode        int    `json:"exit_code"`
	Killed          bool   `json:"killed"`
	StartError      bool   `json:"start_error"`
	TimeoutMS       int64  `json:"timeout_ms"`
	Output          string `json:"output"`
	OutputSHA256    string `json:"output_sha256"`
	OutputBytes     int64  `json:"output_bytes"`
	ElidedBytes     int64  `json:"elided_bytes"`
}

type approvalActionBody struct {
	Version     int                      `json:"version"`
	ContractID  string                   `json:"contract_id,omitempty"`
	Directory   string                   `json:"directory"`
	Shell       string                   `json:"shell"`
	Path        string                   `json:"path"`
	TimeoutNS   int64                    `json:"timeout_ns"`
	Script      string                   `json:"script"`
	Confinement *approvalConfinementBody `json:"confinement,omitempty"`
}

type approvalConfinementBody struct {
	Kind       string   `json:"kind"`
	CagePath   string   `json:"cage_path"`
	CageSHA256 string   `json:"cage_sha256"`
	Argv       []string `json:"argv"`
	Workspace  string   `json:"workspace"`
	TempDir    string   `json:"temp_dir"`
	Network    bool     `json:"network"`
}

type approvalBody struct {
	Version         int                `json:"version"`
	ContractID      string             `json:"contract_id,omitempty"`
	Job             string             `json:"job"`
	Digest          string             `json:"digest"`
	Action          approvalActionBody `json:"action"`
	ActionSHA256    string             `json:"action_sha256"`
	Verdict         string             `json:"verdict"`
	MayPath         string             `json:"may_path"`
	MaySHA256       string             `json:"may_sha256"`
	MayArgv         []string           `json:"may_argv"`
	MayInputSHA256  string             `json:"may_input_sha256"`
	MayStdoutSHA256 string             `json:"may_stdout_sha256"`
	MayStdoutBytes  int64              `json:"may_stdout_bytes"`
	MayExitCode     int                `json:"may_exit_code"`
}

type mayResultBody struct {
	Version int    `json:"version"`
	Job     string `json:"job"`
	Digest  string `json:"digest"`
	Action  string `json:"action"`
	Verdict string `json:"verdict"`
}

var lowerHex64 = regexp.MustCompile(`^[0-9a-f]{64}$`)

func selectTerminalApproval(data []byte, contractID, job, directory, mayPath, maySHA256 string, cage ...string) (ApprovalReceipt, error) {
	events, err := decodeReplayEvents(data)
	if err != nil {
		return ApprovalReceipt{}, err
	}
	if len(events) < 2 {
		return ApprovalReceipt{}, fmt.Errorf("verified Ask replay has no terminal approval receipt")
	}
	i := len(events) - 2
	kind := "ply.approval/v1"
	if len(cage) > 0 && strings.TrimSpace(cage[0]) != "" {
		kind = "ply.approval/v2"
	}
	if !sealedNote(events, i, kind) {
		return ApprovalReceipt{}, fmt.Errorf("Ply approval receipt is not the terminal sealed record")
	}
	var note replayNote
	var seal replaySeal
	if decodeStrict(events[i].Data, &note) != nil || decodeStrict(events[i+1].Data, &seal) != nil || note.Source != "ply" {
		return ApprovalReceipt{}, fmt.Errorf("terminal sealed approval receipt has invalid attribution")
	}
	var body approvalBody
	if decodeStrict(note.Body, &body) != nil || !validTerminalApproval(body, contractID, job, directory, mayPath, maySHA256, cage...) {
		return ApprovalReceipt{}, fmt.Errorf("terminal sealed approval receipt does not match the admitted action boundary")
	}
	action, _ := json.Marshal(body.Action)
	action = append(action, '\n')
	return ApprovalReceipt{
		Seq: events[i].Seq, BodySHA256: digestBytes(note.Body), SealSHA256: seal.SHA256,
		ContractID: body.ContractID, Job: body.Job, Digest: body.Digest, Verdict: body.Verdict,
		Action: string(action), ActionSHA256: body.ActionSHA256, MaySHA256: body.MaySHA256,
	}, nil
}

func selectLatestApprovalResult(data []byte, contractID, job, directory, mayPath, maySHA256 string, cage ...string) (plyexec.ContractResult, bool, error) {
	events, err := decodeReplayEvents(data)
	if err != nil {
		return plyexec.ContractResult{}, false, err
	}
	if len(events) < 2 {
		return plyexec.ContractResult{}, false, nil
	}
	i := len(events) - 2
	var candidate replayNote
	resultKind := "bench.contract-result/v4"
	approvalKind := "ply.approval/v1"
	if len(cage) > 0 && strings.TrimSpace(cage[0]) != "" {
		resultKind, approvalKind = "bench.contract-result/v5", "ply.approval/v2"
	}
	if events[i].Type != "note" || decodeStrict(events[i].Data, &candidate) != nil || candidate.Kind != resultKind {
		return plyexec.ContractResult{}, false, nil
	}
	if !sealedNote(events, i, resultKind) || candidate.Source != "bench" {
		return plyexec.ContractResult{}, false, fmt.Errorf("terminal contract approval result is not a valid sealed Bench record")
	}
	var result plyexec.ContractResult
	if decodeStrict(candidate.Body, &result) != nil || result.ContractID != contractID || result.ApprovalPolicy != plyexec.ApprovalEveryAction || (approvalKind == "ply.approval/v2") != (result.ActionConfinement == plyexec.ConfinementCage) {
		return plyexec.ContractResult{}, false, fmt.Errorf("terminal contract approval result does not match the admitted boundary")
	}
	if result.Status == "confinement_failed" && approvalKind == "ply.approval/v2" {
		if result.WorkerExitCode != 125 || result.StopReason != "confinement_failed" || result.ConfinementReceipt == nil || i < 4 {
			return plyexec.ContractResult{}, false, fmt.Errorf("terminal confinement result has inconsistent status")
		}
		got, err := validateConfinementEvidence(events, i-2, contractID, job, directory, mayPath, maySHA256, cage[0], cage[1])
		if err != nil {
			return plyexec.ContractResult{}, false, err
		}
		ref := result.ConfinementReceipt
		if ref.Seq != got.Seq || ref.BodySHA256 != got.BodySHA256 || ref.SealSHA256 != got.SealSHA256 || ref.ActionSHA256 != got.ActionSHA256 || ref.MayHaveRun != got.MayHaveRun || ref.Detail != got.Detail {
			return plyexec.ContractResult{}, false, fmt.Errorf("terminal confinement result reference does not match Ply evidence")
		}
		return result, true, nil
	}
	if result.Status != "awaiting_approval" && result.Status != "approval_declined" {
		return result, true, nil
	}
	wantExit := map[string]int{"awaiting_approval": 75, "approval_declined": 3}[result.Status]
	wantVerdict := map[string]string{"awaiting_approval": "parked", "approval_declined": "declined"}[result.Status]
	if result.WorkerExitCode != wantExit || result.StopReason != map[string]string{"awaiting_approval": "approval_required", "approval_declined": "approval_declined"}[result.Status] || result.ApprovalReceipt == nil || result.ApprovalReceipt.Verdict != wantVerdict {
		return plyexec.ContractResult{}, false, fmt.Errorf("terminal contract approval result has inconsistent status")
	}
	if i < 2 || !sealedNote(events, i-2, approvalKind) {
		return plyexec.ContractResult{}, false, fmt.Errorf("terminal contract approval result has no adjacent Ply receipt")
	}
	var approvalNote replayNote
	var approvalSeal replaySeal
	var body approvalBody
	if decodeStrict(events[i-2].Data, &approvalNote) != nil || approvalNote.Source != "ply" || decodeStrict(events[i-1].Data, &approvalSeal) != nil || decodeStrict(approvalNote.Body, &body) != nil || !validTerminalApproval(body, contractID, job, directory, mayPath, maySHA256, cage...) {
		return plyexec.ContractResult{}, false, fmt.Errorf("terminal contract approval result references an invalid Ply receipt")
	}
	action, _ := json.Marshal(body.Action)
	action = append(action, '\n')
	ref := result.ApprovalReceipt
	if ref.Seq != events[i-2].Seq || ref.BodySHA256 != digestBytes(approvalNote.Body) || ref.SealSHA256 != approvalSeal.SHA256 || ref.Job != body.Job || ref.Digest != body.Digest || ref.Verdict != body.Verdict || ref.Action != string(action) || ref.ActionSHA256 != body.ActionSHA256 || ref.MayPath != body.MayPath || ref.MaySHA256 != body.MaySHA256 {
		return plyexec.ContractResult{}, false, fmt.Errorf("terminal contract approval result receipt reference does not match Ply evidence")
	}
	return result, true, nil
}

func validTerminalApproval(body approvalBody, contractID, job, directory, mayPath, maySHA256 string, cage ...string) bool {
	caged := len(cage) >= 2 && strings.TrimSpace(cage[0]) != ""
	if body.Version != map[bool]int{false: 1, true: 2}[caged] || body.ContractID != contractID || body.Job != job || body.Action.Version != body.Version ||
		body.Action.ContractID != contractID || body.Action.Directory != directory || body.Action.TimeoutNS <= 0 ||
		!filepath.IsAbs(body.Action.Directory) || !filepath.IsAbs(body.Action.Shell) || body.Action.Script == "" ||
		body.MayPath != mayPath || body.MaySHA256 != maySHA256 || !filepath.IsAbs(body.MayPath) || !lowerHex64.MatchString(body.Digest) ||
		!strings.HasPrefix(body.MaySHA256, "sha256:") || !lowerHex64.MatchString(strings.TrimPrefix(body.MaySHA256, "sha256:")) ||
		len(body.MayArgv) != 3 || body.MayArgv[0] != body.MayPath || body.MayArgv[1] != "request" || body.MayArgv[2] != job {
		return false
	}
	if caged {
		c := body.Action.Confinement
		if c == nil || c.Kind != "cage" || c.CagePath != cage[0] || c.CageSHA256 != cage[1] || !strings.HasPrefix(c.CageSHA256, "sha256:") || !lowerHex64.MatchString(strings.TrimPrefix(c.CageSHA256, "sha256:")) || c.Workspace != directory || c.Network || !filepath.IsAbs(c.TempDir) || len(c.Argv) != 7 || c.Argv[0] != c.CagePath || c.Argv[1] != "-w" || c.Argv[2] != c.Workspace || c.Argv[3] != "--" || c.Argv[4] != body.Action.Shell || c.Argv[5] != "-c" || c.Argv[6] != body.Action.Script {
			return false
		}
	} else if body.Action.Confinement != nil {
		return false
	}
	wantExit, ok := map[string]int{"spent": 0, "parked": 75, "declined": 3}[body.Verdict]
	if !ok || body.MayExitCode != wantExit {
		return false
	}
	action, err := json.Marshal(body.Action)
	if err != nil {
		return false
	}
	action = append(action, '\n')
	if body.ActionSHA256 != digestText(string(action)) || body.MayInputSHA256 != body.ActionSHA256 ||
		body.Digest != mayDigest(body.Job, string(action)) {
		return false
	}
	result, err := json.Marshal(mayResultBody{Version: 1, Job: body.Job, Digest: body.Digest, Action: string(action), Verdict: body.Verdict})
	if err != nil {
		return false
	}
	result = append(result, '\n')
	return body.MayStdoutSHA256 == digestText(string(result)) && body.MayStdoutBytes == int64(len(result))
}

type confinementBody struct {
	Version              int    `json:"version"`
	ContractID           string `json:"contract_id"`
	ApprovalDigest       string `json:"approval_digest"`
	ApprovalActionSHA256 string `json:"approval_action_sha256"`
	ScriptSHA256         string `json:"script_sha256"`
	CagePath             string `json:"cage_path"`
	CageSHA256           string `json:"cage_sha256"`
	Workspace            string `json:"workspace"`
	TempDir              string `json:"temp_dir"`
	ExitCode             int    `json:"exit_code"`
	MayHaveRun           bool   `json:"may_have_run"`
	Detail               string `json:"detail"`
	Output               []byte `json:"output,omitempty"`
	OutputSHA256         string `json:"output_sha256"`
	OutputBytes          int64  `json:"output_bytes"`
	ElidedBytes          int64  `json:"elided_bytes,omitempty"`
}

func selectTerminalConfinement(data []byte, contractID, job, directory, mayPath, maySHA256, cagePath, cageSHA256 string) (ConfinementReceipt, error) {
	events, err := decodeReplayEvents(data)
	if err != nil {
		return ConfinementReceipt{}, err
	}
	if len(events) < 2 {
		return ConfinementReceipt{}, fmt.Errorf("verified Ask replay has no terminal confinement receipt")
	}
	i := len(events) - 2
	return validateConfinementEvidence(events, i, contractID, job, directory, mayPath, maySHA256, cagePath, cageSHA256)
}

func validateConfinementEvidence(events []replayEvent, i int, contractID, job, directory, mayPath, maySHA256, cagePath, cageSHA256 string) (ConfinementReceipt, error) {
	if !sealedNote(events, i, "ply.confinement/v1") {
		return ConfinementReceipt{}, fmt.Errorf("Ply confinement receipt is not terminal and sealed")
	}
	var note replayNote
	var seal replaySeal
	var body confinementBody
	if decodeStrict(events[i].Data, &note) != nil || note.Source != "ply" || decodeStrict(events[i+1].Data, &seal) != nil || decodeStrict(note.Body, &body) != nil {
		return ConfinementReceipt{}, fmt.Errorf("terminal confinement receipt is invalid")
	}
	if body.Version != 1 || body.ContractID != contractID || body.CagePath != cagePath || body.CageSHA256 != cageSHA256 || body.ExitCode != 125 || body.Detail == "" || digestText(string(body.Output)) != body.OutputSHA256 || !validCapturedOutput(body.Output, body.OutputBytes, body.ElidedBytes) {
		return ConfinementReceipt{}, fmt.Errorf("terminal confinement receipt does not match the admitted boundary")
	}
	if i < 2 || !sealedNote(events, i-2, "ply.approval/v2") {
		return ConfinementReceipt{}, fmt.Errorf("terminal confinement receipt has no adjacent caged approval")
	}
	var approvalNote replayNote
	var approval approvalBody
	if decodeStrict(events[i-2].Data, &approvalNote) != nil || approvalNote.Source != "ply" || decodeStrict(approvalNote.Body, &approval) != nil || !validTerminalApproval(approval, contractID, job, directory, mayPath, maySHA256, cagePath, cageSHA256) || approval.Verdict != "spent" || approval.MayExitCode != 0 || body.ApprovalDigest != approval.Digest || body.ApprovalActionSHA256 != approval.ActionSHA256 || body.ScriptSHA256 != digestText(approval.Action.Script) || approval.Action.Confinement == nil || body.Workspace != approval.Action.Confinement.Workspace || body.TempDir != approval.Action.Confinement.TempDir {
		return ConfinementReceipt{}, fmt.Errorf("terminal confinement receipt does not match its adjacent approval")
	}
	return ConfinementReceipt{Seq: events[i].Seq, ExitCode: body.ExitCode, BodySHA256: digestBytes(note.Body), SealSHA256: seal.SHA256, ActionSHA256: body.ApprovalActionSHA256, MayHaveRun: body.MayHaveRun, Detail: body.Detail}, nil
}

func validCapturedOutput(output []byte, total, elided int64) bool {
	if total < 0 || elided < 0 || elided > total {
		return false
	}
	if elided == 0 {
		return int64(len(output)) == total
	}
	marker := []byte(fmt.Sprintf("\n[ply: %d bytes elided]\n", elided))
	return bytes.Contains(output, marker) && int64(len(output)) == total-elided+int64(len(marker))
}

func mayDigest(job, action string) string {
	sum := sha256.Sum256([]byte("may-v1\x00" + job + "\x00" + action))
	return fmt.Sprintf("%x", sum[:])
}

func selectAcceptedVerifier(data []byte, judgeMapSHA256, contractID, verifier, candidateSHA256, directory string) (VerifierReceipt, error) {
	events, err := decodeReplayEvents(data)
	if err != nil {
		return VerifierReceipt{}, err
	}
	if len(events) < 2 {
		return VerifierReceipt{}, fmt.Errorf("verified Ask replay has no terminal verifier receipt")
	}
	receiptIndex := len(events) - 2
	if !sealedNote(events, receiptIndex, "ply.verifier/v1") {
		return VerifierReceipt{}, fmt.Errorf("accepted Ply verifier receipt is not the terminal sealed record")
	}
	mapIndex := -1
	for i := receiptIndex - 1; i >= 0; i-- {
		if events[i].Type != "note" {
			continue
		}
		var probe struct {
			Kind string `json:"kind"`
		}
		if err := json.Unmarshal(events[i].Data, &probe); err != nil {
			return VerifierReceipt{}, fmt.Errorf("decode note before terminal verifier receipt: %w", err)
		}
		if probe.Kind == "bench.judge-map/v1" {
			mapIndex = i
			break
		}
	}
	if mapIndex < 0 || !sealedNote(events, mapIndex, "bench.judge-map/v1") {
		return VerifierReceipt{}, fmt.Errorf("latest judge map is absent or not sealed")
	}
	var mapNote replayNote
	if decodeStrict(events[mapIndex].Data, &mapNote) != nil || mapNote.Source != "bench" || digestBytes(mapNote.Body) != judgeMapSHA256 {
		return VerifierReceipt{}, fmt.Errorf("latest sealed judge map does not match admitted map %s", judgeMapSHA256)
	}
	var note replayNote
	var seal replaySeal
	if decodeStrict(events[receiptIndex].Data, &note) != nil || decodeStrict(events[receiptIndex+1].Data, &seal) != nil || note.Source != "ply" {
		return VerifierReceipt{}, fmt.Errorf("terminal sealed verifier receipt has invalid attribution")
	}
	var body verifierBody
	if decodeStrict(note.Body, &body) != nil || !validAcceptedVerifier(body, contractID, verifier, candidateSHA256, directory) {
		return VerifierReceipt{}, fmt.Errorf("terminal sealed verifier receipt does not match contract, check, candidate, and accepted outcome")
	}
	return VerifierReceipt{
		Seq: events[receiptIndex].Seq, BodySHA256: digestBytes(note.Body), SealSHA256: seal.SHA256,
		ContractID: body.ContractID, Phase: body.Phase, CandidateSHA256: body.CandidateSHA256,
		Verifier: body.Verifier, VerifierSHA256: body.VerifierSHA256, Outcome: body.Outcome, ExitCode: body.ExitCode,
	}, nil
}

func decodeReplayEvents(data []byte) ([]replayEvent, error) {
	var events []replayEvent
	for _, line := range bytes.Split(data, []byte{'\n'}) {
		if len(bytes.TrimSpace(line)) == 0 {
			continue
		}
		var event replayEvent
		if err := json.Unmarshal(line, &event); err != nil {
			return nil, fmt.Errorf("decode Ask replay event: %w", err)
		}
		events = append(events, event)
	}
	return events, nil
}

func sealedNote(events []replayEvent, i int, kind string) bool {
	if i < 0 || i+1 >= len(events) || events[i].Type != "note" || events[i+1].Type != "seal" {
		return false
	}
	var note replayNote
	var seal replaySeal
	return decodeStrict(events[i].Data, &note) == nil && note.Kind == kind && decodeStrict(events[i+1].Data, &seal) == nil && seal.Through == events[i].Seq
}

func validAcceptedVerifier(body verifierBody, contractID, verifier, candidateSHA256, directory string) bool {
	if body.ContractID != contractID || body.Verifier != verifier || body.CandidateSHA256 != candidateSHA256 || body.Directory != directory {
		return false
	}
	if body.Phase != "baseline" && body.Phase != "candidate" {
		return false
	}
	if strings.TrimSpace(body.Shell) == "" || !filepath.IsAbs(body.Shell) || body.TimeoutMS <= 0 || body.OutputBytes < 0 || body.ElidedBytes != 0 {
		return false
	}
	if body.VerifierSHA256 != digestText(body.Shell+"\x00"+body.Verifier) || body.OutputSHA256 != digestText(body.Output) || int64(len(body.Output)) != body.OutputBytes {
		return false
	}
	derived := "accepted"
	if body.StartError || body.ElidedBytes > 0 || body.ExitCode != 0 && body.ExitCode != 1 {
		derived = "broken"
	} else if body.ExitCode == 1 {
		derived = "rejected"
	}
	return derived == "accepted" && body.Outcome == derived && !body.Killed && body.ExitCode == 0
}

func decodeStrict(data []byte, dst any) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(dst); err != nil {
		return err
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return fmt.Errorf("trailing JSON")
	}
	return nil
}

func digestBytes(data []byte) string {
	return digestText(string(data))
}

func digestText(text string) string {
	digest := sha256.Sum256([]byte(text))
	return fmt.Sprintf("sha256:%x", digest)
}
