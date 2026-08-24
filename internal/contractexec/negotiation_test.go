package contractexec

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/patrickyoung/bench/internal/plyexec"
)

func negotiationRequest(t *testing.T) plyexec.TaskRequest {
	t.Helper()
	dir := t.TempDir()
	return plyexec.TaskRequest{
		Dir: dir, Goal: "make the gallery", Session: filepath.Join(dir, "session.jsonl"),
		Skills:  []string{"ascii-cinema"},
		Options: plyexec.TaskOptions{IntentContract: true, Check: "test -s gallery.html"},
	}
}

func collectDraft(t *testing.T, events <-chan DraftEvent) DraftEvent {
	t.Helper()
	var terminal DraftEvent
	for event := range events {
		if event.Done {
			terminal = event
		}
	}
	return terminal
}

func collectPly(t *testing.T, events <-chan plyexec.Event) plyexec.Event {
	t.Helper()
	var terminal plyexec.Event
	for event := range events {
		if event.Done {
			terminal = event
		}
	}
	return terminal
}

func TestNegotiationCompilesDurableEditableDraftWithoutPly(t *testing.T) {
	req := negotiationRequest(t)
	ply := &fakePly{}
	ask := &fakeAsk{answer: fixtureContract}
	store := FileStore{Dir: filepath.Join(req.Dir, "contracts")}
	terminal := collectDraft(t, (Runner{Ask: ask, Ply: ply}).Compile(context.Background(), DraftRequest{Task: req, Store: store}))
	if terminal.ExitCode != 0 || terminal.Err != nil || terminal.Draft == nil {
		t.Fatalf("terminal=%#v", terminal)
	}
	if ply.calls != 0 {
		t.Fatalf("Ply ran while contract was only proposed: %d", ply.calls)
	}
	if ask.records != 1 || ask.record.Kind != "bench.contract-proposal/v1" {
		t.Fatalf("records=%d last=%#v", ask.records, ask.record)
	}
	loaded, status, err := store.Load()
	if err != nil || status != "draft" || loaded.DraftSHA256 == "" {
		t.Fatalf("loaded=%#v status=%q err=%v", loaded, status, err)
	}
	if _, err := os.Stat(store.DraftPath()); err != nil {
		t.Fatal(err)
	}
}

func TestRevisionMessageCarriesApprovalAuthority(t *testing.T) {
	current := Draft{Intent: "publish it", Contract: json.RawMessage(fixtureContract)}
	off := revisionMessage(current, "use the public channel", "", false, plyexec.ApprovalOff)
	gated := revisionMessage(current, "use the public channel", "", false, plyexec.ApprovalEveryAction)
	if !strings.Contains(off, "authorizes the described consequential scope") || !strings.Contains(off, "no execution-time May gate") {
		t.Fatalf("off revision boundary missing:\n%s", off)
	}
	if !strings.Contains(gated, "preparation only") || !strings.Contains(gated, "separate May decision") {
		t.Fatalf("gated revision boundary missing:\n%s", gated)
	}
}

