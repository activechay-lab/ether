package mediabrowser

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"math"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/frontend"
	"github.com/braedonsaunders/ether/internal/frontend/frontendtest"
	"github.com/braedonsaunders/ether/internal/frontend/kit/ids"
)

// kotlinModel lists the properties jellyfin-sdk-kotlin (1.7.1 in Wholphin
// 1.0.9, 1.8.12 in Jellyfin Android TV) declares non-null without a default.
// The SDK decodes with explicitNulls=false and coerceInputValues=true, so a
// missing or null required key, a wrong JSON type, an unparsable UUID or date,
// or an unknown enum member inside a list fails the whole response. Nested
// models are nullable in the SDK and are checked only when present.
type kotlinModel struct {
	required map[string]string // key -> bool|int|string|uuid|date|list|uuids|enum:<set>|enums:<set>
	nested   map[string]*kotlinModel
}

var kotlinEnums = map[string][]string{
	"MediaType":              {"Unknown", "Video", "Audio", "Photo", "Book"},
	"SubtitlePlaybackMode":   {"Default", "Always", "OnlyForced", "None", "Smart"},
	"SyncPlayUserAccessType": {"CreateAndJoinGroups", "JoinGroups", "None"},
	"RepeatMode":             {"RepeatNone", "RepeatAll", "RepeatOne"},
	"PlaybackOrder":          {"Default", "Shuffle"},
	"UnratedItem":            {"Movie", "Trailer", "Series", "Music", "Book", "LiveTvChannel", "LiveTvProgram", "ChannelContent", "Other"},
	"GeneralCommandType": {"MoveUp", "MoveDown", "MoveLeft", "MoveRight", "PageUp", "PageDown", "PreviousLetter", "NextLetter",
		"ToggleOsd", "ToggleContextMenu", "Select", "Back", "TakeScreenshot", "SendKey", "SendString", "GoHome", "GoToSettings",
		"VolumeUp", "VolumeDown", "Mute", "Unmute", "ToggleMute", "SetVolume", "SetAudioStreamIndex", "SetSubtitleStreamIndex",
		"ToggleFullscreen", "DisplayContent", "GoToSearch", "DisplayMessage", "SetRepeatMode", "ChannelUp", "ChannelDown", "Guide",
		"ToggleStats", "PlayMediaSource", "PlayTrailers", "SetShuffleQueue", "PlayState", "PlayNext", "ToggleOsdMenu", "Play",
		"SetMaxStreamingBitrate", "SetPlaybackOrder"},
}

func kotlinUserModel() *kotlinModel {
	policy := &kotlinModel{required: map[string]string{
		"InvalidLoginAttemptCount": "int", "LoginAttemptsBeforeLockout": "int", "MaxActiveSessions": "int", "RemoteClientBitrateLimit": "int",
		"AuthenticationProviderId": "string", "PasswordResetProviderId": "string", "SyncPlayAccess": "enum:SyncPlayUserAccessType",
	}}
	for _, key := range []string{"IsAdministrator", "IsHidden", "IsDisabled", "EnableUserPreferenceAccess", "EnableRemoteControlOfOtherUsers",
		"EnableSharedDeviceControl", "EnableRemoteAccess", "EnableLiveTvManagement", "EnableLiveTvAccess", "EnableMediaPlayback",
		"EnableAudioPlaybackTranscoding", "EnableVideoPlaybackTranscoding", "EnablePlaybackRemuxing", "ForceRemoteSourceTranscoding",
		"EnableContentDeletion", "EnableContentDownloading", "EnableSyncTranscoding", "EnableMediaConversion", "EnableAllDevices",
		"EnableAllChannels", "EnableAllFolders", "EnablePublicSharing"} {
		policy.required[key] = "bool"
	}
	configuration := &kotlinModel{required: map[string]string{
		"PlayDefaultAudioTrack": "bool", "DisplayMissingEpisodes": "bool", "GroupedFolders": "uuids", "SubtitleMode": "enum:SubtitlePlaybackMode",
		"DisplayCollectionsView": "bool", "EnableLocalPassword": "bool", "OrderedViews": "uuids", "LatestItemsExcludes": "uuids",
		"MyMediaExcludes": "uuids", "HidePlayedInLatest": "bool", "RememberAudioSelections": "bool", "RememberSubtitleSelections": "bool",
		"EnableNextEpisodeAutoPlay": "bool",
	}}
	return &kotlinModel{
		required: map[string]string{"Id": "uuid", "HasPassword": "bool", "HasConfiguredPassword": "bool", "HasConfiguredEasyPassword": "bool"},
		nested:   map[string]*kotlinModel{"Policy": policy, "Configuration": configuration},
	}
}

