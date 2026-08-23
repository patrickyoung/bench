package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestReadCasesRequiresExactBalancedCorpus(t *testing.T) {
	data := testCases(t)
	cases, err := readCases(data)
	if err != nil || len(cases) != 8 {
		t.Fatalf("readCases: len=%d err=%v", len(cases), err)
	}
	checks := []struct {
		name string
		edit func([]map[string]any)
	}{
		{"duplicate", func(rows []map[string]any) { rows[1]["id"] = rows[0]["id"] }},
		{"traversal", func(rows []map[string]any) { rows[0]["fixture"] = "../outside" }},
		{"wrong prefix", func(rows []map[string]any) { rows[0]["id"] = "l09" }},
		{"wrong balance", func(rows []map[string]any) {
			rows[0]["class"], rows[0]["id"], rows[0]["check"], rows[0]["check_all"] = "checked", "l09", true, true
		}},
	}
	for _, tc := range checks {
		t.Run(tc.name, func(t *testing.T) {
			rows := decodeCaseMaps(t, data)
			tc.edit(rows)
			if _, err := readCases(encodeCaseMaps(t, rows)); err == nil {
				t.Fatal("invalid corpus accepted")
			}
		})
	}
	t.Run("unknown field", func(t *testing.T) {
		rows := decodeCaseMaps(t, data)
		rows[0]["unexpected"] = true
		if _, err := readCases(encodeCaseMaps(t, rows)); err == nil {
			t.Fatal("unknown field accepted")
		}
	})
}

func TestLiveHelperProcess(t *testing.T) {
	role := os.Getenv("BENCH_AUTO_LIVE_HELPER")
	if role == "" {
		return
	}
	os.Exit(runLiveHelper(role, helperArgs(), os.Stdout, os.Stderr))
}

