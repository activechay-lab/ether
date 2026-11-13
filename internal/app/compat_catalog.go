package app

import (
	"context"
	"time"

	"github.com/braedonsaunders/ether/internal/clientreg"
	"github.com/braedonsaunders/ether/internal/core"
)

// compatApp is one known client application. Device capability lives on
// compatDevice; these rows only name the devices an app is typically used on.
type compatApp struct {
	Protocol      string   `json:"protocol"`
	Name          string   `json:"name"`
	Product       string   `json:"product,omitempty"`
	Devices       []string `json:"devices"`
	VideoCodecs   []string `json:"videoCodecs"`
	Subtitles     []string `json:"subtitles"`
	DirectPlay    string   `json:"directPlay"`
	Note          string   `json:"note"`
	Certification string   `json:"certification"`
}

// compatDevice shows defaults for a representative registry client identity,
// after owner overrides. Runtime app declarations and model-specific matches
// can refine these defaults; a row does not certify every model or display.
type compatDevice struct {
	ID                  string   `json:"id,omitempty"`
	Name                string   `json:"name"`
	DolbyVisionProfiles []int    `json:"dolbyVisionProfiles"`
	SubtitleCodecs      []string `json:"subtitleCodecs"`
	Notes               string   `json:"notes"`
	VideoCodecs         []string `json:"videoCodecs,omitempty"`
	MaxHeight           int      `json:"maxHeight,omitempty"`
	HDR10               bool     `json:"hdr10"`
	DolbyVision         bool     `json:"dolbyVision"`
	BuiltinMaxHeight    int      `json:"builtinMaxHeight,omitempty"`
	BuiltinHDR10        bool     `json:"builtinHdr10"`
	BuiltinDolbyVision  bool     `json:"builtinDolbyVision"`
}

type observedClient struct {
	Protocol string    `json:"protocol"`
	Name     string    `json:"name"`
	LastSeen time.Time `json:"lastSeen"`
}

type compatCatalog struct {
	Apps      []compatApp                     `json:"apps"`
	Devices   []compatDevice                  `json:"devices"`
	Observed  []observedClient                `json:"observed"`
	Overrides []core.ClientCapabilityOverride `json:"overrides"`
}

// builtinApps is the reference list. Names and certification come from
// docs/certification/matrix.json. Codec rows come from a conform device
// profile when one exists; otherwise the app is marked as reporting its own
// capabilities at playback. Nothing here is inferred.
func builtinApps() []compatApp {
	return []compatApp{
		// Protocol strings below identify catalogue entries; they do not select host behavior.
		{Protocol: "plex", Name: "Plex Web", Devices: []string{"Browser"}, DirectPlay: "browser", Certification: "builtin"},
		{Protocol: "plex", Name: "Plex for Android phones", Product: "Plex for Android (Mobile)", Devices: []string{"Android"}, DirectPlay: "reported", Certification: "builtin"},
		{Protocol: "plex", Name: "Plex for Android TV", Product: "Plex for Android (TV)", Devices: []string{"Android TV"}, DirectPlay: "reported", Certification: "builtin"},
		{Protocol: "plex", Name: "Plex for iOS", Devices: []string{"iOS"}, DirectPlay: "reported", Certification: "builtin"},
		{Protocol: "plex", Name: "Plex for Apple TV", Devices: []string{"Apple TV"}, DirectPlay: "reported", Certification: "builtin"},
		{Protocol: "plex", Name: "Plex HTPC", Devices: []string{"Home theater PC"}, DirectPlay: "reported", Certification: "builtin"},
		{Protocol: "plex", Name: "Plex for Windows", Devices: []string{"Home theater PC"}, DirectPlay: "reported", Certification: "builtin"},
		{Protocol: "plex", Name: "Plex for Mac", Devices: []string{"Mac"}, DirectPlay: "reported", Certification: "builtin"},
		{Protocol: "plex", Name: "Plex for Kodi", Product: "PM4K", Devices: []string{"Kodi"}, DirectPlay: "reported", Certification: "builtin"},
		{Protocol: "jellyfin", Name: "Swiftfin", Devices: []string{"Apple TV", "iOS"}, DirectPlay: "reported", Certification: "pending"},
		{Protocol: "jellyfin", Name: "Findroid", Devices: []string{"Android"}, Subtitles: []string{"srt", "ass"}, DirectPlay: "findroid", Certification: "pending"},
		{Protocol: "jellyfin", Name: "Streamyfin", Devices: []string{"Android"}, VideoCodecs: []string{"h264", "hevc", "vp8", "vp9", "av1"}, Subtitles: []string{"srt", "vtt", "ttml"}, DirectPlay: "listed", Note: "no-dv5", Certification: "pending"},
		{Protocol: "jellyfin", Name: "Jellyfin Android TV", Devices: []string{"Android TV", "Google TV", "NVIDIA Shield"}, DirectPlay: "reported", Certification: "pending"},
		{Protocol: "jellyfin", Name: "JellyCon", Devices: []string{"Kodi"}, DirectPlay: "open", Note: "h264-ac3", Certification: "pending"},
		{Protocol: "jellyfin", Name: "Infuse", Devices: []string{"Apple TV", "iOS"}, DirectPlay: "reported", Note: "capture", Certification: "pending"},
		{Protocol: "jellyfin", Name: "Jellyfin for Roku", Devices: []string{"Roku"}, DirectPlay: "reported", Certification: "pending"},
		{Protocol: "emby", Name: "Emby Web", Devices: []string{"Browser"}, DirectPlay: "browser", Certification: "pending"},
		{Protocol: "emby", Name: "Emby for Android TV", Devices: []string{"Android TV"}, DirectPlay: "reported", Certification: "pending"},
		{Protocol: "emby", Name: "Emby for TV", Devices: []string{"Apple TV"}, DirectPlay: "reported", Note: "unsupported-shell", Certification: "unsupported"},
		{Protocol: "emby", Name: "Infuse", Devices: []string{"Apple TV", "iOS"}, DirectPlay: "reported", Note: "capture", Certification: "pending"},
		{Protocol: "emby", Name: "Emby SDK", Devices: []string{}, DirectPlay: "reported", Note: "sdk", Certification: "pending"},
		{Protocol: "silo", Name: "Silo for Apple TV", Devices: []string{"Apple TV"}, DirectPlay: "reported", Certification: "pending"},
		{Protocol: "silo", Name: "Silo Apple", Devices: []string{"iOS", "Mac"}, DirectPlay: "reported", Certification: "pending"},
		{Protocol: "silo", Name: "Silo Android", Devices: []string{"Android", "Android TV"}, DirectPlay: "reported", Certification: "pending"},
		{Protocol: "stremio", Name: "Stremio", Devices: []string{"Android TV", "iOS", "Browser", "NVIDIA Shield"}, DirectPlay: "stream", Certification: "builtin"},
		{Protocol: "dlna", Name: "DLNA renderer", Devices: []string{"DLNA TV or receiver"}, DirectPlay: "renderer", Certification: "builtin"},
		{Protocol: "webdav", Name: "Files and media players", Devices: []string{"Apple TV", "iOS", "Android"}, DirectPlay: "player", Certification: "builtin"},
	}
}