func kotlinSessionModel() *kotlinModel {
	capabilities := &kotlinModel{
		required: map[string]string{"PlayableMediaTypes": "enums:MediaType", "SupportedCommands": "enums:GeneralCommandType", "SupportsMediaControl": "bool", "SupportsPersistentIdentifier": "bool"},
		nested: map[string]*kotlinModel{"DeviceProfile": {required: map[string]string{
			"DirectPlayProfiles": "list", "TranscodingProfiles": "list", "ContainerProfiles": "list", "CodecProfiles": "list", "SubtitleProfiles": "list",
		}}},
	}
	return &kotlinModel{
		required: map[string]string{
			"PlayableMediaTypes": "enums:MediaType", "UserId": "uuid", "LastActivityDate": "date", "LastPlaybackCheckIn": "date", "IsActive": "bool",
			"SupportsMediaControl": "bool", "SupportsRemoteControl": "bool", "HasCustomDeviceName": "bool", "SupportedCommands": "enums:GeneralCommandType",
		},
		nested: map[string]*kotlinModel{
			"PlayState":    {required: map[string]string{"CanSeek": "bool", "IsPaused": "bool", "IsMuted": "bool", "RepeatMode": "enum:RepeatMode", "PlaybackOrder": "enum:PlaybackOrder"}},
			"Capabilities": capabilities,
		},
	}
}

var kotlinQuickConnectResult = &kotlinModel{required: map[string]string{
	"Authenticated": "bool", "Secret": "string", "Code": "string", "DeviceId": "string", "DeviceName": "string", "AppName": "string", "AppVersion": "string", "DateAdded": "date",
}}

func kotlinUUID(value any) bool {
	text, ok := value.(string)
	if len(text) == 36 && text[8] == '-' && text[13] == '-' && text[18] == '-' && text[23] == '-' {
		text = text[:8] + text[9:13] + text[14:18] + text[19:23] + text[24:]
	}
	_, err := hex.DecodeString(text)
	return ok && len(text) == 32 && err == nil
}

func kotlinValue(kind string, value any) bool {
	switch {
	case kind == "bool":
		_, ok := value.(bool)
		return ok
	case kind == "int":
		n, ok := value.(float64)
		return ok && n == math.Trunc(n) && n >= math.MinInt32 && n <= math.MaxInt32
	case kind == "string":
		_, ok := value.(string)
		return ok
	case kind == "uuid":
		return kotlinUUID(value)
	case kind == "date":
		text, ok := value.(string)
		_, err := time.Parse(time.RFC3339Nano, text)
		return ok && err == nil
	case kind == "list":
		_, ok := value.([]any)
		return ok
	case kind == "uuids":
		list, ok := value.([]any)
		for _, item := range list {
			ok = ok && kotlinUUID(item)
		}
		return ok
	case strings.HasPrefix(kind, "enum:"):
		for _, member := range kotlinEnums[kind[5:]] {
			if value == member {
				return true
			}
		}
		return false
	case strings.HasPrefix(kind, "enums:"):
		list, ok := value.([]any)
		for _, item := range list {
			ok = ok && kotlinValue("enum:"+kind[6:], item)
		}
		return ok
	}
	panic("unknown kind " + kind)
}

func (m *kotlinModel) check(t *testing.T, where string, value any) {
	t.Helper()
	object, ok := value.(map[string]any)
	if !ok {
		t.Errorf("%s: not an object: %v", where, value)
		return
	}
	for key, kind := range m.required {
		if v, present := object[key]; !present || !kotlinValue(kind, v) {
			t.Errorf("%s.%s: jellyfin-sdk-kotlin requires a non-null %s, got %#v (present %t)", where, key, kind, v, present)
		}
	}
	for key, nested := range m.nested {
		if v, present := object[key]; present && v != nil {
			nested.check(t, where+"."+key, v)
		}
	}
}

