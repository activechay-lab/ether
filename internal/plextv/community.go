package plextv

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sync"
	"time"
)

// CommunityClient reads only data visible to the owner's Plex account.
// A single client serializes requests, including concurrent explicit syncs.
type CommunityClient struct {
	Client *http.Client
	URL    string
	mu     sync.Mutex
	last   time.Time
}
type CommunityFriend struct {
	ID          string `json:"id"`
	Username    string `json:"username"`
	DisplayName string `json:"displayName"`
	Avatar      string `json:"avatar"`
}
type CommunityItem struct {
	ID    string `json:"id"`
	Title string `json:"title"`
	Type  string `json:"type"`
}
type CommunityPage struct {
	Nodes    []CommunityItem `json:"nodes"`
	PageInfo struct {
		HasNextPage bool   `json:"hasNextPage"`
		EndCursor   string `json:"endCursor"`
	} `json:"pageInfo"`
}

// Wait reserves a request slot for GUID enrichment during a community sync.
func (c *CommunityClient) Wait(ctx context.Context) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if delay := time.Until(c.last.Add(time.Second)); delay > 0 {
		timer := time.NewTimer(delay)
		defer timer.Stop()
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-timer.C:
		}
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	c.last = time.Now()
	return nil
}

func (c *CommunityClient) query(ctx context.Context, token, query string, variables any, out any) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if delay := time.Until(c.last.Add(time.Second)); delay > 0 {
		timer := time.NewTimer(delay)
		defer timer.Stop()
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-timer.C:
		}
	}
	if token == "" {
		return errors.New("Plex community requires owner authorization")
	}
	raw, err := json.Marshal(map[string]any{"query": query, "variables": variables})
	if err != nil {
		return err
	}
	endpoint := c.URL
	if endpoint == "" {
		endpoint = "https://community.plex.tv/api"
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(raw))
	if err != nil {
		return errors.New("Plex community URL invalid")
	}
	req.Header.Set("X-Plex-Token", token)
	req.Header.Set("Content-Type", "application/json")
	client := c.Client
	if client == nil {
		client = &http.Client{Timeout: 20 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	}
	c.last = time.Now()
	resp, err := client.Do(req)
	if err != nil {
		return errors.New("Plex community request failed")
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("Plex community: HTTP %d", resp.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, (4<<20)+1))
	if err != nil || len(data) > 4<<20 {
		return errors.New("Plex community response unreadable or too large")
	}
	var envelope struct {
		Data   json.RawMessage   `json:"data"`
		Errors []json.RawMessage `json:"errors"`
	}
	if json.Unmarshal(data, &envelope) != nil || len(envelope.Errors) > 0 || len(envelope.Data) == 0 || string(envelope.Data) == "null" {
		return errors.New("Plex community query refused or response invalid")
	}
	return json.Unmarshal(envelope.Data, out)
}
func (c *CommunityClient) Friends(ctx context.Context, token string) ([]CommunityFriend, error) {
	var data struct {
		Friends *[]struct {
			User CommunityFriend `json:"user"`
		} `json:"allFriendsV2"`
	}
	err := c.query(ctx, token, `query { allFriendsV2 { user { id username displayName avatar } } }`, nil, &data)
	if err != nil {
		return nil, err
	}
	if data.Friends == nil {
		return nil, errors.New("Plex community friends response missing")
	}
	out := make([]CommunityFriend, 0, len(*data.Friends))
	for _, f := range *data.Friends {
		if f.User.ID == "" {
			return nil, errors.New("Plex community friend identity missing")
		}
		out = append(out, f.User)
	}
	return out, nil
}

// WatchlistPage reads the user's watchlist with UserInput and cursor pagination.
func (c *CommunityClient) WatchlistPage(ctx context.Context, token, id, after string, first int) (*CommunityPage, error) {
	if first < 1 || first > 100 {
		first = 100
	}
	var cursor any
	if after != "" {
		cursor = after
	}
	var data struct {
		User *struct {
			Watchlist *CommunityPage `json:"watchlist"`
		} `json:"userV2"`
	}
	err := c.query(ctx, token, `query ($user: UserInput!, $first: PaginationInt!, $after: String) { userV2(user: $user) { ... on User { watchlist(first: $first, after: $after) { nodes { id title type } pageInfo { hasNextPage endCursor } } } } }`, map[string]any{"user": map[string]string{"id": id}, "first": first, "after": cursor}, &data)
	if err != nil {
		return nil, err
	}
	if data.User == nil || data.User.Watchlist == nil || data.User.Watchlist.Nodes == nil {
		return nil, errors.New("Plex community watchlist unavailable; check friend visibility")
	}
	return data.User.Watchlist, nil
}
