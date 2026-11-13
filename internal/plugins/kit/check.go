package kit

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"image/color"
	"math"
	"net/url"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/braedonsaunders/ether/internal/artwork"
	"github.com/braedonsaunders/ether/internal/plugins"
	"github.com/braedonsaunders/ether/internal/plugins/wasm"
	"github.com/braedonsaunders/ether/internal/viewspec"
	"github.com/srwiley/oksvg"
	"github.com/srwiley/rasterx"
)

// Check runs the full conformance kit against pkg and returns a structured Report.
func Check(ctx context.Context, pkg *Package) (rep Report) {
	start := time.Now()
	rep = Report{OK: true}
	defer func() {
		rep.DurationMS = float64(time.Since(start).Microseconds()) / 1000.0
	}()

	if pkg == nil || len(pkg.ManifestRaw) == 0 {
		rep.addErr("manifest", "manifest.json", "manifest.json is missing or empty")
		return rep
	}

	man, err := plugins.ParseManifest(pkg.ManifestRaw)
	if err != nil {
		rep.addErr("manifest", "manifest.json", err.Error())
		return rep
	}
	pkg.Manifest = man
	rep.PluginID = man.ID
	rep.Version = man.Version
	if man.Ether != nil {
		rep.Kind = string(man.Ether.Kind)
		rep.DeclaredHosts = append([]string(nil), man.Ether.Permissions.Network...)
	}

	// 1. Manifest and kind/capability rules (reusing WS7 Manifest.Validate).
	if err := man.Validate(); err != nil {
		var ve *plugins.ValidationError
		if errors.As(err, &ve) {
			for _, fe := range ve.Fields {
				rep.addErr("manifest", fe.Field, fe.Message)
			}
		} else {
			rep.addErr("manifest", "manifest.json", err.Error())
		}
	}

	if man.Ether == nil {
		return rep
	}

	// 2. Resolve and lint strings across every shipped locale, plus $t: references.
	stringsByLocale := checkStringsAndCopy(&rep, man, pkg.Files)

	// 3. Resolve and lint settings schema, including $t: references in every locale.
	checkSettingsSchema(&rep, man, pkg.Files, stringsByLocale)

	// 3a. Content-kind conformance notes beyond Validate (spec §9.4).
	checkMediaKinds(&rep, man)

	// 4. Validate SVG icon if present (rasterize and check legibility on light and dark).
	checkIcon(&rep, ctx, man, pkg.Files)

	// 5. Runtime checks (hosted WASM/JS/TS or remote with local entry).
	if man.Ether.Runtime.Kind == "hosted" {
		checkHostedRuntime(ctx, &rep, man, pkg.Files, stringsByLocale)
	}

	return rep
}

// stringUsage records how a $t:key is used so every locale is linted against
// the right character cap and sentence rules.
type stringUsage struct {
	field    string
	maxChars int
	isHint   bool // hints must be <= 120 chars and one sentence
}

func checkStringsAndCopy(rep *Report, man plugins.Manifest, files map[string][]byte) map[string]map[string]string {
	e := man.Ether
	out := map[string]map[string]string{}

	locs := make([]string, 0, len(e.Strings))
	for loc := range e.Strings {
		locs = append(locs, loc)
	}
	sort.Strings(locs)

	for _, loc := range locs {
		raw := e.Strings[loc]
		var filePath string
		var doc map[string]string
		if json.Unmarshal(raw, &filePath) == nil && filePath != "" {
			if isUnsafePath(filePath) {
				rep.addErr("strings", "ether.strings."+loc, "file path must stay inside the package")
				continue
			}
			b, ok := files[filePath]
			if !ok {
				rep.addErr("strings", "ether.strings."+loc, fmt.Sprintf("missing strings file %q", filePath))
				continue
			}
			if err := json.Unmarshal(b, &doc); err != nil {
				rep.addErr("strings", "ether.strings."+loc, "strings file must be a JSON object of string values")
				continue
			}
		} else if err := json.Unmarshal(raw, &doc); err != nil {
			rep.addErr("strings", "ether.strings."+loc, "must be a file path or a JSON object of string values")
			continue
		}
		out[loc] = doc
	}

	// Collect all $t: usages from manifest & contributes.
	usages := map[string][]stringUsage{}
	recordRef := func(field, val string, max int, isHint bool) {
		if strings.HasPrefix(val, "$t:") {
			key := strings.TrimPrefix(val, "$t:")
			usages[key] = append(usages[key], stringUsage{field: field, maxChars: max, isHint: isHint})
			return
		}
		// Plain text a Stremio add-on brings (catalogue names) is the
		// add-on's own; only the plugin's summary is held to Ether's rules.
		if e.Stremio && field != "ether.summary" {
			return
		}
		if strings.TrimSpace(val) != "" {
			if msg := plugins.LintCopy(val, max); msg != "" {
				rep.addErr("copy", field, msg)
			} else if isHint && isMultiSentence(val) {
				rep.addErr("copy", field, "must be one sentence")
			}
		}
	}

	recordRef("ether.summary", e.Summary, 80, false)
	c := e.Contributes
	for i, ls := range c.ListSources {
		base := fmt.Sprintf("ether.contributes.listSources[%d]", i)
		recordRef(base+".label", ls.Label, 40, false)
		recordRef(base+".hint", ls.Hint, 120, true)
	}
	for i, rs := range c.RankingSteps {
		base := fmt.Sprintf("ether.contributes.rankingSteps[%d]", i)
		recordRef(base+".label", rs.Label, 40, false)
		recordRef(base+".hint", rs.Hint, 120, true)
	}
	for i, rc := range c.RuleConditions {
		base := fmt.Sprintf("ether.contributes.ruleConditions[%d]", i)
		recordRef(base+".label", rc.Label, 40, false)
	}
	for i, tl := range c.Tools {
		base := fmt.Sprintf("ether.contributes.tools[%d]", i)
		recordRef(base+".title", tl.Title, 40, false)
	}
	for i, pg := range c.Pages {
		base := fmt.Sprintf("ether.contributes.pages[%d]", i)
		recordRef(base+".label", pg.Label, 40, false)
	}
	for i, sl := range c.Slots {
		base := fmt.Sprintf("ether.contributes.slots[%d]", i)
		recordRef(base+".label", sl.Label, 40, false)
	}
	for i, cm := range c.Commands {
		base := fmt.Sprintf("ether.contributes.commands[%d]", i)
		recordRef(base+".label", cm.Label, 40, false)
	}
	if c.Integration != nil {
		for i, a := range c.Integration.Adds {
			recordRef(fmt.Sprintf("ether.contributes.integration.adds[%d]", i), a, 80, false)
		}
	}

	// Lint every string in every locale.
	enDoc := out["en"]
	for _, loc := range locs {
		doc := out[loc]
		keys := make([]string, 0, len(doc))
		for k := range doc {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			val := doc[k]
			fieldPath := fmt.Sprintf("strings.%s.%s", loc, k)
			if strings.TrimSpace(val) == "" {
				rep.addErr("copy", fieldPath, "string value must not be empty")
				continue
			}
			// Base copy check for every string in every shipped locale.
			maxLen := 120
			isHint := strings.HasSuffix(strings.ToLower(k), "hint")
			if strings.HasSuffix(strings.ToLower(k), "label") || strings.HasSuffix(strings.ToLower(k), "title") {
				maxLen = 40
			}
			if ulist, ok := usages[k]; ok && len(ulist) > 0 {
				for _, u := range ulist {
					if msg := plugins.LintCopy(val, u.maxChars); msg != "" {
						rep.addErr("copy", fmt.Sprintf("%s (%s)", fieldPath, u.field), msg)
					} else if u.isHint && isMultiSentence(val) {
						rep.addErr("copy", fmt.Sprintf("%s (%s)", fieldPath, u.field), "must be one sentence")
					}
				}
			} else {
				if msg := plugins.LintCopy(val, maxLen); msg != "" {
					rep.addErr("copy", fieldPath, msg)
				} else if isHint && isMultiSentence(val) {
					rep.addErr("copy", fieldPath, "must be one sentence")
				}
			}
		}
	}

	// Verify all referenced $t: keys exist in en.
	refKeys := make([]string, 0, len(usages))
	for k := range usages {
		refKeys = append(refKeys, k)
	}
	sort.Strings(refKeys)
	for _, k := range refKeys {
		if enDoc == nil {
			rep.addErr("strings", usages[k][0].field, fmt.Sprintf("missing en strings for $t:%s", k))
			continue
		}
		if _, ok := enDoc[k]; !ok {
			rep.addErr("strings", usages[k][0].field, fmt.Sprintf("missing key %q in en strings", k))
		}
	}

	return out
}

