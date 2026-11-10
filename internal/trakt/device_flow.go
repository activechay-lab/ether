package trakt

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"
)

const (
	defaultActivateURL = "https://trakt.tv/activate"
	maxPendingDevices  = 256
)

var (
	// ErrNotConfigured reports that the server owner has not enabled Trakt.
	ErrNotConfigured = errors.New("trakt: not configured")
	// ErrNoDeviceCode reports that no active device-code flow exists for the user.
	ErrNoDeviceCode = errors.New("trakt: no pending device code")
)

// DeviceChallenge is the browser-safe portion of an active OAuth device flow.
// The secret device_code is held only on the server.
type DeviceChallenge struct {
	UserCode        string    `json:"userCode"`
	VerificationURL string    `json:"verificationUrl"`
	ExpiresAt       time.Time `json:"expiresAt"`
	Interval        int       `json:"interval"`
}

// UserStatus is a viewer's Trakt connection state without any OAuth tokens or
// secret device codes.
type UserStatus struct {
	Configured bool             `json:"configured"`
	Connected  bool             `json:"connected"`
	Username   string           `json:"username,omitempty"`
	Status     PollStatus       `json:"status,omitempty"`
	Device     *DeviceChallenge `json:"device,omitempty"`
}

type deviceFlow struct {
	clientID        string
	deviceCode      string
	userCode        string
	verificationURL string
	interval        time.Duration
	intervalSec     int
	nextPoll        time.Time
	expiresAt       time.Time
	polling         bool
}

func (f *deviceFlow) challenge() DeviceChallenge {
	return DeviceChallenge{
		UserCode:        f.userCode,
		VerificationURL: f.verificationURL,
		ExpiresAt:       f.expiresAt,
		Interval:        f.intervalSec,
	}
}

func safeVerificationURL(raw string) string {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.Scheme != "https" || u.User != nil {
		return defaultActivateURL
	}
	host := strings.ToLower(u.Hostname())
	if host != "trakt.tv" && !strings.HasSuffix(host, ".trakt.tv") {
		return defaultActivateURL
	}
	return u.String()
}

func (c *Client) pruneDevicesLocked(now time.Time) {
	for uid, f := range c.devices {
		if f == nil || !now.Before(f.expiresAt) {
			delete(c.devices, uid)
		}
	}
	for len(c.devices) >= maxPendingDevices {
		var oldestID int64
		var oldestAt time.Time
		first := true
		for uid, f := range c.devices {
			if first || f.expiresAt.Before(oldestAt) {
				oldestID = uid
				oldestAt = f.expiresAt
				first = false
			}
		}
		if first {
			break
		}
		delete(c.devices, oldestID)
	}
}

func (c *Client) currentTokens(ctx context.Context, userID int64) (connected bool, username string, err error) {
	tok, ok, err := c.Tokens(ctx, userID)
	if err != nil {
		return false, "", err
	}
	if !ok {
		return false, "", nil
	}
	return true, tok.Username, nil
}

// UserStatus reports whether Trakt is configured by the owner, whether userID
// has connected their Trakt account, and any active browser-safe device code.
func (c *Client) UserStatus(ctx context.Context, userID int64) (UserStatus, error) {
	if !c.Enabled() {
		c.devMu.Lock()
		delete(c.devices, userID)
		c.devMu.Unlock()
		return UserStatus{Configured: false, Connected: false}, nil
	}
	connected, username, err := c.currentTokens(ctx, userID)
	if err != nil {
		return UserStatus{}, err
	}
	id, _ := c.creds()
	now := c.now()
	var dev *DeviceChallenge
	c.devMu.Lock()
	if f := c.devices[userID]; f != nil {
		if f.clientID == id && now.Before(f.expiresAt) {
			ch := f.challenge()
			dev = &ch
		} else {
			delete(c.devices, userID)
		}
	}
	c.devMu.Unlock()
	return UserStatus{
		Configured: true,
		Connected:  connected,
		Username:   username,
		Device:     dev,
	}, nil
}

// StartUserDevice starts a new OAuth device-code flow for userID and retains
// the secret device_code server-side for paced polling.
func (c *Client) StartUserDevice(ctx context.Context, userID int64) (UserStatus, error) {
	if !c.Enabled() {
		return UserStatus{Configured: false}, ErrNotConfigured
	}
	id, _ := c.creds()
	dc, err := c.StartDevice(ctx)
	if err != nil {
		return UserStatus{}, err
	}
	if dc.DeviceCode == "" || dc.UserCode == "" {
		return UserStatus{}, fmt.Errorf("trakt: incomplete device code response")
	}
	intervalSec := max(dc.Interval, 1)
	expiresSec := dc.ExpiresIn
	if expiresSec <= 0 {
		expiresSec = 600
	} else if expiresSec > 3600 {
		expiresSec = 3600
	}
	unit := c.pollUnit
	if unit <= 0 {
		unit = time.Second
	}
	interval := time.Duration(intervalSec) * unit
	now := c.now()
	flow := &deviceFlow{
		clientID:        id,
		deviceCode:      dc.DeviceCode,
		userCode:        dc.UserCode,
		verificationURL: safeVerificationURL(dc.VerificationURL),
		interval:        interval,
		intervalSec:     intervalSec,
		nextPoll:        now.Add(interval),
		expiresAt:       now.Add(time.Duration(expiresSec) * unit).UTC(),
	}
	ch := flow.challenge()
	c.devMu.Lock()
	c.pruneDevicesLocked(now)
	c.devices[userID] = flow
	c.devMu.Unlock()

	connected, username, err := c.currentTokens(ctx, userID)
	if err != nil {
		return UserStatus{}, err
	}
	return UserStatus{
		Configured: true,
		Connected:  connected,
		Username:   username,
		Status:     PollPending,
		Device:     &ch,
	}, nil
}

