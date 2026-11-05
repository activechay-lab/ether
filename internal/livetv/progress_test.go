package livetv

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestImportProgressEndpoint(t *testing.T) {
	s := testService(t, Options{})
	tracker := s.beginImportProgress("job")
	tracker.setPhase("guide")
	tracker.setTotal(4096)
	tracker.addRead(1024)
	tracker.setChannels(12)
	tracker.setProgrammes(34)
	tracker.setSavedChannels(7)
	tracker.setSavedProgrammes(3)

	var got SourceProgress
	if code := ownerJSON(t, s, "GET", "/sources/progress/job", "", &got); code != 200 {
		t.Fatalf("progress: %d", code)
	}
	if got.Phase != "guide" || got.Read != 1024 || got.Total != 4096 || got.Channels != 12 || got.Programmes != 34 || got.SavedChannels != 7 || got.SavedProgrammes != 3 {
		t.Fatalf("progress: %+v", got)
	}
	s.endImportProgress("job")
	if _, ok := s.ImportProgress("job"); ok {
		t.Fatal("progress survived the import")
	}
	if code := ownerJSON(t, s, "GET", "/sources/progress/job", "", nil); code != 404 {
		t.Fatalf("finished progress: %d", code)
	}
}

func TestImportProgressTracksFetchPhases(t *testing.T) {
	release := make(chan struct{})
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/player_api.php":
			writeXtreamDocuments(w, r)
		case "/xmltv.php":
			select {
			case <-release:
			case <-r.Context().Done():
				return
			}
			w.Write([]byte(xmltvFixture(2, "news.us=News")))
		}
	}))
	defer up.Close()
	s := testService(t, Options{HTTPClient: up.Client()})
	tracker := s.beginImportProgress("job")
	ctx := withImportProgress(context.Background(), tracker)
	done := make(chan error, 1)
	go func() {
		_, err := s.ImportSource(ctx, Source{ID: "x", Name: "Panel", Auth: &SourceAuth{Type: "xtream", Server: up.URL, Username: "u", Password: "p"}})
		done <- err
	}()
	deadline := time.Now().Add(3 * time.Second)
	for {
		p, ok := s.ImportProgress("job")
		if ok && p.Phase == "guide" && p.Channels == 1 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("progress never reached the guide: %+v", p)
		}
		time.Sleep(10 * time.Millisecond)
	}
	close(release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	p, ok := s.ImportProgress("job")
	if !ok || p.Phase != "saving" || p.Channels != 1 || p.SavedChannels != 1 {
		t.Fatalf("final progress: %+v ok=%v", p, ok)
	}
	s.endImportProgress("job")
}