func isMultiSentence(s string) bool {
	trimmed := strings.TrimSpace(s)
	trimmed = strings.TrimRight(trimmed, ".")
	return strings.Count(trimmed, ".") > 0
}

func checkSettingsSchema(rep *Report, man plugins.Manifest, files map[string][]byte, stringsByLocale map[string]map[string]string) {
	raw := man.Ether.Settings
	if len(raw) == 0 {
		return
	}
	var doc map[string]any
	if err := json.Unmarshal(raw, &doc); err != nil {
		return // already reported by Manifest.Validate
	}
	if ref, ok := doc["$ref"].(string); ok && ref != "" {
		if isUnsafePath(ref) {
			return
		}
		b, exists := files[ref]
		if !exists {
			rep.addErr("settings", "ether.settings.$ref", fmt.Sprintf("missing settings schema file %q", ref))
			return
		}
		raw = b
	}
	if err := plugins.LintSettingsSchema(raw); err != nil {
		var ve *plugins.ValidationError
		if errors.As(err, &ve) {
			for _, fe := range ve.Fields {
				rep.addErr("settings", fe.Field, fe.Message)
			}
		} else {
			rep.addErr("settings", "ether.settings", err.Error())
		}
	}
	var resolved any
	if json.Unmarshal(raw, &resolved) == nil {
		walkSettingsRefs(rep, resolved, "ether.settings", man, stringsByLocale)
	}
}

func walkSettingsRefs(rep *Report, node any, fieldPath string, man plugins.Manifest, stringsByLocale map[string]map[string]string) {
	switch n := node.(type) {
	case map[string]any:
		if k, ok := n["kind"].(string); ok && k == "folder" {
			if man.Ether.Permissions.Folders != "read" {
				rep.addErr("settings", fieldPath+".kind", "folder field kind requires folders: read permission")
			}
		}
		if label, ok := n["label"].(string); ok && strings.HasPrefix(label, "$t:") {
			key := strings.TrimPrefix(label, "$t:")
			checkLocalizedRef(rep, fieldPath+".label", key, 40, false, stringsByLocale)
		}
		if hint, ok := n["hint"].(string); ok && strings.HasPrefix(hint, "$t:") {
			key := strings.TrimPrefix(hint, "$t:")
			checkLocalizedRef(rep, fieldPath+".hint", key, 120, true, stringsByLocale)
		}
		keys := make([]string, 0, len(n))
		for k := range n {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			walkSettingsRefs(rep, n[k], fieldPath+"."+k, man, stringsByLocale)
		}
	case []any:
		for i, item := range n {
			walkSettingsRefs(rep, item, fmt.Sprintf("%s[%d]", fieldPath, i), man, stringsByLocale)
		}
	}
}

func checkLocalizedRef(rep *Report, fieldPath, key string, max int, isHint bool, stringsByLocale map[string]map[string]string) {
	en := stringsByLocale["en"]
	if en == nil {
		rep.addErr("strings", fieldPath, fmt.Sprintf("missing en strings for $t:%s", key))
		return
	}
	if _, ok := en[key]; !ok {
		rep.addErr("strings", fieldPath, fmt.Sprintf("missing key %q in en strings", key))
		return
	}
	locs := make([]string, 0, len(stringsByLocale))
	for loc := range stringsByLocale {
		locs = append(locs, loc)
	}
	sort.Strings(locs)
	for _, loc := range locs {
		val, ok := stringsByLocale[loc][key]
		if !ok {
			continue
		}
		if msg := plugins.LintCopy(val, max); msg != "" {
			rep.addErr("copy", fmt.Sprintf("%s (%s:%s)", fieldPath, loc, key), msg)
		} else if isHint && isMultiSentence(val) {
			rep.addErr("copy", fmt.Sprintf("%s (%s:%s)", fieldPath, loc, key), "must be one sentence")
		}
	}
}

func checkIcon(rep *Report, ctx context.Context, man plugins.Manifest, files map[string][]byte) {
	iconPath := man.Ether.Icon
	if iconPath == "" || strings.HasPrefix(iconPath, "http://") || strings.HasPrefix(iconPath, "https://") {
		return
	}
	if isUnsafePath(iconPath) {
		rep.addErr("icon", "ether.icon", "icon path must stay inside the package")
		return
	}
	data, ok := files[iconPath]
	if !ok {
		rep.addErr("icon", "ether.icon", fmt.Sprintf("missing icon file %q", iconPath))
		return
	}
	rast := artwork.NewSVGRasterizer()
	if _, _, err := rast.Rasterize(ctx, "kit:"+man.ID, data, artwork.RasterOptions{Width: 64, Height: 64, Format: "png"}); err != nil {
		rep.addErr("icon", "ether.icon", fmt.Sprintf("icon cannot be rasterized safely: %v", err))
		return
	}
	// Check contrast/legibility on light (#f8fafc) and dark (#0f172a) backgrounds.
	if !iconLegibleOnBoth(data) {
		rep.addErr("icon", "ether.icon", "icon is not legible on both light and dark backgrounds")
	}
}

