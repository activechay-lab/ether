// Package kit implements Ether's plugin conformance kit and copy linter
// (SPEC §5.1–§5.6, WS11g), shared across install previews, Studio, the
// `ether plugin` CLI, and catalogue CI.
package kit

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/braedonsaunders/ether/internal/plugins"
	"github.com/braedonsaunders/ether/internal/plugins/wasm"
	"github.com/evanw/esbuild/pkg/api"
)

// Package holds an unpacked or directory-loaded plugin ready for checking
// or packing.
type Package struct {
	ManifestRaw json.RawMessage
	Manifest    plugins.Manifest
	Files       map[string][]byte
	Dir         string
}

// Issue is one error or warning found by the conformance kit.
type Issue struct {
	Rule    string `json:"rule"`
	Field   string `json:"field,omitempty"`
	Message string `json:"message"`
}

func (i Issue) String() string {
	if i.Field != "" {
		return fmt.Sprintf("[%s] %s: %s", i.Rule, i.Field, i.Message)
	}
	return fmt.Sprintf("[%s] %s", i.Rule, i.Message)
}

// RouteTiming records the outcome and duration of one route check.
type RouteTiming struct {
	Method     string        `json:"method"`
	Route      string        `json:"route"`
	Status     int           `json:"status"`
	DurationMS float64       `json:"durationMs"`
	DeadlineMS int64         `json:"deadlineMs"`
	OK         bool          `json:"ok"`
	Skipped    bool          `json:"skipped,omitempty"`
	Error      string        `json:"error,omitempty"`
	Duration   time.Duration `json:"-"`
}

// HotTiming records the outcome and timing of the 10k-candidate hot-path check.
type HotTiming struct {
	Candidates    int     `json:"candidates"`
	Batches       int     `json:"batches"`
	TotalMS       float64 `json:"totalMs"`
	MaxBatchMS    float64 `json:"maxBatchMs"`
	PerCandP95MS  float64 `json:"perCandidateP95Ms"`
	SlowCount     int     `json:"slowCandidates"`
	Deterministic bool    `json:"deterministic"`
}

// Report is the structured result of Check.
type Report struct {
	PluginID      string          `json:"pluginId,omitempty"`
	Version       string          `json:"version,omitempty"`
	Kind          string          `json:"kind,omitempty"`
	OK            bool            `json:"ok"`
	Exports       map[string]bool `json:"exports,omitempty"`
	Errors        []Issue         `json:"errors,omitempty"`
	Warnings      []Issue         `json:"warnings,omitempty"`
	Routes        []RouteTiming   `json:"routes,omitempty"`
	HotPath       *HotTiming      `json:"hotPath,omitempty"`
	DeclaredHosts []string        `json:"declaredHosts,omitempty"`
	ObservedHosts []string        `json:"observedHosts,omitempty"`
	DurationMS    float64         `json:"durationMs"`
}

func (r *Report) addErr(rule, field, msg string) {
	r.Errors = append(r.Errors, Issue{Rule: rule, Field: field, Message: msg})
	r.OK = false
}

func (r *Report) addWarn(rule, field, msg string) {
	r.Warnings = append(r.Warnings, Issue{Rule: rule, Field: field, Message: msg})
}

// JSON returns the report as indented JSON bytes.
func (r Report) JSON() ([]byte, error) {
	return json.MarshalIndent(r, "", "  ")
}

// Text renders the report in plain human-readable text.
func (r Report) Text() string {
	var b strings.Builder
	title := r.PluginID
	if title == "" {
		title = "(unidentified plugin)"
	}
	if r.Version != "" {
		title += " " + r.Version
	}
	if r.Kind != "" {
		title += " (" + r.Kind + ")"
	}
	status := "PASS"
	if !r.OK {
		status = "FAIL"
	}
	fmt.Fprintf(&b, "%s: %s (%.1fms)\n", status, title, r.DurationMS)
	for _, e := range r.Errors {
		fmt.Fprintf(&b, "  error: %s\n", e.String())
	}
	for _, w := range r.Warnings {
		fmt.Fprintf(&b, "  warn:  %s\n", w.String())
	}
	if len(r.Routes) > 0 {
		fmt.Fprintf(&b, "  routes:\n")
		for _, rt := range r.Routes {
			mark := "ok"
			if !rt.OK {
				mark = "fail"
			}
			if rt.Skipped {
				mark = "live test needed"
			}
			fmt.Fprintf(&b, "    %s %s %s -> %d (%.2fms / %dms)\n", mark, rt.Method, rt.Route, rt.Status, rt.DurationMS, rt.DeadlineMS)
		}
	}
	if r.HotPath != nil {
		fmt.Fprintf(&b, "  hot path: %d candidates in %d batches (%.1fms total, max batch %.2fms, slow %d, deterministic=%t)\n",
			r.HotPath.Candidates, r.HotPath.Batches, r.HotPath.TotalMS, r.HotPath.MaxBatchMS, r.HotPath.SlowCount, r.HotPath.Deterministic)
	}
	return b.String()
}

