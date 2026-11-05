package artwork

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"image"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"
)

// Originals have a stable key across client widths and clock buckets. Provider
// scores/styles are revalidated at least every six hours; owner URL/profile/key
// edits select a new key immediately. Viewer ratings do not alter provider art.
type providerImage struct {
	Data               []byte
	ETag, LastModified string
	CacheControl       string
	Expires            time.Time
	NoStore            bool
}

func (im *Images) providerOriginal(ctx context.Context, raw string) (*providerImage, error) {
	key := providerCacheKey(raw)
	read := func() *providerImage {
		im.mu.Lock()
		cached, ok := im.items.Get(key)
		im.mu.Unlock()
		var data []byte
		if ok {
			data = cached.data
		} else {
			data, _, _ = im.readDisk(key)
		}
		var img providerImage
		if json.Unmarshal(data, &img) != nil || len(img.Data) == 0 {
			return nil
		}
		if !ok {
			im.mu.Lock()
			im.addLocked(&memImage{key: key, data: data})
			im.mu.Unlock()
		}
		return &img
	}
	if img := read(); img != nil && time.Now().Before(img.Expires) {
		return img, nil
	}
	// Use the existing detached flight ownership: one cancelled client cannot
	// cancel another client's render, and different local sizes share this flight.
	result, err := im.inflight.DoDetached(ctx, key, func(ctx context.Context) (*memImage, error) {
		old := read()
		if old != nil && time.Now().Before(old.Expires) {
			data, _ := json.Marshal(old)
			return &memImage{key: key, data: data}, nil
		}
		img, err := im.fetchProviderOriginal(ctx, raw, old)
		if err != nil {
			return nil, err
		}
		data, err := json.Marshal(img)
		if err != nil {
			return nil, err
		}
		result := &memImage{key: key, data: data}
		if img.NoStore {
			im.mu.Lock()
			im.items.Remove(key)
			im.mu.Unlock()
			if im.o.Dir != "" {
				im.diskMu.Lock()
				if info, err := os.Stat(im.diskPath(key)); err == nil && os.Remove(im.diskPath(key)) == nil && im.diskKnown {
					im.diskUsed -= info.Size()
				}
				im.diskMu.Unlock()
			}
		} else {
			im.writeDisk(key, data)
			im.mu.Lock()
			im.addLocked(result)
			im.mu.Unlock()
		}
		return result, nil
	})
	if err != nil {
		return nil, err
	}
	var img providerImage
	if json.Unmarshal(result.data, &img) != nil {
		return nil, errProvider
	}
	return &img, nil
}

func (im *Images) fetchProviderOriginal(ctx context.Context, raw string, old *providerImage) (*providerImage, error) {
	ctx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	select {
	case im.posterSlots <- struct{}{}:
		defer func() { <-im.posterSlots }()
	case <-ctx.Done():
		return nil, errProvider
	}
	client := *im.o.HTTPClient
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, raw, nil)
	if err != nil {
		return nil, errProvider
	}
	if old != nil {
		if old.ETag != "" {
			req.Header.Set("If-None-Match", old.ETag)
		}
		if old.LastModified != "" {
			req.Header.Set("If-Modified-Since", old.LastModified)
		}
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, errProvider
	}
	defer resp.Body.Close()
	img := &providerImage{}
	if resp.StatusCode == http.StatusNotModified && old != nil {
		*img = *old
	} else if resp.StatusCode == http.StatusOK {
		img.Data, err = io.ReadAll(io.LimitReader(resp.Body, (8<<20)+1))
		if err != nil || len(img.Data) > 8<<20 {
			return nil, errProvider
		}
		cfg, _, err := image.DecodeConfig(bytes.NewReader(img.Data))
		if err != nil || cfg.Width <= 0 || cfg.Height <= 0 || cfg.Width > 8192 || cfg.Height > 8192 || int64(cfg.Width)*int64(cfg.Height) > 16000000 {
			return nil, errProvider
		}
		// Validate the full raster before persisting an original.
		if _, _, err := image.Decode(bytes.NewReader(img.Data)); err != nil {
			return nil, errProvider
		}
	} else {
		return nil, errProvider
	}
	if v := resp.Header.Get("ETag"); v != "" || resp.StatusCode == 200 {
		img.ETag = v
	}
	if v := resp.Header.Get("Last-Modified"); v != "" || resp.StatusCode == 200 {
		img.LastModified = v
	}
	if v := resp.Header.Get("Cache-Control"); v != "" || resp.StatusCode == 200 {
		img.CacheControl = v
	}
	ttl := 6 * time.Hour
	for _, directive := range strings.Split(img.CacheControl, ",") {
		name, value, _ := strings.Cut(strings.TrimSpace(directive), "=")
		switch strings.ToLower(name) {
		case "no-store":
			img.NoStore = true
		case "no-cache":
			ttl = 0
		case "max-age":
			if n, err := strconv.ParseInt(strings.Trim(value, "\""), 10, 64); err == nil && n >= 0 && n < int64(ttl/time.Second) {
				ttl = time.Duration(n) * time.Second
			}
		}
	}
	if age, err := strconv.ParseInt(resp.Header.Get("Age"), 10, 64); err == nil && age > 0 {
		ttl -= time.Duration(min(age, int64(6*time.Hour/time.Second))) * time.Second
	}
	img.Expires = time.Now().Add(max(0, ttl))
	return img, nil
}

func providerCacheKey(raw string) string {
	sum := sha256.Sum256([]byte(raw))
	return "provider-original-v2/" + hex.EncodeToString(sum[:])
}
