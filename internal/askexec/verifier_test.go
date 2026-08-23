package askexec

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/patrickyoung/bench/internal/plyexec"
)

func TestAcceptedVerifierComposesAsksVerifiedPublicReplay(t *testing.T) {
	mapBody := json.RawMessage(`{"contract_id":"sha256:contract","policy":"all"}`)
	receipt := verifierBody{
		ContractID: "sha256:contract", Phase: "baseline", CandidateSHA256: digestText(""),
		Verifier: "true", Shell: "/bin/sh", Directory: "/workspace", Outcome: "accepted",
		TimeoutMS: 30000, OutputSHA256: digestText(""), OutputBytes: 0,
	}
	receipt.VerifierSHA256 = digestText(receipt.Shell + "\x00" + receipt.Verifier)
	dir := t.TempDir()
	replayPath := filepath.Join(dir, "replay.jsonl")
	if err := os.WriteFile(replayPath, verifierReplay(t, mapBody, receipt), 0o600); err != nil {
		t.Fatal(err)
	}
	logPath := filepath.Join(dir, "argv")
	askPath := filepath.Join(dir, "ask")
	script := "#!/bin/sh\nset -eu\nprintf '%s\\n' \"$*\" >> \"$LOG\"\n[ \"$1\" = replay ]\n[ \"$2\" = -check ]\n[ \"$3\" = -json ]\ncat \"$REPLAY\"\n"
	if err := os.WriteFile(askPath, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("LOG", logPath)
	t.Setenv("REPLAY", replayPath)
	got, err := (Runner{Path: askPath}).AcceptedVerifier(context.Background(), "/sessions/run.jsonl", digestBytes(mapBody), receipt.ContractID, receipt.Verifier, receipt.CandidateSHA256, receipt.Directory)
	if err != nil || got.Outcome != "accepted" {
		t.Fatalf("receipt=%#v err=%v", got, err)
	}
	argv, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(argv) != "replay -check -json /sessions/run.jsonl\n" {
		t.Fatalf("Ask calls=%q", argv)
	}
}

func TestSelectAcceptedVerifierBindsMapContractCheckCandidateAndOutput(t *testing.T) {
	contractID := "sha256:contract"
	check := "test -s artifact"
	candidate := digestText("finished\n")
	dir := "/workspace"
	mapBody := json.RawMessage(`{"contract_id":"sha256:contract","policy":"all"}`)
	receipt := verifierBody{
		ContractID: contractID, Phase: "candidate", CandidateSHA256: candidate,
		Verifier: check, Shell: "/bin/sh", Directory: dir, Outcome: "accepted", ExitCode: 0,
		TimeoutMS: 30000, Output: "ok\n", OutputBytes: 3,
	}
	receipt.VerifierSHA256 = digestText(receipt.Shell + "\x00" + receipt.Verifier)
	receipt.OutputSHA256 = digestText(receipt.Output)
	replay := verifierReplay(t, mapBody, receipt)
	got, err := selectAcceptedVerifier(replay, digestBytes(mapBody), contractID, check, candidate, dir)
	if err != nil {
		t.Fatal(err)
	}
	if got.Seq != 4 || got.Phase != "candidate" || got.CandidateSHA256 != candidate || got.VerifierSHA256 != receipt.VerifierSHA256 || got.Outcome != "accepted" || got.ExitCode != 0 || got.BodySHA256 == "" || got.SealSHA256 != "sha256:receipt-seal" {
		t.Fatalf("receipt=%#v", got)
	}

	tests := []struct {
		name, mapSHA, contract, check, candidate, dir string
	}{
		{name: "map", mapSHA: "sha256:wrong", contract: contractID, check: check, candidate: candidate, dir: dir},
		{name: "contract", mapSHA: digestBytes(mapBody), contract: "sha256:wrong", check: check, candidate: candidate, dir: dir},
		{name: "check", mapSHA: digestBytes(mapBody), contract: contractID, check: "true", candidate: candidate, dir: dir},
		{name: "candidate", mapSHA: digestBytes(mapBody), contract: contractID, check: check, candidate: "sha256:wrong", dir: dir},
		{name: "directory", mapSHA: digestBytes(mapBody), contract: contractID, check: check, candidate: candidate, dir: "/other"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := selectAcceptedVerifier(replay, tt.mapSHA, tt.contract, tt.check, tt.candidate, tt.dir); err == nil {
				t.Fatal("mismatched receipt was accepted")
			}
		})
	}

	rejected := receipt
	rejected.ExitCode = 1
	rejected.Outcome = "rejected"
	rejectedBody, err := json.Marshal(rejected)
	if err != nil {
		t.Fatal(err)
	}
	replay = appendReplayEvent(t, replay, map[string]any{"seq": 6, "type": "note", "data": map[string]any{"source": "ply", "kind": "ply.verifier/v1", "body": json.RawMessage(rejectedBody)}})
	replay = appendReplayEvent(t, replay, map[string]any{"seq": 7, "type": "seal", "data": map[string]any{"through": 6, "sha256": "sha256:later-seal"}})
	if _, err := selectAcceptedVerifier(replay, digestBytes(mapBody), contractID, check, candidate, dir); err == nil {
		t.Fatal("older accepted receipt won over a later terminal rejection")
	}
}

