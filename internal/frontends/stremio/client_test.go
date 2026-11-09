package stremio

import "testing"

func TestStremioClientIdentity(t *testing.T) {
	for _, c := range []struct{ ua, product, platform string }{
		{"okhttp/4.12.0", "Stremio", "Android"},
		{"Mozilla/5.0 (Linux; Android 12; BRAVIA 4K VH2 Build/STT1.211025.001.Z4; wv) AppleWebKit/537.36 (KHTML, like Gecko) Version/4.0 Chrome/120.0.6099.230 Mobile Safari/537.36", "Stremio", "Android"},
		{"Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) QtWebEngine/5.15.2 Chrome/83.0.4103.122 Safari/537.36", "Stremio", "Desktop"},
		{"Nuvio/1.2.0 CFNetwork/1568.100.1 Darwin/24.0.0", "Nuvio", "iOS"},
		{"Nuvio/1.2.0 (Android 14)", "Nuvio", "Android"},
		{"libmpv", "Stremio", "Desktop"},
		{"Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/131.0.0.0 Safari/537.36 Edg/131.0.0.0", "Stremio", ""},
		{"Mozilla/5.0 (Linux; Android 14; Pixel 8) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/131.0.0.0 Mobile Safari/537.36", "Stremio", ""},
		{"Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/18.1 Safari/605.1.15", "Stremio", ""},
		{"", "Stremio", ""},
	} {
		product, platform := stremioClient(c.ua)
		if product != c.product || platform != c.platform {
			t.Errorf("%q: got %s/%s, want %s/%s", c.ua, product, platform, c.product, c.platform)
		}
	}
}
