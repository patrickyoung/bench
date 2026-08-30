package suite

import (
	"strings"
	"testing"
)

func TestEmbeddedManifestIsComplete(t *testing.T) {
	m, err := Current()
	if err != nil {
		t.Fatal(err)
	}
	if m.Version != "0.13.0" || len(m.Components) != 16 {
		t.Fatalf("manifest version=%q components=%d", m.Version, len(m.Components))
	}
	var commands []string
	for _, component := range m.Components {
		for _, command := range component.PublicCommands() {
			commands = append(commands, command.Name)
		}
	}
	if len(commands) != 18 {
		t.Fatalf("public commands=%d: %v", len(commands), commands)
	}
	for _, required := range []string{"action", "mcp", "mcpbox", "mcpserve", "oauth"} {
		if !strings.Contains(" "+strings.Join(commands, " ")+" ", " "+required+" ") {
			t.Fatalf("public command %q is missing: %v", required, commands)
		}
	}
	data, err := JSON(m)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(string(data), "\n") {
		t.Fatal("rendered manifest has no final newline")
	}
}

func TestManifestRejectsMissingSuiteMember(t *testing.T) {
	m, err := Current()
	if err != nil {
		t.Fatal(err)
	}
	for _, missing := range []string{"bench", "ask", "brief", "ply", "context", "action", "cite", "may", "cage", "hone", "trail", "agent", "tend", "draft", "mcp", "oauth"} {
		t.Run(missing, func(t *testing.T) {
			broken := m
			broken.Components = nil
			for _, component := range m.Components {
				if component.Name != missing {
					broken.Components = append(broken.Components, component)
				}
			}
			if err := broken.Validate(); err == nil || !strings.Contains(err.Error(), missing) {
				t.Fatalf("Validate() = %v", err)
			}
		})
	}
}

func TestManifestRejectsUnsafeOrDuplicateCommands(t *testing.T) {
	m, err := Current()
	if err != nil {
		t.Fatal(err)
	}
	for _, commands := range [][]Command{
		{{Name: "../mcp", Package: "./cmd/mcp"}},
		{{Name: "mcp", Package: "../mcp"}},
		{{Name: "bench", Package: "./cmd/mcp"}},
	} {
		broken := m
		broken.Components = append([]Component(nil), m.Components...)
		broken.Components[len(broken.Components)-2].Commands = commands
		if err := broken.Validate(); err == nil {
			t.Fatalf("unsafe commands unexpectedly validated: %#v", commands)
		}
	}
}

func TestManifestRejectsUnsafeComponentFields(t *testing.T) {
	m, err := Current()
	if err != nil {
		t.Fatal(err)
	}
	for _, mutate := range []func(*Manifest){
		func(m *Manifest) { m.Components[0].Name = "../bench" },
		func(m *Manifest) { m.Version = "0.5.0; echo unsafe" },
		func(m *Manifest) { m.Components[1].Revision = "main" },
		func(m *Manifest) { m.Components[5].Assets = []string{"../secret"} },
		func(m *Manifest) { m.Components[0].License = "" },
		func(m *Manifest) { m.Components[1].LicenseFile = "/etc/passwd" },
	} {
		broken := m
		broken.Components = append([]Component(nil), m.Components...)
		mutate(&broken)
		if err := broken.Validate(); err == nil {
			t.Fatal("unsafe manifest unexpectedly validated")
		}
	}
}

func TestEmbeddedManifestKeepsAgentActionShellPrivate(t *testing.T) {
	m, err := Current()
	if err != nil {
		t.Fatal(err)
	}
	for _, component := range m.Components {
		if component.Name != "agent" {
			continue
		}
		if component.Kind != "files" || component.Entry != "bin/agent" || len(component.Assets) != 1 || component.Assets[0] != "bin/agent-action-shell" {
			t.Fatalf("agent component = %#v", component)
		}
		return
	}
	t.Fatal("agent component is missing")
}
