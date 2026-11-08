package core

import (
	"maps"
	"strings"
)

// Ether's built-in custom formats (docs/quality-ranking.md). They score on
// a profile like the owner's and imported formats (QualityProfile.
// CustomFormatScores) but are evaluated from Ether's parsed release and the
// session: the device, the viewer and the connection. Release formats
// describe the release; streaming formats describe how it plays here.

// BuiltinFormatPrefix namespaces built-in format ids.
const BuiltinFormatPrefix = "ether:"

// RuleFormatPrefix keys a Prefer rule's score in CustomFormatScores:
// "rule:<rule id>".
const RuleFormatPrefix = "rule:"

// Built-in format kinds.
const (
	FormatKindRelease   = "release"
	FormatKindStreaming = "streaming"
)

// Built-in format families: at most one format of a family matches a
// release (it has one release type, one video and one audio codec).
const (
	FamilySource = "source"
	FamilyCodec  = "codec"
	FamilyAudio  = "audio"
)

// Built-in format ids that are not per source or codec.
const (
	FormatDolbyVision             = "ether:dolby-vision"
	FormatHDR                     = "ether:hdr"
	FormatHDR10                   = "ether:hdr10"
	FormatAtmos                   = "ether:atmos"
	FormatRepackProper            = "ether:repack-proper"
	FormatLowBitrate              = "ether:low-bitrate"
	FormatPreferredLanguage       = "ether:preferred-language"
	FormatPreferredLanguageRegion = "ether:preferred-language-region"
	FormatOtherLanguage           = "ether:other-language"
	FormatPreferredSubtitles      = "ether:preferred-subtitles"

	FormatViewerLanguage    = "ether:viewer-language"
	FormatSeriesYearDiffers = "ether:series-year-differs"
	FormatVideoTranscode    = "ether:video-transcode"
	FormatAudioDirect       = "ether:audio-direct"
	FormatAudioTranscode    = "ether:audio-transcode"
	FormatAtmosPassthrough  = "ether:atmos-passthrough"
	FormatToneMapping       = "ether:tone-mapping"
	FormatPastRetention     = "ether:past-retention"
)

// FormatSources, FormatCodecs and FormatAudioCodecs are the release types,
// video codecs and audio formats with a built-in format, in the order the
// console lists them.
var (
	FormatSources     = []Source{SourceRemux, SourceBluRay, SourceWEBDL, SourceWEBRip, SourceHDTV, SourceDVD}
	FormatCodecs      = []string{"hevc", "av1", "h264", "vp9", "vc1", "mpeg2", "xvid"}
	FormatAudioCodecs = []string{"truehd", "dtshd", "eac3", "dts", "ac3", "aac", "flac", "opus", "mp3"}
)

// SourceFormat is the built-in format id of a release type.
func SourceFormat(s Source) string { return BuiltinFormatPrefix + string(s) }

// CodecFormat is the built-in format id of a video codec.
func CodecFormat(codec string) string { return BuiltinFormatPrefix + "codec-" + codec }

// AudioFormat is the built-in format id of an audio format.
func AudioFormat(codec string) string { return BuiltinFormatPrefix + "audio-" + codec }

// BuiltinFormat describes one built-in format.
type BuiltinFormat struct {
	ID     string `json:"id"`
	Kind   string `json:"kind"`             // FormatKindRelease or FormatKindStreaming
	Family string `json:"family,omitempty"` // FamilySource, FamilyCodec, FamilyAudio
}

// BuiltinFormats lists every built-in format in console order.
func BuiltinFormats() []BuiltinFormat {
	var out []BuiltinFormat
	for _, s := range FormatSources {
		out = append(out, BuiltinFormat{ID: SourceFormat(s), Kind: FormatKindRelease, Family: FamilySource})
	}
	for _, id := range []string{FormatDolbyVision, FormatHDR, FormatHDR10} {
		out = append(out, BuiltinFormat{ID: id, Kind: FormatKindRelease})
	}
	for _, c := range FormatCodecs {
		out = append(out, BuiltinFormat{ID: CodecFormat(c), Kind: FormatKindRelease, Family: FamilyCodec})
	}
	for _, a := range FormatAudioCodecs {
		out = append(out, BuiltinFormat{ID: AudioFormat(a), Kind: FormatKindRelease, Family: FamilyAudio})
	}
	for _, id := range []string{FormatAtmos, FormatRepackProper, FormatLowBitrate, FormatPreferredLanguage, FormatPreferredLanguageRegion, FormatOtherLanguage, FormatPreferredSubtitles} {
		out = append(out, BuiltinFormat{ID: id, Kind: FormatKindRelease})
	}
	for _, id := range []string{FormatViewerLanguage, FormatSeriesYearDiffers, FormatVideoTranscode, FormatAudioDirect, FormatAudioTranscode, FormatAtmosPassthrough, FormatToneMapping, FormatPastRetention} {
		out = append(out, BuiltinFormat{ID: id, Kind: FormatKindStreaming})
	}
	return out
}

// IsBuiltinFormat reports whether id names a built-in format.
func IsBuiltinFormat(id string) bool {
	if !strings.HasPrefix(id, BuiltinFormatPrefix) {
		return false
	}
	for _, f := range BuiltinFormats() {
		if f.ID == id {
			return true
		}
	}
	return false
}

// StreamingFormatScores are the scores the device and identity streaming
// formats carry on every profile Ether creates or imports. Viewer's
// language is not among them: a profile scores it only when it expresses
// that preference (Ether's own profiles do; an imported pack's language
// formats are its authors' choice).
func StreamingFormatScores() map[string]int {
	return map[string]int{
		FormatSeriesYearDiffers: -10000,
		FormatVideoTranscode:    -20,
		FormatAudioDirect:       10,
		FormatAudioTranscode:    -15,
		FormatAtmosPassthrough:  5,
		FormatToneMapping:       -15,
		FormatPastRetention:     -20,
	}
}

// TVOnlyFormats are built-in formats only TV episodes can match.
var TVOnlyFormats = []string{FormatSeriesYearDiffers}

// ScoresForMedia is scores without the formats a profile's media can never
// match: a movie profile scores no TV-only format.
func ScoresForMedia(scores map[string]int, media string) map[string]int {
	if media != MediaMovie {
		return scores
	}
	out := maps.Clone(scores)
	for _, id := range TVOnlyFormats {
		delete(out, id)
	}
	return out
}

// CountsTowardMinimum reports whether a format's score counts toward a
// profile's minimum custom format score: only the owner's and imported
// formats do.
func CountsTowardMinimum(id string) bool {
	return !strings.HasPrefix(id, BuiltinFormatPrefix) && !strings.HasPrefix(id, RuleFormatPrefix) && !IsPluginRankingStep(id)
}
