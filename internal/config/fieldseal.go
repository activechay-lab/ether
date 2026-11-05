package config

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
)

// Secrets stored outside config.json.
//
// Components that keep credentials in the database (Trakt tokens,
// notification channels, Live TV sources, indexer login cookies, ratings
// keys) seal them with SealField before writing and open them with
// OpenField after reading. A field is bound to the context its owner names
// (table, column, row identity and, for a credential sent somewhere, that
// destination), so a sealed value copied to another row, column or
// destination fails to open instead of authenticating there.
//
// All new database secrets are sealed in v2. Startup converts older rows.

// FieldSealer protects secrets stored outside config.json (*Store
// implements it).
type FieldSealer interface {
	SealField(plain string, context ...string) (string, error)
	OpenField(stored string, context ...string) (plain string, legacy bool, err error)
}

// ErrSealedWithoutKey is returned when a sealed field is read without a
// sealer.
var ErrSealedWithoutKey = errors.New("config: sealed value but no credential key")

// SealField protects plain for storage, bound to context. Empty values
// stay empty.
func (s *Store) SealField(plain string, context ...string) (string, error) {
	if plain == "" {
		return plain, nil
	}
	aead, err := newAEAD(s.credentialKey)
	if err != nil {
		return "", err
	}
	return sealWithAAD(aead, sealedPrefixV2, plain, fieldAAD(context))
}

// OpenField reverses SealField for the same context. A value stored
// unsealed (written before the upgrade) is returned as is with legacy set,
// so the caller can re-seal it. A sealed value that does not open — wrong
// key, damaged, or moved to another context — is an error.
func (s *Store) OpenField(stored string, context ...string) (string, bool, error) {
	if !strings.HasPrefix(stored, sealedMarker) {
		return stored, stored != "", nil
	}
	if !strings.HasPrefix(stored, sealedPrefixV2) {
		return "", false, fmt.Errorf("config: open %s: unsupported sealed format", fieldName(context))
	}
	aead, err := newAEAD(s.credentialKey)
	if err != nil {
		return "", false, err
	}
	plain, err := openWithAAD(aead, sealedPrefixV2, stored, fieldAAD(context))
	if err != nil {
		return "", false, fmt.Errorf("config: open %s: key mismatch, damaged value, or the value was moved to another record or destination", fieldName(context))
	}
	return plain, false, nil
}

// SealWith is s.SealField, or plain unchanged when s is nil (components
// built without a credential key, such as in tests).
func SealWith(s FieldSealer, plain string, context ...string) (string, error) {
	if s == nil {
		return plain, nil
	}
	return s.SealField(plain, context...)
}

// OpenWith is s.OpenField; with a nil s, a sealed value is
// ErrSealedWithoutKey and anything else is returned as is.
func OpenWith(s FieldSealer, stored string, context ...string) (string, bool, error) {
	if s == nil {
		if strings.HasPrefix(stored, sealedMarker) {
			return "", false, ErrSealedWithoutKey
		}
		return stored, stored != "", nil
	}
	return s.OpenField(stored, context...)
}

// IsSealed reports whether a stored value is sealed (any format).
func IsSealed(stored string) bool { return strings.HasPrefix(stored, sealedMarker) }

// fieldAAD is the additional data of a sealed field.
func fieldAAD(context []string) []byte { return contextAAD("ether-field:v2", context) }

// fieldName names a field in errors: its first two context parts (table
// and column by convention), never row identities or destinations.
func fieldName(context []string) string {
	if len(context) > 2 {
		context = context[:2]
	}
	return strings.Join(context, ".")
}

// FieldContext joins an int64 row identity for a field context.
func FieldContext(id int64) string { return strconv.FormatInt(id, 10) }

// KeyCreated reports whether Open generated this data directory's
// credential key, i.e. nothing stored before this start was sealed with
// it. Database rows sealed under another key mean the key file was lost.
func (s *Store) KeyCreated() bool { return s.keyCreated }

// UpgradeCredentials finishes startup conversion of cluster shared settings.
// Open already rewrote config.json atomically. Conflicts retry without changing
// stored values; interrupted conversions resume on the next startup/pass.
func (s *Store) UpgradeCredentials(ctx context.Context) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	s.mu.RLock()
	b, o := s.shared, s.sharedOpts
	s.mu.RUnlock()
	if b == nil {
		return nil
	}
	for attempt := 1; ; attempt++ {
		lctx, cancel := context.WithTimeout(ctx, o.Timeout)
		v, err := s.upgradeShared(lctx, b)
		cancel()
		if err == nil {
			if v > 0 {
				s.mu.Lock()
				if v > s.sharedVer {
					s.sharedVer = v
				}
				n := s.notifier
				s.mu.Unlock()
				if n != nil {
					_ = n.Publish(ctx, SharedTopic, []byte(strconv.FormatInt(v, 10)))
				}
			}
			return nil
		}
		if !errors.Is(err, ErrSharedConflict) || attempt >= maxSharedAttempts {
			return fmt.Errorf("config: upgrade shared credentials: %w", err)
		}
	}
}

// upgradeShared re-seals the stored shared document in v2 without
// changing its settings and returns the new version (0: nothing to do).
func (s *Store) upgradeShared(ctx context.Context, b SharedBackend) (int64, error) {
	doc, found, err := b.LoadShared(ctx)
	if err != nil || !found {
		return 0, err
	}
	if fp := s.KeyFingerprint(); doc.KeyFingerprint != fp {
		return 0, ErrKeyMismatch
	}
	plain, stats, err := openDocument([]byte(doc.Doc), s.credentialKey)
	if err != nil {
		return 0, err
	}
	if stats.strict && stats.plaintext == 0 {
		return 0, nil
	}
	sealed, err := sealDocument(plain, s.credentialKey)
	if err != nil {
		return 0, err
	}
	return b.SaveShared(ctx, SharedDoc{Doc: string(sealed), KeyFingerprint: doc.KeyFingerprint}, doc.Version)
}
