// stremio-poster-fixture writes a synthetic static addon for a coordinator-
// approved native-client check. It does not listen, install, or publish anything.
package main

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"flag"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/braedonsaunders/ether/internal/artwork"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run() error {
	out := flag.String("out", "", "new output directory; existing paths are refused")
	base := flag.String("https-base", "", "approved HTTPS URL mapped to output directory")
	id := flag.String("run-id", "", "unique lowercase letters/digits/hyphens, included in URLs")
	flag.Parse()
	if *out == "" || !regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,39}$`).MatchString(*id) {
		return fmt.Errorf("provide -out and a unique -run-id (1-40 lowercase letters/digits/hyphens)")
	}
	u, err := url.Parse(*base)
	if err != nil || u.Scheme != "https" || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return fmt.Errorf("https-base must be an approved HTTPS origin/path without credentials, query or fragment")
	}
	jpeg, contentType, err := artwork.NewRenderer(nil, "", nil).Title(context.Background(), artwork.TitlePoster{
		Key: "ether-poster-proof", Title: "ETHER POSTER PROOF", Year: 2001, Width: 200,
	})
	if err != nil {
		return err
	}
	if contentType != "image/jpeg" || len(jpeg) == 0 || len(jpeg) > 48<<10 {
		return fmt.Errorf("unexpected poster type/size: %s, %d bytes", contentType, len(jpeg))
	}
	// Mkdir (not MkdirAll) prevents overwriting an existing evidence directory.
	if err := os.Mkdir(*out, 0700); err != nil {
		return err
	}
	write := func(name string, data []byte) error {
		path := filepath.Join(*out, name)
		if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
			return err
		}
		return os.WriteFile(path, data, 0600)
	}
	writeJSON := func(name string, value any) error {
		data, err := json.MarshalIndent(value, "", "  ")
		if err != nil {
			return err
		}
		return write(name, append(data, '\n'))
	}
	imagePath := "poster-" + *id + ".jpg"
	if err := write(imagePath, jpeg); err != nil {
		return err
	}
	var metas []map[string]any
	for _, variant := range []struct{ name, poster string }{
		{"data", "data:image/jpeg;base64," + base64.StdEncoding.EncodeToString(jpeg)},
		{"https", strings.TrimRight(u.String(), "/") + "/" + imagePath},
		{"invalid", "data:image/jpeg;base64," + base64.StdEncoding.EncodeToString([]byte("not a jpeg"))},
	} {
		key := "ether-proof-" + *id + "-" + variant.name
		meta := map[string]any{"id": key, "type": "movie", "name": "Poster proof: " + variant.name,
			"poster": variant.poster, "posterShape": "poster", "description": "Synthetic image transport check; no playable content."}
		metas = append(metas, meta)
		if err := writeJSON("meta/movie/"+key+".json", map[string]any{"meta": meta}); err != nil {
			return err
		}
	}
	if err := writeJSON("catalog/movie/ether-poster-proof.json", map[string]any{"metas": metas}); err != nil {
		return err
	}
	manifest := map[string]any{"id": "org.ether.posterproof." + *id, "version": "1.0.0", "name": "Ether poster proof " + *id,
		"description": "Synthetic native poster check only", "resources": []string{"catalog", "meta"}, "types": []string{"movie"},
		"idPrefixes": []string{"ether-proof-" + *id + "-"}, "catalogs": []map[string]string{{"type": "movie", "id": "ether-poster-proof", "name": "Poster transport controls"}}}
	if err := writeJSON("manifest.json", manifest); err != nil {
		return err
	}
	return writeJSON("evidence.json", map[string]any{"run_id": *id, "jpeg_sha256": fmt.Sprintf("%x", sha256.Sum256(jpeg)),
		"jpeg_bytes": len(jpeg), "content_type": contentType, "width": 200, "height": 300,
		"native_rendering": "NOT RUN", "https_reachability": "NOT CHECKED"})
}
