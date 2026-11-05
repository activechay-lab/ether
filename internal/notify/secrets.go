package notify

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"slices"
	"sort"

	"github.com/braedonsaunders/ether/internal/config"
)

// Channel secrets are sealed inside the stored channel JSON
// (config.FieldSealer). Each is bound to the channel's id and kind; the
// secrets other than the endpoint itself are also bound to the endpoint
// they are sent to, so a stored channel repointed elsewhere cannot use
// them. Endpoints are secrets too: a webhook URL is itself a credential.

// errSecretNeedsDestination is returned by Save when a redacted secret
// would be carried to a different endpoint or kind.
var errSecretNeedsDestination = errors.New("re-enter the channel's token and password when changing its endpoint or kind")

// destination is the value other secrets are bound to: the endpoint they
// are sent to. Channels without an endpoint secret bind to their kind.
func destination(c Channel) string {
	if ep := c.Secret("endpoint"); ep != "" {
		return ep
	}
	return c.Kind
}

func secretContext(c Channel, key string) []string {
	ctx := []string{"notify_channels", "secrets." + key, c.ID, c.Kind}
	if key != "endpoint" {
		ctx = append(ctx, destination(c))
	}
	return ctx
}

func sortedKeys(m map[string]string) []string {
	return append([]string{}, slices.Sorted(maps.Keys(m))...)
}

// sealChannel returns the stored form of c.
func (s *Service) sealChannel(c Channel) (Channel, error) {
	// Contexts bind to the plaintext endpoint, so compute them before any
	// value is sealed.
	ctxs := map[string][]string{}
	for k := range c.Secrets {
		ctxs[k] = secretContext(c, k)
	}
	c = c.clone()
	for _, k := range sortedKeys(c.Secrets) {
		v, err := config.SealWith(s.opts.Sealer, c.Secrets[k], ctxs[k]...)
		if err != nil {
			return Channel{}, fmt.Errorf("notify: seal channel %s: %w", c.ID, err)
		}
		c.Secrets[k] = v
	}
	return c, nil
}

// openChannel reverses sealChannel; legacy reports plaintext secrets.
func (s *Service) openChannel(c Channel) (Channel, bool, error) {
	c = c.clone()
	legacy := false
	// The endpoint opens first: the other secrets are bound to its
	// plaintext.
	keys := sortedKeys(c.Secrets)
	sort.SliceStable(keys, func(i, j int) bool { return keys[i] == "endpoint" && keys[j] != "endpoint" })
	for _, k := range keys {
		v, l, err := config.OpenWith(s.opts.Sealer, c.Secrets[k], secretContext(c, k)...)
		if err != nil {
			return Channel{}, false, fmt.Errorf("notify: channel %s: %w", c.ID, err)
		}
		c.Secrets[k], legacy = v, legacy || l
	}
	return c, legacy, nil
}

func (s *Service) encodeChannel(c Channel) (string, error) {
	sealed, err := s.sealChannel(c)
	if err != nil {
		return "", err
	}
	raw, err := json.Marshal(sealed)
	return string(raw), err
}

// decodeChannel reads one stored channel. A row in the pre-registry flat
// shape is converted and reported as legacy so MigrateSecrets rewrites it
// in the new shape; the flat shape is never written again.
func (s *Service) decodeChannel(raw string) (Channel, bool, error) {
	var c Channel
	if err := json.Unmarshal([]byte(raw), &c); err != nil {
		return Channel{}, false, err
	}
	return s.openChannel(c)
}

// decodeMigrationChannel is used only by the startup conversion, never delivery.
func (s *Service) decodeMigrationChannel(raw string) (Channel, bool, error) {
	var probe map[string]json.RawMessage
	if err := json.Unmarshal([]byte(raw), &probe); err != nil {
		return Channel{}, false, err
	}
	if isLegacyChannel(probe) {
		c, err := s.decodeLegacyChannel(raw)
		return c, true, err
	}
	return s.decodeChannel(raw)
}

var legacyChannelKeys = []string{"smtpMode", "subscription", "endpoint", "token", "target", "username", "password", "from", "to"}

