package session

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestDiscoverListsOnlyRegularSessionsNewestFirst(t *testing.T) {
	dir := t.TempDir()
	old := filepath.Join(dir, "old.jsonl")
	newer := filepath.Join(dir, "new.jsonl")
	if err := os.WriteFile(old, []byte("old"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(newer, []byte("newer"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "ignore.txt"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	if err := os.Chtimes(old, now.Add(-time.Hour), now.Add(-time.Hour)); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(old, filepath.Join(dir, "linked.jsonl")); err != nil {
		t.Fatal(err)
	}

	got, err := Discover(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].Name != "new" || got[1].Name != "old" {
		t.Fatalf("sessions = %#v", got)
	}
}

func TestDiscoverMissingDirectoryIsEmpty(t *testing.T) {
	got, err := Discover(filepath.Join(t.TempDir(), "missing"))
	if err != nil || got != nil {
		t.Fatalf("got %#v, %v", got, err)
	}
}

func TestResolveDoesNotGuess(t *testing.T) {
	dir := "/work/.bench/sessions"
	if got := Resolve(dir, "abc"); got != "/work/.bench/sessions/abc.jsonl" {
		t.Fatalf("bare id = %q", got)
	}
	if got := Resolve(dir, "runs/abc.jsonl"); got != "runs/abc.jsonl" {
		t.Fatalf("path = %q", got)
	}
	if got := Resolve(dir, "./abc.jsonl"); got != "./abc.jsonl" {
		t.Fatalf("explicit relative path = %q", got)
	}
}
