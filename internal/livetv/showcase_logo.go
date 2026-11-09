package livetv

import (
	"embed"
	"strings"
	"sync"
)

// Each shipped decade channel's guide logo is its look's station bug, drawn
// by onair's TestRenderShowcaseLogos. The files are committed so a published
// URL pinned to a release or commit always resolves (Plex for Apple TV draws
// guide logos only from public URLs). New artwork goes in a new versioned
// directory; a published file never changes.
//
//go:embed showcaselogos/v1/*.png
var showcaseLogoFiles embed.FS

const showcaseLogoDir = "showcaselogos/v1/"

// showcaseLogoRepoDir is showcaseLogoDir's path in Ether's repository.
const showcaseLogoRepoDir = "internal/livetv/" + showcaseLogoDir

var showcaseNames = sync.OnceValue(func() map[string]string {
	names := map[string]string{}
	for _, r := range showcaseRecipes() {
		names["showcase-"+r.id] = r.name
	}
	return names
})

// ShowcaseLogo is a shipped channel's own logo: its path in Ether's
// repository and its PNG bytes. An owner's edits win: a channel with a logo
// of the owner's choosing, or renamed, draws that instead.
func ShowcaseLogo(c Channel) (repoPath string, png []byte, ok bool) {
	name, shipped := showcaseNames()[c.ID]
	if !shipped || c.Logo != "" || c.Kind != "virtual" || c.Name != name {
		return "", nil, false
	}
	file := strings.TrimPrefix(c.ID, "showcase-") + ".png"
	data, err := showcaseLogoFiles.ReadFile(showcaseLogoDir + file)
	if err != nil {
		return "", nil, false
	}
	return showcaseLogoRepoDir + file, data, true
}
