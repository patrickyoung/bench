// Package suite owns the release-level compatibility contract for Bench and
// the independent filters it composes.
package suite

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"path/filepath"
	"regexp"
	"strings"
)

var versionPattern = regexp.MustCompile(`^[0-9]+\.[0-9]+\.[0-9]+(?:-[0-9A-Za-z.-]+)?(?:\+[0-9A-Za-z.-]+)?$`)

//go:embed manifest.json
var manifestJSON []byte

type Manifest struct {
	Schema     int         `json:"schema"`
	Version    string      `json:"version"`
	Go         string      `json:"go"`
	Components []Component `json:"components"`
}

type Component struct {
	Name        string    `json:"name"`
	Repository  string    `json:"repository"`
	Revision    string    `json:"revision"`
	Version     string    `json:"version"`
	Kind        string    `json:"kind"`
	Entry       string    `json:"entry,omitempty"`
	Assets      []string  `json:"assets,omitempty"`
	Commands    []Command `json:"commands,omitempty"`
	License     string    `json:"license"`
	LicenseFile string    `json:"license_file"`
	Dirty       bool      `json:"dirty,omitempty"`
}

type Command struct {
	Name    string `json:"name"`
	Package string `json:"package"`
}

// PublicCommands returns the user-facing commands emitted by a component.
// Existing one-command components need no manifest ceremony; a Go repository
// with multiple commands names each main package explicitly.
func (c Component) PublicCommands() []Command {
	if c.Kind == "files" {
		return []Command{{Name: c.Name}}
	}
	if len(c.Commands) == 0 {
		return []Command{{Name: c.Name, Package: "."}}
	}
	return append([]Command(nil), c.Commands...)
}

func Current() (Manifest, error) {
	return Parse(manifestJSON)
}

func Parse(data []byte) (Manifest, error) {
	var m Manifest
	if err := json.Unmarshal(data, &m); err != nil {
		return Manifest{}, fmt.Errorf("decode suite manifest: %w", err)
	}
	if err := m.Validate(); err != nil {
		return Manifest{}, err
	}
	return m, nil
}

func (m Manifest) Validate() error {
	if m.Schema != 1 {
		return fmt.Errorf("suite schema %d is not supported", m.Schema)
	}
	if !versionPattern.MatchString(m.Version) {
		return fmt.Errorf("suite version %q is not a portable semantic version", m.Version)
	}
	if strings.TrimSpace(m.Go) == "" {
		return fmt.Errorf("suite Go version is empty")
	}
	seen := make(map[string]bool, len(m.Components))
	commands := make(map[string]bool, len(m.Components))
	for _, c := range m.Components {
		if c.Name == "" || c.Repository == "" || c.Revision == "" || c.Version == "" {
			return fmt.Errorf("suite component has an empty required field: %q", c.Name)
		}
		if !validName(c.Name) {
			return fmt.Errorf("suite component name %q is not portable", c.Name)
		}
		if !versionPattern.MatchString(c.Version) {
			return fmt.Errorf("suite component %q version %q is not a portable semantic version", c.Name, c.Version)
		}
		if c.Revision != "self" && !isCommit(c.Revision) {
			return fmt.Errorf("suite component %q revision is not a full Git commit", c.Name)
		}
		if seen[c.Name] {
			return fmt.Errorf("suite component %q is duplicated", c.Name)
		}
		seen[c.Name] = true
		switch c.Kind {
		case "go":
			if c.Entry != "" || len(c.Assets) != 0 {
				return fmt.Errorf("Go component %q cannot declare files", c.Name)
			}
			for _, command := range c.PublicCommands() {
				if !validName(command.Name) || !validGoPackage(command.Package) {
					return fmt.Errorf("Go component %q has unsafe command %#v", c.Name, command)
				}
				if commands[command.Name] {
					return fmt.Errorf("public command %q is duplicated", command.Name)
				}
				commands[command.Name] = true
			}
		case "files":
			if len(c.Commands) != 0 {
				return fmt.Errorf("files component %q cannot declare Go commands", c.Name)
			}
			if !validRelative(c.Entry) {
				return fmt.Errorf("files component %q has no entry", c.Name)
			}
			for _, asset := range c.Assets {
				if !validRelative(asset) {
					return fmt.Errorf("component %q has unsafe asset path %q", c.Name, asset)
				}
			}
			if commands[c.Name] {
				return fmt.Errorf("public command %q is duplicated", c.Name)
			}
			commands[c.Name] = true
		default:
			return fmt.Errorf("component %q has unsupported kind %q", c.Name, c.Kind)
		}
		if c.License != "MIT" {
			return fmt.Errorf("component %q has unapproved SPDX license %q", c.Name, c.License)
		}
		if !validRelative(c.LicenseFile) {
			return fmt.Errorf("component %q has unsafe license path %q", c.Name, c.LicenseFile)
		}
	}
	for _, required := range []string{"bench", "ask", "brief", "ply", "context", "action", "cite", "may", "cage", "hone", "trail", "agent", "tend", "draft", "mcp", "oauth"} {
		if !seen[required] {
			return fmt.Errorf("suite is missing required component %q", required)
		}
	}
	return nil
}

func validGoPackage(path string) bool {
	if path == "." {
		return true
	}
	if !strings.HasPrefix(path, "./") {
		return false
	}
	return validRelative(strings.TrimPrefix(path, "./"))
}

func validName(name string) bool {
	for i, r := range name {
		letter := r >= 'a' && r <= 'z'
		afterFirst := i > 0 && (r >= '0' && r <= '9' || r == '-')
		if letter || afterFirst {
			continue
		}
		return false
	}
	return name != "" && name[len(name)-1] != '-'
}

func isCommit(revision string) bool {
	if len(revision) != 40 {
		return false
	}
	for _, r := range revision {
		if r < '0' || r > '9' {
			if r < 'a' || r > 'f' {
				return false
			}
		}
	}
	return true
}

func validRelative(path string) bool {
	return path != "" && !filepath.IsAbs(path) && filepath.Clean(path) == path && path != "." && path != ".." && !strings.HasPrefix(path, ".."+string(filepath.Separator))
}

func JSON(m Manifest) ([]byte, error) {
	data, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(data, '\n'), nil
}