func TestSelectAcceptedVerifierDerivesVerdictAndDigestsRatherThanTrustingProse(t *testing.T) {
	mapBody := json.RawMessage(`{"contract_id":"sha256:contract","policy":"all"}`)
	base := verifierBody{
		ContractID: "sha256:contract", Phase: "baseline", CandidateSHA256: digestText(""),
		Verifier: "true", Shell: "/bin/sh", Directory: "/workspace", Outcome: "accepted",
		TimeoutMS: 30000, Output: "", OutputBytes: 0,
	}
	base.VerifierSHA256 = digestText(base.Shell + "\x00" + base.Verifier)
	base.OutputSHA256 = digestText(base.Output)
	tests := []struct {
		name   string
		mutate func(*verifierBody)
	}{
		{name: "rejected exit", mutate: func(v *verifierBody) { v.ExitCode = 1 }},
		{name: "broken exit", mutate: func(v *verifierBody) { v.ExitCode = 2 }},
		{name: "killed", mutate: func(v *verifierBody) { v.Killed = true }},
		{name: "start error", mutate: func(v *verifierBody) { v.StartError = true }},
		{name: "elided", mutate: func(v *verifierBody) { v.ElidedBytes = 1 }},
		{name: "negative elided", mutate: func(v *verifierBody) { v.ElidedBytes = -1 }},
		{name: "empty shell", mutate: func(v *verifierBody) { v.Shell = ""; v.VerifierSHA256 = digestText("\x00" + v.Verifier) }},
		{name: "relative shell", mutate: func(v *verifierBody) { v.Shell = "sh"; v.VerifierSHA256 = digestText("sh\x00" + v.Verifier) }},
		{name: "zero timeout", mutate: func(v *verifierBody) { v.TimeoutMS = 0 }},
		{name: "negative timeout", mutate: func(v *verifierBody) { v.TimeoutMS = -1 }},
		{name: "negative output bytes", mutate: func(v *verifierBody) { v.OutputBytes = -1 }},
		{name: "forged verifier digest", mutate: func(v *verifierBody) { v.VerifierSHA256 = "sha256:wrong" }},
		{name: "forged output digest", mutate: func(v *verifierBody) { v.OutputSHA256 = "sha256:wrong" }},
		{name: "forged output length", mutate: func(v *verifierBody) { v.OutputBytes = 1 }},
		{name: "forged outcome", mutate: func(v *verifierBody) { v.Outcome = "rejected" }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			body := base
			tt.mutate(&body)
			replay := verifierReplay(t, mapBody, body)
			if _, err := selectAcceptedVerifier(replay, digestBytes(mapBody), base.ContractID, base.Verifier, base.CandidateSHA256, base.Directory); err == nil {
				t.Fatal("invalid receipt was accepted")
			}
		})
	}
}

