package playerlab

import (
	"context"
	"net/http"
	"net/url"
	"strconv"

	"github.com/braedonsaunders/ether/internal/hlsplay"
)

type control struct {
	c           hlsplay.Config
	t           hlsplay.Transport
	playSession string
}

func New(c hlsplay.Config, t hlsplay.Transport) hlsplay.Control { return &control{c: c, t: t} }
func (p *control) Session() string                              { return p.playSession }
func (p *control) fetch(ctx context.Context, raw, kind string, index int, retry bool) ([]byte, error) {
	return p.t.Request(ctx, raw, kind, index, retry, "GET", nil)
}
func (p *control) Open(ctx context.Context) (string, []byte, error) {
	c := p.c
	p.playSession = c.Session
	q := url.Values{"path": {"/library/metadata/" + c.RatingKey}, "session": {c.Session}, "protocol": {"hls"}, "directPlay": {"0"}, "directStream": {"0"}, "videoCodec": {"h264"}, "audioCodec": {"aac"}, "mediaIndex": {"0"}, "partIndex": {"0"}, "offset": {"0"}}
	if c.DirectPlay {
		q.Set("directPlay", "1")
	}
	if c.DirectStream {
		q.Set("directStream", "1")
	}
	if c.MaxVideoBitrate > 0 {
		q.Set("maxVideoBitrate", strconv.Itoa(c.MaxVideoBitrate))
	}
	if c.VideoResolution != "" {
		q.Set("videoResolution", c.VideoResolution)
	}
	control := "/video/:/transcode/universal/"
	if !c.SkipDecision {
		if _, e := p.fetch(ctx, control+"decision?"+q.Encode(), "decision", -1, false); e != nil {
			return "", nil, e
		}
	}
	path := control + "start.m3u8?" + q.Encode()
	data, e := p.fetch(ctx, path, "master", -1, false)
	u, _ := resolve(c.BaseURL, path)
	return u, data, e
}
func (p *control) Progress(ctx context.Context, state string, played float64, started bool) error {
	q := url.Values{"ratingKey": {p.c.RatingKey}, "key": {"/library/metadata/" + p.c.RatingKey}, "state": {state}, "time": {strconv.FormatInt(int64(played*1000), 10)}, "duration": {"0"}, "session": {p.c.Session}}
	_, e := p.fetch(ctx, "/:/timeline?"+q.Encode(), "timeline", -1, false)
	return e
}
func (p *control) Finish(ctx context.Context, played float64) {
	_, _ = p.fetch(ctx, "/video/:/transcode/universal/stop?session="+url.QueryEscape(p.c.Session), "stop", -1, false)
}
func (p *control) Headers(req *http.Request) {
	req.Header.Set("X-Plex-Token", p.c.Token)
	req.Header.Set("X-Plex-Client-Identifier", p.c.Session)
	req.Header.Set("X-Plex-Session-Identifier", p.c.Session)
	req.Header.Set("X-Plex-Product", "Plex Web")
	req.Header.Set("X-Plex-Platform", "Chrome")
}
func resolve(base, raw string) (string, error) { return hlsplay.Resolve(base, raw) }
