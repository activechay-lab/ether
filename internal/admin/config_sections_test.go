package admin

import "testing"

func TestConsoleConfigSections(t *testing.T) {
	// Fixed inventory of putSection/getSection IDs in web/src: direct API
	// calls, ConfigSection endpoints, and ConnectedServices' SERVICES registry
	// (including its enable toggles and setup drawers). Forms without an
	// endpoint use putConfig and deliberately do not expand this allowlist.
	sections := []string{
		"availability", "cache", "catalog", "clientIdentity", "clients",
		"debrid", "indexerSites", "indexers", "livetv", "llm", "markers",
		"mdblist", "opensubtitles", "outbound", "plex", "plexservers",
		"quality", "resolver", "server", "simkl", "spike", "subdl",
		"trakt", "transcode", "updates", "usenet",
	}
	for _, section := range sections {
		if field, ok := configSections[section]; !ok || field == "" {
			t.Errorf("console section %q is missing from configSections", section)
		}
	}
}
