package main

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestUniqueModulesDeduplicatesAndUsesReplacement(t *testing.T) {
	inventory := moduleInventory{Components: []componentInventory{
		{Name: "one", Modules: []moduleRecord{{Path: "example.com/a", Version: "v1.0.0"}}},
		{Name: "two", Modules: []moduleRecord{
			{Path: "example.com/a", Version: "v1.0.0"},
			{Path: "example.com/original", Version: "v2.0.0", Replace: &moduleRecord{Path: "example.com/fork", Version: "v2.0.1"}},
		}},
	}}
	got, err := uniqueModules(inventory)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].Path != "example.com/a" || got[1].Path != "example.com/fork" || got[1].Source.Path != "example.com/original" {
		t.Fatalf("uniqueModules = %#v", got)
	}
}

func TestUniqueModulesRejectsUnversionedDependency(t *testing.T) {
	inventory := moduleInventory{Components: []componentInventory{{
		Name:    "one",
		Modules: []moduleRecord{{Path: "example.com/local", Version: "(devel)"}},
	}}}
	if _, err := uniqueModules(inventory); err == nil {
		t.Fatal("uniqueModules accepted an unversioned dependency")
	}
}

func TestFindLicenseFilesUsesOnlyOrdinaryRootEvidence(t *testing.T) {
	dir := t.TempDir()
	for name, body := range map[string]string{
		"LICENSE": "license", "NOTICE.txt": "notice", "README.md": "readme",
	} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Mkdir(filepath.Join(dir, "COPYING"), 0o755); err != nil {
		t.Fatal(err)
	}
	files, err := findLicenseFiles(dir)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{filepath.Join(dir, "LICENSE"), filepath.Join(dir, "NOTICE.txt")}
	if !reflect.DeepEqual(files, want) {
		t.Fatalf("license files = %#v, want %#v", files, want)
	}
}

func TestLicenseFilename(t *testing.T) {
	for _, name := range []string{"LICENSE", "LICENCE.md", "COPYING-GPL", "NOTICE.txt", "PATENTS"} {
		if !licenseFilename(name) {
			t.Errorf("licenseFilename(%q) = false", name)
		}
	}
	for _, name := range []string{"README.md", "UNLICENSED", "docs/LICENSE"} {
		if licenseFilename(name) {
			t.Errorf("licenseFilename(%q) = true", name)
		}
	}
}
