package plugins

import "testing"

func remoteSidecarManifest() Manifest {
	return Manifest{
		ID: "community.example.sidecar", Version: "1.0.0", Name: "Sidecar",
		Description: "Runs elsewhere.",
		Ether: &EtherBlock{
			Protocol: 1, Kind: KindExtension, Summary: "Runs elsewhere",
			Runtime: RuntimeDecl{Kind: "remote"},
		},
	}
}

func TestManifestSidecarValidate(t *testing.T) {
	if err := remoteSidecarManifest().Validate(); err != nil {
		t.Fatalf("remote manifest without a sidecar must pass: %v", err)
	}

	ok := remoteSidecarManifest()
	ok.Ether.Sidecar = &SidecarDecl{
		Docker: "ghcr.io/ether/home-assistant:1.2.3",
		Docs:   "https://docs.example.com/sidecar",
	}
	if err := ok.Validate(); err != nil {
		t.Fatalf("valid docker sidecar: %v", err)
	}

	download := remoteSidecarManifest()
	download.Ether.Sidecar = &SidecarDecl{
		Download: "https://example.com/sidecar",
		Command:  "python3 plugin.py",
		Docs:     "https://docs.example.com/sidecar",
	}
	if err := download.Validate(); err != nil {
		t.Fatalf("valid download sidecar: %v", err)
	}

	cases := []struct {
		name  string
		mut   func(*Manifest)
		field string
	}{
		{"docker spaces", func(m *Manifest) {
			m.Ether.Sidecar = &SidecarDecl{Docker: "ghcr.io/x/y:tag extra"}
		}, "ether.sidecar.docker"},
		{"docker shell metachar", func(m *Manifest) {
			m.Ether.Sidecar = &SidecarDecl{Docker: "ghcr.io/x/y:tag;rm"}
		}, "ether.sidecar.docker"},
		{"docker traversal", func(m *Manifest) {
			m.Ether.Sidecar = &SidecarDecl{Docker: "ghcr.io/../y"}
		}, "ether.sidecar.docker"},
		{"download not https", func(m *Manifest) {
			m.Ether.Sidecar = &SidecarDecl{Download: "http://example.com/x", Command: "run"}
		}, "ether.sidecar.download"},
		{"docs not https", func(m *Manifest) {
			m.Ether.Sidecar = &SidecarDecl{Docs: "ftp://example.com"}
		}, "ether.sidecar.docs"},
		{"command multiline", func(m *Manifest) {
			m.Ether.Sidecar = &SidecarDecl{Download: "https://example.com/x", Command: "run\nrm"}
		}, "ether.sidecar.command"},
		{"command too long", func(m *Manifest) {
			m.Ether.Sidecar = &SidecarDecl{Download: "https://example.com/x", Command: string(make([]byte, 201))}
		}, "ether.sidecar.command"},
		{"download needs command", func(m *Manifest) {
			m.Ether.Sidecar = &SidecarDecl{Download: "https://example.com/x"}
		}, "ether.sidecar.command"},
		{"hosted sidecar", func(m *Manifest) {
			m.Ether.Runtime = RuntimeDecl{Kind: "hosted", Entry: "dist/plugin.js"}
			m.Ether.Sidecar = &SidecarDecl{Docker: "ghcr.io/x/y"}
		}, "ether.sidecar"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := remoteSidecarManifest()
			tc.mut(&m)
			err := m.Validate()
			if err == nil {
				t.Fatalf("expected a validation error for %s", tc.field)
			}
			if !hasField(err, tc.field) {
				t.Fatalf("error %v does not mention field %s", err, tc.field)
			}
		})
	}
}