// Load reads a plugin from a directory path or an .etherpkg file path.
func Load(target string) (*Package, error) {
	info, err := os.Stat(target)
	if err != nil {
		return nil, err
	}
	if info.IsDir() {
		return LoadDir(target)
	}
	b, err := os.ReadFile(target)
	if err != nil {
		return nil, err
	}
	return LoadPkgBytes(b)
}

// LoadDir loads a plugin package from a filesystem directory.
func LoadDir(dir string) (*Package, error) {
	files := map[string][]byte{}
	err := filepath.WalkDir(dir, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(dir, p)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		if rel == "." {
			return nil
		}
		if d.IsDir() {
			if d.Name() == ".git" || d.Name() == "node_modules" {
				return filepath.SkipDir
			}
			return nil
		}
		if strings.HasSuffix(rel, ".etherpkg") {
			return nil
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		files[rel] = b
		return nil
	})
	if err != nil {
		return nil, err
	}
	manRaw, ok := files["manifest.json"]
	if !ok {
		return nil, fmt.Errorf("kit: directory %s has no manifest.json", dir)
	}
	man, err := plugins.ParseManifest(manRaw)
	if err != nil {
		return &Package{ManifestRaw: manRaw, Files: files, Dir: dir}, err
	}
	pkg := &Package{ManifestRaw: manRaw, Manifest: man, Files: files, Dir: dir}
	if man.Ether != nil && man.Ether.Runtime.Kind == "hosted" && isTSPath(man.Ether.Runtime.Entry) {
		entry := man.Ether.Runtime.Entry
		if isUnsafePath(entry) {
			return nil, fmt.Errorf("kit: unsafe entry %q", entry)
		}
		result := api.Build(api.BuildOptions{AbsWorkingDir: mustAbs(dir), EntryPoints: []string{entry}, Bundle: true, Write: false, Format: api.FormatIIFE, GlobalName: "__plugin", Platform: api.PlatformNeutral, Target: api.ES2020, LogLevel: api.LogLevelSilent})
		if len(result.Errors) != 0 {
			return nil, fmt.Errorf("kit: bundle: %s", result.Errors[0].Text)
		}
		manRaw, err = withEntry(manRaw, bundleEntry)
		if err != nil {
			return nil, err
		}
		man, err = plugins.ParseManifest(manRaw)
		if err != nil {
			return nil, err
		}
		files[bundleEntry] = result.OutputFiles[0].Contents
		files["manifest.json"] = manRaw
		pkg.ManifestRaw, pkg.Manifest = manRaw, man
	}
	return pkg, nil
}

func mustAbs(dir string) string { p, _ := filepath.Abs(dir); return p }

// LoadPkgBytes loads a plugin package from .etherpkg zip bytes, verifying
// the digest entry when present.
func LoadPkgBytes(raw []byte) (*Package, error) {
	zr, err := zip.NewReader(bytes.NewReader(raw), int64(len(raw)))
	if err != nil {
		return nil, fmt.Errorf("kit: open package: %w", err)
	}
	files := map[string][]byte{}
	var total int64
	for _, f := range zr.File {
		name := strings.TrimPrefix(filepath.ToSlash(f.Name), "./")
		if f.FileInfo().IsDir() || isUnsafePath(name) || strings.HasPrefix(name, "__MACOSX") {
			continue
		}
		rc, err := f.Open()
		if err != nil {
			return nil, err
		}
		b, err := io.ReadAll(io.LimitReader(rc, 64<<20))
		_ = rc.Close()
		if err != nil {
			return nil, err
		}
		total += int64(len(b))
		if total > 128<<20 {
			return nil, fmt.Errorf("kit: package exceeds 128 MiB limit")
		}
		files[name] = b
	}
	manRaw, ok := files["manifest.json"]
	if !ok {
		return nil, fmt.Errorf("kit: package has no manifest.json")
	}
	if d, ok := files["digest"]; ok {
		want := plugins.PkgDigest(files)
		if strings.TrimSpace(string(d)) != want {
			return nil, fmt.Errorf("kit: package digest mismatch")
		}
	}
	man, err := plugins.ParseManifest(manRaw)
	if err != nil {
		return &Package{ManifestRaw: manRaw, Files: files}, err
	}
	return &Package{ManifestRaw: manRaw, Manifest: man, Files: files}, nil
}

