package caps

import (
	"bytes"
	"encoding/json"
	"strconv"
	"strings"
)

// The Flex types decode a JSON scalar given as either of the types real
// Jellyfin/Emby clients use for it: a number where the schema says string
// (Litefin sends MaxAudioChannels as 6), or a quoted number/boolean where
// the schema says number/bool. A value of neither shape decodes as the
// zero value instead of an error, so one odd field never fails a whole
// DeviceProfile or PlaybackInfo request (issue #404).

// FlexString accepts a JSON string, number or bool.
type FlexString string

func (f *FlexString) UnmarshalJSON(data []byte) error {
	data = bytes.TrimSpace(data)
	if len(data) == 0 || string(data) == "null" {
		*f = ""
		return nil
	}
	switch data[0] {
	case '"':
		var s string
		if json.Unmarshal(data, &s) == nil {
			*f = FlexString(s)
			return nil
		}
	case 't', 'f':
		var b bool
		if json.Unmarshal(data, &b) == nil {
			*f = FlexString(strconv.FormatBool(b))
			return nil
		}
	default:
		if n, err := strconv.ParseFloat(string(data), 64); err == nil {
			if n == float64(int64(n)) {
				*f = FlexString(strconv.FormatInt(int64(n), 10))
			} else {
				*f = FlexString(string(data))
			}
			return nil
		}
	}
	*f = ""
	return nil
}

func (f FlexString) MarshalJSON() ([]byte, error) { return json.Marshal(string(f)) }

// FlexInt accepts a JSON number or a numeric string.
type FlexInt int

func parseFlexInt64(data []byte) int64 {
	data = bytes.TrimSpace(data)
	if len(data) == 0 || string(data) == "null" {
		return 0
	}
	text := string(data)
	if data[0] == '"' {
		var s string
		if json.Unmarshal(data, &s) != nil {
			return 0
		}
		text = strings.TrimSpace(s)
	}
	if n, err := strconv.ParseInt(text, 10, 64); err == nil {
		return n
	}
	if n, err := strconv.ParseFloat(text, 64); err == nil {
		return int64(n)
	}
	return 0
}

func (f *FlexInt) UnmarshalJSON(data []byte) error {
	*f = FlexInt(parseFlexInt64(data))
	return nil
}

func (f FlexInt) MarshalJSON() ([]byte, error) { return json.Marshal(int(f)) }

// FlexInt64 accepts a JSON number or a numeric string.
type FlexInt64 int64

func (f *FlexInt64) UnmarshalJSON(data []byte) error {
	*f = FlexInt64(parseFlexInt64(data))
	return nil
}

func (f FlexInt64) MarshalJSON() ([]byte, error) { return json.Marshal(int64(f)) }

// FlexBool accepts a JSON bool, a "true"/"false"/"1"/"0" string, or a
// number (non-zero is true).
type FlexBool bool

func (f *FlexBool) UnmarshalJSON(data []byte) error {
	data = bytes.TrimSpace(data)
	*f = false
	if len(data) == 0 || string(data) == "null" {
		return nil
	}
	switch data[0] {
	case 't', 'f':
		var b bool
		if json.Unmarshal(data, &b) == nil {
			*f = FlexBool(b)
		}
	case '"':
		var s string
		if json.Unmarshal(data, &s) == nil {
			if b, err := strconv.ParseBool(strings.TrimSpace(s)); err == nil {
				*f = FlexBool(b)
			}
		}
	default:
		if n, err := strconv.ParseFloat(string(data), 64); err == nil {
			*f = FlexBool(n != 0)
		}
	}
	return nil
}

func (f FlexBool) MarshalJSON() ([]byte, error) { return json.Marshal(bool(f)) }

// The profile structs keep their plain Go types for every other caller;
// only JSON decoding is flexible. Every scalar field goes through a Flex
// type so a client quoting (or unquoting) any one of them still decodes.