func isLegacyChannel(probe map[string]json.RawMessage) bool {
	if _, ok := probe["settings"]; ok {
		return false
	}
	if _, ok := probe["secrets"]; ok {
		return false
	}
	for _, k := range legacyChannelKeys {
		if _, ok := probe[k]; ok {
			return true
		}
	}
	return false
}

// legacyChannel is the flat channel shape stored before the registry. It
// exists only so MigrateSecrets can convert stored rows once.
type legacyChannel struct {
	SMTPMode      string            `json:"smtpMode,omitempty"`
	Subscription  *PushSubscription `json:"subscription,omitempty"`
	ID            string            `json:"id"`
	Name          string            `json:"name"`
	Kind          string            `json:"kind"`
	Enabled       bool              `json:"enabled"`
	Events        []string          `json:"events"`
	Endpoint      string            `json:"endpoint"`
	Token         string            `json:"token"`
	Target        string            `json:"target"`
	Username      string            `json:"username"`
	Password      string            `json:"password"`
	From          string            `json:"from"`
	To            string            `json:"to"`
	PerMinute     int               `json:"perMinute"`
	DigestMinutes int               `json:"digestMinutes"`
}

// decodeLegacyChannel opens a flat channel's secrets under their original
// contexts and returns it in the new shape.
func (s *Service) decodeLegacyChannel(raw string) (Channel, error) {
	var old legacyChannel
	if err := json.Unmarshal([]byte(raw), &old); err != nil {
		return Channel{}, err
	}
	open := func(value string, ctx ...string) (string, error) {
		v, _, err := config.OpenWith(s.opts.Sealer, value, ctx...)
		if err != nil {
			return "", fmt.Errorf("notify: channel %s: %w", old.ID, err)
		}
		return v, nil
	}
	base := []string{"notify_channels"}
	var err error
	if old.Endpoint, err = open(old.Endpoint, append(base, "endpoint", old.ID, old.Kind)...); err != nil {
		return Channel{}, err
	}
	for _, f := range []struct {
		name string
		p    *string
	}{{"token", &old.Token}, {"username", &old.Username}, {"password", &old.Password}} {
		if *f.p, err = open(*f.p, append(base, f.name, old.ID, old.Kind, old.Endpoint)...); err != nil {
			return Channel{}, err
		}
	}
	if old.Subscription != nil {
		sub := old.Subscription
		if sub.Endpoint, err = open(sub.Endpoint, append(base, "subscription.endpoint", old.ID, old.Kind)...); err != nil {
			return Channel{}, err
		}
		if sub.Keys.Auth, err = open(sub.Keys.Auth, append(base, "subscription.auth", old.ID, old.Kind, sub.Endpoint)...); err != nil {
			return Channel{}, err
		}
		if sub.Keys.P256dh, err = open(sub.Keys.P256dh, append(base, "subscription.p256dh", old.ID, old.Kind, sub.Endpoint)...); err != nil {
			return Channel{}, err
		}
	}
	c := Channel{ID: old.ID, Name: old.Name, Kind: old.Kind, Enabled: old.Enabled, Events: old.Events,
		PerMinute: old.PerMinute, DigestMinutes: old.DigestMinutes}
	set := func(m map[string]string) map[string]string {
		if m == nil {
			return map[string]string{}
		}
		return m
	}
	if old.Target != "" {
		c.Settings = set(c.Settings)
		c.Settings["target"] = old.Target
	}
	if old.SMTPMode != "" {
		c.Settings = set(c.Settings)
		c.Settings["smtpMode"] = old.SMTPMode
	}
	if old.From != "" {
		c.Settings = set(c.Settings)
		c.Settings["from"] = old.From
	}
	if old.To != "" {
		c.Settings = set(c.Settings)
		c.Settings["to"] = old.To
	}
	for k, v := range map[string]string{"endpoint": old.Endpoint, "token": old.Token, "username": old.Username, "password": old.Password} {
		if v != "" {
			c.Secrets = set(c.Secrets)
			c.Secrets[k] = v
		}
	}
	if old.Subscription != nil {
		c.SetSubscription(old.Subscription)
	}
	return c, nil
}