// TestJellyfinResponsesCarryKotlinRequiredFields is the lasting guard for
// #308: every sign-in, user, public-user, session and Quick Connect answer a
// Kotlin-SDK client parses must decode, including the fallback paths where no
// viewer capabilities are known and the session list holds other surfaces.
func TestJellyfinResponsesCarryKotlinRequiredFields(t *testing.T) {
	const token, secret = "signed-in", "approved"
	viewer := &frontend.Viewer{ID: 4, Username: "viewer", AllowedLibraries: []core.LibraryID{1}}
	created := time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)
	device := frontend.Device{ID: "tv", Client: "Jellyfin Android TV", DeviceName: "Android TV", Version: "0.18.2"}
	identity := frontendtest.Identity{
		ResolveFunc: func(_ context.Context, credential string) (*frontend.Viewer, error) {
			if credential != token {
				return nil, frontend.ErrIdentityUnavailable
			}
			return viewer, nil
		},
		SignInFunc:   func(context.Context, string, string, frontend.Device) (string, error) { return token, nil },
		InitiateFunc: func(context.Context, frontend.Device) (string, string, error) { return "123456", "pending", nil },
		DetailsFunc: func(_ context.Context, s string) (frontend.QuickConnectDetails, error) {
			return frontend.QuickConnectDetails{Approved: true, Code: "123456", Secret: s, Device: device, CreatedAt: created}, nil
		},
		PollFunc: func(context.Context, string) (string, error) { return token, nil },
		PublicViewersFunc: func(context.Context) ([]frontend.PublicViewer, error) {
			return []frontend.PublicViewer{{ID: 4, Name: "viewer"}}, nil
		},
	}
	yes := true
	sessions := frontendtest.Sessions{
		CapabilitiesFunc: func(context.Context, frontend.SessionAccess) (frontend.SessionCapabilities, error) {
			return frontend.SessionCapabilities{PlayableMediaTypes: []string{"Video", "Audio"}, SupportedCommands: []string{"Play", "DisplayMessage"}}, nil
		},
		SessionsFunc: func(context.Context, frontend.SessionAccess) ([]frontend.Session, error) {
			return []frontend.Session{
				{ID: "jellyfin", Protocol: "jellyfin", ViewerID: 4, Device: device, StartedAt: created, Capabilities: &frontend.SessionCapabilities{PlayableMediaTypes: []string{"Video"}, SupportedCommands: []string{"Play"}, SupportsMediaControl: &yes}},
				// An Emby client's declaration: Emby-only members and an Emby
				// device profile must not reach a Jellyfin client.
				{ID: "emby", Protocol: "emby", ViewerID: 4, Device: frontend.Device{ID: "emby-tv", Client: "Emby Theater"}, StartedAt: created, Capabilities: &frontend.SessionCapabilities{PlayableMediaTypes: []string{"Video", "Game"}, SupportedCommands: []string{"Play", "SetSubtitleOffset", "SetPlaybackRate"}, DeviceProfile: json.RawMessage(`{"Name":"Emby Theater"}`)}},
				{ID: "plex", Protocol: "plex", ViewerID: 4, Device: frontend.Device{ID: "plex-tv", Client: "Plex"}, StartedAt: created},
			}, nil
		},
	}
	inst, err := mount(context.Background(), frontend.Host{Identity: identity, Sessions: sessions}, "kotlin", "jellyfin")
	if err != nil {
		t.Fatal(err)
	}
	userID, _ := ids.GUID(ids.User, viewer.ID)
	call := func(method, path, body string) any {
		t.Helper()
		r := httptest.NewRequest(method, path, strings.NewReader(body))
		r.Header.Set("Authorization", `MediaBrowser Client="Jellyfin Android TV", Device="Android TV", DeviceId="tv", Version="0.18.2", Token="`+token+`"`)
		w := httptest.NewRecorder()
		inst.Handler().ServeHTTP(w, r)
		var out any
		if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &out) != nil {
			t.Fatalf("%s %s: %d %s", method, path, w.Code, w.Body.String())
		}
		return out
	}
	user, session := kotlinUserModel(), kotlinSessionModel()
	for _, signIn := range []struct{ path, body string }{
		{"/Users/AuthenticateByName", `{"Username":"viewer","Pw":"password"}`},
		{"/Users/AuthenticateWithQuickConnect", `{"Secret":"` + secret + `"}`},
	} {
		result := call("POST", signIn.path, signIn.body).(map[string]any)
		user.check(t, signIn.path+" User", result["User"])
		session.check(t, signIn.path+" SessionInfo", result["SessionInfo"])
	}
	user.check(t, "/Users/Me", call("GET", "/Users/Me", ""))
	user.check(t, "/Users/{id}", call("GET", "/Users/"+userID, ""))
	public, _ := call("GET", "/Users/Public", "").([]any)
	if len(public) != 1 {
		t.Fatalf("/Users/Public: %v", public)
	}
	user.check(t, "/Users/Public[0]", public[0])
	list, _ := call("GET", "/Sessions", "").([]any)
	if len(list) != 3 {
		t.Fatalf("/Sessions: %v", list)
	}
	for _, row := range list {
		session.check(t, "/Sessions[]", row)
	}
	kotlinQuickConnectResult.check(t, "/QuickConnect/Initiate", call("POST", "/QuickConnect/Initiate", ""))
	kotlinQuickConnectResult.check(t, "/QuickConnect/Connect", call("GET", "/QuickConnect/Connect?secret="+secret, ""))
}
