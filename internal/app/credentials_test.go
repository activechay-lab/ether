package app

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/braedonsaunders/ether/internal/cluster"
	"github.com/braedonsaunders/ether/internal/config"
	"github.com/braedonsaunders/ether/internal/notify"
	"github.com/braedonsaunders/ether/internal/ratings"
	"github.com/braedonsaunders/ether/internal/store"
	"github.com/braedonsaunders/ether/internal/trakt"
)

func credentialApp(t *testing.T) (*App, *sql.DB) {
	t.Helper()
	ctx := context.Background()
	db, err := store.OpenMemory(ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	cfg, err := config.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := cfg.Update(func(c *config.Config) error { c.Plex.Token = "plex-token-value"; return nil }); err != nil {
		t.Fatal(err)
	}
	a := &App{cfg: cfg, db: db, log: quietLog(),
		cluster: cluster.NewSolo(cluster.Local{Identity: cluster.Identity{NodeID: "self", Name: "self"}}, nil)}
	a.trakt = trakt.New(trakt.Options{DB: db, Sealer: cfg, Logger: quietLog()})
	a.notifications = notify.New(db, notify.Options{Sealer: cfg, Logger: quietLog()})
	a.ratings = ratings.New(db, ratings.Options{Sealer: cfg})
	// Rows as a release before sealing wrote them.
	if _, err := db.Exec(`INSERT INTO trakt_tokens(user_id, access_token, refresh_token, expires_at, username, updated_at) VALUES(1,'legacy-access','legacy-refresh',0,'u',0)`); err != nil {
		t.Fatal(err)
	}
	// Insert the pre-sealing representation directly; the current writer always seals.
	if _, err := db.Exec(`INSERT INTO ratings_settings(id,value) VALUES(1,'{"provider":"omdb","apiKey":"legacy-ratings-key","cacheHours":1}')`); err != nil {
		t.Fatal(err)
	}
	return a, db
}

func traktRow(t *testing.T, db *sql.DB, user int64) string {
	t.Helper()
	var access string
	if err := db.QueryRow(`SELECT access_token FROM trakt_tokens WHERE user_id=?`, user).Scan(&access); err != nil {
		t.Fatal(err)
	}
	return access
}

func TestCredentialPassSurfacesUnreadableRows(t *testing.T) {
	ctx := context.Background()
	a, db := credentialApp(t)
	// A row sealed under another key (a database copied between installs).
	other, err := config.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := other.UpgradeCredentials(ctx); err != nil {
		t.Fatal(err)
	}
	foreign, _ := other.SealField("foreign-access", "trakt_tokens", "access_token", "9")
	if _, err := db.Exec(`INSERT INTO trakt_tokens(user_id, access_token, refresh_token, expires_at, username, updated_at) VALUES(9,?, '', 0, 'u9', 0)`, foreign); err != nil {
		t.Fatal(err)
	}
	if err := a.cfg.UpgradeCredentials(ctx); err != nil {
		t.Fatal(err)
	}
	n, err := a.migrateStoredSecrets(ctx)
	if err == nil || !strings.Contains(err.Error(), "trakt") {
		t.Fatalf("unreadable row hidden: %d %v", n, err)
	}
	if strings.Contains(err.Error(), "foreign-access") {
		t.Fatal("error leaks a secret")
	}
	if traktRow(t, db, 9) != foreign || !config.IsSealed(traktRow(t, db, 1)) {
		t.Fatal("readable rows not sealed or unreadable row rewritten")
	}
}

func TestCheckCredentialKeyDetectsLostKey(t *testing.T) {
	ctx := context.Background()
	a, db := credentialApp(t)
	dir := filepath.Dir(a.cfg.Path())
	if !a.cfg.KeyCreated() {
		t.Fatal("fresh data directory should report a created key")
	}
	if err := a.checkCredentialKey(ctx); err != nil {
		t.Fatalf("plaintext rows with a new key: %v", err)
	}
	// The database holds a value sealed with a key this directory lost:
	// Open generated (and wrote) a new key.
	if _, err := db.Exec(`UPDATE trakt_tokens SET access_token='ether-sealed:v2:AAAA' WHERE user_id=1`); err != nil {
		t.Fatal(err)
	}
	generated, err := os.ReadFile(filepath.Join(dir, config.CredentialKeyFile))
	if err != nil {
		t.Fatal(err)
	}
	if err := a.checkCredentialKey(ctx); err == nil {
		t.Fatal("first start: database sealed with another key accepted")
	}
	// Second start: the generated key file now exists, so Open no longer
	// reports it as created. The refusal must persist.
	for start := 2; start <= 3; start++ {
		reopened, err := config.Open(dir)
		if err != nil {
			t.Fatal(err)
		}
		if reopened.KeyCreated() {
			t.Fatal("key reported as created again")
		}
		a.cfg = reopened
		if err := a.checkCredentialKey(ctx); err == nil {
			t.Fatalf("start %d accepted the generated key", start)
		}
	}
	// Nothing was deleted: the generated key and the sealed row remain.
	if b, err := os.ReadFile(filepath.Join(dir, config.CredentialKeyFile)); err != nil || string(b) != string(generated) {
		t.Fatal("generated key file changed")
	}
	if traktRow(t, db, 1) != "ether-sealed:v2:AAAA" {
		t.Fatal("sealed row changed")
	}
	// Restoring the complete backup (config.json and the original key;
	// here any other key) lifts the refusal and clears the marker.
	if err := os.Remove(filepath.Join(dir, config.CredentialKeyFile)); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(a.cfg.Path(), []byte(`{}`), 0o600); err != nil {
		t.Fatal(err)
	}
	original := make([]byte, 32)
	original[3] = 9
	if err := config.InstallCredentialKey(dir, original); err != nil {
		t.Fatal(err)
	}
	restored, err := config.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	a.cfg = restored
	if err := a.checkCredentialKey(ctx); err != nil {
		t.Fatalf("restored key refused: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, credentialKeyRejectedFile)); !os.IsNotExist(err) {
		t.Fatal("refusal marker kept after the key was replaced")
	}
}

func TestCheckCredentialKeyComparesRecordedFingerprint(t *testing.T) {
	ctx := context.Background()
	a, _ := credentialApp(t)
	if err := a.migrateCredentials(ctx); err != nil {
		t.Fatal(err)
	}
	var recorded string
	if err := a.db.QueryRow(`SELECT key_fingerprint FROM credentials_format WHERE id=1`).Scan(&recorded); err != nil || recorded != a.cfg.KeyFingerprint() {
		t.Fatalf("fingerprint not recorded: %q %v", recorded, err)
	}
	reopened, err := config.Open(filepath.Dir(a.cfg.Path()))
	if err != nil {
		t.Fatal(err)
	}
	a.cfg = reopened
	if err := a.checkCredentialKey(ctx); err != nil {
		t.Fatalf("same key refused: %v", err)
	}
	// Another data directory (another key) pointed at this database is
	// refused on every start, whether or not its key is new.
	other, err := config.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	a.cfg = other
	for i := 0; i < 2; i++ {
		if err := a.checkCredentialKey(ctx); err == nil {
			t.Fatal("database sealed with another key accepted")
		}
	}
}

// A v0.3.3 installation can have v1 config ciphertext beside plaintext DB
// tokens. Startup must preserve all values and a second pass must be a no-op.
func TestV033CredentialsUpgradePreservesSecrets(t *testing.T) {
	a, db := credentialApp(t)
	key := a.cfg.CredentialKey()
	block, err := aes.NewCipher(key)
	if err != nil {
		t.Fatal(err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		t.Fatal(err)
	}
	nonce := make([]byte, gcm.NonceSize())
	token := "ether-sealed:v1:" + base64.RawStdEncoding.EncodeToString(gcm.Seal(nonce, nonce, []byte("old-plex-token"), []byte("token")))
	raw, _ := json.Marshal(map[string]any{"plex": map[string]any{"token": token, "disableVersionTracking": true}, "usenet": map[string]any{"providers": []any{map[string]any{"id": "old", "host": "news.test", "username": "old-user", "password": "old-password"}}}})
	if err := os.WriteFile(a.cfg.Path(), raw, 0600); err != nil {
		t.Fatal(err)
	}
	a.cfg, err = config.Open(filepath.Dir(a.cfg.Path()))
	if err != nil {
		t.Fatal(err)
	}
	if c := a.cfg.Read(); c.Plex.Token != "old-plex-token" || c.Usenet.Providers[0].Password != "old-password" || c.Usenet.Providers[0].Username != "old-user" {
		t.Fatal("config secrets changed")
	}
	// Named blobs also used the v1 writer in v0.3.3.
	blob := "ether-sealed:v1:" + base64.RawStdEncoding.EncodeToString(gcm.Seal(nonce, nonce, []byte("old-cert"), []byte("identity/cert")))
	for name, value := range map[string]string{"identity/cert": blob, "plaintext-cert": "plain-cert"} {
		if _, err := db.Exec("INSERT INTO clusterconfig_secrets(name,version,value,updated_at) VALUES(?,1,?,0)", name, value); err != nil {
			t.Fatal(err)
		}
	}
	if err := a.migrateCredentialBlobs(t.Context()); err != nil {
		t.Fatal(err)
	}
	for name, want := range map[string]string{"identity/cert": "old-cert", "plaintext-cert": "plain-cert"} {
		var stored string
		if err := db.QueryRow("SELECT value FROM clusterconfig_secrets WHERE name=?", name).Scan(&stored); err != nil {
			t.Fatal(err)
		}
		if !strings.HasPrefix(stored, "ether-sealed:v2:") {
			t.Fatal("blob not converted")
		}
		if plain, err := a.cfg.Unseal(name, stored); err != nil || string(plain) != want {
			t.Fatalf("blob unreadable: %v", err)
		}
	}
	if err := a.migrateCredentialBlobs(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := a.migrateCredentials(t.Context()); err != nil {
		t.Fatal(err)
	}
	for column, want := range map[string]string{"access_token": "legacy-access", "refresh_token": "legacy-refresh"} {
		var stored string
		if err := db.QueryRow("SELECT " + column + " FROM trakt_tokens WHERE user_id=1").Scan(&stored); err != nil {
			t.Fatal(err)
		}
		if !strings.HasPrefix(stored, "ether-sealed:v2:") {
			t.Fatal("plaintext token remains")
		}
		plain, legacy, err := a.cfg.OpenField(stored, "trakt_tokens", column, "1")
		if err != nil || legacy || plain != want {
			t.Fatalf("%s unreadable: %v legacy=%v", column, err, legacy)
		}
	}
	if n, err := a.migrateStoredSecrets(t.Context()); err != nil || n != 0 {
		t.Fatalf("second pass: %d %v", n, err)
	}
}