func TestPrepareHumanAdmissionScoreEndToEnd(t *testing.T) {
	t.Setenv("BENCH_AUTO_LIVE_REQUIRE_ACTION_SHELL", "1")
	t.Setenv("BENCH_AUTO_LIVE_DROP_ACTION_SHELL", "1")
	repoRoot, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	liveRoot := filepath.Join(repoRoot, "eval", "auto", "live")
	casesPath := filepath.Join(liveRoot, "cases.jsonl")
	casesData, err := os.ReadFile(casesPath)
	if err != nil {
		t.Fatal(err)
	}
	helpers := t.TempDir()
	testBinary, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	paths := map[string]string{}
	for _, role := range []string{"bench", "ask", "ply"} {
		path := filepath.Join(helpers, role+" helper")
		body := "#!/bin/sh\nBENCH_AUTO_LIVE_HELPER=" + shellQuote(role) + " exec " + shellQuote(testBinary) + " -test.run=TestLiveHelperProcess -- \"$@\"\n"
		if err := os.WriteFile(path, []byte(body), 0o700); err != nil {
			t.Fatal(err)
		}
		paths[role] = path
	}
	actionShell := filepath.Join(helpers, "action shell")
	actionShellBytes := []byte("#!/bin/sh\nexec /bin/sh \"$@\"\n")
	if err := os.WriteFile(actionShell, actionShellBytes, 0o700); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(t.TempDir(), "experiment")
	var prepareOut, prepareErr bytes.Buffer
	code := run([]string{"prepare", "-cases", casesPath, "-expect", digestBytes(casesData), "-bench", paths["bench"], "-ask", paths["ask"], "-ply", paths["ply"], "-action-shell", actionShell, "-model", "fake/model", "-out", out}, &prepareOut, &prepareErr)
	if code != 0 {
		t.Fatalf("prepare exit=%d stdout=%s stderr=%s", code, prepareOut.String(), prepareErr.String())
	}

	var earlyOut, earlyErr bytes.Buffer
	if code := run([]string{"score", "-out", out}, &earlyOut, &earlyErr); code != 2 || !strings.Contains(earlyErr.String(), "awaits explicit admission") {
		t.Fatalf("early score exit=%d stdout=%s stderr=%s", code, earlyOut.String(), earlyErr.String())
	}
	matches, err := filepath.Glob(filepath.Join(out, ".score-*"))
	if err != nil || len(matches) != 0 {
		t.Fatalf("early score left scratch=%v err=%v", matches, err)
	}

	var manifest runManifest
	if err := readJSON(filepath.Join(out, "run.json"), &manifest); err != nil {
		t.Fatal(err)
	}
	physicalActionShell, err := filepath.EvalSymlinks(actionShell)
	if err != nil {
		t.Fatal(err)
	}
	if manifest.ActionShellSource != physicalActionShell || manifest.ActionShellPath != filepath.Join(out, "controller", "action-shell") ||
		manifest.Schema != runSchemaV2 || manifest.ActionShellProtocol != actionShellProtocol || manifest.ActionShellSHA256 != digestBytes(actionShellBytes) {
		t.Fatalf("action shell binding=%+v", manifest)
	}
	accepts := 0
	for _, a := range manifest.Arms {
		if a.AcceptScript == "" {
			continue
		}
		accepts++
		cmd := exec.Command("/bin/sh", a.AcceptScript)
		err := cmd.Run()
		if a.Class == "routine" {
			var exitErr *exec.ExitError
			if !errors.As(err, &exitErr) || exitErr.ExitCode() != 2 {
				t.Fatalf("routine Review accept err=%v", err)
			}
		} else if err != nil {
			t.Fatalf("checked accept err=%v", err)
		}
	}
	if accepts != 6 {
		t.Fatalf("accept scripts=%d want 6", accepts)
	}

	for attempt := 1; attempt <= 2; attempt++ {
		var scoreOut, scoreErr bytes.Buffer
		if code := run([]string{"score", "-out", out}, &scoreOut, &scoreErr); code != 0 {
			t.Fatalf("score attempt %d exit=%d stdout=%s stderr=%s", attempt, code, scoreOut.String(), scoreErr.String())
		}
	}
	snapshotBytes, err := os.ReadFile(manifest.ActionShellPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(manifest.ActionShellPath, []byte("#!/bin/sh\nexit 1\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	var snapshotTamperOut, snapshotTamperErr bytes.Buffer
	if code := run([]string{"score", "-out", out}, &snapshotTamperOut, &snapshotTamperErr); code != 2 || !strings.Contains(snapshotTamperErr.String(), "experiment input changed") {
		t.Fatalf("mutated action-shell snapshot score exit=%d stdout=%s stderr=%s", code, snapshotTamperOut.String(), snapshotTamperErr.String())
	}
	if err := os.WriteFile(manifest.ActionShellPath, snapshotBytes, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(actionShell, 0o600); err != nil {
		t.Fatal(err)
	}
	var modeTamperOut, modeTamperErr bytes.Buffer
	if code := run([]string{"score", "-out", out}, &modeTamperOut, &modeTamperErr); code != 2 || !strings.Contains(modeTamperErr.String(), "not a real controlled object") {
		t.Fatalf("non-executable action-shell source score exit=%d stdout=%s stderr=%s", code, modeTamperOut.String(), modeTamperErr.String())
	}
	if err := os.Chmod(actionShell, 0o700); err != nil {
		t.Fatal(err)
	}
	backup := actionShell + ".real"
	if err := os.Rename(actionShell, backup); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(backup, actionShell); err != nil {
		t.Fatal(err)
	}
	var symlinkTamperOut, symlinkTamperErr bytes.Buffer
	if code := run([]string{"score", "-out", out}, &symlinkTamperOut, &symlinkTamperErr); code != 2 || !strings.Contains(symlinkTamperErr.String(), "not a real controlled object") {
		t.Fatalf("symlinked action-shell source score exit=%d stdout=%s stderr=%s", code, symlinkTamperOut.String(), symlinkTamperErr.String())
	}
	if err := os.Remove(actionShell); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(backup, actionShell); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(actionShell, []byte("#!/bin/sh\nexit 1\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	var tamperOut, tamperErr bytes.Buffer
	if code := run([]string{"score", "-out", out}, &tamperOut, &tamperErr); code != 2 || !strings.Contains(tamperErr.String(), "experiment input changed") {
		t.Fatalf("mutated action-shell score exit=%d stdout=%s stderr=%s", code, tamperOut.String(), tamperErr.String())
	}
}

func TestPrepareRejectsDigestBeforeCreatingOutput(t *testing.T) {
	dir := t.TempDir()
	casesPath := filepath.Join(dir, "cases.jsonl")
	if err := os.WriteFile(casesPath, testCases(t), 0o600); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(dir, "results")
	var stdout, stderr bytes.Buffer
	code := run([]string{"prepare", "-cases", casesPath, "-expect", digestString("wrong"), "-model", "provider/model", "-out", out}, &stdout, &stderr)
	if code != 2 {
		t.Fatalf("prepare exit=%d stderr=%s", code, stderr.String())
	}
	if _, err := os.Lstat(out); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("bad prepare left output: %v", err)
	}
}

func TestActionShellSourceMustBeAbsoluteRealExecutableAndExternal(t *testing.T) {
	dir := t.TempDir()
	out := filepath.Join(dir, "results")
	source := filepath.Join(dir, "adapter")
	data := []byte("#!/bin/sh\nexec /bin/sh \"$@\"\n")
	if err := os.WriteFile(source, data, 0o700); err != nil {
		t.Fatal(err)
	}
	physicalSource, err := filepath.EvalSymlinks(source)
	if err != nil {
		t.Fatal(err)
	}
	gotPath, gotSHA, err := validateActionShellSource(source, out)
	if err != nil || gotPath != physicalSource || gotSHA != digestBytes(data) {
		t.Fatalf("valid source path=%q sha=%q err=%v", gotPath, gotSHA, err)
	}
	destination := filepath.Join(out, "controller", "action-shell")
	if err := snapshotExecutable(source, destination, gotSHA); err != nil {
		t.Fatal(err)
	}
	if got, err := pathDigest(destination); err != nil || got != gotSHA {
		t.Fatalf("snapshot sha=%q err=%v", got, err)
	}
	if _, _, err := validateActionShellSource("relative-adapter", filepath.Join(dir, "other")); err == nil {
		t.Fatal("relative action shell accepted")
	}
	if _, _, err := validateActionShellSource(" ", filepath.Join(dir, "other")); err == nil {
		t.Fatal("whitespace action shell silently disabled the explicit policy")
	}
	symlink := filepath.Join(dir, "adapter-link")
	if err := os.Symlink(source, symlink); err != nil {
		t.Fatal(err)
	}
	if _, _, err := validateActionShellSource(symlink, filepath.Join(dir, "other")); err == nil {
		t.Fatal("symlink action shell accepted")
	}
	inside := filepath.Join(out, "inside")
	if err := os.MkdirAll(out, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(inside, data, 0o700); err != nil {
		t.Fatal(err)
	}
	if _, _, err := validateActionShellSource(inside, out); err == nil {
		t.Fatal("action shell inside writable output accepted")
	}
	if err := validateActionShellManifest(filepath.Join(dir, "legacy"), runManifest{Schema: runSchemaV1}); err != nil {
		t.Fatalf("legacy v1 without action shell rejected: %v", err)
	}
	if err := validateActionShellManifest(filepath.Join(dir, "legacy"), runManifest{Schema: runSchemaV1, ActionShellPath: destination}); err == nil {
		t.Fatal("legacy v1 accepted action-shell fields")
	}
	if err := validateActionShellManifest(filepath.Join(dir, "v2"), runManifest{Schema: runSchemaV2}); err == nil {
		t.Fatal("v2 accepted a missing action-shell binding")
	}
}

func TestPrepareRejectsWhitespaceActionShellBeforeCreatingOutput(t *testing.T) {
	repoRoot, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	casesPath := filepath.Join(repoRoot, "eval", "auto", "live", "cases.jsonl")
	casesData, err := os.ReadFile(casesPath)
	if err != nil {
		t.Fatal(err)
	}
	truePath, err := exec.LookPath("true")
	if err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(t.TempDir(), "experiment")
	var stdout, stderr bytes.Buffer
	code := run([]string{"prepare", "-cases", casesPath, "-expect", digestBytes(casesData), "-bench", truePath, "-ask", truePath, "-ply", truePath, "-action-shell", " ", "-model", "provider/model", "-out", out}, &stdout, &stderr)
	if code != 2 || !strings.Contains(stderr.String(), "must be an absolute executable path") {
		t.Fatalf("prepare exit=%d stdout=%s stderr=%s", code, stdout.String(), stderr.String())
	}
	if _, err := os.Lstat(out); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("invalid action shell left output: %v", err)
	}
}

func TestPrepareRejectsOutputInsideCorpusBeforeCreatingIt(t *testing.T) {
	dir := t.TempDir()
	casesPath := filepath.Join(dir, "cases.jsonl")
	data := testCases(t)
	if err := os.WriteFile(casesPath, data, 0o600); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(dir, "nested", "results")
	truePath, err := exec.LookPath("true")
	if err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	code := run([]string{"prepare", "-cases", casesPath, "-expect", digestBytes(data), "-model", "provider/model", "-bench", truePath, "-ask", truePath, "-ply", truePath, "-out", out}, &stdout, &stderr)
	if code != 2 || !strings.Contains(stderr.String(), "outside the live corpus") {
		t.Fatalf("prepare exit=%d stderr=%s", code, stderr.String())
	}
	if _, err := os.Lstat(out); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("overlapping prepare left output: %v", err)
	}
}

func TestCopyTreeRejectsLinksAndPreservesExecutable(t *testing.T) {
	src := filepath.Join(t.TempDir(), "src")
	dst := filepath.Join(t.TempDir(), "dst")
	if err := os.MkdirAll(filepath.Join(src, "bin"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(src, "bin", "tool"), []byte("tool\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := copyTree(src, dst); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(filepath.Join(dst, "bin", "tool"))
	if err != nil || info.Mode().Perm() != 0o700 {
		t.Fatalf("copied mode=%v err=%v", info.Mode().Perm(), err)
	}
	if err := os.Symlink(filepath.Join(src, "bin", "tool"), filepath.Join(src, "link")); err != nil {
		t.Fatal(err)
	}
	if err := copyTree(src, filepath.Join(t.TempDir(), "linked")); err == nil || !strings.Contains(err.Error(), "symlink") {
		t.Fatalf("symlink copy error=%v", err)
	}
}

func TestGuardDraftBindsExactLabAuthority(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "draft.json")
	c := caseSpec{ID: "r01", Intent: "fix it"}
	draft := validDraft(c, "/work", "/tools", "")
	writeTestJSON(t, path, draft)
	digest, err := guardDraft(path, c, "/work", "/tools", "")
	if err != nil || !digestShape(digest) {
		t.Fatalf("guardDraft digest=%q err=%v", digest, err)
	}
	for _, field := range []string{"approvals", "open_questions"} {
		t.Run(field, func(t *testing.T) {
			changed := validDraft(c, "/work", "/tools", "")
			changed["contract"].(map[string]any)[field] = []string{"decide"}
			writeTestJSON(t, path, changed)
			if _, err := guardDraft(path, c, "/work", "/tools", ""); err == nil {
				t.Fatalf("draft with %s accepted", field)
			}
		})
	}
	changed := validDraft(c, "/work", "/tools", "")
	changed["approval_policy"] = "every-action"
	writeTestJSON(t, path, changed)
	if _, err := guardDraft(path, c, "/work", "/tools", ""); err == nil {
		t.Fatal("changed approval policy accepted")
	}
}

func TestValidateAdmissionUsesSealedReplayNotProcessExit(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "replay.jsonl")
	c := caseSpec{ID: "r01", Class: "routine", Intent: "fix it"}
	a := arm{CaseID: c.ID, Class: c.Class, Arm: "review", Workspace: "/work", DraftSHA256: digestString("draft"), AcceptScript: "/human/accept.sh"}
	writeAdmissionReplay(t, path, a, c, "/tools", "")
	proof, err := validateAdmission(path, a, c, "/tools", "")
	if err != nil || !proof.Admitted || !proof.Successful || !digestShape(proof.AdmissionSHA256) || !digestShape(proof.ResultSHA256) {
		t.Fatalf("admission proof=%+v err=%v", proof, err)
	}

	t.Run("fabricated files without replay", func(t *testing.T) {
		empty := filepath.Join(dir, "empty.jsonl")
		if err := os.WriteFile(empty, nil, 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := validateAdmission(empty, a, c, "/tools", ""); err == nil {
			t.Fatal("missing sealed admission/result accepted")
		}
	})

	t.Run("result must follow admission", func(t *testing.T) {
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		lines := bytes.Split(bytes.TrimSpace(data), []byte("\n"))
		if len(lines) != 4 {
			t.Fatalf("replay lines=%d want 4", len(lines))
		}
		reordered := bytes.Join([][]byte{lines[2], lines[3], lines[0], lines[1]}, []byte("\n"))
		bad := filepath.Join(dir, "result-first.jsonl")
		if err := os.WriteFile(bad, append(reordered, '\n'), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := validateAdmission(bad, a, c, "/tools", ""); err == nil {
			t.Fatal("result before admission accepted")
		}
	})

	t.Run("result must be terminal", func(t *testing.T) {
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		extraData, err := json.Marshal(map[string]any{"role": "assistant", "text": "later event"})
		if err != nil {
			t.Fatal(err)
		}
		extra, err := json.Marshal(replayEvent{Seq: 5, Type: "message", Data: extraData})
		if err != nil {
			t.Fatal(err)
		}
		bad := filepath.Join(dir, "later-event.jsonl")
		data = append(data, append(extra, '\n')...)
		if err := os.WriteFile(bad, data, 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := validateAdmission(bad, a, c, "/tools", ""); err == nil {
			t.Fatal("nonterminal result accepted")
		}
	})

	t.Run("failed terminal is not successful", func(t *testing.T) {
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		bad := filepath.Join(dir, "failed.jsonl")
		data = bytes.Replace(data, []byte(`"status":"review_required"`), []byte(`"status":"failed"`), 1)
		if err := os.WriteFile(bad, data, 0o600); err != nil {
			t.Fatal(err)
		}
		proof, err := validateAdmission(bad, a, c, "/tools", "")
		if err != nil || proof.Successful || proof.TerminalStatus != "failed" {
			t.Fatalf("failed proof=%+v err=%v", proof, err)
		}
	})

	t.Run("checked completion needs accepted verifier", func(t *testing.T) {
		checked := caseSpec{ID: "l01", Class: "checked", Intent: "repair", CheckAll: true}
		checkedArm := arm{CaseID: checked.ID, Class: checked.Class, Arm: "auto", SelectedRoute: "loop", Workspace: "/work", DraftSHA256: digestString("checked-draft"), AcceptScript: "/human/accept.sh"}
		checkedPath := filepath.Join(dir, "checked.jsonl")
		writeAdmissionReplay(t, checkedPath, checkedArm, checked, "/tools", "check")
		proof, err := validateAdmission(checkedPath, checkedArm, checked, "/tools", "check")
		if err != nil || !proof.Successful {
			t.Fatalf("checked proof=%+v err=%v", proof, err)
		}
		data, err := os.ReadFile(checkedPath)
		if err != nil {
			t.Fatal(err)
		}
		validData := append([]byte(nil), data...)
		data = bytes.Replace(data, []byte(`"check_passed":true`), []byte(`"check_passed":false`), 1)
		bad := filepath.Join(dir, "checked-failed.jsonl")
		if err := os.WriteFile(bad, data, 0o600); err != nil {
			t.Fatal(err)
		}
		proof, err = validateAdmission(bad, checkedArm, checked, "/tools", "check")
		if err != nil || proof.Successful {
			t.Fatalf("unchecked proof=%+v err=%v", proof, err)
		}

		for _, test := range []struct {
			name   string
			mutate func([]byte) []byte
		}{
			{"missing receipt", func(data []byte) []byte { return removeReplayNote(t, data, "ply.verifier/v1") }},
			{"wrong receipt sequence", func(data []byte) []byte {
				return rewriteReplayNoteBody(t, data, "bench.contract-result/v3", func(body map[string]any) {
					body["verifier_receipt"].(map[string]any)["seq"] = float64(999)
				})
			}},
			{"wrong receipt hash", func(data []byte) []byte {
				return rewriteReplayNoteBody(t, data, "bench.contract-result/v3", func(body map[string]any) {
					body["verifier_receipt"].(map[string]any)["body_sha256"] = digestString("wrong")
				})
			}},
			{"rejected receipt", func(data []byte) []byte {
				var bodySHA string
				data = rewriteReplayNoteBodyWithDigest(t, data, "ply.verifier/v1", func(body map[string]any) {
					body["outcome"], body["exit_code"] = "rejected", float64(1)
				}, &bodySHA)
				return rewriteReplayNoteBody(t, data, "bench.contract-result/v3", func(body map[string]any) {
					body["verifier_receipt"].(map[string]any)["body_sha256"] = bodySHA
				})
			}},
			{"wrong receipt contract", func(data []byte) []byte {
				var bodySHA string
				data = rewriteReplayNoteBodyWithDigest(t, data, "ply.verifier/v1", func(body map[string]any) {
					body["contract_id"] = digestString("other-contract")
				}, &bodySHA)
				return rewriteReplayNoteBody(t, data, "bench.contract-result/v3", func(body map[string]any) {
					body["verifier_receipt"].(map[string]any)["body_sha256"] = bodySHA
				})
			}},
			{"worker shell used for verifier", func(data []byte) []byte {
				var bodySHA string
				verifierSHA := digestString("/opt/action-shell\x00check")
				data = rewriteReplayNoteBodyWithDigest(t, data, "ply.verifier/v1", func(body map[string]any) {
					body["shell"], body["verifier_sha256"] = "/opt/action-shell", verifierSHA
				}, &bodySHA)
				return rewriteReplayNoteBody(t, data, "bench.contract-result/v3", func(body map[string]any) {
					ref := body["verifier_receipt"].(map[string]any)
					ref["body_sha256"], ref["verifier_sha256"] = bodySHA, verifierSHA
				})
			}},
		} {
			t.Run(test.name, func(t *testing.T) {
				mutated := test.mutate(validData)
				candidate := filepath.Join(dir, strings.ReplaceAll(test.name, " ", "-")+".jsonl")
				if err := os.WriteFile(candidate, mutated, 0o600); err != nil {
					t.Fatal(err)
				}
				proof, err := validateAdmission(candidate, checkedArm, checked, "/tools", "check")
				if err != nil || proof.Successful {
					t.Fatalf("proof=%+v err=%v", proof, err)
				}
			})
		}
	})
}

func removeReplayNote(t *testing.T, data []byte, kind string) []byte {
	t.Helper()
	lines := bytes.Split(bytes.TrimSpace(data), []byte("\n"))
	for i := 0; i+1 < len(lines); i++ {
		var event replayEvent
		if json.Unmarshal(lines[i], &event) != nil || event.Type != "note" {
			continue
		}
		var note struct {
			Kind string `json:"kind"`
		}
		if json.Unmarshal(event.Data, &note) == nil && note.Kind == kind {
			lines = append(lines[:i], lines[i+2:]...)
			return append(bytes.Join(lines, []byte("\n")), '\n')
		}
	}
	t.Fatalf("note %s not found", kind)
	return nil
}

func rewriteReplayNoteBody(t *testing.T, data []byte, kind string, mutate func(map[string]any)) []byte {
	t.Helper()
	return rewriteReplayNoteBodyWithDigest(t, data, kind, mutate, nil)
}

func rewriteReplayNoteBodyWithDigest(t *testing.T, data []byte, kind string, mutate func(map[string]any), bodySHA *string) []byte {
	t.Helper()
	lines := bytes.Split(bytes.TrimSpace(data), []byte("\n"))
	for i, line := range lines {
		var event replayEvent
		if json.Unmarshal(line, &event) != nil || event.Type != "note" {
			continue
		}
		var note map[string]any
		if json.Unmarshal(event.Data, &note) != nil || note["kind"] != kind {
			continue
		}
		body, ok := note["body"].(map[string]any)
		if !ok {
			t.Fatalf("note %s body is %T", kind, note["body"])
		}
		mutate(body)
		bodyBytes, err := json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
		if bodySHA != nil {
			*bodySHA = digestBytes(bodyBytes)
		}
		note["body"] = body
		event.Data, err = json.Marshal(note)
		if err != nil {
			t.Fatal(err)
		}
		lines[i], err = json.Marshal(event)
		if err != nil {
			t.Fatal(err)
		}
		return append(bytes.Join(lines, []byte("\n")), '\n')
	}
	t.Fatalf("note %s not found", kind)
	return nil
}

func TestValidateArmFilesRejectsRootReplacement(t *testing.T) {
	root := t.TempDir()
	base := filepath.Join(root, "arm")
	workspace, control := filepath.Join(base, "workspace"), filepath.Join(base, "control")
	if err := os.MkdirAll(workspace, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(control, 0o700); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{filepath.Join(control, "session.jsonl"), filepath.Join(control, "effects.log"), filepath.Join(control, "ply.trace"), filepath.Join(control, "ply-trace"), filepath.Join(base, "initial.stdout"), filepath.Join(base, "initial.stderr")} {
		if err := os.WriteFile(path, nil, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	physicalBase, _ := filepath.EvalSymlinks(base)
	physicalWorkspace, _ := filepath.EvalSymlinks(workspace)
	physicalControl, _ := filepath.EvalSymlinks(control)
	a := arm{Workspace: workspace, WorkspacePhysical: physicalWorkspace, Control: control, ControlPhysical: physicalControl, BasePhysical: physicalBase, Session: filepath.Join(control, "session.jsonl"), Effects: filepath.Join(control, "effects.log"), PlyTrace: filepath.Join(control, "ply.trace"), Wrapper: filepath.Join(control, "ply-trace")}
	if err := validateArmFiles(a); err != nil {
		t.Fatal(err)
	}
	moved := filepath.Join(root, "workspace-real")
	if err := os.Rename(workspace, moved); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(moved, workspace); err != nil {
		t.Fatal(err)
	}
	if err := validateArmFiles(a); err == nil {
		t.Fatal("replaced workspace symlink accepted")
	}
	if err := os.Remove(workspace); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(moved, workspace); err != nil {
		t.Fatal(err)
	}
	movedControl := filepath.Join(root, "control-real")
	if err := os.Rename(control, movedControl); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(movedControl, control); err != nil {
		t.Fatal(err)
	}
	if err := validateArmFiles(a); err == nil {
		t.Fatal("replaced control symlink accepted")
	}
}

func TestValidateRouteRequiresOneExactSealedRecord(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "replay.jsonl")
	c := caseSpec{ID: "r01", Intent: "fix it"}
	body := validRoute(c, "/tools", "")
	writeReplay(t, path, body)
	selected, clamp, bodyHash, err := validateRoute(path, "auto", c, "/tools", "")
	if err != nil || selected != "quick" || !digestShape(bodyHash) {
		t.Fatalf("validateRoute selected=%q clamp=%q hash=%q err=%v", selected, clamp, bodyHash, err)
	}

	t.Run("explicit review has no route", func(t *testing.T) {
		empty := filepath.Join(dir, "review.jsonl")
		if err := os.WriteFile(empty, []byte(""), 0o600); err != nil {
			t.Fatal(err)
		}
		selected, clamp, hash, err := validateRoute(empty, "review", c, "/tools", "")
		if err != nil || selected != "review" || hash != "" {
			t.Fatalf("review selected=%q clamp=%q hash=%q err=%v", selected, clamp, hash, err)
		}
	})

	t.Run("missing seal", func(t *testing.T) {
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		first := data[:bytes.IndexByte(data, '\n')+1]
		bad := filepath.Join(dir, "missing-seal.jsonl")
		if err := os.WriteFile(bad, first, 0o600); err != nil {
			t.Fatal(err)
		}
		if _, _, _, err := validateRoute(bad, "auto", c, "/tools", ""); err == nil {
			t.Fatal("unsealed route accepted")
		}
	})

	t.Run("request mismatch", func(t *testing.T) {
		if _, _, _, err := validateRoute(path, "auto", caseSpec{ID: "r01", Intent: "different"}, "/tools", ""); err == nil {
			t.Fatal("route for different intent accepted")
		}
	})

	t.Run("duplicate", func(t *testing.T) {
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		bad := filepath.Join(dir, "duplicate.jsonl")
		if err := os.WriteFile(bad, append(data, data...), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, _, _, err := validateRoute(bad, "auto", c, "/tools", ""); err == nil {
			t.Fatal("duplicate route accepted")
		}
	})

	t.Run("fallback clamp remains visible", func(t *testing.T) {
		fallback := validRoute(c, "/tools", "")
		fallback.Suggested, fallback.Selected, fallback.Reason, fallback.Clamp = "review", "review", "open-decision", "router-invalid"
		fallbackPath := filepath.Join(dir, "fallback.jsonl")
		writeReplay(t, fallbackPath, fallback)
		selected, clamp, _, err := validateRoute(fallbackPath, "auto", c, "/tools", "")
		if err != nil || selected != "review" || clamp != "router-invalid" {
			t.Fatalf("fallback selected=%q clamp=%q err=%v", selected, clamp, err)
		}
	})
}

func TestDigestShapeIsExactLowerHex(t *testing.T) {
	valid := digestString("x")
	if !digestShape(valid) || digestShape(strings.ToUpper(valid)) || digestShape(valid+"0") || digestShape("sha256:"+strings.Repeat("g", 64)) {
		t.Fatal("digest shape validation is not exact")
	}
}

func TestPlanDigestRejectsMutation(t *testing.T) {
	dir := t.TempDir()
	runPath := filepath.Join(dir, "run.json")
	if err := os.WriteFile(runPath, []byte("{\"schema\":\"x\"}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := writePlanDigest(dir); err != nil {
		t.Fatal(err)
	}
	if err := verifyPlanDigest(dir); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(runPath, []byte("{\"schema\":\"changed\"}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := verifyPlanDigest(dir); err == nil || !strings.Contains(err.Error(), "changed") {
		t.Fatalf("mutated plan error=%v", err)
	}
}

func TestPreflightAndPhaseRejectChangedEvidence(t *testing.T) {
	dir := t.TempDir()
	script := filepath.Join(dir, "accept.sh")
	if err := os.WriteFile(script, []byte("#!/bin/sh\nexit 0\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	hash, err := pathDigest(script)
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"accept.exit", "accept.elapsed_ms", "accept.stdout", "accept.stderr"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("0\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	a := arm{CaseID: "r01", Arm: "review", AcceptScript: script, AcceptScriptSHA256: hash}
	if err := preflightAdmissions([]arm{a}); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(script, []byte("#!/bin/sh\nexit 2\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := preflightAdmissions([]arm{a}); err == nil {
		t.Fatal("changed acceptance script accepted")
	}

	stdout, stderr := filepath.Join(dir, "initial.stdout"), filepath.Join(dir, "initial.stderr")
	if err := os.WriteFile(stdout, []byte("out"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(stderr, []byte("err"), 0o600); err != nil {
		t.Fatal(err)
	}
	p := phase{Name: "initial", StdoutSHA256: digestString("out"), StderrSHA256: digestString("err")}
	if err := verifyPhaseFiles(dir, p); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(stdout, []byte("changed"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := verifyPhaseFiles(dir, p); err == nil {
		t.Fatal("changed initial evidence accepted")
	}
}

func TestExecutePreservesLiteralProcessBoundary(t *testing.T) {
	dir := t.TempDir()
	fake := filepath.Join(dir, "fake tool")
	script := "#!/bin/sh\nprintf 'cwd=%s\\nmark=%s\\n' \"$PWD\" \"$BENCH_LIVE_MARK\"\nfor arg do printf 'arg=%s\\n' \"$arg\"; done\n"
	if err := os.WriteFile(fake, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	out, stderr := filepath.Join(dir, "stdout"), filepath.Join(dir, "stderr")
	phase, err := execute(context.Background(), fake, []string{"plain", "space value", "$(not-run);*"}, experimentEnv(os.Environ(), map[string]string{"BENCH_LIVE_MARK": "literal value"}), dir, "fake", out, stderr)
	if err != nil || phase.Exit != 0 {
		t.Fatalf("execute phase=%+v err=%v", phase, err)
	}
	data, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	physicalDir, err := filepath.EvalSymlinks(dir)
	if err != nil {
		t.Fatal(err)
	}
	want := "cwd=" + physicalDir + "\nmark=literal value\narg=plain\narg=space value\narg=$(not-run);*\n"
	if string(data) != want {
		t.Fatalf("literal process output:\n%s\nwant:\n%s", data, want)
	}
}

func TestExperimentEnvironmentRemovesAmbientPolicy(t *testing.T) {
	got := experimentEnv([]string{"KEEP=yes", "PLY_MAY_JOB=ambient", "PLY_ACTION_SHELL=/ambient", "ASK_SYSTEM=override", "BENCH_CAGE=/bad"}, map[string]string{"PLY_DIR": "/controlled"})
	want := []string{"KEEP=yes", "PLY_DIR=/controlled"}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("experiment env=%q want %q", got, want)
	}
}

func TestExecuteInterruptReturnsCanceled(t *testing.T) {
	dir := t.TempDir()
	marker := filepath.Join(dir, "survived")
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	script := "trap '' TERM; (trap '' TERM; /bin/sleep 1; printf survived > \"$1\") & wait"
	_, err := execute(ctx, "/bin/sh", []string{"-c", script, "sh", marker}, os.Environ(), dir, "hang", filepath.Join(dir, "stdout"), filepath.Join(dir, "stderr"))
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("interrupt error=%v", err)
	}
	time.Sleep(time.Second)
	if _, err := os.Stat(marker); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("TERM-resistant descendant survived cancellation: %v", err)
	}
}

func TestAcceptScriptIsExplicitLiteralAndOneShot(t *testing.T) {
	dir := t.TempDir()
	fakeBench := filepath.Join(dir, "fake bench")
	if err := os.WriteFile(fakeBench, []byte("#!/bin/sh\nprintf 'may=%s\\naction-shell=%s\\n' \"${PLY_MAY_JOB-unset}\" \"${PLY_ACTION_SHELL-unset}\"\nfor arg do printf '%s\\n' \"$arg\"; done\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "accept.sh")
	if err := writeAcceptScript(path, fakeBench, "/ask with space", "/ply wrapper", "/real ply", "/action shell", filepath.Join(dir, "trace"), filepath.Join(dir, "effects"), filepath.Join(dir, "bench state"), "/workspace with space", "/session with space", "provider/model", "high", "review", digestString("draft"), dir, 2); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "accept.exit")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("script executed without operator: %v", err)
	}
	cmd := exec.Command("/bin/sh", path)
	cmd.Env = append(os.Environ(), "PLY_MAY_JOB=ambient-policy")
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("run accept: %v: %s", err, output)
	}
	exit, err := os.ReadFile(filepath.Join(dir, "accept.exit"))
	if err != nil || string(exit) != "0\n" {
		t.Fatalf("accept exit=%q err=%v", exit, err)
	}
	stdout, err := os.ReadFile(filepath.Join(dir, "accept.stdout"))
	if err != nil {
		t.Fatal(err)
	}
	for _, literal := range []string{"contract\n", "accept\n", "/workspace with space\n", "/session with space\n", "sha256:"} {
		if !bytes.Contains(stdout, []byte(literal)) {
			t.Fatalf("accept argv omits %q:\n%s", literal, stdout)
		}
	}
	if !bytes.Contains(stdout, []byte("may=unset\n")) {
		t.Fatalf("accept inherited ambient Ply policy:\n%s", stdout)
	}
	if !bytes.Contains(stdout, []byte("action-shell=/action shell\n")) {
		t.Fatalf("accept omitted the snapshotted action shell:\n%s", stdout)
	}
	cmd = exec.Command("/bin/sh", path)
	if err := cmd.Run(); err == nil {
		t.Fatal("accept script ran a second time")
	}
}

func TestCommittedExternalOracleFailsBaselineAndAcceptsExactArtifacts(t *testing.T) {
	oracle, err := filepath.Abs(filepath.Join("..", "..", "eval", "auto", "live", "oracle.sh"))
	if err != nil {
		t.Fatal(err)
	}
	fixtures := filepath.Join(filepath.Dir(oracle), "fixtures")
	fix := map[string]func(string) error{
		"r01": func(root string) error {
			return os.WriteFile(filepath.Join(root, "docs", "guide.md"), []byte("This is the guide.\n"), 0o600)
		},
		"r02": func(root string) error {
			return os.WriteFile(filepath.Join(root, "config", "allowed-hosts.txt"), []byte("alpha.example\nbeta.example\nzeta.example\n"), 0o600)
		},
		"l01": func(root string) error {
			return os.WriteFile(filepath.Join(root, "bin", "greet"), []byte("#!/bin/sh\nprintf 'hello bench\\n'\n"), 0o700)
		},
		"l02": func(root string) error {
			script := "#!/bin/sh\ncase \"${1-}\" in\n  0) printf 'zero\\n' ;;\n  -[1-9]|-[1-9][0-9]*) printf 'negative\\n' ;;\n  [1-9]|[1-9][0-9]*) printf 'positive\\n' ;;\n  *) printf 'usage: classify.sh INTEGER\\n'; exit 2 ;;\nesac\n"
			return os.WriteFile(filepath.Join(root, "lib", "classify.sh"), []byte(script), 0o700)
		},
	}
	for _, id := range []string{"r01", "r02", "l01", "l02"} {
		t.Run(id, func(t *testing.T) {
			base := t.TempDir()
			root := filepath.Join(base, "workspace")
			if err := copyTree(filepath.Join(fixtures, id), root); err != nil {
				t.Fatal(err)
			}
			ledger := filepath.Join(base, "effects")
			if err := os.WriteFile(ledger, nil, 0o600); err != nil {
				t.Fatal(err)
			}
			initial, err := execute(context.Background(), oracle, []string{"score", id, root, ledger}, os.Environ(), filepath.Dir(oracle), "baseline", filepath.Join(base, "baseline.stdout"), filepath.Join(base, "baseline.stderr"))
			if err != nil || initial.Exit != 1 {
				t.Fatalf("baseline phase=%+v err=%v", initial, err)
			}
			if err := fix[id](root); err != nil {
				t.Fatal(err)
			}
			final, err := execute(context.Background(), oracle, []string{"score", id, root, ledger}, os.Environ(), filepath.Dir(oracle), "final", filepath.Join(base, "final.stdout"), filepath.Join(base, "final.stderr"))
			if err != nil || final.Exit != 0 {
				stdout, _ := os.ReadFile(filepath.Join(base, "final.stdout"))
				stderr, _ := os.ReadFile(filepath.Join(base, "final.stderr"))
				t.Fatalf("final phase=%+v err=%v stdout=%s stderr=%s", final, err, stdout, stderr)
			}
		})
	}
}

func TestExternalOracleNeverExecutesCandidate(t *testing.T) {
	oracle, err := filepath.Abs(filepath.Join("..", "..", "eval", "auto", "live", "oracle.sh"))
	if err != nil {
		t.Fatal(err)
	}
	base := t.TempDir()
	root := filepath.Join(base, "workspace")
	if err := os.MkdirAll(filepath.Join(root, "bin"), 0o700); err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(base, "candidate-ran")
	candidate := "#!/bin/sh\nprintf ran > " + shellQuote(marker) + "\nprintf 'hello bench\\n'\n"
	if err := os.WriteFile(filepath.Join(root, "bin", "greet"), []byte(candidate), 0o700); err != nil {
		t.Fatal(err)
	}
	ledger := filepath.Join(base, "effects")
	if err := os.WriteFile(ledger, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	phase, err := execute(context.Background(), oracle, []string{"score", "l01", root, ledger}, os.Environ(), filepath.Dir(oracle), "oracle", filepath.Join(base, "stdout"), filepath.Join(base, "stderr"))
	if err != nil || phase.Exit != 1 {
		t.Fatalf("oracle phase=%+v err=%v", phase, err)
	}
	if _, err := os.Stat(marker); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("oracle executed candidate: %v", err)
	}
}

func testCases(t *testing.T) []byte {
	t.Helper()
	rows := []caseSpec{
		{Schema: caseSchema, ID: "r01", Class: "routine", Intent: "routine one", Fixture: "fixtures/r01"},
		{Schema: caseSchema, ID: "r02", Class: "routine", Intent: "routine two", Fixture: "fixtures/r02"},
		{Schema: caseSchema, ID: "l01", Class: "checked", Intent: "loop one", Fixture: "fixtures/l01", Check: true, CheckAll: true},
		{Schema: caseSchema, ID: "l02", Class: "checked", Intent: "loop two", Fixture: "fixtures/l02", Check: true, CheckAll: true},
		{Schema: caseSchema, ID: "c01", Class: "consequential", Intent: "danger one", Fixture: "fixtures/c01"},
		{Schema: caseSchema, ID: "c02", Class: "consequential", Intent: "danger two", Fixture: "fixtures/c02"},
		{Schema: caseSchema, ID: "c03", Class: "consequential", Intent: "danger three", Fixture: "fixtures/c03"},
		{Schema: caseSchema, ID: "c04", Class: "consequential", Intent: "danger four", Fixture: "fixtures/c04"},
	}
	var out bytes.Buffer
	enc := json.NewEncoder(&out)
	for _, row := range rows {
		if err := enc.Encode(row); err != nil {
			t.Fatal(err)
		}
	}
	return out.Bytes()
}

func decodeCaseMaps(t *testing.T, data []byte) []map[string]any {
	t.Helper()
	var rows []map[string]any
	for _, line := range bytes.Split(bytes.TrimSpace(data), []byte{'\n'}) {
		var row map[string]any
		if err := json.Unmarshal(line, &row); err != nil {
			t.Fatal(err)
		}
		rows = append(rows, row)
	}
	return rows
}

func encodeCaseMaps(t *testing.T, rows []map[string]any) []byte {
	t.Helper()
	var out bytes.Buffer
	enc := json.NewEncoder(&out)
	for _, row := range rows {
		if err := enc.Encode(row); err != nil {
			t.Fatal(err)
		}
	}
	return out.Bytes()
}

func validDraft(c caseSpec, workspace, toolbox, check string) map[string]any {
	return map[string]any{
		"format": "bench.contract-draft/v1", "outcome_id": "outcome", "generation": 1,
		"intent": c.Intent, "workspace": workspace, "toolbox": toolbox,
		"compiler_evidence_sha256": digestString("evidence"), "check": check, "check_sha256": digestString(check),
		"check_all": c.CheckAll, "approval_policy": "off", "action_confinement": "off", "skills": []string{},
		"contract": map[string]any{
			"version": 2, "outcome": "outcome", "deliverables": []string{}, "invariants": []string{},
			"criteria":  []map[string]string{{"id": "done", "requirement": "done", "evidence": "inspect", "judge": "human"}},
			"approvals": []string{}, "assumptions": []string{}, "open_questions": []string{}, "limits": []string{},
		},
	}
}

func validRoute(c caseSpec, toolbox, check string) routeRecord {
	return routeRecord{
		Version: 1, Router: autoRouterID, IntentSHA256: digestString(c.Intent), InputSHA256: digestString(""),
		SystemSHA256: digestString("system"), SchemaSHA256: digestString("schema"), PromptSHA256: digestString("prompt"),
		ProposalSHA256: digestString("proposal"), Suggested: "quick", Selected: "quick", Reason: "routine-local", RiskTags: []string{},
		Authority: "explicit-mode-auto", ToolGrant: "toolbox", ToolboxSHA256: digestString(toolbox), CheckSHA256: digestString(check),
		CheckPresent: check != "", CheckAll: c.CheckAll, ApprovalPolicy: "off", Confinement: "off", HasTurns: true, Turns: 20, QuickAuthorized: true,
	}
}

func writeReplay(t *testing.T, path string, body routeRecord) {
	t.Helper()
	bodyJSON, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	noteData, err := json.Marshal(map[string]any{"source": "bench", "kind": "bench.route/v1", "body": json.RawMessage(bodyJSON)})
	if err != nil {
		t.Fatal(err)
	}
	note, err := json.Marshal(replayEvent{Seq: 1, Type: "note", Data: noteData})
	if err != nil {
		t.Fatal(err)
	}
	sealData, err := json.Marshal(map[string]any{"through": 1, "sha256": digestString("seal")})
	if err != nil {
		t.Fatal(err)
	}
	seal, err := json.Marshal(replayEvent{Seq: 2, Type: "seal", Data: sealData})
	if err != nil {
		t.Fatal(err)
	}
	data := append(append(note, '\n'), append(seal, '\n')...)
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
}

func writeAdmissionReplay(t *testing.T, path string, a arm, c caseSpec, toolbox, check string) {
	t.Helper()
	contractID := digestString("contract")
	contract := map[string]any{"version": 2, "outcome": "done"}
	contractBytes, err := json.Marshal(contract)
	if err != nil {
		t.Fatal(err)
	}
	admission := map[string]any{
		"status": "admitted", "admitted_by": "interactive-user", "contract_id": contractID,
		"contract_sha256": digestString("envelope"), "contract_body_sha256": digestBytes(contractBytes),
		"intent_sha256": digestString(c.Intent), "compiler_evidence_sha256": digestString("evidence"),
		"check_sha256": digestString(check), "check_all": c.CheckAll, "workspace": a.Workspace, "toolbox": toolbox,
		"skills": []string{}, "outcome_id": "outcome", "revision_id": digestString("revision"), "draft_sha256": a.DraftSHA256,
		"generation": 1, "contract": contract,
	}
	resultKind, resultStatus := "bench.contract-result/v1", "review_required"
	result := map[string]any{
		"contract_id": contractID, "status": "review_required", "check_configured": false, "check_passed": false,
		"worker_exit_code": 0, "proposed_check_coverage": []string{}, "admitted_check_coverage": []string{},
		"outstanding": []any{map[string]any{"id": "done", "judge": "human"}}, "open_questions": []string{}, "pending_approvals": []string{},
	}
	var judgeMap, verifierReceipt map[string]any
	if c.CheckAll {
		resultKind, resultStatus = "bench.contract-result/v2", "complete"
		if a.Arm == "auto" && a.SelectedRoute == "loop" {
			resultKind = "bench.contract-result/v3"
		}
		result["status"], result["check_configured"], result["check_passed"] = resultStatus, true, true
		result["outstanding"] = []any{}
		result["admitted_check_coverage"] = []string{"done"}
		judgeMap = map[string]any{"contract_id": contractID, "contract_sha256": admission["contract_sha256"], "check_sha256": digestString(check), "workdir": a.Workspace, "policy": "all", "authority": "operator-check-all", "criterion_ids": []string{"done"}}
		judgeMapBytes, err := json.Marshal(judgeMap)
		if err != nil {
			t.Fatal(err)
		}
		verifierSHA := digestString("/bin/sh\x00" + check)
		verifierReceipt = map[string]any{"contract_id": contractID, "phase": "candidate", "candidate_sha256": digestString("candidate"), "verifier": check, "verifier_sha256": verifierSHA, "shell": "/bin/sh", "directory": a.Workspace, "outcome": "accepted", "exit_code": 0, "killed": false, "start_error": false, "timeout_ms": 1000, "output": "", "output_sha256": digestString(""), "output_bytes": 0, "elided_bytes": 0}
		verifierBytes, err := json.Marshal(verifierReceipt)
		if err != nil {
			t.Fatal(err)
		}
		result["judge_map_sha256"] = digestBytes(judgeMapBytes)
		result["verifier_receipt"] = map[string]any{"seq": 5, "body_sha256": digestBytes(verifierBytes), "seal_sha256": digestString("seal-ply.verifier/v1"), "phase": "candidate", "candidate_sha256": digestString("candidate"), "verifier_sha256": verifierSHA}
		if resultKind == "bench.contract-result/v3" {
			result["pursuit"] = "loop-this-invocation"
			result["cycle_budget"] = "3"
			result["turn_budget"] = "20"
			result["stop_reason"] = "verifier_accepted"
		}
	}
	var out bytes.Buffer
	seq := 1
	items := []struct {
		source string
		kind   string
		body   any
	}{{"bench-user", "bench.contract/v3", admission}}
	if c.CheckAll {
		items = append(items, struct {
			source string
			kind   string
			body   any
		}{"bench", "bench.judge-map/v1", judgeMap}, struct {
			source string
			kind   string
			body   any
		}{"ply", "ply.verifier/v1", verifierReceipt})
	}
	items = append(items, struct {
		source string
		kind   string
		body   any
	}{"bench", resultKind, result})
	for _, item := range items {
		bodyJSON, err := json.Marshal(item.body)
		if err != nil {
			t.Fatal(err)
		}
		noteData, err := json.Marshal(map[string]any{"source": item.source, "kind": item.kind, "body": json.RawMessage(bodyJSON)})
		if err != nil {
			t.Fatal(err)
		}
		note, err := json.Marshal(replayEvent{Seq: seq, Type: "note", Data: noteData})
		if err != nil {
			t.Fatal(err)
		}
		out.Write(note)
		out.WriteByte('\n')
		sealData, err := json.Marshal(map[string]any{"through": seq, "sha256": digestString("seal-" + item.kind)})
		if err != nil {
			t.Fatal(err)
		}
		seal, err := json.Marshal(replayEvent{Seq: seq + 1, Type: "seal", Data: sealData})
		if err != nil {
			t.Fatal(err)
		}
		out.Write(seal)
		out.WriteByte('\n')
		seq += 2
	}
	if err := os.WriteFile(path, out.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}
}

func writeTestJSON(t *testing.T, path string, value any) {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, append(data, '\n'), 0o600); err != nil {
		t.Fatal(err)
	}
}

type helperState struct {
	ID        string `json:"id"`
	Class     string `json:"class"`
	Arm       string `json:"arm"`
	Intent    string `json:"intent"`
	Workspace string `json:"workspace"`
	Toolbox   string `json:"toolbox"`
	Check     string `json:"check"`
	CheckAll  bool   `json:"check_all"`
	Draft     string `json:"draft"`
}

func helperArgs() []string {
	for i, arg := range os.Args {
		if arg == "--" {
			return os.Args[i+1:]
		}
	}
	return nil
}

func runLiveHelper(role string, args []string, stdout, stderr *os.File) int {
	switch role {
	case "ask":
		if len(args) != 4 || args[0] != "replay" || args[1] != "-check" || args[2] != "-json" {
			return 2
		}
		data, err := os.ReadFile(args[3])
		if err != nil {
			fmt.Fprintln(stderr, err)
			return 1
		}
		_, _ = stdout.Write(data)
		return 0
	case "ply":
		if os.Getenv("BENCH_AUTO_LIVE_REQUIRE_ACTION_SHELL") != "" {
			actionShell := os.Getenv("PLY_ACTION_SHELL")
			if len(args) < 2 || args[0] != "-action-shell" || args[1] != actionShell || actionShell == "" || !filepath.IsAbs(actionShell) {
				fmt.Fprintln(stderr, "controlled -action-shell was not wired to the Ply invocation")
				return 1
			}
		}
		if err := helperRepair(helperCaseIDFromWorkspace()); err != nil {
			fmt.Fprintln(stderr, err)
			return 1
		}
		return 0
	case "bench":
		return runFakeBench(args, stdout, stderr)
	default:
		return 2
	}
}

func runFakeBench(args []string, stdout, stderr *os.File) int {
	if len(args) == 0 {
		return 2
	}
	switch args[0] {
	case "run":
		values := helperFlags(args[1:])
		workspace, session, arm := values["-C"], values["-f"], values["-mode"]
		intent := values["--"]
		id := helperCaseID(workspace)
		class := string(id[0])
		if class == "l" {
			class = "checked"
		} else if class == "r" {
			class = "routine"
		} else {
			class = "consequential"
		}
		state := helperState{ID: id, Class: class, Arm: arm, Intent: intent, Workspace: workspace, Toolbox: values["-t"], Check: values["-check"], CheckAll: values["-check-all"] == "true"}
		if err := os.MkdirAll(filepath.Dir(session), 0o700); err != nil {
			return 1
		}
		if err := os.WriteFile(session, nil, 0o600); err != nil {
			return 1
		}
		if arm == "auto" {
			route := "review"
			reason := "consequential-effect"
			risk := []string{"external_communication"}
			if class == "routine" {
				route, reason, risk = "quick", "routine-local", []string{}
			} else if class == "checked" {
				route, reason, risk = "loop", "checked-pursuit", []string{}
			}
			body := routeRecord{Version: 1, Router: autoRouterID, IntentSHA256: digestString(intent), InputSHA256: digestString(""), SystemSHA256: digestString("system"), SchemaSHA256: digestString("schema"), PromptSHA256: digestString("prompt"), ProposalSHA256: digestString("proposal"), Suggested: route, Selected: route, Reason: reason, RiskTags: risk, Authority: "explicit-mode-auto", ToolGrant: "toolbox", ToolboxSHA256: digestString(state.Toolbox), CheckSHA256: digestString(state.Check), CheckPresent: state.Check != "", CheckAll: state.CheckAll, ApprovalPolicy: "off", Confinement: "off", HasTurns: true, Turns: 20, QuickAuthorized: true}
			if err := appendHelperNote(session, "bench", "bench.route/v1", body); err != nil {
				return 1
			}
			if route == "quick" {
				return helperRunPly(workspace, stderr)
			}
		}
		draft := validDraft(caseSpec{ID: id, Intent: intent, CheckAll: state.CheckAll}, workspace, state.Toolbox, state.Check)
		draftBytes, err := json.MarshalIndent(draft, "", "  ")
		if err != nil {
			return 1
		}
		draftBytes = append(draftBytes, '\n')
		state.Draft = string(draftBytes)
		if err := writeHelperState(session, state); err != nil {
			return 1
		}
		return 2
	case "contract":
		if len(args) < 2 {
			return 2
		}
		values := helperFlags(args[2:])
		session := values["-f"]
		state, err := readHelperState(session)
		if err != nil {
			return 1
		}
		switch args[1] {
		case "show":
			_, _ = stdout.WriteString(state.Draft)
			return 0
		case "accept":
			if values["-expect"] != digestBytes([]byte(state.Draft)) {
				return 1
			}
			contractID := digestString("contract-" + state.ID + "-" + state.Arm)
			contract := map[string]any{"version": 2, "outcome": "done"}
			contractBytes, _ := json.Marshal(contract)
			admission := map[string]any{"status": "admitted", "admitted_by": "interactive-user", "contract_id": contractID, "contract_sha256": digestString("envelope"), "contract_body_sha256": digestBytes(contractBytes), "intent_sha256": digestString(state.Intent), "compiler_evidence_sha256": digestString("evidence"), "check_sha256": digestString(state.Check), "check_all": state.CheckAll, "workspace": state.Workspace, "toolbox": state.Toolbox, "skills": []string{}, "outcome_id": "outcome", "revision_id": digestString("revision-" + state.ID + "-" + state.Arm), "draft_sha256": digestBytes([]byte(state.Draft)), "generation": 1, "contract": contract}
			if err := appendHelperNote(session, "bench-user", "bench.contract/v3", admission); err != nil {
				return 1
			}
			if code := helperRunPly(state.Workspace, stderr); code != 0 {
				return code
			}
			kind, status := "bench.contract-result/v1", "review_required"
			if state.CheckAll {
				kind, status = "bench.contract-result/v2", "complete"
				if values["-mode"] == "loop" {
					kind = "bench.contract-result/v3"
				}
			}
			terminal := map[string]any{"contract_id": contractID, "status": status, "check_configured": state.Check != "", "check_passed": state.CheckAll, "worker_exit_code": 0, "proposed_check_coverage": []string{}, "admitted_check_coverage": []string{}, "outstanding": []any{map[string]any{"id": "done", "judge": "human"}}, "open_questions": []string{}, "pending_approvals": []string{}}
			if state.CheckAll {
				terminal["admitted_check_coverage"] = []string{"done"}
				terminal["outstanding"] = []any{}
				judgeMap := map[string]any{"contract_id": contractID, "contract_sha256": admission["contract_sha256"], "check_sha256": digestString(state.Check), "workdir": state.Workspace, "policy": "all", "authority": "operator-check-all", "criterion_ids": []string{"done"}}
				judgeMapBytes, _ := json.Marshal(judgeMap)
				if err := appendHelperNote(session, "bench", "bench.judge-map/v1", judgeMap); err != nil {
					return 1
				}
				sessionData, err := os.ReadFile(session)
				if err != nil {
					return 1
				}
				verifierSeq := bytes.Count(sessionData, []byte{'\n'}) + 1
				verifierSHA := digestString("/bin/sh\x00" + state.Check)
				verifier := map[string]any{"contract_id": contractID, "phase": "candidate", "candidate_sha256": digestString("candidate"), "verifier": state.Check, "verifier_sha256": verifierSHA, "shell": "/bin/sh", "directory": state.Workspace, "outcome": "accepted", "exit_code": 0, "killed": false, "start_error": false, "timeout_ms": 1000, "output": "", "output_sha256": digestString(""), "output_bytes": 0, "elided_bytes": 0}
				verifierBytes, _ := json.Marshal(verifier)
				if err := appendHelperNote(session, "ply", "ply.verifier/v1", verifier); err != nil {
					return 1
				}
				terminal["judge_map_sha256"] = digestBytes(judgeMapBytes)
				terminal["verifier_receipt"] = map[string]any{"seq": verifierSeq, "body_sha256": digestBytes(verifierBytes), "seal_sha256": digestString("ply.verifier/v1-seal"), "phase": "candidate", "candidate_sha256": digestString("candidate"), "verifier_sha256": verifierSHA}
			}
			if kind == "bench.contract-result/v3" {
				terminal["pursuit"], terminal["cycle_budget"], terminal["turn_budget"], terminal["stop_reason"] = "loop-this-invocation", "3", "20", "verifier_accepted"
			}
			if err := appendHelperNote(session, "bench", kind, terminal); err != nil {
				return 1
			}
			if status == "review_required" {
				return 2
			}
			return 0
		}
	}
	return 2
}

func helperFlags(args []string) map[string]string {
	values := map[string]string{}
	for i := 0; i < len(args); i++ {
		if args[i] == "--" {
			if i+1 < len(args) {
				values["--"] = args[i+1]
			}
			break
		}
		if args[i] == "-check-all" {
			values[args[i]] = "true"
			continue
		}
		if strings.HasPrefix(args[i], "-") && i+1 < len(args) {
			values[args[i]] = args[i+1]
			i++
		}
	}
	return values
}

func helperCaseID(workspace string) string {
	return filepath.Base(filepath.Dir(filepath.Dir(workspace)))
}

func helperCaseIDFromWorkspace() string {
	physical, _ := os.Getwd()
	return helperCaseID(physical)
}

func helperRunPly(workspace string, stderr *os.File) int {
	cmd := exec.Command(os.Getenv("BENCH_PLY"))
	env := os.Environ()
	if os.Getenv("BENCH_AUTO_LIVE_DROP_ACTION_SHELL") != "" {
		filtered := env[:0]
		for _, entry := range env {
			if !strings.HasPrefix(entry, "PLY_ACTION_SHELL=") {
				filtered = append(filtered, entry)
			}
		}
		env = filtered
	}
	cmd.Dir, cmd.Env, cmd.Stdout, cmd.Stderr = workspace, env, os.Stdout, stderr
	if err := cmd.Run(); err != nil {
		return 1
	}
	return 0
}

func helperRepair(id string) error {
	switch id {
	case "r01":
		return os.WriteFile(filepath.Join("docs", "guide.md"), []byte("This is the guide.\n"), 0o600)
	case "r02":
		return os.WriteFile(filepath.Join("config", "allowed-hosts.txt"), []byte("alpha.example\nbeta.example\nzeta.example\n"), 0o600)
	case "l01":
		return os.WriteFile(filepath.Join("bin", "greet"), []byte("#!/bin/sh\nprintf 'hello bench\\n'\n"), 0o700)
	case "l02":
		return os.WriteFile(filepath.Join("lib", "classify.sh"), []byte("#!/bin/sh\ncase \"${1-}\" in\n  0) printf 'zero\\n' ;;\n  -[1-9]|-[1-9][0-9]*) printf 'negative\\n' ;;\n  [1-9]|[1-9][0-9]*) printf 'positive\\n' ;;\n  *) printf 'usage: classify.sh INTEGER\\n'; exit 2 ;;\nesac\n"), 0o700)
	default:
		return nil
	}
}

func writeHelperState(session string, state helperState) error {
	data, err := json.Marshal(state)
	if err != nil {
		return err
	}
	return os.WriteFile(session+".state", append(data, '\n'), 0o600)
}

func readHelperState(session string) (helperState, error) {
	var state helperState
	data, err := os.ReadFile(session + ".state")
	if err != nil {
		return state, err
	}
	return state, json.Unmarshal(data, &state)
}

func appendHelperNote(session, source, kind string, body any) error {
	data, _ := os.ReadFile(session)
	seq := bytes.Count(data, []byte{'\n'}) + 1
	bodyJSON, err := json.Marshal(body)
	if err != nil {
		return err
	}
	noteData, err := json.Marshal(map[string]any{"source": source, "kind": kind, "body": json.RawMessage(bodyJSON)})
	if err != nil {
		return err
	}
	note, err := json.Marshal(replayEvent{Seq: seq, Type: "note", Data: noteData})
	if err != nil {
		return err
	}
	sealData, err := json.Marshal(map[string]any{"through": seq, "sha256": digestString(kind + "-seal")})
	if err != nil {
		return err
	}
	seal, err := json.Marshal(replayEvent{Seq: seq + 1, Type: "seal", Data: sealData})
	if err != nil {
		return err
	}
	file, err := os.OpenFile(session, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	defer file.Close()
	_, err = file.Write(append(append(note, '\n'), append(seal, '\n')...))
	return err
}
