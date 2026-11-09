package onair

import (
	"bytes"
	"embed"
	"image"
	"image/png"
	"net/http"
	"strings"
	"sync"

	"github.com/braedonsaunders/ether/internal/onair/raster"
)

// Each shipped decade look has a compact corner bug mark in its era style,
// generated reproducibly by TestRenderShowcaseBugs and decoded once for the
// lifetime of the process so playout never decodes or resamples per frame.
//
//go:embed showcasebugs/*.png
var showcaseBugFiles embed.FS

const showcaseBugRoute = "/ether/api/onair/showcase-bugs/"

var showcaseBugNames = map[string]string{
	"showcase-80": "chroma-80.png",
	"showcase-90": "riot-90.png",
	"showcase-00": "orbit-00.png",
	"showcase-10": "shift-10.png",
}

type decodedShowcaseBugs struct {
	original   map[string]image.Image
	silhouette map[string]image.Image
}

var showcaseBugs = sync.OnceValue(func() decodedShowcaseBugs {
	out := decodedShowcaseBugs{
		original:   make(map[string]image.Image, len(showcaseBugNames)),
		silhouette: make(map[string]image.Image, len(showcaseBugNames)),
	}
	for _, file := range showcaseBugNames {
		data, err := showcaseBugFiles.ReadFile("showcasebugs/" + file)
		if err != nil {
			continue
		}
		img, err := png.Decode(bytes.NewReader(data))
		if err != nil {
			continue
		}
		src := showcaseBugRoute + file
		out.original[src] = img
		out.silhouette[src] = raster.Silhouette(img)
	}
	return out
})

func showcaseBugSrc(lookID string) string {
	file, ok := showcaseBugNames[lookID]
	if !ok {
		return ""
	}
	return showcaseBugRoute + file
}

func showcaseBugImage(src string) image.Image {
	if !strings.HasPrefix(src, showcaseBugRoute) {
		return nil
	}
	return showcaseBugs().original[src]
}

func showcaseBugSilhouette(src string) image.Image {
	if !strings.HasPrefix(src, showcaseBugRoute) {
		return nil
	}
	return showcaseBugs().silhouette[src]
}

func serveShowcaseBug(w http.ResponseWriter, r *http.Request, file string) {
	if strings.Contains(file, "/") || !strings.HasSuffix(file, ".png") {
		http.NotFound(w, r)
		return
	}
	data, err := showcaseBugFiles.ReadFile("showcasebugs/" + file)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "image/png")
	w.Header().Set("Cache-Control", "public, max-age=86400")
	_, _ = w.Write(data)
}
