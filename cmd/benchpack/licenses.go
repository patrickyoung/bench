package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
)

type licensedModule struct {
	Path    string
	Version string
	Source  moduleRecord
}

type moduleDownload struct {
	Path    string `json:"Path"`
	Version string `json:"Version"`
	Dir     string `json:"Dir"`
	Error   string `json:"Error"`
}

// collectThirdPartyLicenses turns the modules read from the actual binaries
// into distributable evidence. It fails closed: a dependency without an
// ordinary root license/notice file cannot silently enter a release archive.
func collectThirdPartyLicenses(root string, inventory moduleInventory) error {
	modules, err := uniqueModules(inventory)
	if err != nil {
		return err
	}
	var notice strings.Builder
	notice.WriteString("# Third-party notices\n\n")
	notice.WriteString("These are the license and notice files distributed by the Go modules embedded in this suite. Module versions and checksums also appear in `go-modules.json`.\n\n")
	for i, module := range modules {
		download, err := downloadModule(module.Path, module.Version)
		if err != nil {
			return err
		}
		files, err := findLicenseFiles(download.Dir)
		if err != nil {
			return fmt.Errorf("inspect license for %s@%s: %w", module.Path, module.Version, err)
		}
		if len(files) == 0 {
			return fmt.Errorf("no root license or notice file for shipped module %s@%s", module.Path, module.Version)
		}
		directory := fmt.Sprintf("%03d-%s", i+1, moduleID(module.Path+"@"+module.Version))
		fmt.Fprintf(&notice, "- `%s@%s`", module.Path, module.Version)
		if module.Source.Replace != nil {
			fmt.Fprintf(&notice, " (replacement for `%s@%s`)", module.Source.Path, module.Source.Version)
		}
		notice.WriteString(":")
		for j, source := range files {
			name := fmt.Sprintf("%02d-%s", j+1, filepath.Base(source))
			relative := filepath.Join("licenses", "third-party", directory, name)
			if err := copyFile(source, filepath.Join(root, relative), 0o644); err != nil {
				return fmt.Errorf("copy license for %s@%s: %w", module.Path, module.Version, err)
			}
			fmt.Fprintf(&notice, " [%s](%s)", filepath.Base(source), filepath.ToSlash(relative))
		}
		notice.WriteString("\n")
	}
	return os.WriteFile(filepath.Join(root, "THIRD_PARTY_NOTICES.md"), []byte(notice.String()), 0o644)
}

func uniqueModules(inventory moduleInventory) ([]licensedModule, error) {
	byKey := make(map[string]licensedModule)
	for _, component := range inventory.Components {
		for _, module := range component.Modules {
			actual := module
			if module.Replace != nil {
				actual = *module.Replace
			}
			if actual.Path == "" {
				continue
			}
			if actual.Version == "" || actual.Version == "(devel)" {
				return nil, fmt.Errorf("shipped module %s has no immutable version for license collection", actual.Path)
			}
			key := actual.Path + "@" + actual.Version
			if _, exists := byKey[key]; !exists {
				byKey[key] = licensedModule{Path: actual.Path, Version: actual.Version, Source: module}
			}
		}
	}
	modules := make([]licensedModule, 0, len(byKey))
	for _, module := range byKey {
		modules = append(modules, module)
	}
	sort.Slice(modules, func(i, j int) bool {
		if modules[i].Path == modules[j].Path {
			return modules[i].Version < modules[j].Version
		}
		return modules[i].Path < modules[j].Path
	})
	return modules, nil
}

func downloadModule(path, version string) (moduleDownload, error) {
	cmd := exec.Command("go", "mod", "download", "-json", path+"@"+version)
	cmd.Env = overlay(os.Environ(), "GOWORK=off")
	output, err := cmd.CombinedOutput()
	var download moduleDownload
	decodeErr := json.Unmarshal(output, &download)
	if err != nil {
		return moduleDownload{}, fmt.Errorf("locate license for %s@%s: %w: %s", path, version, err, strings.TrimSpace(string(output)))
	}
	if decodeErr != nil {
		return moduleDownload{}, fmt.Errorf("decode module location for %s@%s: %w", path, version, decodeErr)
	}
	if download.Error != "" || download.Dir == "" {
		return moduleDownload{}, fmt.Errorf("locate license for %s@%s: %s", path, version, download.Error)
	}
	return download, nil
}

func findLicenseFiles(dir string) ([]string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	var files []string
	for _, entry := range entries {
		if entry.IsDir() || !licenseFilename(entry.Name()) {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			return nil, err
		}
		if !info.Mode().IsRegular() {
			continue
		}
		if info.Size() > 1<<20 {
			return nil, fmt.Errorf("license file %s exceeds 1 MiB", filepath.Join(dir, entry.Name()))
		}
		files = append(files, filepath.Join(dir, entry.Name()))
	}
	sort.Strings(files)
	return files, nil
}

func licenseFilename(name string) bool {
	upper := strings.ToUpper(name)
	for _, prefix := range []string{"LICENSE", "LICENCE", "COPYING", "COPYRIGHT", "NOTICE", "PATENTS"} {
		if upper == prefix || strings.HasPrefix(upper, prefix+".") || strings.HasPrefix(upper, prefix+"-") {
			return true
		}
	}
	return false
}

func moduleID(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:6])
}
