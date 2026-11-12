package plextv

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
)

var ErrHomePINRequired = errors.New("Plex Home PIN required")
var ErrPlexRefused = errors.New("Plex refused Home switch")

// HomeUserToken uses the owner's authorized Home switch operation. A PIN, when
// configured by Plex, is still required; this never resets or bypasses it.
func (c *Client) HomeUserToken(ctx context.Context, ownerToken string, id int64, pin string) (string, error) {
	if id <= 0 {
		return "", ErrPlexRefused
	}
	q := url.Values{}
	if pin != "" {
		q.Set("pin", pin)
	}
	res, err := c.do(ctx, request{method: http.MethodPost, path: "/api/home/users/" + strconv.FormatInt(id, 10) + "/switch", token: ownerToken, query: q, accept: "application/xml"})
	if err != nil {
		// Some Plex responses say only "Unauthorized" for a protected member.
		// Look up PIN protection only after a refused switch, never on success.
		var api *APIError
		if pin == "" && errors.As(err, &api) && (api.Status == http.StatusUnauthorized || api.Status == http.StatusForbidden) {
			if res, lookupErr := c.do(ctx, request{method: http.MethodGet, path: "/api/home/users", token: ownerToken, accept: "application/xml"}); lookupErr == nil {
				if root, parseErr := parseXMLNode(res.body); parseErr == nil {
					for _, member := range root.findAll("User") {
						if member.intAttr("id") == id && member.boolAttr("protected") {
							return "", fmt.Errorf("%w: %v", ErrHomePINRequired, err)
						}
					}
				}
			}
		}
		return "", homeSwitchError(err)
	}
	root, err := parseXMLNode(res.body)
	if err != nil {
		return "", homeSwitchError(err)
	}
	token := root.attr("authenticationToken", "authToken")
	if token == "" {
		return "", ErrPlexRefused
	}
	account, err := c.User(ctx, token)
	if err != nil {
		return "", homeSwitchError(err)
	}
	if account.ID != id {
		return "", ErrPlexRefused
	}
	return token, nil
}

// homeSwitchError preserves a typed refusal without confusing it with a shared friend.
func homeSwitchError(err error) error {
	var api *APIError
	if errors.As(err, &api) && strings.Contains(strings.ToLower(api.Message), "pin") {
		return fmt.Errorf("%w: %v", ErrHomePINRequired, err)
	}
	return fmt.Errorf("%w: %v", ErrPlexRefused, err)
}
