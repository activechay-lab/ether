package onair

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"

	"github.com/go-chi/chi/v5"
	"io"
	"net/http"
	"path"
	"regexp"
	"sort"
	"strings"
)

const packLimit = 128 << 20

var digestPattern = regexp.MustCompile(`[a-f0-9]{64}`)

type packAsset struct {
	Digest      string `json:"digest"`
	Name        string `json:"name"`
	Kind        string `json:"kind"`
	License     string `json:"license"`
	Attribution string `json:"attribution"`
	Source      string `json:"source"`
	MIME        string `json:"mime"`
}

func redistributable(license string) bool {
	switch strings.ToLower(strings.TrimSpace(license)) {
	case "ofl", "ofl-1.1", "sil open font license", "sil open font license 1.1", "apache-2.0", "mit", "cc0", "cc0-1.0", "public domain", "cc-by-4.0", "cc-by-sa-4.0":
		return true
	}
	return false
}

// ExportStylePack uses the plugin package digest and data runtime; no executable
// files, credentials, viewers or channel bindings leave this server.
func (s *Service) ExportStylePack(ctx context.Context, id string) ([]byte, []string, error) {
	l, e := s.Look(ctx, id)
	if e != nil {
		return nil, nil, e
	}
	l.ID = ""
	l.ChannelIDs = []string{}
	l.ChannelCount = 0
	l.Render = nil
	l.Pack = nil
	b, e := json.Marshal(l)
	if e != nil {
		return nil, nil, e
	}
	refs := map[string]bool{}
	for _, d := range digestPattern.FindAllString(string(b), -1) {
		refs[d] = true
	}
	files := map[string][]byte{}
	warnings := []string{}
	assets := []packAsset{}
	for digest := range refs {
		var a packAsset
		var content []byte
		a.Digest = digest
		e = s.db.QueryRowContext(ctx, "SELECT name,kind,license,mime,content FROM onair_assets WHERE digest=?", digest).Scan(&a.Name, &a.Kind, &a.License, &a.MIME, &content)
		if e != nil {
			return nil, nil, fmt.Errorf("referenced Library asset %s is unavailable: %w", digest, e)
		}
		if a.Kind == "font" && !redistributable(a.License) {
			warnings = append(warnings, fmt.Sprintf("Font %s excluded: its licence does not permit redistribution. Select a bundled font before sharing.", a.Name))
			b = bytes.ReplaceAll(b, []byte(digest), []byte("inter"))
			continue
		}
		// Source and attribution are held by the Library when available.
		_ = s.db.QueryRowContext(ctx, "SELECT source,attribution FROM onair_assets WHERE digest=?", digest).Scan(&a.Source, &a.Attribution)
		files["assets/"+digest] = content
		assets = append(assets, a)
	}
	files["looks/style.json"] = b
	files["assets.json"], _ = json.Marshal(assets)
	files["warnings.json"], _ = json.Marshal(warnings)
	manifest := map[string]any{"id": "ether.style." + id, "name": l.Name, "version": "1.0.0", "description": "Channel style pack", "ether": map[string]any{"protocol": 1, "kind": "extension", "runtime": map[string]string{"kind": "data"}, "capabilities": []string{"livetv_pack"}, "contributes": map[string]any{"looks": []map[string]string{{"id": "style", "file": "looks/style.json"}}}}}
	files["manifest.json"], _ = json.Marshal(manifest)
	files["digest"] = []byte(stylePackDigest(files))
	var buf bytes.Buffer
	z := zip.NewWriter(&buf)
	names := []string{}
	for name := range files {
		names = append(names, name)
	}
	sort.Strings(names)
	total := 0
	for _, name := range names {
		total += len(files[name])
		if len(files[name]) > 64<<20 || total > packLimit {
			return nil, nil, fmt.Errorf("style pack exceeds the 128 MB limit")
		}
		w, e := z.Create(name)
		if e != nil {
			return nil, nil, e
		}
		if _, e = w.Write(files[name]); e != nil {
			return nil, nil, e
		}
	}
	if e = z.Close(); e != nil {
		return nil, nil, e
	}
	return buf.Bytes(), warnings, nil
}
func (s *Service) ImportStylePack(ctx context.Context, raw []byte) (Look, error) {
	z, e := zip.NewReader(bytes.NewReader(raw), int64(len(raw)))
	if e != nil {
		return Look{}, e
	}
	files := map[string][]byte{}
	total := 0
	for _, f := range z.File {
		if len(files) > 512 || f.UncompressedSize64 > 64<<20 || path.Clean(f.Name) != f.Name || strings.HasPrefix(f.Name, "/") || strings.Contains(f.Name, "\\") || strings.HasPrefix(f.Name, "../") {
			return Look{}, fmt.Errorf("invalid package entry")
		}
		if _, ok := files[f.Name]; ok {
			return Look{}, fmt.Errorf("duplicate package entry")
		}
		r, e := f.Open()
		if e != nil {
			return Look{}, e
		}
		b, e := io.ReadAll(io.LimitReader(r, 64<<20+1))
		r.Close()
		total += len(b)
		if e != nil || len(b) > 64<<20 || total > packLimit {
			return Look{}, fmt.Errorf("package is too large")
		}
		files[f.Name] = b
	}
	if string(files["digest"]) != stylePackDigest(files) {
		return Look{}, fmt.Errorf("package digest does not match its contents")
	}
	var manifest struct {
		Ether struct {
			Runtime struct {
				Kind string `json:"kind"`
			} `json:"runtime"`
			Contributes struct {
				Looks []struct {
					File string `json:"file"`
				} `json:"looks"`
			} `json:"contributes"`
		} `json:"ether"`
	}
	if e = json.Unmarshal(files["manifest.json"], &manifest); e != nil || manifest.Ether.Runtime.Kind != "data" || len(manifest.Ether.Contributes.Looks) != 1 {
		return Look{}, fmt.Errorf("choose a data package containing one style")
	}
	var l Look
	if e = json.Unmarshal(files[manifest.Ether.Contributes.Looks[0].File], &l); e != nil {
		return Look{}, e
	}
	l.ID = ""
	l.Pack = nil
	l.Render = nil
	l.ChannelIDs = []string{}
	l.ChannelCount = 0
	var assets []packAsset
	if e = json.Unmarshal(files["assets.json"], &assets); e != nil {
		return Look{}, e
	}
	for _, a := range assets {
		b, ok := files["assets/"+a.Digest]
		sum := sha256.Sum256(b)
		if !ok || hex.EncodeToString(sum[:]) != a.Digest {
			return Look{}, fmt.Errorf("asset digest mismatch for %s", a.Name)
		}
		if a.Kind == "font" && !redistributable(a.License) {
			return Look{}, fmt.Errorf("font %s has no redistributable licence", a.Name)
		}
	}
	// Decode every document before any assets or records are written.
	for _, sc := range l.Scenes {
		b, _ := json.Marshal(sc)
		var checked StyleScene
		if e = json.Unmarshal(b, &checked); e != nil {
			return Look{}, e
		}
	}
	for _, a := range assets {
		if _, e = s.UploadAsset(ctx, a.Name, a.Kind, a.License, bytes.NewReader(files["assets/"+a.Digest])); e != nil {
			return Look{}, e
		}
		_, _ = s.db.ExecContext(ctx, "UPDATE onair_assets SET source=?,attribution=? WHERE digest=?", a.Source, a.Attribution, a.Digest)
	}
	return s.SaveLook(ctx, l)
}
func (s *Service) StudioRoutes(r chi.Router) {
	r.Post("/draft", func(w http.ResponseWriter, r *http.Request) {
		var v struct {
			Prompt  string `json:"prompt"`
			Style   Look   `json:"style"`
			SceneID string `json:"sceneId"`
		}
		if !body(w, r, &v) {
			return
		}
		l, e := s.GenerateScene(r.Context(), v.Prompt, "", v.Style, v.SceneID)
		result(w, map[string]any{"style": l, "summary": "Your proposed scene design is ready to preview."}, e)
	})
	r.Get("/looks/{id}/pack", func(w http.ResponseWriter, r *http.Request) {
		b, warnings, e := s.ExportStylePack(r.Context(), chi.URLParam(r, "id"))
		if e != nil {
			result(w, nil, e)
			return
		}
		w.Header().Set("Content-Type", "application/zip")
		w.Header().Set("Content-Disposition", `attachment; filename="style.etherpkg"`)
		if len(warnings) > 0 {
			w.Header().Set("X-Ether-Pack-Warnings", strings.Join(warnings, " "))
		}
		_, _ = w.Write(b)
	})
	r.Post("/packs", func(w http.ResponseWriter, r *http.Request) {
		r.Body = http.MaxBytesReader(w, r.Body, packLimit)
		if e := r.ParseMultipartForm(8 << 20); e != nil {
			result(w, nil, e)
			return
		}
		defer r.MultipartForm.RemoveAll()
		f, _, e := r.FormFile("file")
		if e != nil {
			result(w, nil, e)
			return
		}
		defer f.Close()
		b, e := io.ReadAll(io.LimitReader(f, packLimit+1))
		if e != nil {
			result(w, nil, e)
			return
		}
		l, e := s.ImportStylePack(r.Context(), b)
		result(w, l, e)
	})
}

// Same canonical file digest as plugins.PkgDigest. onair cannot import plugins:
// the plugin manager imports onair to install data contributions.
func stylePackDigest(files map[string][]byte) string {
	names := []string{}
	for n := range files {
		if n != "digest" {
			names = append(names, n)
		}
	}
	sort.Strings(names)
	lines := []string{}
	for _, n := range names {
		sum := sha256.Sum256(files[n])
		lines = append(lines, n+"="+hex.EncodeToString(sum[:]))
	}
	sum := sha256.Sum256([]byte(strings.Join(lines, "\n")))
	return hex.EncodeToString(sum[:])
}
