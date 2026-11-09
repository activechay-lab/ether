package livetv

import (
	"context"
	"fmt"
	"slices"
	"testing"
)

func TestAppVisibilityBatchAndPlexFlagConversion(t *testing.T) {
	ctx := context.Background()
	s := testService(t, Options{})
	for i, id := range []string{"a", "b"} {
		if _, err := s.SaveChannel(ctx, Channel{ID: id, Number: fmt.Sprint(i + 1), Name: id, Kind: "iptv", StreamURL: "https://example.test/" + id, Enabled: true}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.UpdateChannels(ctx, ChannelBatch{IDs: []string{"a"}, HideApps: []string{"emby"}}); err != nil {
		t.Fatal(err)
	}
	// Other apps keep each channel's own choice.
	if _, err := s.UpdateChannels(ctx, ChannelBatch{IDs: []string{"a", "b"}, HideApps: []string{"plex"}, ShowApps: []string{"m3u"}}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.UpdateChannels(ctx, ChannelBatch{IDs: []string{"a"}, ShowApps: []string{"plex"}, HideApps: []string{"plex"}}); err == nil {
		t.Fatal("accepted show and hide for one app")
	}
	for id, want := range map[string][]string{"a": {"emby", "plex"}, "b": {"plex"}} {
		if c, err := s.Channel(ctx, id); err != nil || !slices.Equal(c.HiddenApps, want) {
			t.Fatalf("%s: %v %v", id, c.HiddenApps, err)
		}
	}

	if _, err := s.o.DB.ExecContext(ctx, `UPDATE livetv_channels SET data=? WHERE id='b'`, `{"id":"b","number":"2","name":"b","kind":"iptv","streamUrl":"https://example.test/b","enabled":true,"plexHidden":true}`); err != nil {
		t.Fatal(err)
	}
	tx, err := s.o.DB.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := convertPlexHidden(ctx, tx); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	if c, err := s.Channel(ctx, "b"); err != nil || !slices.Equal(c.HiddenApps, []string{"plex"}) {
		t.Fatalf("converted: %v %v", c.HiddenApps, err)
	}
}
