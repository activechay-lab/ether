package releaseparse

import "testing"

func TestHardsubTokens(t *testing.T) {
	for _, tag := range []string{"HC", "HARDSUB", "HARDSUBS", "HARDCODED", "KORSUB", "CHSUB", "CHS-SUB", "CHT-SUB", "ViE-SUB", "Subbed", "SUB.ITA", "HC-HDRip", "CHS", "HDTC", "HDCAM"} {
		name := "The.Odyssey.2026.1080p." + tag + "-GROUP"
		if !Parse(name).HardSubbed || !KeywordMatch(name, "sub") {
			t.Errorf("missed %s", tag)
		}
	}
	for _, name := range []string{"Submarine.2026.1080p", "Subtitles.English", "SUBPACK", "MULTISUB", "HCatalog", "Unsubbed"} {
		if HardSubbed(name) || KeywordMatch(name, "sub") {
			t.Errorf("false match %s", name)
		}
	}
	if !KeywordMatch("Movie.WEB-DL.GROUP", "web dl") || KeywordMatch("Movie.SuperGroup", "group") {
		t.Fatal("keyword boundaries")
	}
}