func (p *DirectPlayProfile) UnmarshalJSON(data []byte) error {
	var raw struct {
		Container  FlexString `json:"Container"`
		VideoCodec FlexString `json:"VideoCodec"`
		AudioCodec FlexString `json:"AudioCodec"`
		Type       FlexString `json:"Type"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	*p = DirectPlayProfile{Container: string(raw.Container), VideoCodec: string(raw.VideoCodec), AudioCodec: string(raw.AudioCodec), Type: string(raw.Type)}
	return nil
}

func (c *ProfileCondition) UnmarshalJSON(data []byte) error {
	var raw struct {
		Condition  FlexString `json:"Condition"`
		Property   FlexString `json:"Property"`
		Value      FlexString `json:"Value"`
		IsRequired *FlexBool  `json:"IsRequired"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	*c = ProfileCondition{Condition: string(raw.Condition), Property: string(raw.Property), Value: string(raw.Value), IsRequired: flexBoolPtr(raw.IsRequired)}
	return nil
}

func (p *TranscodingProfile) UnmarshalJSON(data []byte) error {
	var raw struct {
		Container                 FlexString         `json:"Container"`
		Type                      FlexString         `json:"Type"`
		VideoCodec                FlexString         `json:"VideoCodec"`
		AudioCodec                FlexString         `json:"AudioCodec"`
		Protocol                  FlexString         `json:"Protocol"`
		Context                   FlexString         `json:"Context"`
		EstimateContentLength     FlexBool           `json:"EstimateContentLength"`
		EnableMpegtsM2TsMode      FlexBool           `json:"EnableMpegtsM2TsMode"`
		CopyTimestamps            FlexBool           `json:"CopyTimestamps"`
		EnableSubtitlesInManifest FlexBool           `json:"EnableSubtitlesInManifest"`
		TranscodeSeekInfo         FlexString         `json:"TranscodeSeekInfo"`
		MaxAudioChannels          FlexString         `json:"MaxAudioChannels"`
		MinSegments               FlexInt            `json:"MinSegments"`
		SegmentLength             FlexInt            `json:"SegmentLength"`
		BreakOnNonKeyFrames       *FlexBool          `json:"BreakOnNonKeyFrames"`
		EnableAudioVbrEncoding    *FlexBool          `json:"EnableAudioVbrEncoding"`
		Conditions                []ProfileCondition `json:"Conditions"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	*p = TranscodingProfile{
		Container: string(raw.Container), Type: string(raw.Type), VideoCodec: string(raw.VideoCodec),
		AudioCodec: string(raw.AudioCodec), Protocol: string(raw.Protocol), Context: string(raw.Context),
		EstimateContentLength: bool(raw.EstimateContentLength), EnableMpegtsM2TsMode: bool(raw.EnableMpegtsM2TsMode),
		CopyTimestamps: bool(raw.CopyTimestamps), EnableSubtitlesInManifest: bool(raw.EnableSubtitlesInManifest),
		TranscodeSeekInfo: string(raw.TranscodeSeekInfo), MaxAudioChannels: string(raw.MaxAudioChannels),
		MinSegments: int(raw.MinSegments), SegmentLength: int(raw.SegmentLength),
		BreakOnNonKeyFrames: flexBoolPtr(raw.BreakOnNonKeyFrames), EnableAudioVbrEncoding: flexBoolPtr(raw.EnableAudioVbrEncoding),
		Conditions: raw.Conditions,
	}
	return nil
}

func (p *SubtitleProfile) UnmarshalJSON(data []byte) error {
	var raw struct {
		Format    FlexString `json:"Format"`
		Method    FlexString `json:"Method"`
		DidlMode  FlexString `json:"DidlMode"`
		Language  FlexString `json:"Language"`
		Container FlexString `json:"Container"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	*p = SubtitleProfile{Format: string(raw.Format), Method: string(raw.Method), DidlMode: string(raw.DidlMode), Language: string(raw.Language), Container: string(raw.Container)}
	return nil
}

func (p *ContainerProfile) UnmarshalJSON(data []byte) error {
	var raw struct {
		Type         FlexString         `json:"Type"`
		Container    FlexString         `json:"Container"`
		SubContainer FlexString         `json:"SubContainer"`
		Conditions   []ProfileCondition `json:"Conditions"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	*p = ContainerProfile{Type: string(raw.Type), Container: string(raw.Container), SubContainer: string(raw.SubContainer), Conditions: raw.Conditions}
	return nil
}

func (p *CodecProfile) UnmarshalJSON(data []byte) error {
	var raw struct {
		Type            FlexString         `json:"Type"`
		Codec           FlexString         `json:"Codec"`
		Container       FlexString         `json:"Container"`
		SubContainer    FlexString         `json:"SubContainer"`
		Conditions      []ProfileCondition `json:"Conditions"`
		ApplyConditions []ProfileCondition `json:"ApplyConditions"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	*p = CodecProfile{Type: string(raw.Type), Codec: string(raw.Codec), Container: string(raw.Container), SubContainer: string(raw.SubContainer), Conditions: raw.Conditions, ApplyConditions: raw.ApplyConditions}
	return nil
}

func (p *DeviceProfile) UnmarshalJSON(data []byte) error {
	var raw struct {
		DirectPlayProfiles                        []DirectPlayProfile  `json:"DirectPlayProfiles"`
		TranscodingProfiles                       []TranscodingProfile `json:"TranscodingProfiles"`
		SubtitleProfiles                          []SubtitleProfile    `json:"SubtitleProfiles"`
		ContainerProfiles                         []ContainerProfile   `json:"ContainerProfiles"`
		CodecProfiles                             []CodecProfile       `json:"CodecProfiles"`
		MaxStreamingBitrate                       FlexInt              `json:"MaxStreamingBitrate"`
		AlwaysUseMostCompatibleTranscodingProfile FlexBool             `json:"AlwaysUseMostCompatibleTranscodingProfile"`
		MaxStaticBitrate                          FlexInt              `json:"MaxStaticBitrate"`
		MusicStreamingTranscodingBitrate          FlexInt              `json:"MusicStreamingTranscodingBitrate"`
		MaxStaticMusicBitrate                     FlexInt              `json:"MaxStaticMusicBitrate"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	*p = DeviceProfile{
		DirectPlayProfiles:                        raw.DirectPlayProfiles,
		TranscodingProfiles:                       raw.TranscodingProfiles,
		SubtitleProfiles:                          raw.SubtitleProfiles,
		ContainerProfiles:                         raw.ContainerProfiles,
		CodecProfiles:                             raw.CodecProfiles,
		MaxStreamingBitrate:                       int(raw.MaxStreamingBitrate),
		AlwaysUseMostCompatibleTranscodingProfile: bool(raw.AlwaysUseMostCompatibleTranscodingProfile),
		MaxStaticBitrate:                          int(raw.MaxStaticBitrate),
		MusicStreamingTranscodingBitrate:          int(raw.MusicStreamingTranscodingBitrate),
		MaxStaticMusicBitrate:                     int(raw.MaxStaticMusicBitrate),
	}
	return nil
}

func flexBoolPtr(p *FlexBool) *bool {
	if p == nil {
		return nil
	}
	b := bool(*p)
	return &b
}
