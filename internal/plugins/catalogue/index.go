package catalogue

import (
	"bytes"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/braedonsaunders/ether/internal/plugins"
	"go.yaml.in/yaml/v3"
)

// ErrSignatureInvalid is returned when index.json.sig does not verify
// against index.json and the configured ed25519 public key.
var ErrSignatureInvalid = errors.New("catalogue: index signature verification failed")

// Index is the signed catalogue index (index.json).
type Index struct {
	Schema      int     `json:"schema" yaml:"schema"`
	GeneratedAt string  `json:"generatedAt,omitempty" yaml:"generatedAt,omitempty"`
	Entries     []Entry `json:"entries" yaml:"entries"`
}

// Entry is one reviewed plugin in the catalogue.
type Entry struct {
	ID           string              `json:"id" yaml:"id"`
	Name         string              `json:"name" yaml:"name"`
	Summary      string              `json:"summary" yaml:"summary"`
	Description  string              `json:"description,omitempty" yaml:"description,omitempty"`
	Kind         string              `json:"kind" yaml:"kind"`
	Category     string              `json:"category,omitempty" yaml:"category,omitempty"`
	Icon         string              `json:"icon,omitempty" yaml:"icon"`
	Repo         string              `json:"repo,omitempty" yaml:"repo,omitempty"`
	Homepage     string              `json:"homepage,omitempty" yaml:"homepage,omitempty"`
	Author       string              `json:"author,omitempty" yaml:"author,omitempty"`
	License      string              `json:"license,omitempty" yaml:"license,omitempty"`
	Capabilities []string            `json:"capabilities,omitempty" yaml:"capabilities,omitempty"`
	Permissions  plugins.Permissions `json:"permissions,omitempty" yaml:"permissions,omitempty"`
	Screenshots  []string            `json:"screenshots,omitempty" yaml:"screenshots,omitempty"`
	Versions     []Version           `json:"versions" yaml:"versions"`
	Revoked      bool                `json:"revoked,omitempty" yaml:"revoked,omitempty"`
	Reason       string              `json:"reason,omitempty" yaml:"reason,omitempty"`
}

// Version is a published artifact version of a catalogue entry.
type Version struct {
	Version     string               `json:"version" yaml:"version"`
	URL         string               `json:"url" yaml:"url"`
	SHA256      string               `json:"sha256" yaml:"sha256"`
	ReleasedAt  string               `json:"releasedAt,omitempty" yaml:"releasedAt,omitempty"`
	MinEther    string               `json:"minEther,omitempty" yaml:"minEther,omitempty"`
	Permissions *plugins.Permissions `json:"permissions,omitempty" yaml:"permissions,omitempty"`
}

// Latest returns the highest semantic version for the entry, or the first
// version when none exist.
func (e Entry) Latest() (Version, bool) {
	if len(e.Versions) == 0 {
		return Version{}, false
	}
	best := e.Versions[0]
	for _, v := range e.Versions[1:] {
		if plugins.CompareSemVer(v.Version, best.Version) > 0 {
			best = v
		}
	}
	return best, true
}

// FindVersion returns a specific version or the latest when version is empty.
func (e Entry) FindVersion(version string) (Version, bool) {
	if version == "" {
		return e.Latest()
	}
	for _, v := range e.Versions {
		if v.Version == version {
			return v, true
		}
	}
	return Version{}, false
}

// ParsePublicKey decodes an ed25519 public key from base64 (standard or raw) or hex.
func ParsePublicKey(raw string) (ed25519.PublicKey, error) {
	s := strings.TrimSpace(raw)
	if s == "" {
		return nil, fmt.Errorf("catalogue: empty public key")
	}
	b, err := decodeBytes(s)
	if err != nil {
		return nil, fmt.Errorf("catalogue: decode public key: %w", err)
	}
	if len(b) != ed25519.PublicKeySize {
		return nil, fmt.Errorf("catalogue: invalid ed25519 public key length %d", len(b))
	}
	return ed25519.PublicKey(b), nil
}

