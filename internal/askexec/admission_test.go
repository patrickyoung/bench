package askexec

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestSelectAdmissionRequiresLatestExactSealedReplayCompleteRecord(t *testing.T) {
	contract := json.RawMessage(`{"version":2,"outcome":"done"}`)
	body := admissionBody{
		Status: "admitted", AdmittedBy: "interactive-user", ContractSHA: "sha256:canonical", ContractBodySHA: digestBytes(contract),
		IntentSHA: "sha256:intent", EvidenceSHA: "sha256:evidence", CheckSHA: "sha256:check", CheckAll: true,
		Workspace: "/work", Toolbox: "/tools", Skills: []string{"art"}, OutcomeID: "outcome",
		RevisionID: "sha256:revision", DraftSHA: "sha256:draft", Generation: 2, Parent: "sha256:parent", Contract: contract,
	}
	body.ContractID = recomputeAdmissionID(body)
	want := AdmissionExpectation{
		ContractID: body.ContractID, ContractSHA256: body.ContractSHA, ContractBodySHA256: body.ContractBodySHA, IntentSHA256: body.IntentSHA,
		CompilerEvidenceSHA256: body.EvidenceSHA, CheckSHA256: body.CheckSHA, CheckAll: body.CheckAll,
		Workspace: body.Workspace, Toolbox: body.Toolbox, Skills: body.Skills, OutcomeID: body.OutcomeID,
		RevisionID: body.RevisionID, DraftSHA256: body.DraftSHA, Generation: body.Generation, ParentRevisionID: body.Parent,
	}
	bodyJSON, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	replay := marshalReplayEvents(t, []map[string]any{
		{"seq": 1, "type": "note", "data": map[string]any{"source": "bench-user", "kind": "bench.contract/v3", "body": json.RawMessage(bodyJSON)}},
		{"seq": 2, "type": "seal", "data": map[string]any{"through": 1, "sha256": "sha256:seal"}},
	})
	if err := selectAdmission(replay, want); err != nil {
		t.Fatal(err)
	}

	tampered := want
	tampered.Workspace = "/elsewhere"
	if err := selectAdmission(replay, tampered); err == nil || !strings.Contains(err.Error(), "does not match") {
		t.Fatalf("tampered workspace admission error=%v", err)
	}

	newer := appendReplayEvent(t, replay, map[string]any{"seq": 3, "type": "note", "data": map[string]any{"source": "bench-user", "kind": "bench.contract/v3", "body": json.RawMessage(`{"broken":true}`)}})
	newer = appendReplayEvent(t, newer, map[string]any{"seq": 4, "type": "seal", "data": map[string]any{"through": 3, "sha256": "sha256:new"}})
	if err := selectAdmission(newer, want); err == nil {
		t.Fatal("newer malformed admission was skipped")
	}
}
