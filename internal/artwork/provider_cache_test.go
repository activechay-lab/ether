package artwork

import (
	"bytes"
	"context"
	"encoding/json"
	"image"
	"image/jpeg"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/config"
	"github.com/braedonsaunders/ether/internal/core"
)

// Durable original identity and validators must be independent of client sizes,
// and local derivatives must change when a revalidated original changes.
func TestProviderOriginalLifecycle(t *testing.T) {
	var hits atomic.Int32
	var version atomic.Int32
	version.Store(1)
	var first, second bytes.Buffer
	jpeg.Encode(&first, image.NewRGBA(image.Rect(0, 0, 400, 600)), nil)
	light := image.NewGray(image.Rect(0, 0, 400, 600))
	for i := range light.Pix {
		light.Pix[i] = 200
	}
	jpeg.Encode(&second, light, nil)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		tag := "\"" + string(rune('0'+version.Load())) + "\""
		w.Header().Set("ETag", tag)
		w.Header().Set("Last-Modified", "Mon, 02 Jan 2006 15:04:05 GMT")
		if r.Header.Get("If-None-Match") == tag {
			if r.Header.Get("If-Modified-Since") == "" {
				t.Error("missing Last-Modified validator")
			}
			w.WriteHeader(304)
			return
		}
		if version.Load() == 1 {
			w.Write(first.Bytes())
		} else {
			w.Write(second.Bytes())
		}
	}))
	defer srv.Close()
	opts := ImagesOptions{Dir: t.TempDir(), Posters: func() config.Posters { return config.Posters{Enabled: true, Source: "xrdb", BaseURL: srv.URL} }}
	im := NewImages(opts)
	title := &core.Title{Type: core.TypeMovie, IDs: core.IDs{TMDB: 1}}
	get := func(width int) []byte {
		t.Helper()
		data, _, err := im.GetPoster(context.Background(), title, "/fallback.jpg", width)
		if err != nil {
			t.Fatal(err)
		}
		cfg, _, err := image.DecodeConfig(bytes.NewReader(data))
		if err != nil || cfg.Width != width {
			t.Fatalf("size %d: %v %v", width, cfg, err)
		}
		return data
	}
	before := get(200)
	get(300)
	im = NewImages(opts)
	get(400)
	if hits.Load() != 1 {
		t.Fatal("refetched original for size or restart", hits.Load())
	}
	raw, _ := providerURL(opts.Posters().Selected(), title)
	expire := func() {
		original, err := im.providerOriginal(context.Background(), raw)
		if err != nil {
			t.Fatal(err)
		}
		original.Expires = time.Now().Add(-time.Second)
		data, _ := json.Marshal(original)
		// Find the original's stable disk key without depending on file layout.
		sum := providerCacheKey(raw)
		im.writeDisk(sum, data)
		im = NewImages(opts)
	}
	expire()
	get(200)
	if hits.Load() != 2 {
		t.Fatal("missing conditional revalidation", hits.Load())
	}
	get(200)
	if hits.Load() != 2 {
		t.Fatal("304 did not renew freshness")
	}
	version.Store(2)
	expire()
	if bytes.Equal(before, get(200)) {
		t.Fatal("changed original reused stale local resize")
	}
	if hits.Load() != 3 {
		t.Fatal("missing changed upstream revalidation")
	}
}

func TestProviderFreshnessPolicy(t *testing.T) {
	var data bytes.Buffer
	jpeg.Encode(&data, image.NewRGBA(image.Rect(0, 0, 200, 300)), nil)
	for _, policy := range []struct {
		control      string
		fresh, store bool
	}{
		{"max-age=60", true, true},
		{"max-age=999999", true, true},
		{"no-cache, max-age=60", false, true},
		{"max-age=0", false, true},
		{"no-store", true, false},
	} {
		t.Run(policy.control, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Cache-Control", policy.control)
				w.Write(data.Bytes())
			}))
			defer srv.Close()
			im := NewImages(ImagesOptions{Dir: t.TempDir()})
			img, err := im.providerOriginal(context.Background(), srv.URL)
			if err != nil {
				t.Fatal(err)
			}
			if time.Now().Before(img.Expires) != policy.fresh || img.NoStore == policy.store {
				t.Fatalf("bad policy: %+v", img)
			}
			if time.Until(img.Expires) > 6*time.Hour {
				t.Fatal("upstream extended refresh beyond six hours")
			}
			_, _, stored := im.readDisk(providerCacheKey(srv.URL))
			if stored != policy.store {
				t.Fatal("wrong persistence", stored)
			}
		})
	}
}
