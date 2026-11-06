// Package xtream implements the upstream panel protocol. Errors never contain
// request URLs, provider reply bodies, or credentials.
package xtream

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"strconv"
	"strings"
)

var ErrBusy = errors.New("Xtream provider busy")

type Value string

func (v *Value) UnmarshalJSON(b []byte) error {
	b = bytes.TrimSpace(b)
	switch {
	case len(b) == 0 || string(b) == "null":
		*v = ""
	case b[0] == '"':
		var s string
		if err := json.Unmarshal(b, &s); err != nil {
			return err
		}
		*v = Value(strings.TrimSpace(s))
	case string(b) == "true":
		*v = "1"
	case string(b) == "false":
		*v = "0"
	case b[0] == '{' || b[0] == '[':
		*v = ""
	default:
		*v = Value(b)
	}
	return nil
}
func (v Value) Int() int64 { n, _ := strconv.ParseFloat(string(v), 64); return int64(n) }

type Category struct {
	ID   Value  `json:"category_id"`
	Name string `json:"category_name"`
}
type Item struct {
	ID        Value  `json:"stream_id"`
	SeriesID  Value  `json:"series_id"`
	Name      string `json:"name"`
	TMDB      Value  `json:"tmdb_id"`
	TMDBAlt   Value  `json:"tmdb"`
	Year      Value  `json:"year"`
	Extension string `json:"container_extension"`
	Modified  Value  `json:"last_modified"`
	Added     Value  `json:"added"`
}
type MediaInfo struct {
	TMDB        Value  `json:"tmdb_id"`
	TMDBAlt     Value  `json:"tmdb"`
	Name        string `json:"name"`
	ReleaseDate string `json:"releasedate"`
	Size        Value  `json:"size"`
	Video       struct {
		Codec  string `json:"codec_name"`
		Height Value  `json:"height"`
	} `json:"video"`
	Audio struct {
		Codec string `json:"codec_name"`
	} `json:"audio"`
}
type VODInfo struct {
	Info      MediaInfo `json:"info"`
	MovieData Item      `json:"movie_data"`
}
type Episode struct {
	ID        Value     `json:"id"`
	Season    Value     `json:"season"`
	Number    Value     `json:"episode_num"`
	Extension string    `json:"container_extension"`
	Info      MediaInfo `json:"info"`
}
type SeriesInfo struct {
	Info     MediaInfo            `json:"info"`
	Episodes map[string][]Episode `json:"episodes"`
}

type Client struct {
	Base, Username, Password string
	MaxBytes                 int64
	Open                     func(context.Context, string) (io.ReadCloser, error)
}

func (c Client) URL(action string, params url.Values) string {
	q := url.Values{"username": {c.Username}, "password": {c.Password}}
	if action != "" {
		q.Set("action", action)
	}
	for k, vs := range params {
		for _, v := range vs {
			q.Add(k, v)
		}
	}
	return strings.TrimRight(c.Base, "/") + "/player_api.php?" + q.Encode()
}
func (c Client) StreamURL(kind, id, ext string) (string, error) {
	if kind != "movie" && kind != "series" {
		return "", errors.New("invalid Xtream media kind")
	}
	if _, err := strconv.ParseUint(id, 10, 64); err != nil {
		return "", errors.New("invalid Xtream stream id")
	}
	switch strings.ToLower(ext) {
	case "mkv", "mp4", "m4v", "avi", "ts", "webm", "mov":
	default:
		return "", errors.New("unsupported Xtream container")
	}
	return strings.TrimRight(c.Base, "/") + "/" + kind + "/" + url.PathEscape(c.Username) + "/" + url.PathEscape(c.Password) + "/" + id + "." + ext, nil
}
func (c Client) read(ctx context.Context, action string, p url.Values, fn func(*json.Decoder) error) error {
	rc, err := c.Open(ctx, c.URL(action, p))
	if err != nil {
		if errors.Is(err, ErrBusy) {
			return ErrBusy
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return errors.New("Xtream provider request failed")
	}
	defer rc.Close()
	// Lists may exceed 64 MB; decode each entry without retaining the document.
	limit := c.MaxBytes
	if limit <= 0 {
		limit = 64 << 20
	}
	d := json.NewDecoder(io.LimitReader(rc, limit))
	if err = fn(d); err != nil {
		return fmt.Errorf("Xtream provider sent an unreadable %s reply", action)
	}
	return nil
}
func (c Client) Get(ctx context.Context, action string, p url.Values, into any) error {
	return c.read(ctx, action, p, func(d *json.Decoder) error { return d.Decode(into) })
}
func (c Client) Items(ctx context.Context, action string, visit func(Item) error) error {
	c.MaxBytes = 512 << 20
	var visitErr error
	err := c.read(ctx, action, nil, func(d *json.Decoder) error {
		t, e := d.Token()
		if e != nil {
			return e
		}
		if t != json.Delim('[') {
			return errors.New("expected list")
		}
		for d.More() {
			var v Item
			if e = d.Decode(&v); e != nil {
				return e
			}
			if e = visit(v); e != nil {
				visitErr = e
				return e
			}
		}
		_, e = d.Token()
		return e
	})
	if visitErr != nil {
		return visitErr
	}
	return err
}
