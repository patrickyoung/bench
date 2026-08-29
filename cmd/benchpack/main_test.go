package main

import (
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	runtimedebug "runtime/debug"
	"strings"
	"testing"

	"github.com/patrickyoung/bench/internal/suite"
)

func TestOverlayReplacesOnlyNamedVariables(t *testing.T) {
	got := overlay([]string{"PATH=/bin", "GOOS=old", "X=1"}, "GOOS=linux", "GOARCH=arm64")
	want := []string{"PATH=/bin", "X=1", "GOOS=linux", "GOARCH=arm64"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("overlay = %#v, want %#v", got, want)
	}
}

func TestCopyTreeAndChecksums(t *testing.T) {
	source := t.TempDir()
	target := filepath.Join(t.TempDir(), "bundle")
	if err := os.MkdirAll(filepath.Join(source, "nested"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(source, "nested", "tool"), []byte("hello"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := copyTree(source, target); err != nil {
		t.Fatal(err)
	}
	if err := writeChecksums(target); err != nil {
		t.Fatal(err)
	}
	checksums, err := os.ReadFile(filepath.Join(target, "SHA256SUMS"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(checksums), "  nested/tool\n") {
		t.Fatalf("checksums = %q", checksums)
	}
	if runtime.GOOS != "windows" {
		info, err := os.Stat(filepath.Join(target, "nested", "tool"))
		if err != nil {
			t.Fatal(err)
		}
		if info.Mode().Perm() != 0o755 {
			t.Fatalf("mode=%v", info.Mode().Perm())
		}
	}
}

func TestCopyTreeAcceptsSingleExecutableAsset(t *testing.T) {
	source := filepath.Join(t.TempDir(), "agent-action-shell")
	if err := os.WriteFile(source, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(t.TempDir(), "bin", "agent-action-shell")
	if err := copyTree(source, target); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(target)
	if err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS != "windows" && info.Mode().Perm() != 0o755 {
		t.Fatalf("mode=%v", info.Mode().Perm())
	}
}

func TestExecutableSuffix(t *testing.T) {
	if got := executable("bench", "windows"); got != "bench.exe" {
		t.Fatalf("windows executable = %q", got)
	}
	if got := executable("bench", "linux"); got != "bench" {
		t.Fatalf("linux executable = %q", got)
	}
}

func TestInstallScriptLinksEveryRequiredSuiteCommand(t *testing.T) {
	m, err := suite.Current()
	if err != nil {
		t.Fatal(err)
	}
	script := installScript("0.6.6", componentNames(m.Components))
	want := "tools='" + strings.Join(componentNames(m.Components), " ") + "'"
	if !strings.Contains(script, want) {
		t.Fatalf("installer tool set is incomplete:\n%s", script)
	}
	if strings.Contains(script, "agent-action-shell") {
		t.Fatalf("installer exposed Agent's private action adapter:\n%s", script)
	}
}

func TestInstallScriptDerivesNewCommandsFromManifest(t *testing.T) {
	components := []suite.Component{
		{Name: "bench", Kind: "go"},
		{Name: "edge", Kind: "go", Commands: []suite.Command{
			{Name: "mcp", Package: "./cmd/mcp"},
			{Name: "mcpbox", Package: "./cmd/mcpbox"},
		}},
		{Name: "agent", Kind: "files"},
	}
	script := installScript("0.7.0", componentNames(components))
	if !strings.Contains(script, "tools='bench mcp mcpbox agent'") {
		t.Fatalf("installer did not derive commands:\n%s", script)
	}
}

func TestComponentVersionIsIndependentOfSuiteVersion(t *testing.T) {
	m := suite.Manifest{
		Version: "9.0.0",
		Components: []suite.Component{
			{Name: "bench", Version: "1.2.3"},
		},
	}
	got, err := componentVersion(m, "bench")
	if err != nil || got != "1.2.3" {
		t.Fatalf("componentVersion = %q, %v", got, err)
	}
	if _, err := componentVersion(m, "may"); err == nil {
		t.Fatal("missing component version was accepted")
	}
}

func TestTargetPartRejectsPaths(t *testing.T) {
	for _, value := range []string{"amd64", "arm64", "386"} {
		if !targetPart(value) {
			t.Errorf("targetPart(%q) = false", value)
		}
	}
	for _, value := range []string{"", "../arm64", "arm-64", "ARM64"} {
		if targetPart(value) {
			t.Errorf("targetPart(%q) = true", value)
		}
	}
}

func TestSourcePathsKeepsSelfOutsideComponentCache(t *testing.T) {
	m, err := suite.Current()
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	self := filepath.Join(root, "checkout-with-any-name")
	cache := filepath.Join(root, "cache")
	paths, err := sourcePaths(m, cache, self, false)
	if err != nil {
		t.Fatal(err)
	}
	if paths["bench"] != self {
		t.Fatalf("Bench source = %q, want %q", paths["bench"], self)
	}
	if want := filepath.Join(cache, "ask"); paths["ask"] != want {
		t.Fatalf("Ask source = %q, want %q", paths["ask"], want)
	}
}

func TestFetchedSourcePathsAreRevisionAddressed(t *testing.T) {
	m, err := suite.Current()
	if err != nil {
		t.Fatal(err)
	}
	cache := filepath.Join(t.TempDir(), "cache")
	paths, err := sourcePaths(m, cache, t.TempDir(), true)
	if err != nil {
		t.Fatal(err)
	}
	for _, component := range m.Components {
		if component.Revision == "self" {
			continue
		}
		if want := filepath.Join(cache, component.Name+"-"+component.Revision); paths[component.Name] != want {
			t.Fatalf("%s source = %q, want %q", component.Name, paths[component.Name], want)
		}
	}
}

func TestFetchComponentChecksOutPinnedCommit(t *testing.T) {
	root := t.TempDir()
	origin := filepath.Join(root, "origin")
	if err := os.Mkdir(origin, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{
		{"init", "--quiet"},
		{"config", "user.name", "Bench Test"},
		{"config", "user.email", "bench@example.test"},
	} {
		if err := command(origin, "git", args...); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(origin, "tool.txt"), []byte("pinned\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := command(origin, "git", "add", "tool.txt"); err != nil {
		t.Fatal(err)
	}
	if err := command(origin, "git", "commit", "--quiet", "-m", "fixture"); err != nil {
		t.Fatal(err)
	}
	revision, err := git(origin, "rev-parse", "HEAD")
	if err != nil {
		t.Fatal(err)
	}
	workspace := filepath.Join(root, "workspace")
	if err := os.Mkdir(workspace, 0o755); err != nil {
		t.Fatal(err)
	}
	component := suite.Component{Name: "ask", Repository: origin, Revision: revision}
	if err := fetchComponent(workspace, component); err != nil {
		t.Fatal(err)
	}
	checkout := managedSourcePath(workspace, component)
	data, err := os.ReadFile(filepath.Join(checkout, "tool.txt"))
	if err != nil || string(data) != "pinned\n" {
		t.Fatalf("fetched file=%q err=%v", data, err)
	}
	head, err := git(checkout, "rev-parse", "HEAD")
	if err != nil || head != revision {
		t.Fatalf("fetched revision=%q err=%v", head, err)
	}
}

func TestWriteArchiveChecksum(t *testing.T) {
	archive := filepath.Join(t.TempDir(), "suite.tar.gz")
	if err := os.WriteFile(archive, []byte("archive"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := writeArchiveChecksum(archive); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(archive + ".sha256")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(string(data), "  suite.tar.gz\n") {
		t.Fatalf("sidecar = %q", data)
	}
}

func TestModuleFromKeepsReplacementEvidence(t *testing.T) {
	record := moduleFrom(&runtimedebug.Module{
		Path: "example.com/original", Version: "v1.2.3", Sum: "h1:original",
		Replace: &runtimedebug.Module{Path: "example.com/fork", Version: "v1.2.4", Sum: "h1:fork"},
	})
	if record.Path != "example.com/original" || record.Replace == nil || record.Replace.Path != "example.com/fork" || record.Replace.Sum != "h1:fork" {
		t.Fatalf("module record = %#v", record)
	}
}