// ParsePrivateKey decodes an ed25519 private key (32-byte seed or 64-byte key)
// from base64 or hex.
func ParsePrivateKey(raw string) (ed25519.PrivateKey, error) {
	s := strings.TrimSpace(raw)
	if s == "" {
		return nil, fmt.Errorf("catalogue: empty private key")
	}
	b, err := decodeBytes(s)
	if err != nil {
		return nil, fmt.Errorf("catalogue: decode private key: %w", err)
	}
	switch len(b) {
	case ed25519.SeedSize:
		return ed25519.NewKeyFromSeed(b), nil
	case ed25519.PrivateKeySize:
		return ed25519.PrivateKey(b), nil
	default:
		return nil, fmt.Errorf("catalogue: invalid ed25519 private key length %d", len(b))
	}
}

func decodeBytes(s string) ([]byte, error) {
	if len(s) == 64 || len(s) == 128 {
		if b, err := hex.DecodeString(s); err == nil {
			return b, nil
		}
	}
	for _, enc := range []*base64.Encoding{
		base64.StdEncoding,
		base64.RawStdEncoding,
		base64.URLEncoding,
		base64.RawURLEncoding,
	} {
		if b, err := enc.DecodeString(s); err == nil {
			return b, nil
		}
	}
	if b, err := hex.DecodeString(s); err == nil {
		return b, nil
	}
	return nil, fmt.Errorf("neither valid base64 nor hex")
}

// SignDetached signs indexJSON with priv and returns the base64-encoded
// detached signature suitable for index.json.sig.
func SignDetached(indexJSON []byte, priv ed25519.PrivateKey) string {
	sig := ed25519.Sign(priv, indexJSON)
	return base64.StdEncoding.EncodeToString(sig)
}

// VerifyDetached verifies a detached signature (base64 or hex) over indexJSON.
func VerifyDetached(indexJSON []byte, sigRaw []byte, pub ed25519.PublicKey) error {
	if len(pub) != ed25519.PublicKeySize {
		return fmt.Errorf("%w: invalid public key", ErrSignatureInvalid)
	}
	s := strings.TrimSpace(string(sigRaw))
	if s == "" {
		return fmt.Errorf("%w: empty signature", ErrSignatureInvalid)
	}
	sig, err := decodeBytes(s)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrSignatureInvalid, err)
	}
	if len(sig) != ed25519.SignatureSize || !ed25519.Verify(pub, indexJSON, sig) {
		return ErrSignatureInvalid
	}
	return nil
}

// ParseIndex decodes and validates index.json bytes.
func ParseIndex(indexJSON []byte) (Index, error) {
	var idx Index
	if err := json.Unmarshal(indexJSON, &idx); err != nil {
		return Index{}, fmt.Errorf("catalogue: parse index: %w", err)
	}
	if idx.Schema == 0 {
		idx.Schema = 1
	}
	if err := idx.Validate(); err != nil {
		return Index{}, err
	}
	return idx, nil
}

// ParseAndVerify verifies index.json against index.json.sig and parses the index.
func ParseAndVerify(indexJSON, sigRaw []byte, pub ed25519.PublicKey) (Index, error) {
	if err := VerifyDetached(indexJSON, sigRaw, pub); err != nil {
		return Index{}, err
	}
	return ParseIndex(indexJSON)
}

// Validate checks structural and copy rules on the index.
func (idx Index) Validate() error {
	if idx.Schema != 1 {
		return fmt.Errorf("catalogue: unsupported index schema %d", idx.Schema)
	}
	seen := make(map[string]bool, len(idx.Entries))
	for i, e := range idx.Entries {
		if err := e.Validate(); err != nil {
			return fmt.Errorf("catalogue: entries[%d] (%s): %w", i, e.ID, err)
		}
		if seen[e.ID] {
			return fmt.Errorf("catalogue: duplicate entry id %q", e.ID)
		}
		seen[e.ID] = true
	}
	return nil
}

