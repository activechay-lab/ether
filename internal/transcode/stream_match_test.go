package transcode

import (
	"testing"

	"github.com/braedonsaunders/ether/internal/core"
)

func TestMatchStreamProtectsMainDialogueAndPreservesSpecialRoles(t *testing.T) {
	commTrue := true
	viTrue := true

	replacement := core.MediaInfo{Streams: []core.StreamInfo{
		{Index: 1, Type: "audio", Codec: "ac3", Language: "eng", Channels: 2, Title: "Director's Commentary", Commentary: &commTrue, Default: true},
		{Index: 2, Type: "audio", Codec: "ac3", Language: "eng", Channels: 2, Title: "English Audio Description (AD)", VisualImpaired: &viTrue},
		{Index: 3, Type: "audio", Codec: "eac3", Language: "eng", Channels: 6, Title: "English 5.1"},
	}}

	// 1. Main dialogue track (even AC3 2.0) must NOT swap onto track 1 (Commentary) or track 2 (AD).
	prevMain := &core.StreamInfo{Index: 1, Type: "audio", Codec: "ac3", Language: "eng", Channels: 2, Title: "English Stereo"}
	gotMain := matchStream(replacement, prevMain, "audio")
	if gotMain == nil || gotMain.Index != 3 {
		t.Fatalf("main dialogue swapped onto %+v, want index 3 (main dialogue)", gotMain)
	}

	// 2. Intentionally selected commentary track preserves commentary role (index 1).
	prevComm := &core.StreamInfo{Index: 4, Type: "audio", Codec: "aac", Language: "en", Channels: 2, Title: "Cast & Crew Commentary"}
	gotComm := matchStream(replacement, prevComm, "audio")
	if gotComm == nil || gotComm.Index != 1 {
		t.Fatalf("selected commentary swapped onto %+v, want index 1 (commentary)", gotComm)
	}

	// 3. Intentionally selected audio-description track preserves AD role (index 2).
	prevAD := &core.StreamInfo{Index: 5, Type: "audio", Codec: "aac", Language: "en", Channels: 2, Title: "English Audio Description"}
	gotAD := matchStream(replacement, prevAD, "audio")
	if gotAD == nil || gotAD.Index != 2 {
		t.Fatalf("selected audio description swapped onto %+v, want index 2 (AD)", gotAD)
	}

	// 4. When commentary is absent in the replacement release, intentionally selected
	// commentary fails closed (nil) rather than silently replacing the role with AD or main dialogue.
	noCommReplacement := core.MediaInfo{Streams: []core.StreamInfo{
		{Index: 1, Type: "audio", Codec: "ac3", Language: "eng", Channels: 2, Title: "English Audio Description (AD)", VisualImpaired: &viTrue},
		{Index: 2, Type: "audio", Codec: "eac3", Language: "eng", Channels: 6, Title: "English 5.1", Default: true},
	}}
	if gotFallback := matchStream(noCommReplacement, prevComm, "audio"); gotFallback != nil {
		t.Fatalf("missing commentary role swapped onto %+v, want nil (no silent role loss)", gotFallback)
	}

	// 5. When all replacement audio tracks are special roles (commentary/AD), main
	// dialogue fails closed (nil) rather than falling back to def/first commentary/AD.
	onlySpecialRoles := core.MediaInfo{Streams: []core.StreamInfo{
		{Index: 1, Type: "audio", Codec: "ac3", Language: "eng", Channels: 2, Title: "Director's Commentary", Commentary: &commTrue, Default: true},
		{Index: 2, Type: "audio", Codec: "ac3", Language: "eng", Channels: 2, Title: "English Audio Description (AD)", VisualImpaired: &viTrue},
	}}
	if gotSpecialOnly := matchStream(onlySpecialRoles, prevMain, "audio"); gotSpecialOnly != nil {
		t.Fatalf("main dialogue swapped onto special-only replacement %+v, want nil", gotSpecialOnly)
	}

	// 6. Prior fallback contract preserved when main-dialogue language is absent but
	// replacement has a main-dialogue default/first track.
	foreignMainOnly := core.MediaInfo{Streams: []core.StreamInfo{
		{Index: 1, Type: "audio", Codec: "ac3", Language: "fra", Channels: 2, Title: "Commentaire Audio", Commentary: &commTrue, Default: true},
		{Index: 2, Type: "audio", Codec: "eac3", Language: "fra", Channels: 6, Title: "Français 5.1"},
	}}
	gotMainFallback := matchStream(foreignMainOnly, prevMain, "audio")
	if gotMainFallback == nil || gotMainFallback.Index != 2 {
		t.Fatalf("main dialogue cross-language fallback picked %+v, want index 2 (firstMain)", gotMainFallback)
	}
}

func TestMatchStreamLanguageTitleInferenceAndChannelProximity(t *testing.T) {
	// 1. Untagged ("und") replacement stream with language in Title matches tagged prev stream,
	// and regional BCP-47 (es-419) beats es-ES.
	prevLatino := &core.StreamInfo{Index: 1, Type: "audio", Codec: "eac3", Language: "spa", Channels: 6, Title: "Español Latino 5.1"}
	nextInfo := core.MediaInfo{Streams: []core.StreamInfo{
		{Index: 1, Type: "audio", Codec: "eac3", Language: "es-ES", Channels: 6, Title: "Castellano 5.1", Default: true},
		{Index: 2, Type: "audio", Codec: "ac3", Language: "und", Channels: 6, Title: "AC3 5.1 Spanish (Latin America)"},
	}}
	got := matchStream(nextInfo, prevLatino, "audio")
	if got == nil || got.Index != 2 {
		t.Fatalf("es-419 stream matched %+v, want index 2 (Latin American Spanish)", got)
	}

	// 2. Channel proximity prefers 6ch over 2ch when swapping from a 6ch track.
	prevSurround := &core.StreamInfo{Index: 1, Type: "audio", Codec: "truehd", Language: "eng", Channels: 8, ChannelLayout: "7.1"}
	nextChannels := core.MediaInfo{Streams: []core.StreamInfo{
		{Index: 1, Type: "audio", Codec: "aac", Language: "en", Channels: 2, ChannelLayout: "stereo", Default: true},
		{Index: 2, Type: "audio", Codec: "eac3", Language: "en", Channels: 6, ChannelLayout: "5.1"},
	}}
	gotCh := matchStream(nextChannels, prevSurround, "audio")
	if gotCh == nil || gotCh.Index != 2 {
		t.Fatalf("surround failover matched %+v, want index 2 (6ch)", gotCh)
	}

	// 3. Forced subtitle never swaps onto a full non-forced subtitle track.
	prevForcedSub := &core.StreamInfo{Index: 3, Type: "subtitle", Codec: "subrip", Language: "eng", Forced: true}
	nextSubs := core.MediaInfo{Streams: []core.StreamInfo{
		{Index: 3, Type: "subtitle", Codec: "subrip", Language: "eng", Forced: false, Default: true},
	}}
	if gotSub := matchStream(nextSubs, prevForcedSub, "subtitle"); gotSub != nil {
		t.Fatalf("forced subtitle swapped onto full subtitle %+v, want nil", gotSub)
	}
}
