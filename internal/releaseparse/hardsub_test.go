package releaseparse

import (
	"encoding/json"
	"os"
	"testing"
)

func TestHardsubTokens(t *testing.T) {
	for _, tag := range []string{"HC.HDRip", "HC-WEBRip", "HARDSUB", "HARDSUBS", "HARDCODED", "HARDSUBBED", "KORSUB"} {
		if !HardSubbed("Movie.2024.1080p." + tag) {
			t.Error(tag)
		}
	}
	for _, tag := range []string{"SUB.ITA", "Multi.Sub", "ENG.SUB", "Subbed", "CHS", "CHT.SUB", "ViE.SUB", "HDTC", "HDCAM", "CAMRip", "SUBPACK", "Subtitles", "HCatalog"} {
		if HardSubbed("Movie.2024.1080p.WEB-DL." + tag) {
			t.Error(tag)
		}
	}
	if !HardSubbed("Movie.HC-HDRip") {
		t.Fatal("source HC without year")
	}
	if HardSubbed("The.HC.Club.2024.WEBRip") || HardSubbed("HC.2024.1080p.WEBRip") {
		t.Fatal("title HC is not source HC")
	}
	if !KeywordMatch("Movie.SUB.ITA", "sub") || KeywordMatch("Movie.KORSUB", "sub") || KeywordMatch("Movie.Subtitles.SUBPACK", "sub") {
		t.Fatal("plain keyword semantics")
	}
}
func TestHardsubRealNames(t *testing.T) {
	var rows []struct {
		Name   string `json:"name"`
		Burned bool   `json:"burnedEvidence"`
		Source string `json:"source"`
	}
	raw, err := os.ReadFile("testdata/hardsub_names.json")
	if err != nil {
		t.Fatal(err)
	}
	if err = json.Unmarshal(raw, &rows); err != nil {
		t.Fatal(err)
	}
	tp, fp, fn, tn := 0, 0, 0, 0
	for _, r := range rows {
		if r.Source == "" {
			t.Fatal("missing provenance")
		}
		got := HardSubbed(r.Name)
		if got && r.Burned {
			tp++
		} else if got {
			fp++
		} else if r.Burned {
			fn++
		} else {
			tn++
		}
		if got != r.Burned {
			t.Errorf("%s got=%v want=%v", r.Name, got, r.Burned)
		}
	}
	t.Logf("names=%d TP=%d FP=%d FN=%d TN=%d precision=%.2f%% recall=%.2f%%", len(rows), tp, fp, fn, tn, 100*float64(tp)/float64(tp+fp), 100*float64(tp)/float64(tp+fn))
}