func iconLegibleOnBoth(data []byte) bool {
	icon, err := oksvg.ReadIconStream(strings.NewReader(string(data)), oksvg.StrictErrorMode)
	if err != nil || icon == nil {
		return false
	}
	const w, h = 64, 64
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	icon.SetTarget(0, 0, float64(w), float64(h))
	dasher := rasterx.NewDasher(w, h, rasterx.NewScannerGV(w, h, img, img.Bounds()))
	for _, p := range icon.SVGPaths {
		p.DrawTransformed(dasher, 1, icon.Transform)
	}
	lightBG := color.RGBA{R: 248, G: 250, B: 252, A: 255}
	darkBG := color.RGBA{R: 15, G: 23, B: 42, A: 255}
	var visibleLight, visibleDark int
	var nonTransparent int
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			c := img.RGBAAt(x, y)
			if c.A < 25 {
				continue
			}
			nonTransparent++
			if colorContrast(c, lightBG) >= 1.25 {
				visibleLight++
			}
			if colorContrast(c, darkBG) >= 1.25 {
				visibleDark++
			}
		}
	}
	return nonTransparent > 0 && visibleLight > 0 && visibleDark > 0
}

func colorContrast(fg, bg color.RGBA) float64 {
	alpha := float64(fg.A) / 255.0
	r := float64(fg.R)*alpha + float64(bg.R)*(1-alpha)
	g := float64(fg.G)*alpha + float64(bg.G)*(1-alpha)
	b := float64(fg.B)*alpha + float64(bg.B)*(1-alpha)
	l1 := relLuminance(r, g, b)
	l2 := relLuminance(float64(bg.R), float64(bg.G), float64(bg.B))
	if l1 < l2 {
		l1, l2 = l2, l1
	}
	return (l1 + 0.05) / (l2 + 0.05)
}

func relLuminance(r, g, b float64) float64 {
	chanLum := func(v float64) float64 {
		v /= 255.0
		if v <= 0.03928 {
			return v / 12.92
		}
		return math.Pow((v+0.055)/1.055, 2.4)
	}
	return 0.2126*chanLum(r) + 0.7152*chanLum(g) + 0.0722*chanLum(b)
}

// recordingHost implements wasm.Host for conformance checking, recording
// outbound http.fetch hosts and auditing them against manifest permissions.
type recordingHost struct {
	mu       sync.Mutex
	man      plugins.Manifest
	kv       map[string]string
	observed map[string]int
	undecl   map[string]bool
	deferred int
}

func newRecordingHost(man plugins.Manifest) *recordingHost {
	return &recordingHost{
		man:      man,
		kv:       map[string]string{},
		observed: map[string]int{},
		undecl:   map[string]bool{},
	}
}

func (h *recordingHost) CallHost(ctx context.Context, name string, raw json.RawMessage) (json.RawMessage, error) {
	var args []json.RawMessage
	if len(raw) > 0 && string(raw) != "null" {
		_ = json.Unmarshal(raw, &args)
	}
	str := func(i int) string {
		var s string
		if i < len(args) {
			_ = json.Unmarshal(args[i], &s)
		}
		return s
	}
	h.mu.Lock()
	defer h.mu.Unlock()

	switch name {
	case "log":
		return json.RawMessage("null"), nil
	case "kv.get":
		if v, ok := h.kv[str(0)]; ok {
			b, _ := json.Marshal(v)
			return b, nil
		}
		return json.RawMessage("null"), nil
	case "kv.put":
		var val string
		if len(args) > 1 {
			if json.Unmarshal(args[1], &val) != nil {
				val = string(args[1])
			}
		}
		h.kv[str(0)] = val
		return json.RawMessage("null"), nil
	case "kv.delete":
		delete(h.kv, str(0))
		return json.RawMessage("null"), nil
	case "kv.list":
		prefix := str(0)
		var keys []string
		for k := range h.kv {
			if strings.HasPrefix(k, prefix) {
				keys = append(keys, k)
			}
		}
		sort.Strings(keys)
		b, _ := json.Marshal(keys)
		return b, nil
	case "secret.get":
		sec := str(0)
		for _, allowed := range h.man.Ether.Permissions.Secrets {
			if allowed == sec {
				return json.RawMessage(`"fixture-secret"`), nil
			}
		}
		return nil, wasm.Forbidden("undeclared secret " + sec)
	case "settings.get":
		return json.RawMessage(`{}`), nil
	case "addon.get":
		// An add-on-backed plugin's checks run offline: the add-on answers
		// with an empty, well-formed Stremio response.
		if !h.man.Ether.Stremio {
			return nil, wasm.Forbidden("addon.get is only for plugins made from an add-on")
		}
		body := `{"metas":[],"streams":[],"subtitles":[],"addons":[],"meta":{"id":"tt0000001","type":"movie","name":"Fixture"}}`
		if strings.HasSuffix(str(0), "/manifest.json") {
			body = `{"id":"fixture","version":"1.0.0","name":"Fixture","resources":[],"types":[]}`
		}
		b, _ := json.Marshal(map[string]any{"status": 200, "body": body})
		return b, nil
	case "http.fetch":
		var req struct {
			Method  string            `json:"method"`
			URL     string            `json:"url"`
			Headers map[string]string `json:"headers"`
			Body    string            `json:"body"`
		}
		if len(args) > 0 {
			_ = json.Unmarshal(args[0], &req)
		}
		u, err := url.Parse(req.URL)
		if err != nil || u.Hostname() == "" {
			return nil, wasm.Forbidden("invalid url in http.fetch: " + req.URL)
		}
		host := strings.ToLower(u.Hostname())
		h.observed[host]++
		if !hostMatches(h.man.Ether.Permissions.Network, host) {
			h.undecl[host] = true
			return nil, wasm.Forbidden("undeclared network host " + host)
		}
		// The offline checker cannot invent the upstream API's response shape.
		// Stop at this boundary and leave an explicit live-test warning.
		h.deferred++
		return nil, &wasm.Fault{Code: "check_dependency", Message: "declared network request needs a live test"}
	case "catalog.search":
		if h.man.Ether.Permissions.Catalog != "read" {
			return nil, wasm.Forbidden("catalog permission not declared")
		}
		return json.RawMessage(`{"titles":[{"id":1,"title":"The Kid","type":"movie","year":1921}]}`), nil
	case "catalog.get":
		if h.man.Ether.Permissions.Catalog != "read" {
			return nil, wasm.Forbidden("catalog permission not declared")
		}
		return json.RawMessage(`{"id":1,"title":"The Kid","type":"movie","year":1921}`), nil
	case "viewers.granted":
		return json.RawMessage(`[]`), nil
	case "viewers.history":
		if h.man.Ether.Permissions.Viewers.WatchHistory == "" || h.man.Ether.Permissions.Viewers.WatchHistory == "none" {
			return nil, wasm.Forbidden("viewers.watch_history permission not declared")
		}
		return json.RawMessage(`[]`), nil
	case "viewers.setWatchState":
		if h.man.Ether.Permissions.Viewers.WriteWatchState == "" || h.man.Ether.Permissions.Viewers.WriteWatchState == "none" {
			return nil, wasm.Forbidden("viewers.write_watch_state permission not declared")
		}
		return json.RawMessage("null"), nil
	case "collections.upsert":
		if h.man.Ether.Permissions.Collections != "write" {
			return nil, wasm.Forbidden("collections: write permission not declared")
		}
		return json.RawMessage("null"), nil
	case "folders.list":
		if h.man.Ether.Permissions.Folders != "read" {
			return nil, wasm.Forbidden("folders: read permission not declared")
		}
		return json.RawMessage(`[]`), nil
	case "folders.hash":
		if h.man.Ether.Permissions.Folders != "read" {
			return nil, wasm.Forbidden("folders: read permission not declared")
		}
		return json.RawMessage(`{"size":1024,"modTime":0,"crc32":"00000000","md5":"00000000000000000000000000000000","sha1":"0000000000000000000000000000000000000000"}`), nil
	case "folders.stat":
		if h.man.Ether.Permissions.Folders != "read" {
			return nil, wasm.Forbidden("folders: read permission not declared")
		}
		return json.RawMessage(`{"name":"movie.mkv","path":"movie.mkv","dir":false,"size":1024}`), nil
	case "notify.owner":
		return json.RawMessage("null"), nil
	default:
		return nil, wasm.Forbidden("unknown host service " + name)
	}
}

