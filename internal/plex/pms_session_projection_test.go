package plex

import (
	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/plex/model"
	"strings"
	"testing"
	"time"
)

func TestPMSSessionProjectionAndPublicIdentity(t *testing.T) {
	f := newFixture()
	defer f.srv.Close()
	f.get(t, "/video/:/transcode/universal/decision?path=%2Flibrary%2Fmetadata%2F1000000000000550&session=projection", "X-Plex-Platform", "Chrome")
	f.get(t, "/:/timeline?ratingKey=1000000000000550&state=playing&time=1234&duration=10000", "X-Plex-Platform", "Chrome")
	rec := f.get(t, "/status/sessions", jsonAccept...)
	if rec.Code != 200 {
		t.Fatal(rec.Code, rec.Body.String())
	}
	rows := decodeJSON(t, rec)["Metadata"].([]any)
	md := rows[0].(map[string]any)
	if _, ok := md["librarySectionID"].(string); !ok {
		t.Fatalf("section id: %#v", md)
	}
	if _, ok := md["Image"]; ok {
		t.Fatal("session contains Image")
	}
	if _, ok := md["Guid"]; ok {
		t.Fatal("session contains Guid")
	}
	if md["sessionKey"] == nil || md["viewOffset"] != float64(1234) {
		t.Fatalf("timeline identity/offset: %#v", md)
	}
	sess := md["Session"].(map[string]any)
	player := md["Player"].(map[string]any)
	if sess["id"] != player["machineIdentifier"] {
		t.Fatalf("public fallback: %#v %#v", sess, player)
	}
	if player["profile"] != "Web" {
		t.Fatalf("resolved profile: %#v", player)
	}
	media := md["Media"].([]any)[0].(map[string]any)
	if _, ok := media["id"].(string); !ok {
		t.Fatalf("media id: %#v", media)
	}
	for _, p := range media["Part"].([]any) {
		part := p.(map[string]any)
		if _, ok := part["id"].(string); !ok {
			t.Fatalf("part id: %#v", part)
		}
	}
	rec = f.get(t, "/status/sessions/terminate?sessionId="+md["sessionKey"].(string))
	if rec.Code != 404 {
		t.Fatalf("sessionKey accepted as public id: %d", rec.Code)
	}
	rec = f.get(t, "/status/sessions/terminate?sessionId="+sess["id"].(string))
	if rec.Code != 200 {
		t.Fatalf("public id refused: %d", rec.Code)
	}
}

func TestPMSSessionProjectionNestedTypes(t *testing.T) {
	md := model.Metadata{LibrarySectionID: 1, Image: []model.Image{{}}, Media: []model.Media{{ID: 9, AspectRatio: 1.5, Part: []model.Part{{ID: 7, Stream: []model.Stream{{ID: 8}}}}}}}
	sessionItem(&md)
	mc := model.MediaContainer{Size: 1, Metadata: []model.Metadata{md}}
	b, err := model.Marshal(mc, model.JSON)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`"id":"9"`, `"aspectRatio":"1.5"`, `"id":"7"`, `"id":"8"`} {
		if !strings.Contains(string(b), want) {
			t.Fatalf("missing %s: %s", want, b)
		}
	}
	x, err := model.Marshal(mc, model.XML)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(x), `<Media aspectRatio="1.5" id="9"`) {
		t.Fatalf("unsorted media: %s", x)
	}
}

func TestPMSDecisionArmingIsSessionAndItemScoped(t *testing.T) {
	f := newFixture()
	defer f.srv.Close()
	start := "/video/:/transcode/universal/start.m3u8?path=%2Flibrary%2Fmetadata%2F1000000000000550&session=arm"
	if got := f.get(t, start); got.Code != 400 {
		t.Fatalf("unarmed start: %d", got.Code)
	}
	decision := strings.Replace(start, "start.m3u8", "decision", 1)
	if got := f.get(t, decision, "X-Plex-Session-Identifier", "first"); got.Code != 200 {
		t.Fatal(got.Code, got.Body.String())
	}
	if got := f.get(t, start, "X-Plex-Session-Identifier", "second"); got.Code != 400 {
		t.Fatalf("another player borrowed decision: %d", got.Code)
	}
	other := strings.Replace(start, "1000000000000550", alien.Key.String(), 1)
	if got := f.get(t, other, "X-Plex-Session-Identifier", "first"); got.Code != 400 {
		t.Fatalf("another item borrowed decision: %d", got.Code)
	}
	if got := f.get(t, start, "X-Plex-Session-Identifier", "first"); got.Code != 200 {
		t.Fatalf("armed start: %d %s", got.Code, got.Body.String())
	}
}

