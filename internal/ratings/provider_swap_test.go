package ratings

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/braedonsaunders/ether/internal/config"
	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/store"
)

func TestSettingsProviderSwapHTTP(t *testing.T) {
	ctx := context.Background()
	db, err := store.OpenMemory(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	sealer, err := config.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := sealer.UpgradeCredentials(ctx); err != nil {
		t.Fatal(err)
	}
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		want := "synthetic-mdblist"
		if r.URL.Path == "/omdb" {
			want = "synthetic-omdb"
		}
		if r.URL.Query().Get("apikey") != want {
			t.Error("request used the wrong provider key")
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		_, _ = w.Write([]byte(`{"Response":"True","Ratings":[],"ratings":[]}`))
	}))
	defer provider.Close()
	opts := Options{Sealer: sealer, OMDbBaseURL: provider.URL + "/omdb", MDBListBaseURL: provider.URL + "/mdblist"}
	s := New(db, opts)
	cfg := Settings{Enabled: true, Provider: "omdb", APIKey: "synthetic-omdb", BackupProvider: "mdblist", BackupAPIKey: "synthetic-mdblist", CacheHours: 24}
	if err := s.SaveSettings(ctx, cfg); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		// These are the masked references returned to RatingSources by GET.
		get := httptest.NewRecorder()
		s.Router(nil).ServeHTTP(get, httptest.NewRequest(http.MethodGet, "/settings", nil))
		var masked Settings
		if err := json.Unmarshal(get.Body.Bytes(), &masked); err != nil {
			t.Fatal(err)
		}
		masked.Provider, masked.BackupProvider = masked.BackupProvider, masked.Provider
		body, _ := json.Marshal(masked)
		put := httptest.NewRecorder()
		s.Router(nil).ServeHTTP(put, httptest.NewRequest(http.MethodPut, "/settings", bytes.NewReader(body)))
		if put.Code != http.StatusOK {
			t.Fatalf("swap %d: HTTP %d", i, put.Code)
		}
		if strings.Contains(put.Body.String(), "synthetic-") {
			t.Fatal("HTTP response exposed a key")
		}
		var result Settings
		if err := json.Unmarshal(put.Body.Bytes(), &result); err != nil {
			t.Fatal(err)
		}
		if result.Provider != masked.Provider || result.BackupProvider != masked.BackupProvider {
			t.Fatal("response did not reflect the saved provider order")
		}
		if result.APIKey != Redacted || result.BackupAPIKey != Redacted {
			t.Fatal("response keys not masked")
		}
		// Reconstruct the service, then open both ciphertexts in their NEW contexts.
		s = New(db, opts)
		plain, err := s.settings(ctx)
		if err != nil {
			t.Fatal(err)
		}
		keys := map[string]string{"omdb": "synthetic-omdb", "mdblist": "synthetic-mdblist"}
		if plain.Provider != masked.Provider || plain.BackupProvider != masked.BackupProvider || plain.APIKey != keys[plain.Provider] || plain.BackupAPIKey != keys[plain.BackupProvider] {
			t.Fatal("provider credentials changed during swap")
		}
		title := &core.Title{Type: core.TypeMovie, IDs: core.IDs{TMDB: 1, IMDB: "tt0000001"}}
		if _, _, err := s.fetchProvider(ctx, title, plain.Provider, plain.APIKey); err != nil {
			t.Fatal(err)
		}
		if _, _, err := s.fetchProvider(ctx, title, plain.BackupProvider, plain.BackupAPIKey); err != nil {
			t.Fatal(err)
		}
		raw := rawSettings(t, db)
		if strings.Contains(raw, "synthetic-") || strings.Count(raw, "ether-sealed:v2:") != 2 {
			t.Fatal("credentials not sealed")
		}
	}
}

func TestSettingsProviderKeyReuse(t *testing.T) {
	tests := []struct {
		name                  string
		change                func(*Settings)
		wantError             bool
		primaryKey, backupKey string
	}{
		{"same slots", func(v *Settings) {}, false, "synthetic-omdb", "synthetic-mdblist"},
		{"explicit replacement", func(v *Settings) { v.APIKey = "replacement" }, false, "replacement", "synthetic-mdblist"},
		{"explicit swap keys", func(v *Settings) {
			v.Provider, v.BackupProvider = "mdblist", "omdb"
			v.APIKey, v.BackupAPIKey = "new-mdb", "new-omdb"
		}, false, "new-mdb", "new-omdb"},
		{"clear disabled primary", func(v *Settings) { v.Enabled = false; v.APIKey = "" }, false, "", "synthetic-mdblist"},
		{"remove backup", func(v *Settings) { v.BackupProvider = ""; v.BackupAPIKey = "" }, false, "synthetic-omdb", ""},
		{"empty enabled primary", func(v *Settings) { v.APIKey = "" }, true, "", ""},
		{"empty backup key", func(v *Settings) { v.BackupAPIKey = "" }, true, "", ""},
		{"duplicate providers", func(v *Settings) { v.BackupProvider = v.Provider }, true, "", ""},
		{"invalid provider", func(v *Settings) { v.Provider = "unknown" }, true, "", ""},
		{"invalid after key resolution", func(v *Settings) { v.Provider, v.BackupProvider = v.BackupProvider, v.Provider; v.Audience = "invalid" }, true, "", ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			db, err := store.OpenMemory(ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()
			s := New(db, Options{})
			cfg := Settings{Enabled: true, Provider: "omdb", APIKey: "synthetic-omdb", BackupProvider: "mdblist", BackupAPIKey: "synthetic-mdblist", CacheHours: 24}
			if err := s.SaveSettings(ctx, cfg); err != nil {
				t.Fatal(err)
			}
			before := rawSettings(t, db)
			masked, err := s.Settings(ctx)
			if err != nil {
				t.Fatal(err)
			}
			tc.change(&masked)
			err = s.SaveSettings(ctx, masked)
			if (err != nil) != tc.wantError {
				t.Fatalf("save error: %v", err)
			}
			if tc.wantError {
				if rawSettings(t, db) != before {
					t.Fatal("failed save modified settings")
				}
				return
			}
			plain, err := s.settings(ctx)
			if err != nil {
				t.Fatal(err)
			}
			if plain.APIKey != tc.primaryKey || plain.BackupAPIKey != tc.backupKey {
				t.Fatal("incorrect key reuse")
			}
		})
	}
}