func hostMatches(patterns []string, host string) bool {
	host = strings.ToLower(host)
	for _, p := range patterns {
		p = strings.ToLower(p)
		if strings.HasPrefix(p, "*.") {
			if strings.HasSuffix(host, p[1:]) && host != p[2:] {
				return true
			}
			continue
		}
		if p == host {
			return true
		}
	}
	return false
}

func checkHostedRuntime(ctx context.Context, rep *Report, man plugins.Manifest, files map[string][]byte, stringsByLocale map[string]map[string]string) {
	eng := wasm.NewEngine(nil)
	defer func() { _ = eng.Close() }()

	host := newRecordingHost(man)
	decl := man.Ether.Runtime
	var plug *wasm.Plugin
	var err error

	switch {
	case decl.Module != "":
		modBytes, ok := files[decl.Module]
		if !ok {
			rep.addErr("runtime", "ether.runtime.module", fmt.Sprintf("missing WASM module %q", decl.Module))
			return
		}
		plug, err = eng.LoadWASM(man.ID, man.Version, modBytes, host, wasm.Limits{})
	case decl.Entry != "":
		jsSrc, compErr := loadOrCompileEntry(decl.Entry, files)
		if compErr != nil {
			rep.addErr("runtime", "ether.runtime.entry", compErr.Error())
			return
		}
		plug, err = eng.LoadJS(man.ID, man.Version, jsSrc, host, wasm.Limits{})
	default:
		return
	}
	if err != nil {
		rep.addErr("runtime", "ether.runtime", err.Error())
		return
	}
	defer func() { _ = plug.Close() }()
	rep.Exports = map[string]bool{"handle": plug.HasHandle(), "rank_key": plug.HasRankKey(), "cond_match": plug.HasCondMatch()}

	// Exercise declared routes and capabilities.
	checkRoutes(ctx, rep, man, plug, stringsByLocale, host)

	// Record observed and undeclared network hosts.
	host.mu.Lock()
	for h := range host.observed {
		rep.ObservedHosts = append(rep.ObservedHosts, h)
	}
	sort.Strings(rep.ObservedHosts)
	var undecl []string
	for h := range host.undecl {
		undecl = append(undecl, h)
	}
	sort.Strings(undecl)
	host.mu.Unlock()
	for _, h := range undecl {
		rep.addErr("permissions", "ether.permissions.network", fmt.Sprintf("plugin attempted http.fetch to undeclared host %q", h))
	}

	// Hot path check: ranking_step and rule_condition on a 10k-candidate corpus.
	if man.HasCapability(plugins.CapRankingStep) || man.HasCapability(plugins.CapRuleCondition) {
		checkHotPath(ctx, rep, man, plug)
	}
}

func loadOrCompileEntry(entry string, files map[string][]byte) (string, error) {
	if b, ok := files[entry]; ok {
		if isTSPath(entry) {
			srcMap := collectSources(files)
			js, _, diags := wasm.Compile(srcMap, entry)
			if err := compileDiagsError(diags); err != nil {
				return "", err
			}
			return string(js), nil
		}
		return string(b), nil
	}
	tsEntry := findTSEntry(files, entry)
	if tsEntry == "" {
		return "", fmt.Errorf("missing entry file %q", entry)
	}
	srcMap := collectSources(files)
	js, _, diags := wasm.Compile(srcMap, tsEntry)
	if err := compileDiagsError(diags); err != nil {
		return "", err
	}
	return string(js), nil
}

type routeFixture struct {
	method   string
	path     string
	query    map[string][]string
	headers  map[string]string
	body     json.RawMessage
	validate func(resp wasm.Response) string
}

