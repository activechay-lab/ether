package watchlist

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/braedonsaunders/ether/internal/core"
)

const communitySource = "plex-community"

func (s *Service) communityWatchlist(ctx context.Context, token string, user *core.User) ([]core.Title, error) {
	if user == nil || user.IsOwner || user.UUID == "" || token == "" {
		return nil, core.ErrUnauthorized
	}
	base := strings.TrimRight(s.o.CommunityBaseURL, "/")
	if base == "" {
		base = "https://community.plex.tv"
	}
	client := safeCommunityClient(s.o.Client, base)
	verify := map[string]any{"query": "query allFriendsV2 { allFriendsV2 { user { id username } } }"}
	var friends struct {
		Data *struct {
			AllFriendsV2 []struct {
				User *struct {
					ID       string `json:"id"`
					Username string `json:"username"`
				} `json:"user"`
			} `json:"allFriendsV2"`
		} `json:"data"`
		Errors []json.RawMessage `json:"errors"`
	}
	if err := communityPost(ctx, client, base, token, verify, &friends); err != nil {
		return nil, err
	}
	if friends.Data == nil || friends.Data.AllFriendsV2 == nil {
		return nil, errors.New("Plex community friends list incomplete")
	}
	found := false
	for _, f := range friends.Data.AllFriendsV2 {
		if f.User != nil && CommunityIDMatchesUUID(f.User.ID, user.UUID) {
			found = true
			break
		}
	}
	if !found {
		return nil, core.ErrUnauthorized
	}
	out := []core.Title{}
	var cursor *string
	seenCursors := map[string]bool{}
	for page := 0; page < 200; page++ {
		vars := map[string]any{"user": map[string]any{"id": user.UUID}, "first": 100, "after": cursor}
		payload := map[string]any{"query": "query userV2($user: UserInput!, $first: PaginationInt!, $after: String) { userV2(user: $user) { ... on User { watchlist(first: $first, after: $after) { nodes { id type title } pageInfo { hasNextPage endCursor } } } } }", "variables": vars}
		var response struct {
			Data *struct {
				UserV2 *struct {
					Watchlist *struct {
						Nodes []struct {
							ID    string `json:"id"`
							Type  string `json:"type"`
							Title string `json:"title"`
						} `json:"nodes"`
						PageInfo *struct {
							HasNextPage *bool   `json:"hasNextPage"`
							EndCursor   *string `json:"endCursor"`
						} `json:"pageInfo"`
					} `json:"watchlist"`
				} `json:"userV2"`
			} `json:"data"`
			Errors []json.RawMessage `json:"errors"`
		}
		if err := communityPost(ctx, client, base, token, payload, &response); err != nil {
			return nil, err
		}
		if response.Data == nil || response.Data.UserV2 == nil || response.Data.UserV2.Watchlist == nil || response.Data.UserV2.Watchlist.Nodes == nil || response.Data.UserV2.Watchlist.PageInfo == nil || response.Data.UserV2.Watchlist.PageInfo.HasNextPage == nil {
			return nil, errors.New("Plex community watchlist incomplete")
		}
		pageData := response.Data.UserV2.Watchlist
		for _, node := range pageData.Nodes {
			title, err := s.communityNodeTitle(ctx, token, node.ID, node.Type)
			if errors.Is(err, core.ErrNotFound) {
				continue
			}
			if err != nil {
				return nil, err
			}
			title, err = validateCommunityTitle(title, node.Type)
			if err != nil {
				return nil, err
			}
			out = append(out, *title)
		}
		if !*pageData.PageInfo.HasNextPage {
			return out, nil
		}
		if pageData.PageInfo.EndCursor == nil || *pageData.PageInfo.EndCursor == "" || seenCursors[*pageData.PageInfo.EndCursor] {
			return nil, errors.New("Plex community watchlist pagination incomplete")
		}
		seenCursors[*pageData.PageInfo.EndCursor] = true
		next := *pageData.PageInfo.EndCursor
		cursor = &next
	}
	return nil, errors.New("Plex community watchlist exceeds page limit")
}

func safeCommunityClient(original *http.Client, base string) *http.Client {
	var client http.Client
	if original != nil {
		client = *original
	} else {
		client = *http.DefaultClient
	}
	origin, _ := url.Parse(base)
	client.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		if len(via) >= 5 {
			return http.ErrUseLastResponse
		}
		if origin == nil || req.URL.Scheme != origin.Scheme || req.URL.Host != origin.Host {
			return http.ErrUseLastResponse
		}
		return nil
	}
	return &client
}

func communityPost(ctx context.Context, client *http.Client, base, token string, payload any, dst any) error {
	body, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, base+"/api", bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("X-Plex-Token", token)
	resp, err := client.Do(req)
	if err != nil {
		return errors.New("Plex community request failed")
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20+1))
	if err != nil || len(data) > 4<<20 {
		return errors.New("Plex community response unreadable")
	}
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("Plex community HTTP %d", resp.StatusCode)
	}
	if err = json.Unmarshal(data, dst); err != nil {
		return errors.New("Plex community response invalid")
	}
	var envelope struct {
		Errors []json.RawMessage `json:"errors"`
	}
	_ = json.Unmarshal(data, &envelope)
	if len(envelope.Errors) > 0 {
		return errors.New("Plex community GraphQL error")
	}
	return nil
}

// CommunityIDMatchesUUID performs the only supported mapping: exact identity match.
// Exact equality has been observed for two ordinary friends only; broader mapping is unverified.
func CommunityIDMatchesUUID(communityID, userUUID string) bool {
	return communityID != "" && userUUID != "" && communityID == userUUID
}

