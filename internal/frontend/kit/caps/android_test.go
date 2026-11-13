package caps

import (
	"context"
	"encoding/json"
	"os"
	"testing"
)

func TestAndroidRealProfiles(t *testing.T) {
	for _, tc := range []struct {
		name    string
		hdr, dv bool
	}{
		{"streamyfin-android-exoplayer", true, false},
		{"findroid-detail", false, false},
		{"jellyfin-android-exoplayer", true, false},
		{"jellyfin-android-external-mpv", false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			data, err := os.ReadFile("../../../conform/profiles/jellyfin/deviceprofiles/" + tc.name + ".json")
			if err != nil {
				t.Fatal(err)
			}
			var fixture struct {
				Profile DeviceProfile `json:"device_profile"`
			}
			if err = json.Unmarshal(data, &fixture); err != nil {
				t.Fatal(err)
			}
			c, err := (Builder{}).Build(context.Background(), DeviceInfo{}, fixture.Profile, nil, "")
			if err != nil {
				t.Fatal(err)
			}
			t.Logf("height=%d codecs=%v HDR10=%v DV=%v declaredHeight=%v", c.MaxHeight, c.VideoCodecs, c.HDR10, c.DolbyVision, c.ExplicitMaxHeight)
			if c.MaxHeight != 0 || c.HDR10 != tc.hdr || c.DolbyVision != tc.dv {
				t.Fatalf("unexpected capabilities: %+v", c)
			}
		})
	}
	t.Run("explicit UHD HDR and DV declaration", func(t *testing.T) {
		p := DeviceProfile{DirectPlayProfiles: []DirectPlayProfile{{Type: "Video", VideoCodec: "hevc", Container: "mkv"}}, CodecProfiles: []CodecProfile{{Type: "Video", Codec: "hevc", Conditions: []ProfileCondition{
			{Property: "Height", Condition: "LessThanEqual", Value: "2160"},
			{Property: "VideoRangeType", Condition: "EqualsAny", Value: "SDR|HDR10|DOVI"},
		}}}}
		c, err := (Builder{}).Build(context.Background(), DeviceInfo{}, p, nil, "")
		if err != nil || c.MaxHeight != 2160 || !c.ExplicitMaxHeight || !c.HDR10 || !c.DolbyVision || len(c.DVProfiles) == 0 {
			t.Fatalf("UHD declaration lost: %+v (%v)", c, err)
		}
	})
	t.Run("Silo Android captured capability shape", func(t *testing.T) {
		data, err := os.ReadFile("../../../conform/profiles/silo/silo-android.json")
		if err != nil {
			t.Fatal(err)
		}
		var fixture struct {
			Flows struct {
				Play []struct {
					Body struct {
						Caps struct {
							Video      []string `json:"codecs_video"`
							Audio      []string `json:"codecs_audio"`
							Containers []string `json:"containers"`
							HDR        bool     `json:"hdr"`
							Resolution string   `json:"max_resolution"`
						} `json:"client_capabilities"`
					} `json:"body"`
				} `json:"play_direct"`
			} `json:"flows"`
		}
		if err = json.Unmarshal(data, &fixture); err != nil {
			t.Fatal(err)
		}
		if len(fixture.Flows.Play) < 2 {
			t.Fatal("missing Silo playback fixture")
		}
		caps := fixture.Flows.Play[1].Body.Caps
		if caps.Resolution != "1080p" {
			t.Fatalf("fixture resolution %q", caps.Resolution)
		}
		c, err := (Builder{}).Build(context.Background(), DeviceInfo{VideoCodecs: caps.Video, AudioCodecs: caps.Audio, Containers: caps.Containers, MaxHeight: 1080, HDR10: caps.HDR}, DeviceProfile{}, nil, "")
		t.Logf("Silo height=%d codecs=%v HDR10=%v DV=%v", c.MaxHeight, c.VideoCodecs, c.HDR10, c.DolbyVision)
		if err != nil || c.MaxHeight != 1080 || c.HDR10 || c.DolbyVision {
			t.Fatalf("unexpected Silo caps: %+v (%v)", c, err)
		}
	})

}