func checkRoutes(ctx context.Context, rep *Report, man plugins.Manifest, plug *wasm.Plugin, stringsByLocale map[string]map[string]string, host *recordingHost) {
	needsHandle := len(man.Ether.Contributes.Pages) > 0 ||
		len(man.Ether.Contributes.Slots) > 0 ||
		len(man.Ether.Contributes.Commands) > 0 ||
		len(man.Ether.Contributes.Tools) > 0 ||
		len(man.Ether.Contributes.ListSources) > 0 ||
		len(man.Ether.Schedule) > 0 ||
		man.HasCapability(plugins.CapStreamSource) ||
		man.HasCapability(plugins.CapDebridProvider) ||
		man.HasCapability(plugins.CapLiveTV) ||
		man.HasCapability(plugins.CapSubtitles) ||
		man.HasCapability(plugins.CapMarkers) ||
		man.HasCapability(plugins.CapNotify) ||
		man.HasCapability(plugins.CapEvents)

	if !plug.HasHandle() {
		if needsHandle {
			rep.addErr("routes", "handle", "plugin declares HTTP routes or capabilities that require exporting handle(request)")
		}
		return
	}

	var fixtures []routeFixture

	// Health check is standard on all handle-exporting plugins.
	fixtures = append(fixtures, routeFixture{
		method: "GET",
		path:   "/ether/health",
		validate: func(resp wasm.Response) string {
			var doc struct {
				Status string `json:"status"`
			}
			if err := decodeResponseBody(resp.Body, &doc); err != nil {
				return "health response must be a JSON object: " + err.Error()
			}
			if doc.Status != "ok" && doc.Status != "attention" && doc.Status != "error" {
				return fmt.Sprintf("health status must be ok, attention or error (got %q)", doc.Status)
			}
			return ""
		},
	})

	// Configure check.
	fixtures = append(fixtures, routeFixture{
		method: "POST",
		path:   "/ether/configure",
		body:   json.RawMessage(`{}`),
		validate: func(resp wasm.Response) string {
			var doc map[string]any
			if err := decodeResponseBody(resp.Body, &doc); err != nil {
				return "configure response must be a JSON object: " + err.Error()
			}
			return ""
		},
	})

	// Test route when declared.
	if (man.Ether.Contributes.Channels != nil && man.Ether.Contributes.Channels.Test) ||
		(man.Ether.Contributes.Notification != nil && man.Ether.Contributes.Notification.SendTest) ||
		(man.Ether.Contributes.Integration != nil && man.Ether.Contributes.Integration.Test) ||
		(man.HasCapability(plugins.CapStreamSource) || man.HasCapability(plugins.CapDebridProvider)) {
		fixtures = append(fixtures, routeFixture{
			method: "POST",
			path:   "/ether/test",
			body:   json.RawMessage(`{}`),
			validate: func(resp wasm.Response) string {
				var doc struct {
					OK *bool `json:"ok"`
				}
				if err := decodeResponseBody(resp.Body, &doc); err != nil {
					return "test response must be a JSON object: " + err.Error()
				}
				if doc.OK == nil {
					return "test response must include boolean field 'ok'"
				}
				return ""
			},
		})
	}

	// Stream source routes.
	if man.HasCapability(plugins.CapStreamSource) {
		fixtures = append(fixtures, routeFixture{
			method: "GET",
			path:   "/stream/movie/tt0012349.json",
			validate: func(resp wasm.Response) string {
				var doc struct {
					Streams []map[string]any `json:"streams"`
				}
				if err := decodeResponseBody(resp.Body, &doc); err != nil {
					return "stream response must be a JSON object with streams[]: " + err.Error()
				}
				if doc.Streams == nil {
					return "stream response must contain a streams array"
				}
				for i, st := range doc.Streams {
					if eth, ok := st["ether"].(map[string]any); ok {
						if f, hasFile := eth["file"].(string); hasFile && f != "" {
							if man.Ether.Permissions.Folders != "read" {
								return fmt.Sprintf("streams[%d].ether.file requires folders: read permission", i)
							}
							if isUnsafePath(f) {
								return fmt.Sprintf("streams[%d].ether.file must be a relative path without '..'", i)
							}
						}
					}
				}
				return ""
			},
		})
	}

	// List sources.
	for _, ls := range man.Ether.Contributes.ListSources {
		catPath := "/catalog/movie/" + ls.Catalog + ".json"
		if strings.Contains(ls.Catalog, "/") {
			// Native and adapted catalogues may both declare type/id.
			catPath = "/catalog/" + ls.Catalog + ".json"
		}
		fixtures = append(fixtures, routeFixture{
			method: "GET",
			path:   catPath,
			validate: func(resp wasm.Response) string {
				var doc struct {
					Metas []map[string]any `json:"metas"`
				}
				if err := decodeResponseBody(resp.Body, &doc); err != nil {
					return "catalog response must be a JSON object with metas[]: " + err.Error()
				}
				if doc.Metas == nil {
					return "catalog response must contain a metas array"
				}
				return ""
			},
		})
	}

	// Live TV channels and guide.
	if man.HasCapability(plugins.CapLiveTV) {
		fixtures = append(fixtures, routeFixture{
			method: "GET",
			path:   "/ether/channels.json",
			validate: func(resp wasm.Response) string {
				var doc struct {
					Channels []map[string]any `json:"channels"`
				}
				if err := decodeResponseBody(resp.Body, &doc); err != nil {
					return "channels response must be a JSON object with channels[]: " + err.Error()
				}
				if doc.Channels == nil {
					return "channels response must contain a channels array"
				}
				return ""
			},
		})
		fixtures = append(fixtures, routeFixture{
			method: "GET",
			path:   "/ether/guide/ch-1.json",
			query:  map[string][]string{"from": {"2026-01-01T00:00:00Z"}, "to": {"2026-01-01T06:00:00Z"}},
			validate: func(resp wasm.Response) string {
				var doc struct {
					Programmes []map[string]any `json:"programmes"`
				}
				if err := decodeResponseBody(resp.Body, &doc); err != nil {
					return "guide response must be a JSON object: " + err.Error()
				}
				return ""
			},
		})
	}

	// Listing is public to the host; download requires a viewer pick.
	if man.HasCapability(plugins.CapSubtitles) {
		subPath := "/subtitles/movie/tt0012349/videoHash=0123456789abcdef.json"
		// Download must refuse a missing pick token. A plugin that answers
		// as a Stremio add-on hands out subtitle links instead and has no
		// download route.
		if !man.SpeaksStremio() {
			callNoPick(ctx, rep, plug, "/ether/subtitles/download")
		}
		// Listing succeeds without a pick and returns a subtitles array.
		fixtures = append(fixtures, routeFixture{
			method: "GET",
			path:   subPath,
			validate: func(resp wasm.Response) string {
				var doc struct {
					Subtitles []map[string]any `json:"subtitles"`
				}
				if err := decodeResponseBody(resp.Body, &doc); err != nil {
					return "subtitles response must be a JSON object with subtitles[]: " + err.Error()
				}
				if doc.Subtitles == nil {
					return "subtitles response must contain a subtitles array"
				}
				return ""
			},
		})
	}

	// Markers.
	if man.HasCapability(plugins.CapMarkers) {
		fixtures = append(fixtures, routeFixture{
			method: "GET",
			path:   "/ether/markers/movie/tt0012349.json",
			validate: func(resp wasm.Response) string {
				var doc struct {
					Markers []map[string]any `json:"markers"`
				}
				if err := decodeResponseBody(resp.Body, &doc); err != nil {
					return "markers response must be a JSON object with markers[]: " + err.Error()
				}
				if doc.Markers == nil {
					return "markers response must contain a markers array"
				}
				return ""
			},
		})
	}

	// Events.
	if man.HasCapability(plugins.CapEvents) {
		evName := plugins.EventPlaybackStarted
		if man.Ether.Contributes.Events != nil && len(man.Ether.Contributes.Events.Subscribe) > 0 {
			evName = man.Ether.Contributes.Events.Subscribe[0]
		}
		evBody, _ := json.Marshal(map[string]any{
			"events": []map[string]any{
				{"id": "ev-1", "name": evName, "at": "2026-01-01T00:00:00Z", "payload": map[string]any{"ratingKey": 1}},
			},
		})
		fixtures = append(fixtures, routeFixture{
			method: "POST",
			path:   "/ether/event",
			body:   evBody,
			validate: func(resp wasm.Response) string {
				var doc struct {
					Ack []string `json:"ack"`
				}
				if err := decodeResponseBody(resp.Body, &doc); err != nil {
					return "event response must be a JSON object with ack[]: " + err.Error()
				}
				if doc.Ack == nil {
					return "event response must contain an ack array"
				}
				return ""
			},
		})
	}

	// Pages & Slots (ViewSpec validation + literal string rejection).
	for _, pg := range man.Ether.Contributes.Pages {
		route := pg.Route
		field := fmt.Sprintf("page:%s (%s)", pg.Key, route)
		fixtures = append(fixtures, routeFixture{
			method: "GET",
			path:   route,
			query:  map[string][]string{"page": {"1"}, "q": {""}},
			validate: func(resp wasm.Response) string {
				return validateViewResponse(rep, field, resp.Body, stringsByLocale)
			},
		})
	}
	for _, sl := range man.Ether.Contributes.Slots {
		if strings.HasPrefix(sl.Slot, "actionMenu.") || sl.Slot == "command" {
			continue
		}
		route := sl.Route
		field := fmt.Sprintf("slot:%s (%s)", sl.Key, route)
		fixtures = append(fixtures, routeFixture{
			method: "GET",
			path:   route,
			query:  map[string][]string{"id": {"1"}, "viewerId": {"1"}},
			validate: func(resp wasm.Response) string {
				return validateViewResponse(rep, field, resp.Body, stringsByLocale)
			},
		})
	}

	// Commands (actions).
	for _, cm := range man.Ether.Contributes.Commands {
		actPath := "/ether/action/" + cm.Action
		fixtures = append(fixtures, routeFixture{
			method: "POST",
			path:   actPath,
			body:   json.RawMessage(`{}`),
			validate: func(resp wasm.Response) string {
				var doc map[string]any
				if err := decodeResponseBody(resp.Body, &doc); err != nil {
					return "action response must be a JSON object: " + err.Error()
				}
				return ""
			},
		})
	}

	// Tools.
	for _, tl := range man.Ether.Contributes.Tools {
		toolPath := "/ether/tool/" + tl.Name
		fixtures = append(fixtures, routeFixture{
			method: "POST",
			path:   toolPath,
			body:   sampleInputForSchema(tl.InputSchema),
			validate: func(resp wasm.Response) string {
				var doc map[string]any
				if err := decodeResponseBody(resp.Body, &doc); err != nil {
					return "tool response must be a JSON object: " + err.Error()
				}
				return ""
			},
		})
	}

	// Scheduled jobs.
	for _, job := range man.Ether.Schedule {
		fixtures = append(fixtures, routeFixture{
			method: "POST",
			path:   job.Call,
			body:   json.RawMessage(`{}`),
			validate: func(resp wasm.Response) string {
				var doc map[string]any
				if err := decodeResponseBody(resp.Body, &doc); err != nil {
					return "job response must be a JSON object: " + err.Error()
				}
				return ""
			},
		})
	}

	for _, fx := range fixtures {
		runRouteFixture(ctx, rep, plug, fx, host)
	}
}

