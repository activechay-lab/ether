package livetv

import (
	"context"
	"testing"

	"github.com/braedonsaunders/ether/internal/config"
	"github.com/braedonsaunders/ether/internal/debrid"
	"github.com/braedonsaunders/ether/internal/newznab"
	"github.com/braedonsaunders/ether/internal/usenet"
)

func TestDisabledSourceManagers(t *testing.T) {
	for _, kind := range []string{"usenet", "debrid", "newznab", "torznab", "cardigann", "stremio", "zilean", "prowlarr", "m3u", "xmltv", "xtream", "addon"} {
		t.Run(kind, func(t *testing.T) {
			switch kind {
			case "usenet":
				f := usenet.New([]config.UsenetProvider{{ID: "off", Host: "invalid", Enabled: false}}, usenet.Options{})
				defer f.Close()
				if len(f.Stats()) != 0 {
					t.Fatal("disabled provider acquired a pool")
				}
			case "debrid":
				s := debrid.NewService(config.Debrid{Providers: []config.DebridProvider{{ID: "off", Enabled: false}}}, debrid.Options{NewProvider: func(config.DebridProvider, debrid.ProviderOptions) (debrid.Provider, error) {
					t.Fatal("constructed disabled provider")
					return nil, nil
				}})
				if s.Enabled() {
					t.Fatal("disabled debrid is available")
				}
			case "m3u", "xmltv", "xtream", "addon":
				s := testService(t, Options{})
				src := Source{ID: "off", Name: "Off", Disabled: true, PlaylistURL: "https://invalid/list"}
				b, err := s.encodeSource(src)
				if err != nil {
					t.Fatal(err)
				}
				if _, err = s.o.DB.ExecContext(context.Background(), `INSERT INTO livetv_sources(id,data) VALUES(?,?)`, src.ID, b); err != nil {
					t.Fatal(err)
				}
				if s.channelSourceEnabled(context.Background(), src.ID) {
					t.Fatal("disabled source can tune")
				}
				if _, ok := refreshInterval(src); ok {
					t.Fatal("disabled source scheduled")
				}
			default:
				ix := config.Indexer{ID: "off", Kind: kind, URL: "https://invalid", Enabled: false}
				m := newznab.NewManager(nil, []config.Indexer{ix}, newznab.Options{})
				if kind == "prowlarr" {
					ix.Enabled = true
					ix.ProwlarrImport = &config.IndexerImport{}
					m.ApplyConfig(config.Config{Indexers: []config.Indexer{ix}, Prowlarr: config.Prowlarr{Disabled: true}})
				}
				if m.MissingSources() == nil {
					t.Fatal("disabled indexer available")
				}
			}
		})
	}
}
