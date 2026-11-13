package watchlist

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
)

func TestCommunityWriterLockExcludesScheduledAndAsyncWriters(t *testing.T) {
	s, _, _, _, _, _ := setup(t)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	entered, release := make(chan struct{}), make(chan struct{})
	firstDone := make(chan error, 1)
	go func() {
		firstDone <- s.withCommunityWriterLock(ctx, 2, func() error { close(entered); <-release; return nil })
	}()
	<-entered
	secondDone := make(chan error, 1)
	go func() { secondDone <- s.withCommunityWriterLock(ctx, 2, func() error { return nil }) }()
	select {
	case err := <-secondDone:
		t.Fatalf("second writer passed active lease, err=%v", err)
	case <-time.After(150 * time.Millisecond):
	}
	close(release)
	if err := <-firstDone; err != nil {
		t.Fatal(err)
	}
	if err := <-secondDone; err != nil {
		t.Fatal(err)
	}
}

func TestCommunityJobRechecksOptOutBeforeCommit(t *testing.T) {
	s, _, _, _, source, _ := setup(t)
	ctx := context.Background()
	viewer := &core.User{ID: 2, UUID: "friend-uuid"}
	if err := s.SetCommunitySyncEnabled(ctx, 2, true); err != nil {
		t.Fatal(err)
	}
	if err := s.upsert(ctx, 2, communitySource, source.titles[0]); err != nil {
		t.Fatal(err)
	}
	s.o.CommunityEligible = func(*core.User) bool { return true }
	calls := 0
	s.o.Users = func(context.Context) ([]core.User, error) {
		calls++
		if calls >= 2 {
			s.o.CommunityEligible = func(*core.User) bool { return false }
		}
		return []core.User{{ID: 1, IsOwner: true}, {ID: 2, UUID: "friend-uuid"}}, nil
	}
	if _, err := s.replaceCommunitySnapshot(ctx, viewer, []core.Title{source.titles[1]}); err == nil {
		t.Fatal("expected opt-out to prevent commit")
	}
	items, err := s.Items(ctx, viewer)
	if err != nil || len(items) != 1 || items[0].Title.Key != source.titles[0].Key {
		t.Fatalf("previous snapshot lost: %+v err=%v", items, err)
	}
}

func TestCommunityJobRecoveryRunsOncePerService(t *testing.T) {
	s, _, _, _, _, _ := setup(t)
	ctx := context.Background()
	if err := s.initCommunityJobs(); err != nil {
		t.Fatal(err)
	}
	now := s.o.Now().Unix()
	if _, err := s.db.ExecContext(ctx, `INSERT INTO watchlist_community_jobs(user_id,state,created_at) VALUES(2,'queued',?)`, now); err != nil {
		t.Fatal(err)
	}
	if err := s.initCommunityJobs(); err != nil {
		t.Fatal(err)
	}
	var state string
	if err := s.db.QueryRowContext(ctx, `SELECT state FROM watchlist_community_jobs WHERE user_id=2`).Scan(&state); err != nil {
		t.Fatal(err)
	}
	if state != "queued" {
		t.Fatalf("new queued job changed to %q", state)
	}

	// A new Service instance on the same database marks prior queued/running jobs interrupted at startup.
	restarted := New(s.db, s.o)
	job, err := restarted.communityJobForUser(ctx, 1, 2)
	if err != nil {
		t.Fatal(err)
	}
	if job.State != CommunityJobInterrupted || job.FinishedAt == nil {
		t.Fatalf("restarted job = %+v, want interrupted with finishedAt", job)
	}
}