func TestPMSSessionExpiryBoundary(t *testing.T) {
	tracker := newSessionActivities()
	now := time.Now()
	tracker.update("live", func(e *playEntry) { e.updatedAt = now; e.key = core.MovieKey(550) })
	if got := tracker.expire(now.Add(180*time.Second), 180*time.Second); len(got) != 0 {
		t.Fatal("expired at cutoff")
	}
	if got := tracker.expire(now.Add(180*time.Second+time.Nanosecond), 180*time.Second); len(got) != 1 {
		t.Fatal("did not expire after cutoff")
	}
}

func TestPMSSessionControlUnknownGrammar(t *testing.T) {
	f := newFixture()
	defer f.srv.Close()
	for _, action := range []string{"ping", "stop"} {
		for _, tc := range []struct {
			q      string
			status int
		}{{"", 400}, {"?session=", 404}, {"?session=unknown", 404}} {
			rec := f.get(t, "/video/:/transcode/universal/"+action+tc.q)
			if rec.Code != tc.status {
				t.Errorf("%s%s: %d want %d", action, tc.q, rec.Code, tc.status)
			}
		}
	}
	for _, path := range []string{"/status/sessions/garbage", "/transcode/sessions/unknown"} {
		if rec := f.get(t, path); rec.Code != 404 {
			t.Errorf("%s: %d", path, rec.Code)
		}
	}
	if rec := f.request(t, "PATCH", "/status/sessions", nil); rec.Code != 404 {
		t.Fatalf("PATCH sessions: %d", rec.Code)
	}
	if rec := f.get(t, "/status/sessions/terminate"); rec.Code != 400 || !strings.Contains(rec.Body.String(), "400") {
		t.Fatalf("missing terminate id: %d", rec.Code)
	}
}

func TestPMSSessionAppearanceLifecycle(t *testing.T) {
	f := newFixture()
	defer f.srv.Close()
	count := func() int { return int(decodeJSON(t, f.get(t, "/status/sessions", jsonAccept...))["size"].(float64)) }
	f.get(t, "/library/parts/1000000000000550/file.mkv")
	if count() != 0 {
		t.Fatal("part admission created Now Playing")
	}
	f.get(t, "/:/timeline?ratingKey=1000000000000550&state=paused&time=1000&duration=10000")
	if count() != 0 {
		t.Fatal("initial pause created visible session")
	}
	f.get(t, "/:/timeline?ratingKey=1000000000000550&state=playing&time=1000&duration=10000")
	rec := decodeJSON(t, f.get(t, "/status/sessions", jsonAccept...))
	if rec["size"] != float64(1) {
		t.Fatalf("playing not listed: %#v", rec)
	}
	md := rec["Metadata"].([]any)[0].(map[string]any)
	if _, ok := md["Session"]; ok {
		t.Fatal("unarmed player has Session")
	}
	f.get(t, "/:/timeline?ratingKey=1000000000000550&state=stopped&time=1000&duration=10000")
	if count() != 0 {
		t.Fatal("stop retained session")
	}
}

func TestPMSTerminationBlocksOwnerKillsOnly(t *testing.T) {
	f := newFixture()
	defer f.srv.Close()
	pr := &Request{User: &core.User{ID: 1}, ClientID: "client", SessionID: "session"}
	id := trackID(1, "client", "session")
	f.srv.setTermination(id, "failed source")
	if f.srv.playbackTerminated(pr) {
		t.Fatal("failed source blocked retry")
	}
	f.srv.takeTermination(id)
	f.srv.setOwnerTermination(id, "owner stop")
	if !f.srv.playbackTerminated(pr) {
		t.Fatal("owner stop did not block")
	}
	if _, ok := f.srv.takeTermination(id); !ok {
		t.Fatal("missing owner notice")
	}
	if _, ok := f.srv.takeTermination(id); ok {
		t.Fatal("repeated owner notice")
	}
	if !f.srv.playbackTerminated(pr) {
		t.Fatal("notice consumption allowed reopen")
	}
}

// decisionPath preserves the player's session and request options while selecting
// the decision endpoint for tests that exercise a subsequent media start.
func decisionPath(startPath string) string {
	path, query, _ := strings.Cut(startPath, "?")
	i := strings.LastIndex(path, "/")
	return path[:i+1] + "decision?" + query
}
