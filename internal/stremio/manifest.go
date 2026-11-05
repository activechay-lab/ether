package stremio

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/url"
	"strings"
)

// Manifest is a Stremio add-on manifest: the one model of it in Ether. A
// plugin manifest (internal/plugins) embeds it and adds the `ether` block,
// so a plain Stremio add-on and an Ether plugin share every Stremio field
// and every rule below.
type Manifest struct {
	ID            string        `json:"id"`
	Version       string        `json:"version"`
	Name          string        `json:"name"`
	Description   string        `json:"description,omitempty"`
	Logo          string        `json:"logo,omitempty"`
	Background    string        `json:"background,omitempty"`
	Types         []string      `json:"types,omitempty"`
	IDPrefixes    []string      `json:"idPrefixes,omitempty"`
	Resources     Resources     `json:"resources,omitempty"`
	Catalogs      []Catalog     `json:"catalogs,omitempty"`
	AddonCatalogs []Catalog     `json:"addonCatalogs,omitempty"`
	BehaviorHints BehaviorHints `json:"behaviorHints,omitzero"`
}

// Resource names: the protocol's routes.
const (
	ResourceCatalog      = "catalog"
	ResourceMeta         = "meta"
	ResourceStream       = "stream"
	ResourceSubtitles    = "subtitles"
	ResourceAddonCatalog = "addon_catalog"
)

// Content types Ether maps.
const (
	TypeMovie  = "movie"
	TypeSeries = "series"
	TypeTV     = "tv"
)

// Resource is one manifest resource. The protocol allows a bare name, which
// inherits the manifest's types and id prefixes, or an object with its
// own; Short remembers which, so the manifest round-trips unchanged.
type Resource struct {
	Name       string   `json:"name"`
	Types      []string `json:"types,omitempty"`
	IDPrefixes []string `json:"idPrefixes,omitempty"`
	Short      bool     `json:"-"`
}

// Resources is a manifest's resource list.
type Resources []Resource

// UnmarshalJSON accepts a bare resource name or a resource object.
func (r *Resource) UnmarshalJSON(b []byte) error {
	b = bytes.TrimSpace(b)
	if len(b) > 0 && b[0] == '"' {
		var name string
		if err := json.Unmarshal(b, &name); err != nil {
			return err
		}
		*r = Resource{Name: name, Short: true}
		return nil
	}
	type plain Resource
	var p plain
	if err := json.Unmarshal(b, &p); err != nil {
		return err
	}
	*r = Resource(p)
	return nil
}

// MarshalJSON writes the form the manifest used.
func (r Resource) MarshalJSON() ([]byte, error) {
	if r.Short {
		return json.Marshal(r.Name)
	}
	type plain Resource
	return json.Marshal(plain(r))
}

// UnmarshalJSON skips entries that are neither a name nor an object, so one
// malformed resource does not hide the rest of an add-on.
func (rs *Resources) UnmarshalJSON(b []byte) error {
	var raw []json.RawMessage
	if err := json.Unmarshal(b, &raw); err != nil {
		return err
	}
	out := make(Resources, 0, len(raw))
	for _, item := range raw {
		var r Resource
		if json.Unmarshal(item, &r) == nil && r.Name != "" {
			out = append(out, r)
		}
	}
	*rs = out
	return nil
}

// Catalog is one catalog (or add-on catalog) declaration.
type Catalog struct {
	Type   string   `json:"type"`
	ID     string   `json:"id"`
	Name   string   `json:"name,omitempty"`
	Genres []string `json:"genres,omitempty"`
	Extra  []Extra  `json:"extra,omitempty"`
	// ExtraSupported and ExtraRequired are the protocol's older way of
	// declaring extras; Extras reads both forms.
	ExtraSupported []string `json:"extraSupported,omitempty"`
	ExtraRequired  []string `json:"extraRequired,omitempty"`
}

// Extra is one extra property a catalog accepts (search, genre, skip…).
type Extra struct {
	Name         string   `json:"name"`
	IsRequired   bool     `json:"isRequired,omitempty"`
	Options      []string `json:"options,omitempty"`
	OptionsLimit int      `json:"optionsLimit,omitempty"`
}

