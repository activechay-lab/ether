package playerlab

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strconv"
	"strings"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/frontend/kit/ids"
	"github.com/braedonsaunders/ether/internal/hlsplay"
)

type control struct {
	c                                hlsplay.Config
	t                                hlsplay.Transport
	playSession, itemID, mediaSource string
	product, scheme                  string
}

func New(product, scheme string) func(hlsplay.Config, hlsplay.Transport) hlsplay.Control {
	return func(c hlsplay.Config, t hlsplay.Transport) hlsplay.Control {
		return &control{c: c, t: t, product: product, scheme: scheme}
	}
}
func (p *control) Session() string { return p.playSession }
func (p *control) fetch(ctx context.Context, raw, kind string, index int, retry bool) ([]byte, error) {
	return p.t.Request(ctx, raw, kind, index, retry, "GET", nil)
}
func (p *control) Open(ctx context.Context) (string, []byte, error) {
	prefix := strings.TrimSuffix(p.c.Prefix, "/")
	if p.c.CredentialsFile != "" {
		data, e := os.ReadFile(p.c.CredentialsFile)
		if e != nil {
			return "", nil, fmt.Errorf("cannot read lab credentials")
		}
		var credentials struct{ Username, Password string }
		if json.Unmarshal(data, &credentials) != nil || credentials.Username == "" || credentials.Password == "" {
			return "", nil, fmt.Errorf("invalid lab credentials")
		}
		body, _ := json.Marshal(map[string]string{"Username": credentials.Username, "Pw": credentials.Password})
		data, e = p.t.Request(ctx, prefix+"/Users/AuthenticateByName", "authenticate", -1, false, "POST", body)
		if e != nil {
			return "", nil, e
		}
		var auth struct{ AccessToken string }
		if json.Unmarshal(data, &auth) != nil || auth.AccessToken == "" {
			return "", nil, fmt.Errorf("authentication returned no grant")
		}
		p.c.Token = auth.AccessToken
	}
	if _, e := p.fetch(ctx, prefix+"/Users/Me", "authenticate-viewer", -1, false); e != nil {
		return "", nil, e
	}
	key, e := strconv.ParseUint(p.c.RatingKey, 10, 64)
	if e != nil || key == 0 {
		return "", nil, fmt.Errorf("invalid rating key")
	}
	p.itemID = ids.RatingKeyGUID(core.RatingKey(key))
	// A normal Jellyfin HLS profile: closed MPEG-TS H264/AAC output, source
	// direct play disabled so PlaybackInfo admits an actual transcoding session.
	profile := map[string]any{"Name": p.product, "MaxStreamingBitrate": 1000000, "TranscodingProfiles": []any{map[string]any{"Type": "Video", "Context": "Streaming", "Protocol": "hls", "Container": "ts", "VideoCodec": "h264", "AudioCodec": "aac", "MaxAudioChannels": "2", "MinSegments": 1, "BreakOnNonKeyFrames": false}}}
	body, _ := json.Marshal(map[string]any{"IsPlayback": true, "EnableDirectPlay": false, "EnableDirectStream": false, "EnableTranscoding": true, "StartTimeTicks": 0, "DeviceProfile": profile, "MaxStreamingBitrate": 1000000})
	data, e := p.t.Request(ctx, prefix+"/Items/"+p.itemID+"/PlaybackInfo", "playback-info", -1, false, "POST", body)
	if e != nil {
		return "", nil, e
	}
	var info struct {
		PlaySessionId, ErrorCode string
		MediaSources             []struct{ Id, TranscodingUrl, TranscodingSubProtocol string }
	}
	if json.Unmarshal(data, &info) != nil || info.ErrorCode != "" || info.PlaySessionId == "" || len(info.MediaSources) == 0 {
		return "", nil, fmt.Errorf("PlaybackInfo returned no playable session")
	}
	p.playSession = info.PlaySessionId
	p.mediaSource = info.MediaSources[0].Id
	p.t.Event(map[string]any{"kind": "session", "session": p.playSession, "protocol": p.c.Protocol})
	source := info.MediaSources[0]
	if source.TranscodingUrl == "" || source.TranscodingSubProtocol != "hls" {
		return "", nil, fmt.Errorf("PlaybackInfo did not author HLS")
	}
	authored, e := resolve(p.c.BaseURL, source.TranscodingUrl)
	if e != nil {
		return "", nil, e
	}
	data, e = p.fetch(ctx, authored, "master", -1, false)
	return authored, data, e
}
func (p *control) Progress(ctx context.Context, state string, played float64, started bool) error {
	if p.playSession == "" {
		return nil
	}
	tail := "/Sessions/Playing/Progress"
	if started {
		tail = "/Sessions/Playing"
	}
	if state == "stopped" {
		tail = "/Sessions/Playing/Stopped"
	}
	body, _ := json.Marshal(map[string]any{"PlaySessionId": p.playSession, "ItemId": p.itemID, "MediaSourceId": p.mediaSource, "PositionTicks": int64(played * 10000000), "IsPaused": false, "PlayMethod": "Transcode"})
	kind := "timeline"
	if state == "stopped" {
		kind = "stop"
	}
	_, e := p.t.Request(ctx, strings.TrimSuffix(p.c.Prefix, "/")+tail, kind, -1, false, "POST", body)
	return e
}
func (p *control) Finish(ctx context.Context, played float64) {
	_ = p.Progress(ctx, "stopped", played, false)
}
func (p *control) Headers(req *http.Request) {
	req.Header.Set("X-Emby-Token", p.c.Token)
	req.Header.Set("X-Emby-Device-Id", p.c.DeviceID)
	req.Header.Set("Authorization", p.scheme+` Client="`+p.product+`", Device="Ether lab", DeviceId="`+p.c.DeviceID+`", Version="10.10.0"`)
}
func resolve(base, raw string) (string, error) { return hlsplay.Resolve(base, raw) }
