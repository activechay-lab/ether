package mediabrowser

import (
	"crypto/sha256"
	"encoding/hex"

	"fmt"

	"net/http"

	"time"

	"github.com/braedonsaunders/ether/internal/frontend"

	"github.com/braedonsaunders/ether/internal/frontend/kit/ids"
)

func (i *instance) sessionDTO(r *http.Request, v *frontend.Viewer) map[string]any {
	d := device(r)
	userID, _ := ids.GUID(ids.User, v.ID)
	sum := sha256.Sum256([]byte(fmt.Sprintf("%s:%d:%s", i.protocol, v.ID, d.ID)))
	sessionID := hex.EncodeToString(sum[:16])
	peer := ""
	if i.host.ClientIP != nil {
		if ip, ok := i.host.ClientIP(r); ok {
			peer = ip.String()
		}
	}
	now := time.Now().UTC()
	state := map[string]any{"CanSeek": false, "IsPaused": false, "IsMuted": false, "RepeatMode": "RepeatNone"}
	session := map[string]any{"Id": sessionID, "UserId": userID, "UserName": v.Username, "ServerId": i.info.ID, "Client": d.Client, "DeviceId": d.ID, "DeviceName": d.DeviceName, "ApplicationVersion": d.Version, "LastActivityDate": wireTime(now), "RemoteEndPoint": peer, "AdditionalUsers": []any{}, "PlayableMediaTypes": []any{}, "SupportedCommands": []any{}, "SupportsRemoteControl": false, "PlayState": state}
	if i.protocol == "jellyfin" {
		// This sign-in response precedes any playback check-in.
		session["LastPlaybackCheckIn"] = wireTime(time.Time{})
		state["PlaybackOrder"] = "Default"
		session["IsActive"] = true
		session["HasCustomDeviceName"] = false
		session["SupportsMediaControl"] = false
		session["NowPlayingQueue"] = []any{}
		session["Capabilities"] = map[string]any{"PlayableMediaTypes": []any{}, "SupportedCommands": []any{}, "SupportsMediaControl": false, "SupportsPersistentIdentifier": d.ID != ""}
	} else {
		state["PlaybackRate"] = 1
		state["Shuffle"] = false
		state["SleepTimerMode"] = "None"
		state["SubtitleOffset"] = 0
		session["Protocol"] = r.Proto
		session["PlaylistIndex"] = 0
		session["PlaylistLength"] = 0
	}
	if i.host.Sessions != nil {
		caps, err := i.host.Sessions.Capabilities(r.Context(), frontend.SessionAccess{Viewer: v, Device: d, Protocol: i.protocol})
		if err == nil {
			c := i.sessionCapabilitiesDTO(caps)
			if i.protocol == "jellyfin" {
				session["Capabilities"] = c
			}
			for _, key := range []string{"PlayableMediaTypes", "SupportedCommands", "SupportsRemoteControl", "SupportsMediaControl"} {
				if i.protocol == "emby" && key == "SupportsMediaControl" {
					continue
				}
				if value, ok := c[key]; ok {
					session[key] = value
				}
			}
			if caps.SupportsRemoteControl != nil {
				session["SupportsRemoteControl"] = *caps.SupportsRemoteControl
			}
		}
	}
	if i.protocol == "emby" {
		i.embySessionDTO(r.Context(), session, d.ID)
	}
	return session
}