// BehaviorHints is the manifest's behaviorHints. EpgURL (an XMLTV guide)
// and EPGProvider (dated guide catalogs) are extensions some `tv` add-ons
// use.
type BehaviorHints struct {
	Adult                 bool   `json:"adult,omitempty"`
	P2P                   bool   `json:"p2p,omitempty"`
	Configurable          bool   `json:"configurable,omitempty"`
	ConfigurationRequired bool   `json:"configurationRequired,omitempty"`
	EpgURL                string `json:"epgUrl,omitempty"`
	EPGProvider           bool   `json:"epgProvider,omitempty"`
}

// ErrConfigurationRequired is returned for an add-on installed from its
// unconfigured link.
var ErrConfigurationRequired = errors.New("the add-on must be configured first: open its configure page and paste the install link it gives you")

// Extras returns the catalog's extras in the current form, folding in the
// older extraSupported/extraRequired lists.
func (c Catalog) Extras() []Extra {
	out := append([]Extra(nil), c.Extra...)
	have := map[string]int{}
	for i, e := range out {
		have[e.Name] = i
	}
	for _, name := range c.ExtraSupported {
		if _, ok := have[name]; !ok && name != "" {
			have[name] = len(out)
			out = append(out, Extra{Name: name})
		}
	}
	for _, name := range c.ExtraRequired {
		if i, ok := have[name]; ok {
			out[i].IsRequired = true
		} else if name != "" {
			have[name] = len(out)
			out = append(out, Extra{Name: name, IsRequired: true})
		}
	}
	return out
}

// ExtraNamed returns the catalog's extra called name.
func (c Catalog) ExtraNamed(name string) (Extra, bool) {
	for _, e := range c.Extras() {
		if e.Name == name {
			return e, true
		}
	}
	return Extra{}, false
}

// SupportsSkip reports whether the catalog pages with skip. `tv` catalogs
// page by convention even when they do not declare it.
func (c Catalog) SupportsSkip() bool {
	_, ok := c.ExtraNamed("skip")
	return ok || c.Type == TypeTV
}

// Key is a stable identifier for a catalog within one add-on.
func (c Catalog) Key() string {
	hash := sha256.Sum256([]byte(c.Type + "\x00" + c.ID))
	return "catalog-" + hex.EncodeToString(hash[:8])
}

// Resource returns the named resource with inherited types and prefixes
// resolved: a bare name takes the manifest's; an object keeps its own, and
// an object without idPrefixes accepts every id.
func (m Manifest) Resource(name string) (Resource, bool) {
	for _, r := range m.Resources {
		if r.Name != name {
			continue
		}
		if r.Short {
			return Resource{Name: name, Types: m.Types, IDPrefixes: m.IDPrefixes, Short: true}, true
		}
		return r, true
	}
	return Resource{}, false
}

// HasResource reports whether the add-on declares the named resource.
func (m Manifest) HasResource(name string) bool {
	_, ok := m.Resource(name)
	return ok
}

// Serves reports whether the named resource answers content type typ.
func (m Manifest) Serves(name, typ string) bool {
	r, ok := m.Resource(name)
	return ok && (len(r.Types) == 0 || contains(r.Types, typ))
}

// TakesID reports whether the named resource accepts id: catalog routes
// match a declared catalog; the rest match the resource's id prefixes (none
// declared accepts every id).
func (m Manifest) TakesID(name, typ, id string) bool {
	switch name {
	case ResourceCatalog, ResourceAddonCatalog:
		if !m.HasResource(name) {
			return false
		}
		cats := m.Catalogs
		if name == ResourceAddonCatalog {
			cats = m.AddonCatalogs
		}
		for _, c := range cats {
			if c.Type == typ && c.ID == id {
				return true
			}
		}
		return false
	}
	r, ok := m.Resource(name)
	if !ok || (len(r.Types) > 0 && !contains(r.Types, typ)) {
		return false
	}
	if len(r.IDPrefixes) == 0 {
		return true
	}
	for _, p := range r.IDPrefixes {
		if p != "" && strings.HasPrefix(id, p) {
			return true
		}
	}
	return false
}

// takesPrefix reports whether the named resource looks titles up by ids of
// one database ("tt", "tmdb", "kitsu"). An add-on that declares no prefixes
// is asked by IMDb id only: that is what such add-ons answer in practice,
// and asking them by other ids only spends their rate limits.
func (m Manifest) takesPrefix(name, prefix string) bool {
	r, ok := m.Resource(name)
	if !ok {
		return false
	}
	if len(r.IDPrefixes) == 0 {
		return prefix == "tt"
	}
	for _, p := range r.IDPrefixes {
		if strings.EqualFold(strings.TrimSuffix(strings.TrimSpace(p), ":"), prefix) {
			return true
		}
	}
	return false
}