func builtinDevices(overrides []core.ClientCapabilityOverride) []compatDevice {
	devices := clientreg.Devices()
	out := make([]compatDevice, 0, len(devices)+1)
	for _, item := range devices {
		id := item.Identity
		basePolicy := clientreg.Policy(id, nil)
		edited := clientreg.Policy(id, overrides)
		playBase := clientreg.Playback(id, nil)
		play := clientreg.Playback(id, overrides)
		recordID := basePolicy.ID
		if recordID == "generic" {
			recordID = playBase.ID
		}
		out = append(out, compatDevice{
			ID:                  recordID,
			Name:                item.Name,
			DolbyVisionProfiles: edited.DolbyVisionProfiles,
			SubtitleCodecs:      edited.Subtitles,
			Notes:               edited.Notes,
			VideoCodecs:         play.Video,
			MaxHeight:           play.MaxHeight,
			HDR10:               play.HDR10,
			DolbyVision:         play.DolbyVision,
			BuiltinMaxHeight:    playBase.MaxHeight,
			BuiltinHDR10:        playBase.HDR10,
			BuiltinDolbyVision:  playBase.DolbyVision,
		})
	}
	out = append(out, compatDevice{
		Name:           "DLNA TV or receiver",
		SubtitleCodecs: []string{"srt", "subrip"},
		Notes:          "The TV or receiver advertises what it can play.",
	})
	return out
}

func (a *App) compatibilityCatalog(ctx context.Context) (compatCatalog, error) {
	observed, err := a.observedClients(ctx)
	if err != nil {
		return compatCatalog{}, err
	}
	if observed == nil {
		observed = []observedClient{}
	}
	overrides := a.cfg.Read().Clients.Overrides
	if overrides == nil {
		overrides = []core.ClientCapabilityOverride{}
	}
	return compatCatalog{Apps: builtinApps(), Devices: builtinDevices(overrides), Observed: observed, Overrides: overrides}, nil
}

func (a *App) observedClients(ctx context.Context) ([]observedClient, error) {
	result, err := a.db.QueryContext(ctx, `SELECT surface, family, last_seen FROM frontend_client_families ORDER BY surface, family`)
	if err != nil {
		return nil, err
	}
	defer result.Close()
	var rows []observedClient
	for result.Next() {
		var surface, family string
		var seen int64
		if err := result.Scan(&surface, &family, &seen); err != nil {
			return nil, err
		}
		rows = append(rows, observedClient{Protocol: surface, Name: family, LastSeen: time.Unix(seen, 0).UTC()})
	}
	return rows, result.Err()
}
