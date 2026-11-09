package mediabrowser

import (
	"encoding/json"
	"testing"
)

// Issue #404: a PlaybackInfo body whose scalar fields use the other JSON
// type (number for a string field, quoted number/boolean) must decode
// instead of 400ing the play request.
func TestPlaybackRequestFlexibleJSONTypes(t *testing.T) {
	var body playbackRequest
	input := `{"IsPlayback":"true","EnableDirectPlay":1,"EnableTranscoding":"false","StartTimeTicks":"1230000","AudioStreamIndex":"2","SubtitleStreamIndex":-1,"DeviceProfile":{"MaxStreamingBitrate":"8000000","TranscodingProfiles":[{"Type":"Video","Container":"ts","Protocol":"hls","MaxAudioChannels":2}]}}`
	if err := json.Unmarshal([]byte(input), &body); err != nil {
		t.Fatal(err)
	}
	if body.IsPlayback == nil || !*body.IsPlayback || body.EnableDirectPlay == nil || !*body.EnableDirectPlay || body.EnableTranscoding == nil || *body.EnableTranscoding {
		t.Fatal(body)
	}
	if body.StartTimeTicks != 1230000 || body.AudioStreamIndex == nil || *body.AudioStreamIndex != 2 || body.SubtitleStreamIndex == nil || *body.SubtitleStreamIndex != -1 {
		t.Fatal(body)
	}
	if body.DeviceProfile.MaxStreamingBitrate != 8000000 || len(body.DeviceProfile.TranscodingProfiles) != 1 || body.DeviceProfile.TranscodingProfiles[0].MaxAudioChannels != "2" {
		t.Fatal(body.DeviceProfile)
	}
	// Ordinary well-typed bodies keep decoding exactly as before, and
	// absent pointer fields stay absent (not zero).
	var plain playbackRequest
	if err := json.Unmarshal([]byte(`{"IsPlayback":true,"AudioStreamIndex":3}`), &plain); err != nil {
		t.Fatal(err)
	}
	if plain.IsPlayback == nil || !*plain.IsPlayback || plain.AudioStreamIndex == nil || *plain.AudioStreamIndex != 3 || plain.SubtitleStreamIndex != nil || plain.EnableDirectPlay != nil {
		t.Fatal(plain)
	}
}