// PollUserDevice checks an active device-code flow for userID, honouring
// DeviceCode.Interval, PollSlowDown (+5s), and DeviceCode.ExpiresIn on the
// server before calling Trakt.
func (c *Client) PollUserDevice(ctx context.Context, userID int64) (UserStatus, error) {
	if !c.Enabled() {
		c.devMu.Lock()
		delete(c.devices, userID)
		c.devMu.Unlock()
		return UserStatus{Configured: false}, ErrNotConfigured
	}
	id, _ := c.creds()
	now := c.now()

	c.devMu.Lock()
	flow := c.devices[userID]
	if flow == nil {
		c.devMu.Unlock()
		connected, username, err := c.currentTokens(ctx, userID)
		if err != nil {
			return UserStatus{}, err
		}
		if connected {
			return UserStatus{Configured: true, Connected: true, Username: username, Status: PollAuthorized}, nil
		}
		return UserStatus{Configured: true}, ErrNoDeviceCode
	}
	if flow.clientID != id || !now.Before(flow.expiresAt) {
		delete(c.devices, userID)
		c.devMu.Unlock()
		connected, username, err := c.currentTokens(ctx, userID)
		if err != nil {
			return UserStatus{}, err
		}
		return UserStatus{Configured: true, Connected: connected, Username: username, Status: PollExpired}, nil
	}
	if flow.polling || now.Before(flow.nextPoll) {
		ch := flow.challenge()
		c.devMu.Unlock()
		connected, username, err := c.currentTokens(ctx, userID)
		if err != nil {
			return UserStatus{}, err
		}
		return UserStatus{
			Configured: true,
			Connected:  connected,
			Username:   username,
			Status:     PollPending,
			Device:     &ch,
		}, nil
	}
	flow.polling = true
	flow.nextPoll = now.Add(flow.interval)
	code := flow.deviceCode
	c.devMu.Unlock()

	st, err := c.PollDevice(ctx, userID, code)

	c.devMu.Lock()
	current := c.devices[userID]
	if current != flow {
		disconnected := current == nil
		c.devMu.Unlock()
		if err != nil {
			return UserStatus{}, err
		}
		if st == PollAuthorized && disconnected {
			// Disconnect ran while the token request was in flight; honour it.
			_ = c.Disconnect(ctx, userID)
			return UserStatus{Configured: true, Connected: false}, ErrNoDeviceCode
		}
		return c.UserStatus(ctx, userID)
	}
	if err != nil {
		flow.polling = false
		c.devMu.Unlock()
		return UserStatus{}, err
	}
	unit := c.pollUnit
	if unit <= 0 {
		unit = time.Second
	}
	switch st {
	case PollPending:
		flow.polling = false
		flow.nextPoll = c.now().Add(flow.interval)
		ch := flow.challenge()
		c.devMu.Unlock()
		connected, username, terr := c.currentTokens(ctx, userID)
		if terr != nil {
			return UserStatus{}, terr
		}
		return UserStatus{
			Configured: true,
			Connected:  connected,
			Username:   username,
			Status:     PollPending,
			Device:     &ch,
		}, nil
	case PollSlowDown:
		flow.polling = false
		flow.intervalSec += 5
		flow.interval += 5 * unit
		flow.nextPoll = c.now().Add(flow.interval)
		ch := flow.challenge()
		c.devMu.Unlock()
		connected, username, terr := c.currentTokens(ctx, userID)
		if terr != nil {
			return UserStatus{}, terr
		}
		return UserStatus{
			Configured: true,
			Connected:  connected,
			Username:   username,
			Status:     PollSlowDown,
			Device:     &ch,
		}, nil
	default:
		delete(c.devices, userID)
		c.devMu.Unlock()
		connected, username, terr := c.currentTokens(ctx, userID)
		if terr != nil {
			return UserStatus{}, terr
		}
		return UserStatus{
			Configured: true,
			Connected:  connected,
			Username:   username,
			Status:     st,
		}, nil
	}
}

// DisconnectUser removes stored Trakt tokens and any pending device code for userID.
func (c *Client) DisconnectUser(ctx context.Context, userID int64) (UserStatus, error) {
	if err := c.Disconnect(ctx, userID); err != nil {
		return UserStatus{}, err
	}
	return UserStatus{Configured: c.Enabled(), Connected: false}, nil
}
