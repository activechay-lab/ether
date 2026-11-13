package deliverypolicy

import "strings"

func neutralMediaSession(path string) (string, bool) {
	const prefix = "/sessions/media/"
	if len(path) > 8192 || !strings.HasPrefix(path, prefix) {
		return "", false
	}
	parts := strings.Split(strings.TrimPrefix(path, prefix), "/")
	if len(parts) < 3 || len(parts) > 8 {
		return "", false
	}
	for _, part := range parts {
		if part == "" || part == "." || part == ".." || len(part) > 512 {
			return "", false
		}
		for _, c := range part {
			if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '-' || c == '_' || c == '.') {
				return "", false
			}
		}
	}
	return parts[0], true
}

func DeliveryRoute(path string) (canonical, id, protocol string, ok bool) {
	canonical = path
	switch {
	case strings.HasPrefix(path, "/jellyfin/sessions/media/"):
		canonical = strings.TrimPrefix(path, "/jellyfin")
		protocol = "jellyfin"
	case strings.HasPrefix(path, "/emby/sessions/media/"):
		canonical = strings.TrimPrefix(path, "/emby")
		protocol = "emby"
	}
	id, ok = neutralMediaSession(canonical)
	return
}