func communityKind(kind string) string {
	switch strings.ToUpper(kind) {
	case "SHOW":
		return "show"
	case "MOVIE":
		return "movie"
	default:
		return ""
	}
}

func validateCommunityTitle(title *core.Title, kind string) (*core.Title, error) {
	kind = communityKind(kind)
	if title == nil {
		return nil, errors.New("Plex community item unresolved")
	}
	wantType := core.TypeMovie
	wantKind := core.KindMovie
	if kind == "show" {
		wantType = core.TypeShow
		wantKind = core.KindShow
	} else if kind != "movie" {
		return nil, fmt.Errorf("unsupported Plex community media type %q", kind)
	}
	if !title.Key.Valid() || title.Key.Kind() != wantKind || title.Type != wantType {
		return nil, errors.New("Plex community title has invalid or mismatched identity")
	}
	return title, nil
}

func (s *Service) communityNodeTitle(ctx context.Context, token, nodeID, kind string) (*core.Title, error) {
	// Community IDs are opaque alphanumeric path segments, not Plex rating keys.
	kind = communityKind(kind)
	if kind == "" {
		return nil, errors.New("unsupported Plex community media type")
	}
	if nodeID == "" {
		return nil, errors.New("invalid Plex community metadata ID")
	}
	for _, c := range nodeID {
		if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z')) {
			return nil, errors.New("invalid Plex community metadata ID")
		}
	}
	{
		id := url.PathEscape(nodeID)

		base := strings.TrimRight(s.o.PlexBaseURL, "/")
		if base == "" {
			return nil, errors.New("Plex metadata URL unavailable for community item resolution")
		}
		req, e := http.NewRequestWithContext(ctx, http.MethodGet, base+"/library/metadata/"+id+"?includeGuids=1", nil)
		if e != nil {
			return nil, errors.New("Plex metadata URL invalid")
		}
		req.Header.Set("X-Plex-Token", token)
		req.Header.Set("Accept", "application/json")
		client := safeCommunityClient(s.o.Client, base)
		resp, e := client.Do(req)
		if e != nil {
			return nil, errors.New("Plex metadata lookup failed")
		}
		data, e := io.ReadAll(io.LimitReader(resp.Body, (1<<20)+1))
		resp.Body.Close()
		if e != nil || len(data) > 1<<20 {
			return nil, errors.New("Plex metadata response unreadable")
		}
		if resp.StatusCode == http.StatusNotFound {
			return nil, core.ErrNotFound
		}
		if resp.StatusCode != http.StatusOK {
			return nil, fmt.Errorf("Plex metadata HTTP %d", resp.StatusCode)
		}
		var envelope struct {
			MediaContainer *struct {
				Metadata []struct {
					// Discover returns both guid (the Plex identity) and Guid
					// (external IDs). Declare both to prevent encoding/json's
					// case-insensitive fallback from decoding the lowercase
					// string into the external-ID slice.
					PlexGUID string `json:"guid"`
					Type     string `json:"type"`
					Guids    []struct {
						ID string `json:"id"`
					} `json:"Guid"`
				} `json:"Metadata"`
			} `json:"MediaContainer"`
		}
		if json.Unmarshal(data, &envelope) != nil || envelope.MediaContainer == nil || len(envelope.MediaContainer.Metadata) == 0 {
			return nil, errors.New("Plex metadata response invalid")
		}
		metadata := envelope.MediaContainer.Metadata[0]
		if !strings.EqualFold(metadata.Type, communityKind(kind)) {
			return nil, core.ErrNotFound
		}
		for _, prefix := range []string{"tmdb://", "imdb://"} {
			for _, guid := range metadata.Guids {
				if !strings.HasPrefix(guid.ID, prefix) {
					continue
				}
				t, e := s.communityExternalTitle(ctx, guid.ID, kind)
				if e == nil {
					if validated, verr := validateCommunityTitle(t, kind); verr == nil {
						return validated, nil
					}
					continue
				}
				if !errors.Is(e, core.ErrNotFound) {
					return nil, e
				}
			}
		}
		return nil, core.ErrNotFound
	}
}

func (s *Service) communityExternalTitle(ctx context.Context, nodeID, kind string) (*core.Title, error) {
	if kind != "movie" && kind != "show" {
		return nil, fmt.Errorf("unsupported Plex community media type %q", kind)
	}
	for _, prefix := range []string{"tmdb://", "imdb://"} {
		if strings.HasPrefix(nodeID, prefix) {
			ext := strings.TrimPrefix(nodeID, prefix)
			if prefix == "imdb://" {
				t, err := s.o.Catalog.ByExternalID(ctx, "imdb", ext)
				if err != nil {
					return nil, err
				}
				validated, verr := validateCommunityTitle(t, kind)
				if verr != nil {
					return nil, core.ErrNotFound
				}
				return validated, nil
			}
			n, e := strconv.ParseInt(ext, 10, 64)
			if e != nil || n <= 0 {
				return nil, core.ErrNotFound
			}
			k := core.MovieKey(n)
			if strings.EqualFold(kind, "show") {
				k = core.ShowKey(n)
			}
			t, err := s.o.Catalog.Get(ctx, k)
			if err != nil {
				return nil, err
			}
			validated, verr := validateCommunityTitle(t, kind)
			if verr != nil {
				return nil, core.ErrNotFound
			}
			return validated, nil
		}
	}
	return nil, core.ErrNotFound
}
