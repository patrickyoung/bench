package askexec

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/patrickyoung/bench/internal/filterexec"
)

type AdmissionExpectation struct {
	ContractID             string
	ContractSHA256         string
	ContractBodySHA256     string
	IntentSHA256           string
	CompilerEvidenceSHA256 string
	CheckSHA256            string
	CheckAll               bool
	Workspace              string
	Toolbox                string
	Skills                 []string
	OutcomeID              string
	RevisionID             string
	DraftSHA256            string
	Generation             int
	ParentRevisionID       string
}

type AdmissionReader interface {
	AdmittedContract(context.Context, string, AdmissionExpectation) error
}

type admissionBody struct {
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
}

// AdmittedContract verifies one Ask snapshot and requires its latest contract
// admission to be a sealed, replay-complete match for the controller's exact
// immutable revision and policy envelope.
func (r Runner) AdmittedContract(ctx context.Context, session string, want AdmissionExpectation) error {
	path := r.Path
	if path == "" {
		path = "ask"
	}
	replay := lockedBuffer{limit: 64 << 20}
	outcome := filterexec.Execute(ctx, filterexec.Spec{Path: path, Args: []string{"replay", "-check", "-json", session}}, replay.write)
	if outcome.Err != nil || outcome.ExitCode != 0 {
		return fmt.Errorf("read verified contract admission: %w: %s", outcome.Err, strings.TrimSpace(replay.stderr.String()))
	}
	if replay.exceeded() {
		return fmt.Errorf("read contract admission: replay exceeds 64 MiB")
	}
	return selectAdmission(replay.stdout.Bytes(), want)
}

func selectAdmission(data []byte, want AdmissionExpectation) error {
	events, err := decodeReplayEvents(data)
	if err != nil {
		return err
	}
	index := -1
	for i := len(events) - 1; i >= 0; i-- {
		if events[i].Type != "note" {
			continue
		}
		var probe struct {
			Kind string `json:"kind"`
		}
		if err := json.Unmarshal(events[i].Data, &probe); err != nil {
			return fmt.Errorf("decode latest contract admission candidate: %w", err)
		}
		if probe.Kind == "bench.contract/v3" {
			index = i
			break
		}
	}
	if index < 0 || !sealedNote(events, index, "bench.contract/v3") {
		return fmt.Errorf("latest contract admission is absent or not sealed")
	}
	var note replayNote
	if decodeStrict(events[index].Data, &note) != nil || note.Source != "bench-user" {
		return fmt.Errorf("latest sealed contract admission has invalid attribution")
	}
	var body admissionBody
	if err := decodeStrict(note.Body, &body); err != nil {
		return fmt.Errorf("decode latest sealed contract admission: %w", err)
	}
	var compactContract bytes.Buffer
	if err := json.Compact(&compactContract, body.Contract); err != nil {
		return fmt.Errorf("latest sealed contract admission contains invalid contract JSON")
	}
	contractDigest := sha256.Sum256(compactContract.Bytes())
	if body.Status != "admitted" || body.AdmittedBy != "interactive-user" ||
		body.ContractID != want.ContractID || body.ContractSHA != want.ContractSHA256 || body.ContractBodySHA != want.ContractBodySHA256 || body.ContractBodySHA != "sha256:"+hex.EncodeToString(contractDigest[:]) ||
		body.IntentSHA != want.IntentSHA256 || body.EvidenceSHA != want.CompilerEvidenceSHA256 || body.CheckSHA != want.CheckSHA256 ||
		body.CheckAll != want.CheckAll || body.Workspace != want.Workspace || body.Toolbox != want.Toolbox || !equalStrings(body.Skills, want.Skills) ||
		body.OutcomeID != want.OutcomeID || body.RevisionID != want.RevisionID || body.DraftSHA != want.DraftSHA256 ||
		body.Generation != want.Generation || body.Parent != want.ParentRevisionID || recomputeAdmissionID(body) != body.ContractID {
		return fmt.Errorf("latest sealed contract admission does not match the durable revision and policy")
	}
	return nil
}

func recomputeAdmissionID(body admissionBody) string {
	value, _ := json.Marshal(struct {
		Revision  string   `json:"revision_id"`
		Draft     string   `json:"draft_sha256"`
		Intent    string   `json:"intent_sha256"`
		Evidence  string   `json:"compiler_evidence_sha256"`
		Check     string   `json:"check_sha256"`
		CheckAll  bool     `json:"check_all"`
		Workspace string   `json:"workspace"`
		Toolbox   string   `json:"toolbox,omitempty"`
		Skills    []string `json:"skills"`
		Method    string   `json:"method"`
	}{
		Revision: body.RevisionID, Draft: body.DraftSHA, Intent: body.IntentSHA, Evidence: body.EvidenceSHA,
		Check: body.CheckSHA, CheckAll: body.CheckAll, Workspace: body.Workspace, Toolbox: body.Toolbox,
		Skills: append([]string{}, body.Skills...), Method: body.AdmittedBy,
	})
	digest := sha256.Sum256(value)
	return "sha256:" + hex.EncodeToString(digest[:])
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