func TestSelectAcceptedVerifierRequiresCurrentTerminalEvidence(t *testing.T) {
	mapBody := json.RawMessage(`{"contract_id":"sha256:contract","policy":"all"}`)
	receipt := verifierBody{
		ContractID: "sha256:contract", Phase: "baseline", CandidateSHA256: digestText(""),
		Verifier: "true", Shell: "/bin/sh", Directory: "/workspace", Outcome: "accepted",
		TimeoutMS: 30000, OutputSHA256: digestText(""), OutputBytes: 0,
	}
	receipt.VerifierSHA256 = digestText(receipt.Shell + "\x00" + receipt.Verifier)
	accepted := verifierReplay(t, mapBody, receipt)

	afterTurn := appendReplayEvent(t, accepted, map[string]any{"seq": 6, "type": "assistant", "data": map[string]any{"text": "later"}})
	afterTurn = appendReplayEvent(t, afterTurn, map[string]any{"seq": 7, "type": "done", "data": map[string]any{"status": "ok"}})
	if _, err := selectAcceptedVerifier(afterTurn, digestBytes(mapBody), receipt.ContractID, receipt.Verifier, receipt.CandidateSHA256, receipt.Directory); err == nil {
		t.Fatal("stale accepted receipt survived a later assistant turn")
	}

	newMapBody := json.RawMessage(`{"contract_id":"sha256:other","policy":"all"}`)
	afterMap := appendReplayEvent(t, accepted, map[string]any{"seq": 6, "type": "note", "data": map[string]any{"source": "bench", "kind": "bench.judge-map/v1", "body": newMapBody}})
	afterMap = appendReplayEvent(t, afterMap, map[string]any{"seq": 7, "type": "seal", "data": map[string]any{"through": 6, "sha256": "sha256:new-map"}})
	if _, err := selectAcceptedVerifier(afterMap, digestBytes(mapBody), receipt.ContractID, receipt.Verifier, receipt.CandidateSHA256, receipt.Directory); err == nil {
		t.Fatal("stale accepted receipt survived a newer judge map")
	}

	receiptBody, err := json.Marshal(receipt)
	if err != nil {
		t.Fatal(err)
	}
	newerMalformedMap := marshalReplayEvents(t, []map[string]any{
		{"seq": 1, "type": "session", "data": map[string]any{"id": "run"}},
		{"seq": 2, "type": "note", "data": map[string]any{"source": "bench", "kind": "bench.judge-map/v1", "body": mapBody}},
		{"seq": 3, "type": "seal", "data": map[string]any{"through": 2, "sha256": "sha256:map-seal"}},
		{"seq": 4, "type": "note", "data": map[string]any{"source": "bench", "kind": "bench.judge-map/v1", "body": map[string]any{}}},
		{"seq": 5, "type": "seal", "data": map[string]any{"through": 4, "sha256": "sha256:newer-map-seal"}},
		{"seq": 6, "type": "note", "data": map[string]any{"source": "ply", "kind": "ply.verifier/v1", "body": json.RawMessage(receiptBody)}},
		{"seq": 7, "type": "seal", "data": map[string]any{"through": 6, "sha256": "sha256:receipt-seal"}},
	})
	if _, err := selectAcceptedVerifier(newerMalformedMap, digestBytes(mapBody), receipt.ContractID, receipt.Verifier, receipt.CandidateSHA256, receipt.Directory); err == nil {
		t.Fatal("older matching judge map won over a newer malformed map")
	}
}