// Validate checks a single catalogue entry.
func (e Entry) Validate() error {
	if strings.TrimSpace(e.ID) == "" {
		return fmt.Errorf("id is required")
	}
	if strings.TrimSpace(e.Name) == "" {
		return fmt.Errorf("name is required")
	}
	if strings.TrimSpace(e.Summary) == "" {
		return fmt.Errorf("summary is required")
	}
	if reason := plugins.LintCopy(e.Summary, 140); reason != "" {
		return fmt.Errorf("summary: %s", reason)
	}
	switch plugins.Kind(e.Kind) {
	case plugins.KindSource, plugins.KindIntegration, plugins.KindNotificationChannel,
		plugins.KindLiveTVSource, plugins.KindExtension, plugins.KindContent:
	default:
		return fmt.Errorf("unknown kind %q", e.Kind)
	}
	if e.Revoked && strings.TrimSpace(e.Reason) == "" {
		return fmt.Errorf("revoked entry must include a reason")
	}
	if e.Reason != "" {
		if reason := plugins.LintCopy(e.Reason, 240); reason != "" {
			return fmt.Errorf("reason: %s", reason)
		}
	}
	if !e.Revoked && len(e.Versions) == 0 {
		return fmt.Errorf("at least one version is required")
	}
	seenVer := make(map[string]bool, len(e.Versions))
	for j, v := range e.Versions {
		if _, ok := plugins.ParseSemVer(v.Version); !ok {
			return fmt.Errorf("versions[%d]: invalid version %q", j, v.Version)
		}
		if seenVer[v.Version] {
			return fmt.Errorf("duplicate version %q", v.Version)
		}
		seenVer[v.Version] = true
		if strings.TrimSpace(v.URL) == "" {
			return fmt.Errorf("versions[%d] (%s): url is required", j, v.Version)
		}
		if !isHexDigest(v.SHA256) {
			return fmt.Errorf("versions[%d] (%s): sha256 must be 64 hex characters", j, v.Version)
		}
	}
	return nil
}

func isHexDigest(s string) bool {
	s = strings.TrimSpace(s)
	if len(s) != 64 {
		return false
	}
	_, err := hex.DecodeString(s)
	return err == nil
}

// ParseEntryYAML parses a single plugin YAML file into an Entry.
func ParseEntryYAML(data []byte) (Entry, error) {
	var e Entry
	decoder := yaml.NewDecoder(bytes.NewReader(data))
	decoder.KnownFields(true)
	if err := decoder.Decode(&e); err != nil {
		return Entry{}, fmt.Errorf("catalogue: parse yaml: %w", err)
	}
	for i := range e.Versions {
		e.Versions[i].SHA256 = strings.ToLower(strings.TrimSpace(e.Versions[i].SHA256))
	}
	if err := e.Validate(); err != nil {
		return Entry{}, err
	}
	return e, nil
}

// LoadEntriesDir reads all *.yaml / *.yml files in dir and compiles an Index.
func LoadEntriesDir(dir string) (Index, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return Index{}, fmt.Errorf("catalogue: read dir %s: %w", dir, err)
	}
	out := make([]Entry, 0)
	for _, de := range entries {
		if de.IsDir() {
			continue
		}
		ext := strings.ToLower(filepath.Ext(de.Name()))
		if ext != ".yaml" && ext != ".yml" {
			continue
		}
		path := filepath.Join(dir, de.Name())
		raw, err := os.ReadFile(path) //nolint:gosec
		if err != nil {
			return Index{}, fmt.Errorf("catalogue: read %s: %w", path, err)
		}
		e, err := ParseEntryYAML(raw)
		if err != nil {
			return Index{}, fmt.Errorf("catalogue: %s: %w", de.Name(), err)
		}
		out = append(out, e)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	idx := Index{
		Schema:      1,
		GeneratedAt: time.Now().UTC().Format(time.RFC3339Nano),
		Entries:     out,
	}
	if err := idx.Validate(); err != nil {
		return Index{}, err
	}
	return idx, nil
}
