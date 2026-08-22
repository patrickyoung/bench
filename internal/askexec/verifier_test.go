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