func TestSelectTerminalApprovalBindsExactMayTransition(t *testing.T) {
	contractID := "sha256:contract"
	job := "bench-job"
	mayPath := "/suite/bin/may"
	action := approvalActionBody{
		Version: 1, ContractID: contractID, Directory: "/workspace", Shell: "/bin/sh",
		Path: "/tools", TimeoutNS: 30_000_000_000, Script: "printf done > result",
	}
	actionBytes, err := json.Marshal(action)
	if err != nil {
		t.Fatal(err)
	}
	actionBytes = append(actionBytes, '\n')
	digest := mayDigest(job, string(actionBytes))
	result, err := json.Marshal(mayResultBody{Version: 1, Job: job, Digest: digest, Action: string(actionBytes), Verdict: "parked"})
	if err != nil {
		t.Fatal(err)
	}
	result = append(result, '\n')
	body := approvalBody{
		Version: 1, ContractID: contractID, Job: job, Digest: digest, Action: action,
		ActionSHA256: digestText(string(actionBytes)), Verdict: "parked", MayPath: mayPath,
		MaySHA256: "sha256:" + strings.Repeat("a", 64), MayArgv: []string{mayPath, "request", job},
		MayInputSHA256: digestText(string(actionBytes)), MayStdoutSHA256: digestText(string(result)),
		MayStdoutBytes: int64(len(result)), MayExitCode: 75,
	}
	maySHA256 := body.MaySHA256
	replay := approvalReplay(t, body)
	got, err := selectTerminalApproval(replay, contractID, job, action.Directory, mayPath, maySHA256)
	if err != nil {
		t.Fatal(err)
	}
	if got.Verdict != "parked" || got.Digest != digest || got.Action != string(actionBytes) || got.Seq != 2 || got.SealSHA256 != "sha256:approval-seal" {
		t.Fatalf("receipt=%#v", got)
	}

	for _, test := range []struct {
		name   string
		mutate func(*approvalBody)
	}{
		{name: "contract", mutate: func(v *approvalBody) { v.ContractID = "sha256:other" }},
		{name: "action contract", mutate: func(v *approvalBody) { v.Action.ContractID = "sha256:other" }},
		{name: "job", mutate: func(v *approvalBody) { v.Job = "other" }},
		{name: "directory", mutate: func(v *approvalBody) { v.Action.Directory = "/other" }},
		{name: "relative shell", mutate: func(v *approvalBody) { v.Action.Shell = "sh" }},
		{name: "timeout", mutate: func(v *approvalBody) { v.Action.TimeoutNS = 0 }},
		{name: "digest", mutate: func(v *approvalBody) { v.Digest = strings.Repeat("b", 64) }},
		{name: "input digest", mutate: func(v *approvalBody) { v.MayInputSHA256 = "sha256:wrong" }},
		{name: "stdout digest", mutate: func(v *approvalBody) { v.MayStdoutSHA256 = "sha256:wrong" }},
		{name: "stdout bytes", mutate: func(v *approvalBody) { v.MayStdoutBytes++ }},
		{name: "exit", mutate: func(v *approvalBody) { v.MayExitCode = 0 }},
		{name: "may path", mutate: func(v *approvalBody) { v.MayPath = "/other/may" }},
		{name: "may digest", mutate: func(v *approvalBody) { v.MaySHA256 = "sha256:" + strings.Repeat("b", 64) }},
	} {
		t.Run(test.name, func(t *testing.T) {
			changed := body
			changed.MayArgv = append([]string{}, body.MayArgv...)
			test.mutate(&changed)
			if _, err := selectTerminalApproval(approvalReplay(t, changed), contractID, job, action.Directory, mayPath, maySHA256); err == nil {
				t.Fatal("invalid approval receipt was accepted")
			}
		})
	}

	stale := appendReplayEvent(t, replay, map[string]any{"seq": 4, "type": "assistant", "data": map[string]any{"text": "later"}})
	stale = appendReplayEvent(t, stale, map[string]any{"seq": 5, "type": "done", "data": map[string]any{"status": "ok"}})
	if _, err := selectTerminalApproval(stale, contractID, job, action.Directory, mayPath, maySHA256); err == nil {
		t.Fatal("stale approval receipt survived a later turn")
	}
}

