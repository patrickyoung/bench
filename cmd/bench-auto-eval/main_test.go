package main

import (
	"bytes"
	"path/filepath"
	"testing"
)

func TestFrozenRoutingCorpusPasses(t *testing.T) {
	root := filepath.Join("..", "..", "eval", "auto")
	var stdout, stderr bytes.Buffer
	code := run([]string{
		"-tasks", filepath.Join(root, "tasks.jsonl"),
		"-proposals", filepath.Join(root, "proposals.jsonl"),
		"-order", filepath.Join(root, "order.jsonl"),
	}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("run exit=%d stderr=%q stdout=%q", code, stderr.String(), stdout.String())
	}
	want := "routine Quick 20/20 · consequential Review 20/20 · checked Loop 20/20 · false Quick 0\n"
	if !bytes.Contains(stdout.Bytes(), []byte(want)) {
		t.Fatalf("stdout=%q, want %q", stdout.String(), want)
	}
}

func TestCorpusJSONIsStrict(t *testing.T) {
	var value task
	if err := decodeStrict([]byte(`{"schema":"bench.auto-eval/task/v1","id":"r01","extra":true}`), &value); err == nil {
		t.Fatal("unknown field was accepted")
	}
	if err := decodeStrict([]byte(`{} {}`), &value); err == nil {
		t.Fatal("trailing JSON was accepted")
	}
}

func TestRoutineThresholdPassesNineteenAndFailsSeventeen(t *testing.T) {
	root := filepath.Join("..", "..", "eval", "auto")
	tasks, err := readTasks(filepath.Join(root, "tasks.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	_, proposals, err := readProposals(filepath.Join(root, "proposals.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	order, err := readOrder(filepath.Join(root, "order.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	review := []byte(`{"version":1,"route":"review","reason":"open-decision","risk_tags":[]}`)
	proposals["r01"] = review
	got, err := evaluate(tasks, proposals, order)
	if err != nil || got.routineQuick != 19 || !passes(got) {
		t.Fatalf("19/20 result=%#v err=%v", got, err)
	}
	proposals["r02"] = review
	proposals["r03"] = review
	got, err = evaluate(tasks, proposals, order)
	if err != nil || got.routineQuick != 17 || passes(got) {
		t.Fatalf("17/20 result=%#v err=%v", got, err)
	}
}
