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
	if m.Version != "0.6.5" || len(m.Components) != 8 {
		t.Fatalf("manifest version=%q components=%d", m.Version, len(m.Components))
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
	m.Components = m.Components[:len(m.Components)-1]
	if err := m.Validate(); err == nil || !strings.Contains(err.Error(), "draft") {
		t.Fatalf("Validate() = %v", err)
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