func TestEveryActionPolicyIsVisibleDurableAndRequiredForAdmittedWork(t *testing.T) {
	req := negotiationRequest(t)
	req.Options.ApprovalPolicy = plyexec.ApprovalEveryAction
	may := filepath.Join(t.TempDir(), "may")
	if err := os.WriteFile(may, []byte("#!/bin/sh\nexit 0\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	ply := &fakePly{event: plyexec.Event{Done: true, ExitCode: 2}}
	ask := &fakeAsk{answer: fixtureContract}
	store := FileStore{Dir: filepath.Join(req.Dir, "contracts")}
	draft := *collectDraft(t, (Runner{Ask: ask, Ply: ply}).Compile(context.Background(), DraftRequest{Task: req, Store: store})).Draft
	if draft.Schema != 2 || draft.ApprovalPolicy != plyexec.ApprovalEveryAction || ask.record.Kind != "bench.contract-proposal/v2" {
		t.Fatalf("draft=%#v record=%#v", draft, ask.record)
	}
	body, err := os.ReadFile(store.DraftPath())
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(body), `"format": "bench.contract-draft/v2"`) || !strings.Contains(string(body), `"approval_policy": "every-action"`) {
		t.Fatalf("draft file omitted approval authority:\n%s", body)
	}

	terminal := collectPly(t, (Runner{Ask: ask, Ply: ply, MayPath: may}).Admit(context.Background(), AdmitRequest{
		Task: req, Draft: draft, Store: store, ExpectedDraftSHA256: draft.DraftSHA256,
	}))
	if terminal.ContractResult == nil || terminal.ContractResult.ApprovalPolicy != plyexec.ApprovalEveryAction || ply.calls != 1 || ply.req.Options.ApprovalPolicy != plyexec.ApprovalEveryAction || ply.req.Options.ContractID == "" {
		t.Fatalf("terminal=%#v ply=%#v", terminal, ply)
	}
	foundAdmission := false
	for _, record := range ask.recordLog {
		if record.Kind == "bench.contract/v4" && strings.Contains(record.JSON, `"approval_policy":"every-action"`) {
			foundAdmission = true
		}
	}
	if !foundAdmission || ask.record.Kind != "bench.contract-result/v4" {
		t.Fatalf("records=%#v", ask.recordLog)
	}
}

func TestEveryActionPolicyCannotBeRemovedAtAdmission(t *testing.T) {
	req := negotiationRequest(t)
	req.Options.ApprovalPolicy = plyexec.ApprovalEveryAction
	ply := &fakePly{}
	ask := &fakeAsk{answer: fixtureContract}
	store := FileStore{Dir: filepath.Join(req.Dir, "contracts")}
	draft := *collectDraft(t, (Runner{Ask: ask, Ply: ply}).Compile(context.Background(), DraftRequest{Task: req, Store: store})).Draft
	req.Options.ApprovalPolicy = plyexec.ApprovalOff
	terminal := collectPly(t, (Runner{Ask: ask, Ply: ply}).Admit(context.Background(), AdmitRequest{
		Task: req, Draft: draft, Store: store, ExpectedDraftSHA256: draft.DraftSHA256,
	}))
	if terminal.Err == nil || !strings.Contains(terminal.Err.Error(), "approval policy changed") || ply.calls != 0 {
		t.Fatalf("terminal=%#v ply=%d", terminal, ply.calls)
	}
}

func TestCagePolicyIsEditableAdmittedAndCannotBeRemovedAtRun(t *testing.T) {
	req := negotiationRequest(t)
	req.Options.ApprovalPolicy = plyexec.ApprovalEveryAction
	req.Options.ActionConfinement = plyexec.ConfinementCage
	ply := &fakePly{event: plyexec.Event{Done: true, ExitCode: 2}}
	ask := &fakeAsk{answer: fixtureContract}
	store := FileStore{Dir: filepath.Join(req.Dir, "contracts")}
	draft := *collectDraft(t, (Runner{Ask: ask, Ply: ply}).Compile(context.Background(), DraftRequest{Task: req, Store: store})).Draft
	if draft.Schema != 3 || draft.ActionConfinement != plyexec.ConfinementCage || ask.record.Kind != "bench.contract-proposal/v3" {
		t.Fatalf("draft=%#v record=%#v", draft, ask.record)
	}
	raw, err := os.ReadFile(store.DraftPath())
	if err != nil {
		t.Fatal(err)
	}
	body := string(raw)
	if !strings.Contains(body, `"format": "bench.contract-draft/v3"`) || !strings.Contains(body, `"action_confinement": "cage"`) {
		t.Fatalf("draft file:\n%s", body)
	}
	controllerDir := t.TempDir()
	may := filepath.Join(controllerDir, "may")
	cage := filepath.Join(controllerDir, "cage")
	for _, path := range []string{may, cage} {
		if err := os.WriteFile(path, []byte("#!/bin/sh\nexit 0\n"), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	terminal := collectPly(t, (Runner{Ask: ask, Ply: ply, MayPath: may, CagePath: cage}).Admit(context.Background(), AdmitRequest{Task: req, Draft: draft, Store: store, ExpectedDraftSHA256: draft.DraftSHA256}))
	if terminal.ContractResult == nil || terminal.ContractResult.ActionConfinement != plyexec.ConfinementCage || ask.record.Kind != "bench.contract-result/v5" {
		t.Fatalf("terminal=%#v record=%#v", terminal, ask.record)
	}
	found := false
	for _, record := range ask.recordLog {
		if record.Kind == "bench.contract/v5" {
			found = true
		}
	}
	if !found {
		t.Fatalf("records=%#v", ask.recordLog)
	}
	admitted, status, err := store.Load()
	if err != nil || status != "admitted" {
		t.Fatalf("load admitted status=%q err=%v", status, err)
	}
	req.Options.ActionConfinement = plyexec.ConfinementOff
	bad := collectPly(t, (Runner{Ask: ask, Ply: ply, MayPath: may, CagePath: cage}).Run(context.Background(), RunRequest{Task: req, Draft: admitted, Store: store}))
	if bad.Err == nil || !strings.Contains(bad.Err.Error(), "action confinement changed") {
		t.Fatalf("bad=%#v", bad)
	}
}

func TestAdmissionRequiresExactReviewedBytesBeforePly(t *testing.T) {
	req := negotiationRequest(t)
	ply := &fakePly{event: plyexec.Event{Done: true, ExitCode: 0}}
	ask := &fakeAsk{answer: fixtureContract}
	store := FileStore{Dir: filepath.Join(req.Dir, "contracts")}
	draft := *collectDraft(t, (Runner{Ask: ask, Ply: ply}).Compile(context.Background(), DraftRequest{Task: req, Store: store})).Draft

	body, err := os.ReadFile(store.DraftPath())
	if err != nil {
		t.Fatal(err)
	}
	body = []byte(strings.Replace(string(body), "A complete gallery exists", "A revised gallery exists", 1))
	if err := os.WriteFile(store.DraftPath(), body, 0o600); err != nil {
		t.Fatal(err)
	}
	terminal := collectPly(t, (Runner{Ask: ask, Ply: ply}).Admit(context.Background(), AdmitRequest{
		Task: req, Draft: draft, Store: store, ExpectedDraftSHA256: draft.DraftSHA256,
	}))
	if terminal.ExitCode == 0 || terminal.Err == nil || ply.calls != 0 {
		t.Fatalf("terminal=%#v ply=%d", terminal, ply.calls)
	}
	if _, status, err := store.Load(); err != nil || status != "draft" {
		t.Fatalf("status=%q err=%v", status, err)
	}
}

func TestAdmissionRecordFailureLeavesReviewableDraftAndRetryRunsOnce(t *testing.T) {
	req := negotiationRequest(t)
	ply := &fakePly{event: plyexec.Event{Done: true, ExitCode: 0}}
	ask := &fakeAsk{answer: fixtureContract, recordErr: errors.New("record unavailable"), recordErrAt: 2}
	store := FileStore{Dir: filepath.Join(req.Dir, "contracts")}
	draft := *collectDraft(t, (Runner{Ask: ask, Ply: ply}).Compile(context.Background(), DraftRequest{Task: req, Store: store})).Draft
	terminal := collectPly(t, (Runner{Ask: ask, Ply: ply}).Admit(context.Background(), AdmitRequest{
		Task: req, Draft: draft, Store: store, ExpectedDraftSHA256: draft.DraftSHA256,
	}))
	if terminal.ExitCode == 0 || terminal.Err == nil || ply.calls != 0 {
		t.Fatalf("failed admission terminal=%#v ply=%d", terminal, ply.calls)
	}
	loaded, status, err := store.Load()
	if err != nil || status != "draft" || loaded.RecordedDraftSHA256 != loaded.DraftSHA256 {
		t.Fatalf("draft=%#v status=%q err=%v", loaded, status, err)
	}
	ask.recordErr = nil
	ask.recordErrAt = 0
	_ = collectPly(t, (Runner{Ask: ask, Ply: ply}).Admit(context.Background(), AdmitRequest{
		Task: req, Draft: loaded, Store: store, ExpectedDraftSHA256: loaded.DraftSHA256,
	}))
	if ply.calls != 1 {
		t.Fatalf("retry Ply calls=%d", ply.calls)
	}
	if _, status, err := store.Load(); err != nil || status != "admitted" {
		t.Fatalf("retry status=%q err=%v", status, err)
	}
}

func TestRetryRequiresVerifiedSealedAdmissionBeforePly(t *testing.T) {
	req := negotiationRequest(t)
	ply := &fakePly{event: plyexec.Event{Done: true, ExitCode: 0}}
	ask := &fakeAsk{answer: fixtureContract}
	store := FileStore{Dir: filepath.Join(req.Dir, "contracts")}
	draft := *collectDraft(t, (Runner{Ask: ask, Ply: ply}).Compile(context.Background(), DraftRequest{Task: req, Store: store})).Draft
	_ = collectPly(t, (Runner{Ask: ask, Ply: ply}).Admit(context.Background(), AdmitRequest{
		Task: req, Draft: draft, Store: store, ExpectedDraftSHA256: draft.DraftSHA256,
	}))
	admitted, status, err := store.Load()
	if err != nil || status != "admitted" {
		t.Fatalf("status=%q err=%v", status, err)
	}
	ply.calls = 0
	ask.admissionErr = errors.New("sealed admission missing")
	terminal := collectPly(t, (Runner{Ask: ask, Ply: ply}).Run(context.Background(), RunRequest{Task: req, Draft: admitted, Store: store}))
	if terminal.ExitCode == 0 || terminal.Err == nil || !strings.Contains(terminal.Err.Error(), "verify admitted contract") || ply.calls != 0 {
		t.Fatalf("terminal=%#v ply=%d", terminal, ply.calls)
	}
}

func TestInvalidLoopRerunStopsBeforeReplayRecordsOrPly(t *testing.T) {
	req := negotiationRequest(t)
	store := FileStore{Dir: filepath.Join(req.Dir, "contracts")}
	draft, err := store.SaveDraft(contractDraftFixture(t, req, "outcome", 1, ""))
	if err != nil {
		t.Fatal(err)
	}
	draft, err = store.MarkDraftRecorded(draft)
	if err != nil {
		t.Fatal(err)
	}
	draft, err = store.PublishRevision(draft, draft.DraftSHA256)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.MarkAdmitted(draft); err != nil {
		t.Fatal(err)
	}
	ask := &fakeAsk{}
	ply := &fakePly{}
	req.Options = plyexec.TaskOptions{IntentContract: true, Loop: true}
	terminal := collectPly(t, (Runner{Ask: ask, Ply: ply}).Run(context.Background(), RunRequest{Task: req, Draft: draft, Store: store}))
	if terminal.ExitCode == 0 || terminal.Err == nil || !strings.Contains(terminal.Err.Error(), "configured check") || ask.admissions != 0 || ask.records != 0 || ply.calls != 0 {
		t.Fatalf("terminal=%#v admissions=%d records=%d ply=%d", terminal, ask.admissions, ask.records, ply.calls)
	}
}

func TestUnresolvedContractCannotBeAdmittedOrRun(t *testing.T) {
	req := negotiationRequest(t)
	question := strings.Replace(fixtureContract, `"open_questions": []`, `"open_questions": ["Which printer?"]`, 1)
	ply := &fakePly{}
	ask := &fakeAsk{answer: question}
	store := FileStore{Dir: filepath.Join(req.Dir, "contracts")}
	draft := *collectDraft(t, (Runner{Ask: ask, Ply: ply}).Compile(context.Background(), DraftRequest{Task: req, Store: store})).Draft
	terminal := collectPly(t, (Runner{Ask: ask, Ply: ply}).Admit(context.Background(), AdmitRequest{
		Task: req, Draft: draft, Store: store, ExpectedDraftSHA256: draft.DraftSHA256,
	}))
	if terminal.ExitCode != 2 || ply.calls != 0 {
		t.Fatalf("terminal=%#v ply=%d", terminal, ply.calls)
	}
	if _, status, err := store.Load(); err != nil || status != "draft" {
		t.Fatalf("status=%q err=%v", status, err)
	}
}

func TestAdmissionRejectsChangedToolGrantBeforePly(t *testing.T) {
	req := negotiationRequest(t)
	req.Toolbox = filepath.Join(req.Dir, "tools-a")
	ply := &fakePly{}
	ask := &fakeAsk{answer: fixtureContract}
	store := FileStore{Dir: filepath.Join(req.Dir, "contracts")}
	draft := *collectDraft(t, (Runner{Ask: ask, Ply: ply}).Compile(context.Background(), DraftRequest{Task: req, Store: store})).Draft
	req.Toolbox = filepath.Join(req.Dir, "tools-b")
	terminal := collectPly(t, (Runner{Ask: ask, Ply: ply}).Admit(context.Background(), AdmitRequest{
		Task: req, Draft: draft, Store: store, ExpectedDraftSHA256: draft.DraftSHA256,
	}))
	if terminal.ExitCode == 0 || terminal.Err == nil || !strings.Contains(terminal.Err.Error(), "tool grant changed") || ply.calls != 0 {
		t.Fatalf("terminal=%#v ply=%d", terminal, ply.calls)
	}
}

func TestManualImportUsesAskRecordAndNeverPly(t *testing.T) {
	req := negotiationRequest(t)
	ply := &fakePly{}
	ask := &fakeAsk{answer: fixtureContract}
	store := FileStore{Dir: filepath.Join(req.Dir, "contracts")}
	_ = collectDraft(t, (Runner{Ask: ask, Ply: ply}).Compile(context.Background(), DraftRequest{Task: req, Store: store}))
	records := ask.records
	draft, err := (Runner{Ask: ask, Ply: ply}).Import(context.Background(), ImportRequest{Session: req.Session, Store: store})
	if err != nil || draft.DraftSHA256 == "" || ask.records != records+1 || ask.record.Kind != "bench.contract-proposal/v1" || ply.calls != 0 {
		t.Fatalf("draft=%#v err=%v records=%d kind=%q ply=%d", draft, err, ask.records, ask.record.Kind, ply.calls)
	}
	body, err := os.ReadFile(store.DraftPath())
	if err != nil {
		t.Fatal(err)
	}
	changed := strings.Replace(string(body), `"version": 2,`, `"version": 2, "surprise": true,`, 1)
	if changed == string(body) {
		t.Fatal("test did not corrupt the manual contract")
	}
	body = []byte(changed)
	if err := os.WriteFile(store.DraftPath(), body, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := (Runner{Ask: ask, Ply: ply}).Import(context.Background(), ImportRequest{Session: req.Session, Store: store}); err == nil {
		t.Fatal("invalid manual contract import succeeded")
	}
	if ask.records != records+1 || ply.calls != 0 {
		t.Fatalf("invalid import recorded or ran work: records=%d ply=%d", ask.records, ply.calls)
	}
}

func TestConcurrentImmutableRevisionPublicationIsIdempotent(t *testing.T) {
	req := negotiationRequest(t)
	store := FileStore{Dir: filepath.Join(req.Dir, "contracts")}
	_, canonical, digest, err := Parse(fixtureContract)
	if err != nil {
		t.Fatal(err)
	}
	draft, err := store.SaveDraft(Draft{
		Schema: 1, OutcomeID: "outcome", Generation: 1, Intent: req.Goal, Workspace: req.Dir,
		Contract: []byte(canonical), ContractSHA256: "sha256:" + digest,
		CompilerEvidenceSHA256: sha256Text("evidence"), Check: req.Options.Check,
		CheckSHA256: sha256Text(req.Options.Check), Skills: append([]string{}, req.Skills...),
	})
	if err != nil {
		t.Fatal(err)
	}
	draft, err = store.MarkDraftRecorded(draft)
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	results := make(chan error, 2)
	for range 2 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := store.PublishRevision(draft, draft.DraftSHA256)
			results <- err
		}()
	}
	wg.Wait()
	close(results)
	successes := 0
	for err := range results {
		if err == nil {
			successes++
		}
	}
	if successes != 2 {
		t.Fatalf("successful identical publications=%d, want 2", successes)
	}
}

func TestManualWhitespaceChangesExactDraftDigestButNotContractDigest(t *testing.T) {
	req := negotiationRequest(t)
	store := FileStore{Dir: filepath.Join(req.Dir, "contracts")}
	draft, err := store.SaveDraft(contractDraftFixture(t, req, "outcome", 1, ""))
	if err != nil {
		t.Fatal(err)
	}
	body, err := os.ReadFile(store.DraftPath())
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(store.DraftPath(), append(body, '\n'), 0o600); err != nil {
		t.Fatal(err)
	}
	loaded, status, err := store.Load()
	if err != nil || status != "draft" {
		t.Fatalf("status=%q err=%v", status, err)
	}
	if loaded.DraftSHA256 == draft.DraftSHA256 || loaded.ContractSHA256 != draft.ContractSHA256 {
		t.Fatalf("before=%#v after=%#v", draft, loaded)
	}
	if _, err := store.PublishRevision(loaded, loaded.DraftSHA256); err == nil || !strings.Contains(err.Error(), "not been sealed") {
		t.Fatalf("raw manual edit became admissible without Import: %v", err)
	}
}

func TestStaleConcurrentDraftRevisionCannotOverwriteNewerProposal(t *testing.T) {
	req := negotiationRequest(t)
	store := FileStore{Dir: filepath.Join(req.Dir, "contracts")}
	root, err := store.SaveDraft(contractDraftFixture(t, req, "outcome", 1, ""))
	if err != nil {
		t.Fatal(err)
	}
	first := root
	first.Generation++
	first.Contract = []byte(strings.Replace(fixtureContract, "A complete gallery", "A first revised gallery", 1))
	if _, err := store.SaveDraftCAS(first, root.DraftSHA256); err != nil {
		t.Fatal(err)
	}
	stale := root
	stale.Generation++
	stale.Contract = []byte(strings.Replace(fixtureContract, "A complete gallery", "A stale revised gallery", 1))
	if _, err := store.SaveDraftCAS(stale, root.DraftSHA256); err == nil || !strings.Contains(err.Error(), "changed") {
		t.Fatalf("stale revision overwrite err=%v", err)
	}
	loaded, status, err := store.Load()
	if err != nil || status != "draft" || !strings.Contains(string(loaded.Contract), "first revised") {
		t.Fatalf("loaded=%#v status=%q err=%v", loaded, status, err)
	}
}

func TestLoadRecoversUnrecordedDraftWhenStateWriteWasLost(t *testing.T) {
	req := negotiationRequest(t)
	store := FileStore{Dir: filepath.Join(req.Dir, "contracts")}
	draft, err := store.SaveDraft(contractDraftFixture(t, req, "outcome", 1, ""))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(store.Dir, "state.json")); err != nil {
		t.Fatal(err)
	}
	recovered, status, err := store.Load()
	if err != nil || status != "draft" || recovered.DraftSHA256 != draft.DraftSHA256 || recovered.RecordedDraftSHA256 != "" {
		t.Fatalf("recovered=%#v status=%q err=%v", recovered, status, err)
	}
	if _, err := store.PublishRevision(recovered, recovered.DraftSHA256); err == nil || !strings.Contains(err.Error(), "not been sealed") {
		t.Fatalf("recovered unrecorded bytes became admissible: %v", err)
	}
}

func TestLoadAndImportRecoverNewDraftWithStaleState(t *testing.T) {
	req := negotiationRequest(t)
	store := FileStore{Dir: filepath.Join(req.Dir, "contracts")}
	root, err := store.SaveDraft(contractDraftFixture(t, req, "outcome", 1, ""))
	if err != nil {
		t.Fatal(err)
	}
	root, err = store.MarkDraftRecorded(root)
	if err != nil {
		t.Fatal(err)
	}
	oldState, err := os.ReadFile(filepath.Join(store.Dir, "state.json"))
	if err != nil {
		t.Fatal(err)
	}
	newer := root
	newer.Generation++
	newer.Contract = []byte(strings.Replace(fixtureContract, "A complete gallery", "A recovered gallery", 1))
	newer, err = store.SaveDraftCAS(newer, root.DraftSHA256)
	if err != nil {
		t.Fatal(err)
	}
	// Simulate power loss after draft.json was published but before its state
	// pointer replaced the preceding generation.
	if err := os.WriteFile(filepath.Join(store.Dir, "state.json"), oldState, 0o600); err != nil {
		t.Fatal(err)
	}
	recovered, status, err := store.Load()
	if err != nil || status != "draft" || recovered.DraftSHA256 != newer.DraftSHA256 || recovered.RecordedDraftSHA256 != "" {
		t.Fatalf("recovered=%#v status=%q err=%v", recovered, status, err)
	}
	ply := &fakePly{}
	ask := &fakeAsk{}
	sealed, err := (Runner{Ask: ask, Ply: ply}).Import(context.Background(), ImportRequest{Session: req.Session, Store: store})
	if err != nil || sealed.RecordedDraftSHA256 != sealed.DraftSHA256 || ply.calls != 0 || ask.record.Kind != "bench.contract-proposal/v1" {
		t.Fatalf("sealed=%#v err=%v ply=%d record=%#v", sealed, err, ply.calls, ask.record)
	}
}

func TestLoadAndImportRecoverNewOutcomeOverStaleAdmittedState(t *testing.T) {
	req := negotiationRequest(t)
	store := FileStore{Dir: filepath.Join(req.Dir, "contracts")}
	root, err := store.SaveDraft(contractDraftFixture(t, req, "old-outcome", 1, ""))
	if err != nil {
		t.Fatal(err)
	}
	root, err = store.MarkDraftRecorded(root)
	if err != nil {
		t.Fatal(err)
	}
	root, err = store.PublishRevision(root, root.DraftSHA256)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.MarkAdmitted(root); err != nil {
		t.Fatal(err)
	}
	oldState, err := os.ReadFile(filepath.Join(store.Dir, "state.json"))
	if err != nil {
		t.Fatal(err)
	}
	newOutcome, err := store.SaveDraftCAS(contractDraftFixture(t, req, "new-outcome", 1, ""), "")
	if err != nil {
		t.Fatal(err)
	}
	// Simulate power loss after the new root proposal was published while the
	// state pointer still names the preceding admitted outcome.
	if err := os.WriteFile(filepath.Join(store.Dir, "state.json"), oldState, 0o600); err != nil {
		t.Fatal(err)
	}
	recovered, status, err := store.Load()
	if err != nil || status != "draft" || recovered.OutcomeID != "new-outcome" || recovered.DraftSHA256 != newOutcome.DraftSHA256 || recovered.RecordedDraftSHA256 != "" {
		t.Fatalf("recovered=%#v status=%q err=%v", recovered, status, err)
	}
	ply := &fakePly{}
	ask := &fakeAsk{}
	sealed, err := (Runner{Ask: ask, Ply: ply}).Import(context.Background(), ImportRequest{Session: req.Session, Store: store})
	if err != nil || sealed.RecordedDraftSHA256 != sealed.DraftSHA256 || sealed.OutcomeID != "new-outcome" || ply.calls != 0 {
		t.Fatalf("sealed=%#v err=%v ply=%d", sealed, err, ply.calls)
	}
}

func TestAmendmentPublishesChildWithoutChangingOldRevision(t *testing.T) {
	req := negotiationRequest(t)
	store := FileStore{Dir: filepath.Join(req.Dir, "contracts")}
	root, err := store.SaveDraft(contractDraftFixture(t, req, "outcome", 1, ""))
	if err != nil {
		t.Fatal(err)
	}
	root, err = store.MarkDraftRecorded(root)
	if err != nil {
		t.Fatal(err)
	}
	root, err = store.PublishRevision(root, root.DraftSHA256)
	if err != nil {
		t.Fatal(err)
	}
	rootPath := filepath.Join(store.Dir, "revisions", strings.TrimPrefix(root.RevisionID, "sha256:")+".json")
	rootBytes, err := os.ReadFile(rootPath)
	if err != nil {
		t.Fatal(err)
	}
	child := root
	child.Generation++
	child.ParentRevisionID = root.RevisionID
	child.Contract = []byte(strings.Replace(fixtureContract, "A complete gallery", "A revised gallery", 1))
	child, err = store.SaveDraft(child)
	if err != nil {
		t.Fatal(err)
	}
	child, err = store.MarkDraftRecorded(child)
	if err != nil {
		t.Fatal(err)
	}
	child, err = store.PublishRevision(child, child.DraftSHA256)
	if err != nil {
		t.Fatal(err)
	}
	after, err := os.ReadFile(rootPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != string(rootBytes) || child.ParentRevisionID != root.RevisionID || child.RevisionID == root.RevisionID || child.ContractID == root.ContractID {
		t.Fatalf("root=%#v child=%#v old revision changed=%v", root, child, string(after) != string(rootBytes))
	}
}

func contractDraftFixture(t *testing.T, req plyexec.TaskRequest, outcome string, generation int, parent string) Draft {
	t.Helper()
	_, canonical, digest, err := Parse(fixtureContract)
	if err != nil {
		t.Fatal(err)
	}
	return Draft{
		Schema: 1, OutcomeID: outcome, Generation: generation, ParentRevisionID: parent,
		Intent: req.Goal, Workspace: req.Dir, Toolbox: req.Toolbox, Contract: []byte(canonical),
		ContractSHA256: "sha256:" + digest, CompilerEvidenceSHA256: sha256Text("evidence"),
		Check: req.Options.Check, CheckSHA256: sha256Text(req.Options.Check), Skills: append([]string{}, req.Skills...),
	}
}