func TestSelectLatestApprovalResultRestoresOnlyAdjacentReceipt(t *testing.T) {
	contractID := "sha256:contract"
	job := "bench-job"
	mayPath := "/suite/bin/may"
	maySHA256 := "sha256:" + strings.Repeat("a", 64)
	action := approvalActionBody{Version: 1, ContractID: contractID, Directory: "/workspace", Shell: "/bin/sh", Path: "/tools", TimeoutNS: 1, Script: "publish"}
	actionBytes, _ := json.Marshal(action)
	actionBytes = append(actionBytes, '\n')
	digest := mayDigest(job, string(actionBytes))
	mayResult, _ := json.Marshal(mayResultBody{Version: 1, Job: job, Digest: digest, Action: string(actionBytes), Verdict: "parked"})
	mayResult = append(mayResult, '\n')
	body := approvalBody{
		Version: 1, ContractID: contractID, Job: job, Digest: digest, Action: action,
		ActionSHA256: digestText(string(actionBytes)), Verdict: "parked", MayPath: mayPath, MaySHA256: maySHA256,
		MayArgv: []string{mayPath, "request", job}, MayInputSHA256: digestText(string(actionBytes)),
		MayStdoutSHA256: digestText(string(mayResult)), MayStdoutBytes: int64(len(mayResult)), MayExitCode: 75,
	}
	bodyBytes, _ := json.Marshal(body)
	result := plyexec.ContractResult{
		ContractID: contractID, Status: "awaiting_approval", WorkerExitCode: 75,
		ApprovalPolicy: plyexec.ApprovalEveryAction, StopReason: "approval_required",
		ApprovalReceipt: &plyexec.ApprovalReceiptRef{
			Seq: 2, BodySHA256: digestBytes(bodyBytes), SealSHA256: "sha256:approval-seal",
			Job: job, Digest: digest, Verdict: "parked", Action: string(actionBytes), ActionSHA256: digestText(string(actionBytes)), MayPath: mayPath, MaySHA256: maySHA256,
		},
	}
	replay := approvalReplay(t, body)
	resultBytes, _ := json.Marshal(result)
	replay = appendReplayEvent(t, replay, map[string]any{"seq": 4, "type": "note", "data": map[string]any{"source": "bench", "kind": "bench.contract-result/v4", "body": json.RawMessage(resultBytes)}})
	replay = appendReplayEvent(t, replay, map[string]any{"seq": 5, "type": "seal", "data": map[string]any{"through": 4, "sha256": "sha256:result-seal"}})
	got, found, err := selectLatestApprovalResult(replay, contractID, job, action.Directory, mayPath, maySHA256)
	if err != nil || !found || got.Status != "awaiting_approval" || got.ApprovalReceipt == nil || got.ApprovalReceipt.Digest != digest {
		t.Fatalf("found=%v result=%#v err=%v", found, got, err)
	}

	stale := appendReplayEvent(t, replay, map[string]any{"seq": 6, "type": "assistant", "data": map[string]any{"text": "later"}})
	if _, found, err := selectLatestApprovalResult(stale, contractID, job, action.Directory, mayPath, maySHA256); err != nil || found {
		t.Fatalf("stale result restored: found=%v err=%v", found, err)
	}
}

