package askexec

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"path/filepath"
	"strings"
	"sync"

	"github.com/patrickyoung/bench/internal/filterexec"
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

// AcceptedVerifier reads one verified Ask snapshot, then selects the terminal
// sealed accepted Ply receipt matching the controller's exact contract,
// command, and candidate digest. It composes Ask's public replay interface;
// Bench never reads Ask's log format from disk.
func (r Runner) AcceptedVerifier(ctx context.Context, session, judgeMapSHA256, contractID, verifier, candidateSHA256, directory string) (VerifierReceipt, error) {
	path := r.Path
	if path == "" {
		path = "ask"
	}
	replay := lockedBuffer{limit: 64 << 20}
	outcome := filterexec.Execute(ctx, filterexec.Spec{Path: path, Args: []string{"replay", "-check", "-json", session}}, replay.write)
	if outcome.Err != nil || outcome.ExitCode != 0 {
		return VerifierReceipt{}, fmt.Errorf("read verified verifier receipts: %w: %s", outcome.Err, strings.TrimSpace(replay.stderr.String()))
	}
	if replay.exceeded() {
		return VerifierReceipt{}, fmt.Errorf("read verifier receipts: replay exceeds 64 MiB")
	}
	return selectAcceptedVerifier(replay.stdout.Bytes(), judgeMapSHA256, contractID, verifier, candidateSHA256, directory)
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
