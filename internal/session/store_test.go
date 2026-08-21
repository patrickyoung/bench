package session

import (
	"os"
	"path/filepath"
	"strings"
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

func TestSubagentsDirIsStableAndDistinguishesEqualBasenames(t *testing.T) {
	root := "/work/.bench"
	a := SubagentsDir(root, "/one/task.jsonl")
	b := SubagentsDir(root, "/two/task.jsonl")
	if a == b || filepath.Dir(a) != filepath.Join(root, "subagents") || filepath.Base(a)[:5] != "task-" {
		t.Fatalf("a=%q b=%q", a, b)
	}
	if got := SubagentsDir(root, "/one/./task.jsonl"); got != a {
		t.Fatalf("clean path changed directory: got %q want %q", got, a)
	}
}

func TestSubagentsDirSanitizesUntrustedExplicitSessionNames(t *testing.T) {
	parent := filepath.Join("/tmp", strings.Repeat("long", 100)+"\n\x1b[31m.jsonl")
	got := SubagentsDir("/work/.bench", parent)
	base := filepath.Base(got)
	if len(base) > 57 || strings.ContainsAny(base, "\n\r\x1b") || strings.Contains(base, string(os.PathSeparator)) {
		t.Fatalf("unsafe subagent directory %q", got)
	}
	if parts := strings.Split(base, "-"); len(parts[len(parts)-1]) != 16 {
		t.Fatalf("directory lacks 64-bit path hash: %q", base)
	}
}