func callNoPick(ctx context.Context, rep *Report, plug *wasm.Plugin, subPath string) {
	deadline := plugins.DeadlineFor(subPath)
	cctx, cancel := context.WithTimeout(ctx, deadline)
	defer cancel()
	t0 := time.Now()
	resp, _, err := plug.CallDetailed(cctx, wasm.Request{
		Method:  "POST",
		Path:    subPath,
		Headers: map[string]string{"X-Ether-Request-Id": "kit-nopick"},
	})
	dur := time.Since(t0)
	rt := RouteTiming{
		Method:     "POST",
		Route:      subPath + " (without X-Ether-Pick)",
		Status:     resp.Status,
		Duration:   dur,
		DurationMS: float64(dur.Microseconds()) / 1000.0,
		DeadlineMS: deadline.Milliseconds(),
	}
	if err != nil {
		var guestErr *wasm.Error
		if errors.As(err, &guestErr) && (guestErr.Status == wasm.StatusForbidden || guestErr.Status == wasm.StatusAborted) {
			rt.OK = true
			rep.Routes = append(rep.Routes, rt)
			return
		}
		rt.Error = fmt.Sprintf("subtitle refusal failed: %v", err)
		rep.Routes = append(rep.Routes, rt)
		rep.addErr("subtitles", subPath, rt.Error)
		return
	}
	if resp.Status != 403 {
		rt.Error = "subtitle route must refuse calls without X-Ether-Pick (expected HTTP 403)"
		rep.Routes = append(rep.Routes, rt)
		rep.addErr("subtitles", subPath, rt.Error)
		return
	}

	if msg := checkErrorShape(resp.Body); msg != "" {
		rt.OK = false
		rt.Error = msg
		rep.Routes = append(rep.Routes, rt)
		rep.addErr("subtitles", subPath, msg)
		return
	}
	rt.OK = true
	rep.Routes = append(rep.Routes, rt)
}

func runRouteFixture(ctx context.Context, rep *Report, plug *wasm.Plugin, fx routeFixture, host *recordingHost) {
	host.mu.Lock()
	before := host.deferred
	host.mu.Unlock()
	deadline := plugins.DeadlineFor(fx.path)
	cctx, cancel := context.WithTimeout(ctx, deadline)
	defer cancel()
	headers := map[string]string{
		"X-Ether-Request-Id": "kit-check",
		"X-Ether-Locale":     "en",
		"X-Ether-Deadline":   time.Now().Add(deadline).UTC().Format(time.RFC3339),
	}
	for k, v := range fx.headers {
		headers[k] = v
	}
	t0 := time.Now()
	resp, _, err := plug.CallDetailed(cctx, wasm.Request{
		Method:  fx.method,
		Path:    fx.path,
		Query:   fx.query,
		Headers: headers,
		Body:    fx.body,
	})
	dur := time.Since(t0)
	rt := RouteTiming{
		Method:     fx.method,
		Route:      fx.path,
		Status:     resp.Status,
		Duration:   dur,
		DurationMS: float64(dur.Microseconds()) / 1000.0,
		DeadlineMS: deadline.Milliseconds(),
		OK:         true,
	}
	host.mu.Lock()
	deferred := host.deferred > before
	host.mu.Unlock()
	if deferred {
		rt.OK = false
		rt.Skipped = true
		rt.Error = "Needs a live test: this route uses a declared network service."
		rep.Routes = append(rep.Routes, rt)
		rep.addWarn("live_test", fx.path, rt.Error)
		return
	}
	if err != nil {
		rt.OK = false
		rt.Error = err.Error()
		rep.Routes = append(rep.Routes, rt)
		rep.addErr("routes", fx.path, fmt.Sprintf("%s %s failed: %v", fx.method, fx.path, err))
		return
	}
	if dur > deadline {
		rt.OK = false
		rt.Error = fmt.Sprintf("exceeded route deadline of %s (took %s)", deadline, dur)
		rep.Routes = append(rep.Routes, rt)
		rep.addErr("deadline", fx.path, rt.Error)
		return
	}
	if resp.Status >= 400 {
		if msg := checkErrorShape(resp.Body); msg != "" {
			rt.OK = false
			rt.Error = msg
			rep.Routes = append(rep.Routes, rt)
			rep.addErr("error_shape", fx.path, msg)
			return
		}
		rt.OK = false
		rt.Error = fmt.Sprintf("returned error status %d", resp.Status)
		rep.Routes = append(rep.Routes, rt)
		rep.addErr("routes", fx.path, fmt.Sprintf("%s %s returned status %d", fx.method, fx.path, resp.Status))
		return
	}
	if fx.validate != nil {
		if msg := fx.validate(resp); msg != "" {
			rt.OK = false
			rt.Error = msg
			rep.Routes = append(rep.Routes, rt)
			rep.addErr("routes", fx.path, msg)
			return
		}
	}
	rep.Routes = append(rep.Routes, rt)
}