// CatalogsOfType returns the catalogs of one content type.
func (m Manifest) CatalogsOfType(typ string) []Catalog {
	var out []Catalog
	for _, c := range m.Catalogs {
		if c.Type == typ {
			out = append(out, c)
		}
	}
	return out
}

// CatalogNamed returns the catalog of typ with id.
func (m Manifest) CatalogNamed(typ, id string) (Catalog, bool) {
	for _, c := range m.Catalogs {
		if c.Type == typ && c.ID == id {
			return c, true
		}
	}
	return Catalog{}, false
}

// Valid reports whether m looks like an add-on manifest at all.
func (m Manifest) Valid() bool {
	return m.ID != "" && (len(m.Resources) > 0 || len(m.Catalogs) > 0)
}

// CheckStreams reports why Ether cannot search the add-on for streams, or
// nil.
func (m Manifest) CheckStreams() error {
	if m.BehaviorHints.ConfigurationRequired {
		return ErrConfigurationRequired
	}
	if !m.HasResource(ResourceStream) {
		return errors.New("the add-on does not provide streams")
	}
	if !m.Serves(ResourceStream, TypeMovie) && !m.Serves(ResourceStream, TypeSeries) {
		return errors.New("the add-on does not provide movie or series streams")
	}
	for _, prefix := range append([]string{"tt", "tmdb"}, animeDatabases...) {
		if m.takesPrefix(ResourceStream, prefix) {
			return nil
		}
	}
	return errors.New("the add-on does not look titles up by IMDb, TMDB, Kitsu, MyAnimeList, AniList or AniDB id")
}

// GuideURL returns the add-on's XMLTV guide URL, if it declares one.
func (m Manifest) GuideURL() string { return m.BehaviorHints.EpgURL }

// NormalizeURL turns an install link into the add-on's base URL: a
// stremio:// link becomes https://, a trailing /manifest.json or /configure
// is removed, and the query (some add-ons keep their configuration there)
// is kept. User info and fragments are dropped.
func NormalizeURL(raw string) (*url.URL, error) {
	raw = strings.TrimSpace(raw)
	if rest, ok := strings.CutPrefix(raw, "stremio://"); ok {
		raw = "https://" + rest
	}
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return nil, errors.New("stremio: the add-on link must be an http(s) or stremio:// install link")
	}
	u.Fragment, u.RawFragment, u.User = "", "", nil
	p := strings.TrimRight(u.EscapedPath(), "/")
	for _, suffix := range []string{"/manifest.json", "/configure"} {
		p = strings.TrimSuffix(p, suffix)
	}
	u.RawPath, u.Path = "", ""
	if p != "" {
		if u.Path, err = url.PathUnescape(p); err != nil {
			return nil, errors.New("stremio: the add-on link's path is not valid")
		}
		u.RawPath = p
	}
	u.Scheme, u.Host = strings.ToLower(u.Scheme), strings.ToLower(u.Host)
	return u, nil
}

// ManifestURL is the manifest address of a normalised base URL.
func ManifestURL(base *url.URL) string {
	return ResourceURL(base, "/manifest.json")
}

// ResourceURL joins a route path ("/stream/movie/tt1.json") to a base URL,
// keeping the base's escaping and query.
func ResourceURL(base *url.URL, route string) string {
	u := *base
	u.Path += route
	if u.RawPath != "" {
		u.RawPath += route
	}
	return u.String()
}

// Origin is the base URL's scheme://host[:port].
func Origin(base *url.URL) string {
	return (&url.URL{Scheme: base.Scheme, Host: base.Host}).String()
}

// Fingerprint identifies one configured add-on: its normalised link. Two
// configurations of the same add-on differ; the same link pasted on two
// pages is the same add-on.
func Fingerprint(base *url.URL) string {
	sum := sha256.Sum256([]byte(base.String()))
	return hex.EncodeToString(sum[:])
}

func contains(list []string, want string) bool {
	for _, v := range list {
		if v == want {
			return true
		}
	}
	return false
}