func TestSelectTerminalConfinementBindsAdjacentSpentCagedApproval(t *testing.T) {
	contractID, job := "sha256:contract", "bench-job"
	mayPath, cagePath := "/suite/bin/may", "/suite/bin/cage"
	maySHA, cageSHA := "sha256:"+strings.Repeat("a", 64), "sha256:"+strings.Repeat("b", 64)
	conf := &approvalConfinementBody{Kind: "cage", CagePath: cagePath, CageSHA256: cageSHA, Argv: []string{cagePath, "-w", "/workspace", "--", "/bin/sh", "-c", "write then fail"}, Workspace: "/workspace", TempDir: "/control/run.cage-tmp", Network: false}
	action := approvalActionBody{Version: 2, ContractID: contractID, Directory: "/workspace", Shell: "/bin/sh", Path: "/tools", TimeoutNS: 1, Script: "write then fail", Confinement: conf}
	actionBytes, _ := json.Marshal(action)
	actionBytes = append(actionBytes, '\n')
	digest := mayDigest(job, string(actionBytes))
	mayResult, _ := json.Marshal(mayResultBody{Version: 1, Job: job, Digest: digest, Action: string(actionBytes), Verdict: "spent"})
	mayResult = append(mayResult, '\n')
	approval := approvalBody{Version: 2, ContractID: contractID, Job: job, Digest: digest, Action: action, ActionSHA256: digestText(string(actionBytes)), Verdict: "spent", MayPath: mayPath, MaySHA256: maySHA, MayArgv: []string{mayPath, "request", job}, MayInputSHA256: digestText(string(actionBytes)), MayStdoutSHA256: digestText(string(mayResult)), MayStdoutBytes: int64(len(mayResult)), MayExitCode: 0}
	approvalBytes, _ := json.Marshal(approval)
	output := []byte{0xff, 'x'}
	confinement := confinementBody{Version: 1, ContractID: contractID, ApprovalDigest: digest, ApprovalActionSHA256: approval.ActionSHA256, ScriptSHA256: digestText(action.Script), CagePath: cagePath, CageSHA256: cageSHA, Workspace: conf.Workspace, TempDir: conf.TempDir, ExitCode: 125, MayHaveRun: true, Detail: "child returned reserved status 125", Output: output, OutputSHA256: digestText(string(output)), OutputBytes: int64(len(output))}
	confinementBytes, _ := json.Marshal(confinement)
	replay := marshalReplayEvents(t, []map[string]any{{"seq": 1, "type": "session", "data": map[string]any{"id": "run"}}, {"seq": 2, "type": "note", "data": map[string]any{"source": "ply", "kind": "ply.approval/v2", "body": json.RawMessage(approvalBytes)}}, {"seq": 3, "type": "seal", "data": map[string]any{"through": 2, "sha256": "sha256:approval-seal"}}, {"seq": 4, "type": "note", "data": map[string]any{"source": "ply", "kind": "ply.confinement/v1", "body": json.RawMessage(confinementBytes)}}, {"seq": 5, "type": "seal", "data": map[string]any{"through": 4, "sha256": "sha256:confinement-seal"}}})
	got, err := selectTerminalConfinement(replay, contractID, job, "/workspace", mayPath, maySHA, cagePath, cageSHA)
	if err != nil || !got.MayHaveRun || got.ExitCode != 125 || got.ActionSHA256 != approval.ActionSHA256 {
		t.Fatalf("got=%#v err=%v", got, err)
	}
	result := plyexec.ContractResult{ContractID: contractID, Status: "confinement_failed", WorkerExitCode: 125, ApprovalPolicy: plyexec.ApprovalEveryAction, ActionConfinement: plyexec.ConfinementCage, StopReason: "confinement_failed", ConfinementReceipt: &plyexec.ConfinementReceiptRef{Seq: got.Seq, BodySHA256: got.BodySHA256, SealSHA256: got.SealSHA256, ActionSHA256: got.ActionSHA256, MayHaveRun: got.MayHaveRun, Detail: got.Detail}}
	resultBytes, _ := json.Marshal(result)
	withResult := appendReplayEvent(t, replay, map[string]any{"seq": 6, "type": "note", "data": map[string]any{"source": "bench", "kind": "bench.contract-result/v5", "body": json.RawMessage(resultBytes)}})
	withResult = appendReplayEvent(t, withResult, map[string]any{"seq": 7, "type": "seal", "data": map[string]any{"through": 6, "sha256": "sha256:result-seal"}})
	restored, found, err := selectLatestApprovalResult(withResult, contractID, job, "/workspace", mayPath, maySHA, cagePath, cageSHA)
	if err != nil || !found || restored.Status != "confinement_failed" {
		t.Fatalf("restored=%#v found=%v err=%v", restored, found, err)
	}
	confinement.ApprovalDigest = "wrong"
	badBytes, _ := json.Marshal(confinement)
	bad := bytes.Replace(replay, confinementBytes, badBytes, 1)
	if _, err := selectTerminalConfinement(bad, contractID, job, "/workspace", mayPath, maySHA, cagePath, cageSHA); err == nil {
		t.Fatal("mismatched confinement receipt accepted")
	}
	confinement.ApprovalDigest = digest
	confinement.Output = []byte("head\n[ply: 10 bytes elided]\ntail")
	confinement.OutputSHA256 = digestText(string(confinement.Output))
	confinement.OutputBytes = int64(len("headtail")) + 10
	confinement.ElidedBytes = 10
	elidedBytes, _ := json.Marshal(confinement)
	elidedReplay := bytes.Replace(replay, confinementBytes, elidedBytes, 1)
	if _, err := selectTerminalConfinement(elidedReplay, contractID, job, "/workspace", mayPath, maySHA, cagePath, cageSHA); err != nil {
		t.Fatalf("valid elided confinement receipt rejected: %v", err)
	}
	for _, counts := range [][2]int64{{-1, 0}, {1, -1}, {1, 2}} {
		if validCapturedOutput(nil, counts[0], counts[1]) {
			t.Fatalf("accepted invalid byte counts total=%d elided=%d", counts[0], counts[1])
		}
	}
}