func TestCommunityJobRoutesLifecycleScopingAndEpisodeCleanup(t *testing.T) {
	s, _, _, _, _, _ := setup(t)
	ctx := context.Background()
	showKey := core.ShowKey(65804)
	s.o.Catalog = fakeCatalog{titles: map[core.RatingKey]core.Title{
		showKey: {Key: showKey, Type: core.TypeShow, Title: "Show A"},
	}}
	s.o.Users = func(context.Context) ([]core.User, error) {
		return []core.User{
			{ID: 1, IsOwner: true},
			{ID: 2, PlexID: 7, UUID: "friend-uuid"},
			{ID: 3, PlexID: 8, UUID: "other-uuid"},
		}, nil
	}
	s.o.Tokens = func(_ context.Context, id int64) (string, error) {
		if id == 1 {
			return "owner-token", nil
		}
		return "", core.ErrUnauthorized
	}
	s.o.CommunityEligible = func(u *core.User) bool { return u != nil && !u.IsOwner }

	metadata := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"MediaContainer":{"Metadata":[{"type":"show","guid":"plex://show/example","Guid":[{"id":"tmdb://65804"}]}]}}`)
	}))
	defer metadata.Close()

	blockFetch := make(chan struct{})
	communitySrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Query string `json:"query"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		w.Header().Set("Content-Type", "application/json")
		if strings.Contains(req.Query, "allFriendsV2") {
			<-blockFetch
			fmt.Fprint(w, `{"data":{"allFriendsV2":[{"user":{"id":"friend-uuid","username":"friend"}}]}}`)
			return
		}
		fmt.Fprint(w, `{"data":{"userV2":{"watchlist":{"nodes":[{"id":"opaqueShowA","type":"SHOW"}],"pageInfo":{"hasNextPage":false,"endCursor":null}}}}}`)
	}))
	defer communitySrv.Close()

	s.o.PlexBaseURL = metadata.URL
	s.o.CommunityBaseURL = communitySrv.URL
	router := s.Router()

	call := func(method, path string) *httptest.ResponseRecorder {
		t.Helper()
		r := httptest.NewRequest(method, path, strings.NewReader("{}"))
		w := httptest.NewRecorder()
		router.ServeHTTP(w, r)
		return w
	}

	// Without opt-in, POST /community/sync returns 403.
	if w := call(http.MethodPost, "/users/2/community/sync"); w.Code != http.StatusForbidden {
		t.Fatalf("unopted sync status=%d, want 403", w.Code)
	}
	if err := s.SetCommunitySyncEnabled(ctx, 2, true); err != nil {
		t.Fatal(err)
	}

	// Start async job and verify concurrent POST deduplicates to the same job ID.
	w1 := call(http.MethodPost, "/users/2/community/sync")
	if w1.Code != http.StatusAccepted {
		t.Fatalf("first POST status=%d body=%s, want 202", w1.Code, w1.Body.String())
	}
	var job1 CommunityJob
	if err := json.Unmarshal(w1.Body.Bytes(), &job1); err != nil {
		t.Fatal(err)
	}
	w2 := call(http.MethodPost, "/users/2/community/sync")
	if w2.Code != http.StatusAccepted {
		t.Fatalf("second POST status=%d body=%s, want 202", w2.Code, w2.Body.String())
	}
	var job2 CommunityJob
	if err := json.Unmarshal(w2.Body.Bytes(), &job2); err != nil {
		t.Fatal(err)
	}
	if job1.ID <= 0 || job2.ID != job1.ID {
		t.Fatalf("expected deduplicated job ID: job1=%+v job2=%+v", job1, job2)
	}

	// Another viewer cannot inspect viewer 2's job.
	if wOther := call(http.MethodGet, fmt.Sprintf("/users/3/community/jobs/%d", job1.ID)); wOther.Code != http.StatusNotFound {
		t.Fatalf("cross-viewer job GET status=%d, want 404", wOther.Code)
	}

	close(blockFetch)
	deadline := time.Now().Add(3 * time.Second)
	for {
		wPoll := call(http.MethodGet, fmt.Sprintf("/users/2/community/jobs/%d", job1.ID))
		if wPoll.Code != http.StatusOK {
			t.Fatalf("poll status=%d body=%s", wPoll.Code, wPoll.Body.String())
		}
		var polled CommunityJob
		if err := json.Unmarshal(wPoll.Body.Bytes(), &polled); err != nil {
			t.Fatal(err)
		}
		if polled.State == CommunityJobCompleted {
			if polled.Count != 1 {
				t.Fatalf("completed count=%d, want 1", polled.Count)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("job did not complete in time; last=%+v", polled)
		}
		time.Sleep(20 * time.Millisecond)
	}

	// Seed a handled episode for showKey, then replace snapshot with empty list and verify episode is forgotten.
	if _, err := s.db.ExecContext(ctx, `INSERT INTO watchlist_episodes(user_id,item_key,episode_key,at) VALUES(2,?,?,1)`, showKey, core.EpisodeKey(65804, 1, 1)); err != nil {
		t.Fatal(err)
	}
	if _, err := s.replaceCommunitySnapshot(ctx, &core.User{ID: 2, UUID: "friend-uuid"}, nil); err != nil {
		t.Fatal(err)
	}
	var epCount int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM watchlist_episodes WHERE user_id=2 AND item_key=?`, showKey).Scan(&epCount); err != nil {
		t.Fatal(err)
	}
	if epCount != 0 {
		t.Fatalf("handled episodes remaining after Community removal = %d, want 0", epCount)
	}
}
