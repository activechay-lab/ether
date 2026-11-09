package livetv

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"slices"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/store"
)

// Apps are the frontend surfaces that list Live TV channels. A channel
// appears in every app unless the owner hides it from one (Channel.HiddenApps).
// Viewer access restrictions are independent and apply in every app.
var Apps = []string{"plex", "jellyfin", "emby", "silo", "xtream", "m3u"}

func publishedTo(ctx context.Context, c Channel) bool {
	app := core.LiveApp(ctx)
	return app == "" || !slices.Contains(c.HiddenApps, app)
}

// normalizeHiddenApps sorts and de-duplicates an app list and rejects apps
// that do not list Live TV.
func normalizeHiddenApps(apps []string) ([]string, error) {
	if len(apps) == 0 {
		return nil, nil
	}
	out := slices.Clone(apps)
	slices.Sort(out)
	out = slices.Compact(out)
	for _, app := range out {
		if !slices.Contains(Apps, app) {
			return nil, fmt.Errorf("unknown Live TV app %q", app)
		}
	}
	return out, nil
}

// livetv/17 converts the Plex-only publication flag (plexHidden, never
// released) into the per-app list.
func init() {
	store.Register("livetv", 17, "SELECT 1;")
	store.RegisterApply("livetv", 17, convertPlexHidden)
}

func convertPlexHidden(ctx context.Context, tx *sql.Tx) error {
	rows, err := tx.QueryContext(ctx, `SELECT id, data FROM livetv_channels WHERE data LIKE '%"plexHidden"%'`)
	if err != nil {
		return err
	}
	type change struct {
		id   string
		data []byte
	}
	var changes []change
	for rows.Next() {
		var id string
		var raw []byte
		if err := rows.Scan(&id, &raw); err != nil {
			rows.Close()
			return err
		}
		var data map[string]json.RawMessage
		if err := json.Unmarshal(raw, &data); err != nil {
			rows.Close()
			return fmt.Errorf("live TV channel %s: %w", id, err)
		}
		var hidden bool
		_ = json.Unmarshal(data["plexHidden"], &hidden)
		delete(data, "plexHidden")
		if hidden {
			var apps []string
			_ = json.Unmarshal(data["hiddenApps"], &apps)
			if !slices.Contains(apps, "plex") {
				apps = append(apps, "plex")
			}
			slices.Sort(apps)
			data["hiddenApps"], _ = json.Marshal(apps)
		}
		b, err := json.Marshal(data)
		if err != nil {
			rows.Close()
			return err
		}
		changes = append(changes, change{id, b})
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	for _, c := range changes {
		if _, err := tx.ExecContext(ctx, `UPDATE livetv_channels SET data=? WHERE id=?`, string(c.data), c.id); err != nil {
			return err
		}
	}
	return nil
}