// keepChannelSecrets restores secrets the owner API sent back redacted,
// refusing to carry one to another endpoint or kind.
func keepChannelSecrets(c *Channel, old Channel) error {
	if c.Secrets == nil {
		c.Secrets = map[string]string{}
	}
	if c.Secret("endpoint") == Redacted {
		c.Secrets["endpoint"] = old.Secret("endpoint")
	}
	moved := c.Kind != old.Kind || c.Secret("endpoint") != old.Secret("endpoint")
	for k, v := range c.Secrets {
		if v != Redacted {
			continue
		}
		oldV := old.Secret(k)
		if moved && oldV != "" {
			return errSecretNeedsDestination
		}
		if oldV == "" {
			delete(c.Secrets, k)
		} else {
			c.Secrets[k] = oldV
		}
	}
	return nil
}

func pushKeyContext(public string) []string {
	return []string{"notify_push_keys", "private_key", "vapid", public}
}

// MigrateSecrets seals channel secrets stored in plaintext, rewrites flat
// pre-registry channel rows in the new {settings, secrets} shape, and seals
// the push identity. It returns how many rows it rewrote. A row is replaced
// only if it is unchanged since read, so it is safe to rerun on any node; a
// row that does not open is left untouched and reported in the error.
func (s *Service) MigrateSecrets(ctx context.Context) (int, error) {
	if s.db == nil {
		return 0, nil
	}
	rows, err := s.db.QueryContext(ctx, `SELECT id, config FROM notify_channels ORDER BY id`)
	if err != nil {
		return 0, err
	}
	type row struct{ id, raw string }
	var all []row
	for rows.Next() {
		var r row
		if err := rows.Scan(&r.id, &r.raw); err != nil {
			rows.Close()
			return 0, err
		}
		all = append(all, r)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return 0, err
	}
	n, failed := 0, 0
	var firstErr error
	for _, r := range all {
		c, legacy, err := s.decodeMigrationChannel(r.raw)
		if err != nil {
			failed++
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		raw, err := s.encodeChannel(c)
		if err != nil {
			return n, err
		}
		// Rewriting a legacy row converts its shape; rewriting a current
		// row would only re-seal the same values.
		if !legacy || raw == r.raw {
			continue
		}
		res, err := s.db.ExecContext(ctx, `UPDATE notify_channels SET config=? WHERE id=? AND config=?`, raw, r.id, r.raw)
		if err != nil {
			return n, err
		}
		if k, _ := res.RowsAffected(); k > 0 {
			n++
		}
	}
	var private, public string
	if s.opts.Sealer == nil {
		// Shape migration above does not need a key; sealing the push
		// identity does.
		if failed > 0 {
			return n, fmt.Errorf("notify: %d stored secret row(s) could not be opened and were left unchanged: %w", failed, firstErr)
		}
		return n, nil
	}
	err = s.db.QueryRowContext(ctx, `SELECT private_key, public_key FROM notify_push_keys WHERE id='vapid'`).Scan(&private, &public)
	switch {
	case err == nil:
		plain, legacy, oerr := config.OpenWith(s.opts.Sealer, private, pushKeyContext(public)...)
		if oerr != nil {
			failed++
			if firstErr == nil {
				firstErr = fmt.Errorf("notify: push identity: %w", oerr)
			}
			break
		}
		if !legacy {
			break
		}
		sealed, serr := config.SealWith(s.opts.Sealer, plain, pushKeyContext(public)...)
		if serr != nil {
			return n, serr
		}
		if sealed != private {
			res, uerr := s.db.ExecContext(ctx, `UPDATE notify_push_keys SET private_key=? WHERE id='vapid' AND private_key=?`, sealed, private)
			if uerr != nil {
				return n, uerr
			}
			if k, _ := res.RowsAffected(); k > 0 {
				n++
			}
		}
	case !errors.Is(err, sql.ErrNoRows):
		return n, err
	}
	if failed > 0 {
		return n, fmt.Errorf("notify: %d stored secret row(s) could not be opened and were left unchanged: %w", failed, firstErr)
	}
	return n, nil
}