// Pack compiles any TypeScript entry via wasm.Compile, bundles manifest,
// compiled JS/WASM, assets, schema, strings, and writes the verified
// `digest` entry into an installable .etherpkg archive.
func Pack(pkg *Package) ([]byte, error) {
	if pkg == nil || len(pkg.ManifestRaw) == 0 {
		return nil, fmt.Errorf("kit: pack: empty package")
	}
	man, err := plugins.ParseManifest(pkg.ManifestRaw)
	if err != nil {
		return nil, err
	}
	if err := man.Validate(); err != nil {
		return nil, err
	}
	outFiles := map[string][]byte{}
	for k, v := range pkg.Files {
		if k == "digest" || strings.HasPrefix(k, "tests/") || strings.HasSuffix(k, ".test.js") || strings.HasSuffix(k, ".test.ts") {
			continue
		}
		outFiles[k] = v
	}
	outFiles["manifest.json"] = pkg.ManifestRaw

	if man.Ether != nil && man.Ether.Runtime.Kind == "hosted" && man.Ether.Runtime.Entry != "" {
		entry := man.Ether.Runtime.Entry
		if _, hasEntry := outFiles[entry]; !hasEntry {
			// Look for a TypeScript source that compiles to entry (e.g. src/index.ts -> dist/plugin.js).
			tsEntry := findTSEntry(pkg.Files, entry)
			if tsEntry == "" {
				return nil, fmt.Errorf("kit: pack: entry %q not found and no TypeScript source found", entry)
			}
			srcMap := collectSources(pkg.Files)
			js, smap, diags := wasm.Compile(srcMap, tsEntry)
			if err := compileDiagsError(diags); err != nil {
				return nil, err
			}
			outFiles[entry] = js
			if len(smap) > 0 {
				outFiles[entry+".map"] = smap
			}
		} else if isTSPath(entry) {
			// The bundle goes to a .js entry and the manifest follows it,
			// keeping the source beside it. Left at the .ts path, the host
			// would compile the bundle again and its exports would vanish
			// inside the second wrapper.
			srcMap := collectSources(pkg.Files)
			js, smap, diags := wasm.Compile(srcMap, entry)
			if err := compileDiagsError(diags); err != nil {
				return nil, err
			}
			manRaw, err := withEntry(pkg.ManifestRaw, bundleEntry)
			if err != nil {
				return nil, err
			}
			outFiles["manifest.json"] = manRaw
			outFiles[bundleEntry] = js
			delete(outFiles, bundleEntry+".map")
			if len(smap) > 0 {
				outFiles[bundleEntry+".map"] = smap
			}
		}
	}

	// Compute deterministic digest over sorted package files.
	outFiles["digest"] = []byte(plugins.PkgDigest(outFiles))

	names := make([]string, 0, len(outFiles))
	for name := range outFiles {
		names = append(names, name)
	}
	sort.Strings(names)

	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for _, name := range names {
		w, err := zw.Create(name)
		if err != nil {
			_ = zw.Close()
			return nil, err
		}
		if _, err := w.Write(outFiles[name]); err != nil {
			_ = zw.Close()
			return nil, err
		}
	}
	if err := zw.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// bundleEntry is where a package's compiled TypeScript bundle lives.
const bundleEntry = "dist/plugin.js"

// withEntry returns manifest JSON whose hosted runtime entry is entry.
func withEntry(manRaw []byte, entry string) ([]byte, error) {
	var doc map[string]any
	if err := json.Unmarshal(manRaw, &doc); err != nil {
		return nil, err
	}
	ether, _ := doc["ether"].(map[string]any)
	runtime, _ := ether["runtime"].(map[string]any)
	if runtime == nil {
		return nil, fmt.Errorf("kit: manifest runtime is missing")
	}
	runtime["entry"] = entry
	return json.Marshal(doc)
}

func isTSPath(p string) bool {
	ext := strings.ToLower(path.Ext(p))
	return ext == ".ts" || ext == ".tsx" || ext == ".mts"
}

func findTSEntry(files map[string][]byte, entry string) string {
	candidates := []string{
		strings.TrimSuffix(entry, path.Ext(entry)) + ".ts",
		"src/index.ts",
		"src/plugin.ts",
		"index.ts",
		"plugin.ts",
	}
	for _, c := range candidates {
		if _, ok := files[c]; ok {
			return c
		}
	}
	return ""
}

func collectSources(files map[string][]byte) map[string]string {
	out := map[string]string{}
	for k, v := range files {
		ext := strings.ToLower(path.Ext(k))
		if ext == ".ts" || ext == ".tsx" || ext == ".mts" || ext == ".js" || ext == ".json" {
			out[k] = string(v)
		}
	}
	return out
}

func compileDiagsError(diags []wasm.Diagnostic) error {
	var errs []string
	for _, d := range diags {
		if d.Severity == "error" {
			if d.File != "" && d.Line > 0 {
				errs = append(errs, fmt.Sprintf("%s:%d:%d: %s", d.File, d.Line, d.Column, d.Text))
			} else {
				errs = append(errs, d.Text)
			}
		}
	}
	if len(errs) == 0 {
		return nil
	}
	return fmt.Errorf("compile: %s", strings.Join(errs, "; "))
}

func isUnsafePath(p string) bool {
	return p != "" && (strings.HasPrefix(p, "/") || strings.Contains(p, ".."))
}

// CheckInstall adapts the conformance report to the plugin host's validation error.
func CheckInstall(ctx context.Context, manifest []byte, files map[string][]byte) error {
	report := Check(ctx, &Package{ManifestRaw: manifest, Files: files})
	if report.OK {
		return nil
	}
	fields := make([]plugins.FieldError, 0, len(report.Errors))
	for _, issue := range report.Errors {
		fields = append(fields, plugins.FieldError{Field: issue.Field, Message: issue.Message})
	}
	return &plugins.ValidationError{Fields: fields}
}
