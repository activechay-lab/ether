package app

import (
	"context"
	"errors"

	"github.com/braedonsaunders/ether/internal/admin"
	"github.com/braedonsaunders/ether/internal/trakt"
)

func (a *App) traktAPI() admin.Trakt {
	if a.trakt == nil {
		return nil
	}
	return traktAdmin{client: a.trakt}
}

type traktAdmin struct {
	client *trakt.Client
}

func mapTraktStatus(st trakt.UserStatus) admin.TraktStatus {
	out := admin.TraktStatus{
		Configured: st.Configured,
		Connected:  st.Connected,
		Username:   st.Username,
		Status:     string(st.Status),
	}
	if st.Device != nil {
		out.Device = &admin.TraktDevice{
			UserCode:        st.Device.UserCode,
			VerificationURL: st.Device.VerificationURL,
			ExpiresAt:       st.Device.ExpiresAt,
			Interval:        st.Device.Interval,
		}
	}
	return out
}

func mapTraktErr(err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, trakt.ErrNotConfigured):
		return admin.ErrTraktNotConfigured
	case errors.Is(err, trakt.ErrNoDeviceCode):
		return admin.ErrTraktNoDeviceCode
	default:
		return err
	}
}

func (t traktAdmin) Status(ctx context.Context, userID int64) (admin.TraktStatus, error) {
	st, err := t.client.UserStatus(ctx, userID)
	return mapTraktStatus(st), mapTraktErr(err)
}

func (t traktAdmin) StartDevice(ctx context.Context, userID int64) (admin.TraktStatus, error) {
	st, err := t.client.StartUserDevice(ctx, userID)
	return mapTraktStatus(st), mapTraktErr(err)
}

func (t traktAdmin) PollDevice(ctx context.Context, userID int64) (admin.TraktStatus, error) {
	st, err := t.client.PollUserDevice(ctx, userID)
	return mapTraktStatus(st), mapTraktErr(err)
}

func (t traktAdmin) Disconnect(ctx context.Context, userID int64) (admin.TraktStatus, error) {
	st, err := t.client.DisconnectUser(ctx, userID)
	return mapTraktStatus(st), mapTraktErr(err)
}
