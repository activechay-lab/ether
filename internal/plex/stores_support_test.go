package plex

import (
	"encoding/json"
	"github.com/braedonsaunders/ether/internal/core"
	"strconv"
)

func streamSlot(index int, info core.StreamInfo) string {
	if info.External && info.ExternalURL != "" {
		return "external/" + info.ExternalURL
	}
	if info.Type == "subtitle" {
		source := "embedded"
		if info.External {
			source = "external"
		}
		raw, _ := json.Marshal([]any{info.Language, info.Codec, source, info.Forced, info.Title})
		return "subtitle/" + string(raw)
	}
	if info.Type == "audio" {
		raw, _ := json.Marshal([]any{index, info.Type, info.Language, info.Codec, info.Channels, info.Title, info.Forced, info.External, info.ExternalURL})
		return "track-v2/" + string(raw)
	}
	return strconv.Itoa(index)
}