func approvalReplay(t *testing.T, receipt approvalBody) []byte {
	t.Helper()
	body, err := json.Marshal(receipt)
	if err != nil {
		t.Fatal(err)
	}
	return marshalReplayEvents(t, []map[string]any{
		{"seq": 1, "type": "session", "data": map[string]any{"id": "run"}},
		{"seq": 2, "type": "note", "data": map[string]any{"source": "ply", "kind": "ply.approval/v1", "body": json.RawMessage(body)}},
		{"seq": 3, "type": "seal", "data": map[string]any{"through": 2, "sha256": "sha256:approval-seal"}},
	})
}

func verifierReplay(t *testing.T, mapBody json.RawMessage, receipt verifierBody) []byte {
	t.Helper()
	receiptBody, err := json.Marshal(receipt)
	if err != nil {
		t.Fatal(err)
	}
	events := []map[string]any{
		{"seq": 1, "type": "session", "data": map[string]any{"id": "run"}},
		{"seq": 2, "type": "note", "data": map[string]any{"source": "bench", "kind": "bench.judge-map/v1", "body": mapBody}},
		{"seq": 3, "type": "seal", "data": map[string]any{"through": 2, "sha256": "sha256:map-seal"}},
		{"seq": 4, "type": "note", "data": map[string]any{"source": "ply", "kind": "ply.verifier/v1", "body": json.RawMessage(receiptBody)}},
		{"seq": 5, "type": "seal", "data": map[string]any{"through": 4, "sha256": "sha256:receipt-seal"}},
	}
	return marshalReplayEvents(t, events)
}

func marshalReplayEvents(t *testing.T, events []map[string]any) []byte {
	t.Helper()
	var out bytes.Buffer
	for _, event := range events {
		line, err := json.Marshal(event)
		if err != nil {
			t.Fatal(err)
		}
		out.Write(line)
		out.WriteByte('\n')
	}
	return out.Bytes()
}

func appendReplayEvent(t *testing.T, replay []byte, event map[string]any) []byte {
	t.Helper()
	line, err := json.Marshal(event)
	if err != nil {
		t.Fatal(err)
	}
	replay = append(replay, line...)
	return append(replay, '\n')
}

func TestBodyDigestNamesExactVerifiedBytes(t *testing.T) {
	compact := json.RawMessage(`{"a":1,"b":2}`)
	spaced := json.RawMessage("{\n  \"a\": 1, \"b\": 2\n}")
	if digestBytes(compact) == digestBytes(spaced) {
		t.Fatalf("different body bytes share digest %s", digestBytes(compact))
	}
	want := fmt.Sprintf("sha256:%x", sha256.Sum256([]byte(`{"a":1,"b":2}`)))
	if got := digestBytes(compact); got != want || !strings.HasPrefix(got, "sha256:") {
		t.Fatalf("digest=%q want %q", got, want)
	}
}

func TestLockedReplayBufferStopsAtItsEvidenceLimit(t *testing.T) {
	b := lockedBuffer{limit: 4}
	b.write(Stdout, "abcdef")
	b.write(Stderr, "12345")
	if !b.exceeded() || b.stdout.String() != "abcd" || b.stderr.String() != "1234" {
		t.Fatalf("stdout=%q stderr=%q exceeded=%v", b.stdout.String(), b.stderr.String(), b.exceeded())
	}
}
