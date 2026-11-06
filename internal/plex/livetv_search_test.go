package plex

import (
	"context"
	"encoding/xml"
	"net/url"
	"strings"
	"testing"

	"github.com/braedonsaunders/ether/internal/livetv"
)

// Search is another channel policy boundary; returning even a name or a count
// from an inaccessible channel leaks owner/private/current-programme metadata.
func TestLiveChannelSearchContractAndPolicy(t *testing.T) {
	f, service, ch := liveFixture(t)
	ctx := context.Background()
	ch.Name = `Lantern "News" & More`
	ch.Number = "9981"
	ch.Group = ""
	var err error
	if ch, err = service.SaveChannel(ctx, ch); err != nil {
		t.Fatal(err)
	}
	provider := f.srv.liveTVProvider(ctx, &ownerUser)
	advertised := false
	for _, feature := range provider.Feature {
		if feature.Type == "universalsearch" && feature.Key == liveProviderPath+"/library/search" {
			advertised = true
		}
	}
	if !advertised {
		t.Fatal("channel search is not discoverable")
	}
	path := liveProviderPath + "/library/search?query="
	request := func(query, token string) map[string]any {
		t.Helper()
		r := f.request(t, "GET", path+url.QueryEscape(query), map[string]string{"Accept": "application/json", "X-Plex-Token": token})
		if r.Code != 200 {
			t.Fatalf("search: %d %s", r.Code, r.Body.String())
		}
		return decodeJSON(t, r)
	}
	for _, q := range []string{"lantern", "9981", "NEWS lantern", "  Lantern  "} {
		mc := request(q, ownerToken)
		if mc["size"] != float64(1) {
			t.Fatalf("%q: %#v", q, mc)
		}
		hit := mc["SearchResult"].([]any)[0].(map[string]any)
		channel := hit["Channel"].(map[string]any)
		if hit["Metadata"] != nil || channel["id"] != ch.ID || channel["title"] != ch.Name || channel["source"] != "server://"+f.srv.identity().MachineIdentifier+liveProviderPath {
			t.Fatalf("channel result: %#v", hit)
		}
		if cats, ok := channel["categories"].([]any); !ok || len(cats) != 0 {
			t.Fatalf("required empty array missing: %#v", channel)
		}
		if channel["streamUrl"] != nil {
			t.Fatal("upstream exposed")
		}
	}
	for _, q := range []string{"", "  ", "Owner programme", "not-present"} {
		if mc := request(q, ownerToken); mc["size"] != float64(0) {
			t.Fatalf("not channel-only for %q: %#v", q, mc)
		}
	}
	r := f.request(t, "GET", path+"Lantern", map[string]string{"Accept": "application/xml"})
	var wire struct {
		Results []struct {
			Channel struct {
				ID         string `xml:"id,attr"`
				Title      string `xml:"title,attr"`
				Categories string `xml:"categories,attr"`
			} `xml:"Channel"`
		} `xml:"SearchResult"`
	}
	if err := xml.Unmarshal(r.Body.Bytes(), &wire); err != nil || len(wire.Results) != 1 || wire.Results[0].Channel.Title != ch.Name {
		t.Fatalf("XML: %v %s", err, r.Body.String())
	}
	// The returned channel identifier remains the existing tune identifier.
	tune := f.request(t, "POST", "/livetv/dvrs/1/channels/"+url.PathEscape(ch.ID)+"/tune", map[string]string{"Accept": "application/json"})
	if tune.Code != 200 {
		t.Fatalf("tune: %d %s", tune.Code, tune.Body.String())
	}
	if err := service.SavePolicy(ctx, livetv.Policy{UserID: friendUser.ID, Restricted: true}); err != nil {
		t.Fatal(err)
	}
	if mc := request("Lantern", friendToken); mc["size"] != float64(0) {
		t.Fatalf("restricted name leak: %#v", mc)
	}
	if err := service.SavePolicy(ctx, livetv.Policy{UserID: friendUser.ID, MaxRating: "G"}); err != nil {
		t.Fatal(err)
	}
	if mc := request("Lantern", friendToken); mc["size"] != float64(0) {
		t.Fatalf("current programme rating leaked: %#v", mc)
	}
	if err := service.SavePolicy(ctx, livetv.Policy{UserID: friendUser.ID, Disabled: true}); err != nil {
		t.Fatal(err)
	}
	if r := f.request(t, "GET", path+"Lantern", map[string]string{"X-Plex-Token": friendToken}); r.Code != 404 {
		t.Fatalf("disabled search: %d", r.Code)
	}
	ch.Enabled = false
	if _, err := service.SaveChannel(ctx, ch); err != nil {
		t.Fatal(err)
	}
	if mc := request("Lantern", ownerToken); mc["size"] != float64(0) {
		t.Fatalf("disabled channel leak: %#v", mc)
	}
	if r := f.request(t, "GET", path+strings.Repeat("a", 257), nil); r.Code != 400 {
		t.Fatalf("oversize query: %d", r.Code)
	}
}
