package app

import "testing"

// Published logo URLs are pinned to the source a build came from: a release
// to its tag, anything else to its commit.
func TestSourceRef(t *testing.T) {
	sha := "4661ae7ca0b1c2d3e4f5a6b7c8d9e0f1a2b3c4d5"
	for _, c := range []struct{ version, commit, revision, want string }{
		{"0.3.6", sha, "", "v0.3.6"},
		{"v0.3.6-beta.1", "", "", "v0.3.6-beta.1"},
		{"dev", "4661ae7ca", sha, "4661ae7ca"},
		{"pr-412", sha, "", sha},
		{"dev", "", sha, sha},
		{"", "", "", ""},
		{"dev", "../main", "", ""},
	} {
		if got := sourceRef(c.version, c.commit, c.revision); got != c.want {
			t.Errorf("sourceRef(%q, %q, %q) = %q, want %q", c.version, c.commit, c.revision, got, c.want)
		}
	}
	if got := repoFileURL("v0.3.6")("internal/livetv/showcaselogos/v1/riot-90.png"); got != "https://cdn.jsdelivr.net/gh/braedonsaunders/ether@v0.3.6/internal/livetv/showcaselogos/v1/riot-90.png" {
		t.Fatal(got)
	}
	if repoFileURL("")("a.png") != "" {
		t.Fatal("unknown source published a URL")
	}
}