func decodeResponseBody(raw json.RawMessage, dst any) error {
	if len(raw) == 0 {
		return fmt.Errorf("empty response body")
	}
	// Hosted JS plugins often return body as a JSON-encoded string or as a direct object.
	var asStr string
	if json.Unmarshal(raw, &asStr) == nil {
		return json.Unmarshal([]byte(asStr), dst)
	}
	return json.Unmarshal(raw, dst)
}

func unwrapResponseBytes(raw json.RawMessage) ([]byte, error) {
	if len(raw) == 0 {
		return nil, fmt.Errorf("empty response body")
	}
	var asStr string
	if json.Unmarshal(raw, &asStr) == nil {
		return []byte(asStr), nil
	}
	return raw, nil
}

// checkErrorShape enforces SPEC §5.1.2: `{ "error": { "code", "message" } }`.
func checkErrorShape(raw json.RawMessage) string {
	b, err := unwrapResponseBytes(raw)
	if err != nil {
		return "error response must have a JSON body"
	}
	var env struct {
		Error *struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(b, &env); err != nil || env.Error == nil {
		return `error response must use shape {"error":{"code":"...","message":"..."}}`
	}
	if strings.TrimSpace(env.Error.Code) == "" || strings.TrimSpace(env.Error.Message) == "" {
		return `error response must have non-empty "error.code" and "error.message"`
	}
	return ""
}

func validateViewResponse(rep *Report, field string, raw json.RawMessage, stringsByLocale map[string]map[string]string) string {
	b, err := unwrapResponseBytes(raw)
	if err != nil {
		return "view response is empty"
	}
	var envelope struct {
		Spec json.RawMessage `json:"spec"`
		Data json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(b, &envelope); err != nil {
		return "view response must be a JSON object with {spec, data}: " + err.Error()
	}
	if len(envelope.Spec) == 0 {
		return "view response is missing 'spec'"
	}
	res := viewspec.ValidatePlugin(envelope.Spec)
	if !res.OK {
		for _, e := range res.Errors {
			rep.addErr("viewspec", field, e)
		}
		return "ViewSpec validation failed: " + strings.Join(res.Errors, "; ")
	}
	var specDoc any
	if json.Unmarshal(envelope.Spec, &specDoc) == nil {
		checkViewSpecStrings(rep, field, specDoc, "", stringsByLocale)
	}
	return ""
}

// userVisibleSpecFields lists ViewSpec properties that hold user-visible labels
// and therefore must use `$t:key` (or a `{$: "path"}` binding), never raw literals
// (SPEC §5.6: "Every label in a plugin document is a $t:key ... Literal strings
// are rejected by the kit for user-visible fields").
var userVisibleSpecFields = map[string]bool{
	"title":             true,
	"description":       true,
	"label":             true,
	"header":            true, // table column header when string
	"content":           true, // heading / text block content
	"text":              true, // empty / emptyRow text
	"searchPlaceholder": true,
	"placeholder":       true,
	"submitLabel":       true,
	"openLabel":         true,
	"empty":             true, // when string prop on posterGrid/posterShelf
}

func checkViewSpecStrings(rep *Report, viewField string, node any, docPath string, stringsByLocale map[string]map[string]string) {
	switch t := node.(type) {
	case map[string]any:
		keys := make([]string, 0, len(t))
		for k := range t {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			v := t[k]
			p := k
			if docPath != "" {
				p = docPath + "." + k
			}
			if s, isStr := v.(string); isStr && userVisibleSpecFields[k] {
				if !strings.HasPrefix(s, "$t:") {
					rep.addErr("viewspec_copy", fmt.Sprintf("%s:%s", viewField, p),
						fmt.Sprintf("user-visible field %q must use a $t:key reference or field binding, not literal string %q", k, s))
				} else {
					key := strings.TrimPrefix(s, "$t:")
					max := 80
					if k == "label" || k == "header" || k == "submitLabel" || k == "openLabel" {
						max = 40
					}
					checkLocalizedRef(rep, fmt.Sprintf("%s:%s", viewField, p), key, max, false, stringsByLocale)
				}
				continue
			}
			checkViewSpecStrings(rep, viewField, v, p, stringsByLocale)
		}
	case []any:
		for i, item := range t {
			checkViewSpecStrings(rep, viewField, item, fmt.Sprintf("%s[%d]", docPath, i), stringsByLocale)
		}
	}
}

func sampleInputForSchema(raw json.RawMessage) json.RawMessage {
	var schema map[string]any
	if json.Unmarshal(raw, &schema) != nil {
		return json.RawMessage(`{}`)
	}
	props, _ := schema["properties"].(map[string]any)
	out := map[string]any{}
	for name, pRaw := range props {
		pm, ok := pRaw.(map[string]any)
		if !ok {
			continue
		}
		switch pm["type"] {
		case "string":
			out[name] = "test"
		case "number", "integer":
			out[name] = 1
		case "boolean":
			out[name] = true
		case "array":
			out[name] = []any{}
		case "object":
			out[name] = map[string]any{}
		}
	}
	b, _ := json.Marshal(out)
	return b
}

// checkHotPath validates determinism and budget on a generated 10,000-candidate
// corpus (SPEC §5.2, WS11g).
func checkHotPath(ctx context.Context, rep *Report, man plugins.Manifest, p1 *wasm.Plugin) {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	const corpusSize = 10000
	const batchSize = 25

	if man.HasCapability(plugins.CapRankingStep) && !p1.HasRankKey() {
		rep.addErr("hot_path", "rank_key", "plugin declares ranking_step capability but does not export rank_key")
		return
	}
	if man.HasCapability(plugins.CapRuleCondition) && !p1.HasCondMatch() {
		rep.addErr("hot_path", "cond_match", "plugin declares rule_condition capability but does not export cond_match")
		return
	}

	corpusA, corpusB := sharedCorpora()

	ht := &HotTiming{
		Candidates:    corpusSize,
		Batches:       corpusSize / batchSize,
		Deterministic: true,
	}
	rep.HotPath = ht

	if p1.HasRankKey() {
		var batchDurs []float64
		scoresA := make([]int64, 0, corpusSize)
		cfg := json.RawMessage(`{"language":"en"}`)

		t0 := time.Now()
		for b := 0; b < corpusSize; b += batchSize {
			batch := corpusA[b : b+batchSize]
			bt0 := time.Now()
			sc, out, err := rankWithRetries(ctx, p1, batch, cfg)
			bdur := float64(time.Since(bt0).Microseconds()) / 1000.0
			batchDurs = append(batchDurs, bdur)
			if bdur > ht.MaxBatchMS {
				ht.MaxBatchMS = bdur
			}
			ht.SlowCount += out.SlowCandidates
			if err != nil || out.SlowCandidates != 0 {
				rep.addErr("hot_path", "rank_key", fmt.Sprintf("batch %d exceeded budget or failed: %v", b/batchSize, err))
				return
			}

			scoresA = append(scoresA, sc...)
		}
		ht.TotalMS = float64(time.Since(t0).Microseconds()) / 1000.0
		sort.Float64s(batchDurs)
		if len(batchDurs) > 0 {
			p95Batch := batchDurs[int(float64(len(batchDurs)-1)*0.95)]
			ht.PerCandP95MS = p95Batch / float64(batchSize)
		}
		if ht.SlowCount > corpusSize/100 {
			rep.addErr("hot_path", "rank_key", fmt.Sprintf("%d of %d candidates exceeded the 2ms per-candidate or 50ms per-batch budget", ht.SlowCount, corpusSize))
		}

		// Different memo keys ensure that every candidate is executed again.
		for b := 0; b < corpusSize; b += batchSize {
			scB, outB, err := rankWithRetries(ctx, p1, corpusB[b:b+batchSize], cfg)
			if err != nil || outB.SlowCandidates != 0 {
				ht.Deterministic = false
				rep.addErr("hot_path", "rank_key", fmt.Sprintf("second pass batch %d failed or exceeded budget: %v", b/batchSize, err))
				return
			}
			for i, got := range scB {
				if got != scoresA[b+i] {
					ht.Deterministic = false
					rep.addErr("determinism", "rank_key", fmt.Sprintf("nondeterministic rank_key at candidate %d: first pass returned %d, second pass returned %d", b+i, scoresA[b+i], got))
					return
				}
			}
		}

	}

	if p1.HasCondMatch() {
		op := "is"
		if len(man.Ether.Contributes.RuleConditions) > 0 && len(man.Ether.Contributes.RuleConditions[0].Operators) > 0 {
			op = man.Ether.Contributes.RuleConditions[0].Operators[0]
		}
		val := json.RawMessage(`"en"`)
		// Adjacent duplicate facts detect stateful hooks, with no memoization.
		for b := 0; b < corpusSize; b += batchSize {
			candidates := make([]wasm.Candidate, 0, 2*batchSize)
			for i := b; i < b+batchSize; i++ {
				candidates = append(candidates, corpusA[i], corpusB[i])
			}
			results, out, err := condBatchWithRetries(ctx, p1, candidates, op, val)
			if err != nil || out.SlowCandidates != 0 {
				rep.addErr("hot_path", "cond_match", fmt.Sprintf("condition batch %d exceeded budget or failed: %v", b/batchSize, err))
				return
			}
			for i := 0; i < len(results); i += 2 {
				if results[i] != results[i+1] {
					ht.Deterministic = false
					rep.addErr("determinism", "cond_match", fmt.Sprintf("nondeterministic cond_match at candidate %d: %v vs %v", b+i/2, results[i], results[i+1]))
					return
				}
			}
		}

	}
}

// A transient scheduler or GC pause can exhaust a deadline; retry with fresh
// memo keys so only repeatable budget violations fail the check.
func rankWithRetries(ctx context.Context, p *wasm.Plugin, batch []wasm.Candidate, cfg json.RawMessage) ([]int64, wasm.Outcome, error) {
	var scores []int64
	var out wasm.Outcome
	var err error
	for attempt := 0; attempt < 30; attempt++ {
		fresh := append([]wasm.Candidate(nil), batch...)
		for i := range fresh {
			fresh[i].ReleaseID += fmt.Sprintf("-try-%d", attempt)
		}
		scores, out, err = p.RankBatch(ctx, fresh, cfg)
		if err == nil && out.SlowCandidates == 0 {
			return scores, out, nil
		}
		select {
		case <-ctx.Done():
			return scores, out, ctx.Err()
		case <-time.After(20 * time.Millisecond):
		}
	}
	return scores, out, err
}
func condBatchWithRetries(ctx context.Context, p *wasm.Plugin, cands []wasm.Candidate, op string, val json.RawMessage) ([]bool, wasm.Outcome, error) {
	var result []bool
	var out wasm.Outcome
	var err error
	for attempt := 0; attempt < 30; attempt++ {
		result, out, err = p.CondBatch(ctx, cands, op, val)
		if err == nil && out.SlowCandidates == 0 {
			return result, out, nil
		}
		select {
		case <-ctx.Done():
			return result, out, ctx.Err()
		case <-time.After(20 * time.Millisecond):
		}
	}
	return result, out, err
}

var (
	corpusOnce sync.Once
	cachedA    []wasm.Candidate
	cachedB    []wasm.Candidate
)

func sharedCorpora() ([]wasm.Candidate, []wasm.Candidate) {
	corpusOnce.Do(func() {
		cachedA = generateCorpus("runA", 10000, 10000)
		cachedB = generateCorpus("runB", 10000, 10000)
	})
	return cachedA, cachedB
}

func generateCorpus(prefix string, n, unique int) []wasm.Candidate {
	resolutions := []string{"2160p", "1080p", "720p"}
	groups := []string{"FLUX", "NTb", "FRAMESTOR", "GRP"}
	langs := []string{`["en"]`, `["en","fr"]`, `["ja","en"]`, `["de"]`}

	rawFacts := make([]json.RawMessage, unique)
	for i := 0; i < unique; i++ {
		rawFacts[i] = json.RawMessage(fmt.Sprintf(
			`{"releaseId":"candidate-%d","resolution":%q,"languages":%s,"group":%q,"cached":%t}`,
			i, resolutions[i%len(resolutions)],
			langs[i%len(langs)],
			groups[i%len(groups)],
			i%2 == 0,
		))
	}

	out := make([]wasm.Candidate, n)
	for i := 0; i < n; i++ {
		u := i % unique
		out[i] = wasm.Candidate{
			ReleaseID: fmt.Sprintf("%s-%d", prefix, i),
			Facts:     rawFacts[u],
		}
	}
	return out
}

var _ = utf8.RuneCountInString