func TestSettingsSwapWithoutStoredProviderKey(t *testing.T) {
	ctx := context.Background()
	db, err := store.OpenMemory(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	s := New(db, Options{})
	cfg := Settings{Enabled: true, Provider: "omdb", APIKey: "synthetic-omdb", CacheHours: 24}
	if err := s.SaveSettings(ctx, cfg); err != nil {
		t.Fatal(err)
	}
	before := rawSettings(t, db)
	for _, v := range []Settings{
		{Enabled: true, Provider: "mdblist", APIKey: Redacted, CacheHours: 24},
		{Enabled: true, Provider: "omdb", APIKey: Redacted, BackupProvider: "mdblist", BackupAPIKey: Redacted, CacheHours: 24},
	} {
		if err := s.SaveSettings(ctx, v); err == nil {
			t.Fatal("unknown provider key reused")
		}
		if rawSettings(t, db) != before {
			t.Fatal("failed save changed settings")
		}
	}
}

// Fail the second seal to ensure the first new ciphertext is never committed alone.
type failBackupSeal struct{ config.FieldSealer }

func (s failBackupSeal) SealField(plain string, context ...string) (string, error) {
	if len(context) > 1 && context[1] == "backupApiKey" {
		return "", errors.New("synthetic seal failure")
	}
	return s.FieldSealer.SealField(plain, context...)
}

func TestSettingsSwapSealFailureIsAtomic(t *testing.T) {
	ctx := context.Background()
	db, err := store.OpenMemory(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	sealer, err := config.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := sealer.UpgradeCredentials(ctx); err != nil {
		t.Fatal(err)
	}
	s := New(db, Options{Sealer: sealer})
	cfg := Settings{Enabled: true, Provider: "omdb", APIKey: "synthetic-omdb", BackupProvider: "mdblist", BackupAPIKey: "synthetic-mdblist", CacheHours: 24}
	if err := s.SaveSettings(ctx, cfg); err != nil {
		t.Fatal(err)
	}
	before := rawSettings(t, db)
	masked, err := s.Settings(ctx)
	if err != nil {
		t.Fatal(err)
	}
	masked.Provider, masked.BackupProvider = masked.BackupProvider, masked.Provider
	s = New(db, Options{Sealer: failBackupSeal{sealer}})
	if err := s.SaveSettings(ctx, masked); err == nil {
		t.Fatal("seal failure ignored")
	}
	if rawSettings(t, db) != before {
		t.Fatal("seal failure partially changed settings")
	}
}

func TestConcurrentSettingsSwapsKeepProviderKeys(t *testing.T) {
	ctx := context.Background()
	db, err := store.OpenMemory(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	s := New(db, Options{})
	cfg := Settings{Enabled: true, Provider: "omdb", APIKey: "synthetic-omdb", BackupProvider: "mdblist", BackupAPIKey: "synthetic-mdblist", CacheHours: 24}
	if err := s.SaveSettings(ctx, cfg); err != nil {
		t.Fatal(err)
	}
	// Separate services model different callers; the settings row is replaced
	// atomically, with the existing last-writer-wins contract.
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			caller := New(db, Options{})
			v := cfg
			v.APIKey, v.BackupAPIKey = Redacted, Redacted
			if i%2 == 0 {
				v.Provider, v.BackupProvider = v.BackupProvider, v.Provider
			}
			if err := caller.SaveSettings(ctx, v); err != nil {
				t.Error(err)
				return
			}
			plain, err := caller.settings(ctx)
			if err != nil {
				t.Error(err)
				return
			}
			keys := map[string]string{"omdb": "synthetic-omdb", "mdblist": "synthetic-mdblist"}
			if plain.APIKey != keys[plain.Provider] || plain.BackupAPIKey != keys[plain.BackupProvider] {
				t.Error("concurrent save mixed provider credentials")
			}
		}(i)
	}
	wg.Wait()
}
