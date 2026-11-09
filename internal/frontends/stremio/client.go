package stremio

import "strings"

// Stremio add-on requests carry no device profile. The only identity signal
// is the User-Agent of whatever fetched the stream list, so this maps it onto
// a client-registry identity (product, platform). The registry, not this
// file, decides what each identity can play (records stremio-native-desktop,
// stremio-native-android, nuvio-ios and quirk native-player-dynamic-range).
//
// Signals, checked in order:
//   - "nuvio" anywhere: Nuvio. Platform Android when the agent names Android
//     or okhttp, iOS when it names CFNetwork, Darwin, iPhone or iPad.
//   - "okhttp/": a native Android app's HTTP stack (Stremio for Android and
//     Android TV, Nuvio's React Native fetch). These play through ExoPlayer,
//     libVLC or libmpv.
//   - "; wv)" with "Android": an Android WebView shell (the Stremio Android TV
//     app's UI) whose playback is a native player.
//   - "QtWebEngine" or the token "Stremio": Stremio's desktop shell, which
//     plays through mpv. "Android" in the same agent means the Android app.
//   - "libmpv", "mpv/", "VLC/", "LibVLC", "ExoPlayer": a native player that
//     fetched the list itself.
//
// Everything else, including every ordinary browser agent, is Stremio Web:
// an HTML5 player that keeps generic capabilities. Stremio desktop v5 shells
// that present a plain browser agent (WebView2, WKWebView) cannot be told
// apart from Stremio Web and stay generic too.
func stremioClient(userAgent string) (product, platform string) {
	ua := strings.ToLower(userAgent)
	android := strings.Contains(ua, "android")
	switch {
	case strings.Contains(ua, "nuvio"):
		switch {
		case android || strings.Contains(ua, "okhttp"):
			return "Nuvio", "Android"
		case strings.Contains(ua, "cfnetwork") || strings.Contains(ua, "darwin") || strings.Contains(ua, "iphone") || strings.Contains(ua, "ipad"):
			return "Nuvio", "iOS"
		}
		return "Stremio", ""
	case strings.HasPrefix(ua, "okhttp/"):
		return "Stremio", "Android"
	case android && strings.Contains(ua, "; wv)"):
		return "Stremio", "Android"
	case strings.Contains(ua, "qtwebengine") || strings.Contains(ua, "stremio"):
		if android {
			return "Stremio", "Android"
		}
		return "Stremio", "Desktop"
	case strings.Contains(ua, "libmpv") || strings.Contains(ua, "mpv/") || strings.Contains(ua, "vlc/") || strings.Contains(ua, "libvlc") || strings.Contains(ua, "exoplayer"):
		if android {
			return "Stremio", "Android"
		}
		return "Stremio", "Desktop"
	}
	return "Stremio", ""
}
