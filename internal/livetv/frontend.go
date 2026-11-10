package livetv

import (
	"context"
	"net/http"
	"time"

	"github.com/braedonsaunders/ether/internal/frontend"
)

// Frontend returns the frontend.LiveTVHost adapter for this service, or nil when s is nil.
func (s *Service) Frontend() frontend.LiveTVHost {
	if s == nil {
		return nil
	}
	return liveTVHost{s: s}
}

// ToFrontendChannel converts a Channel to the frontend boundary value type.
func ToFrontendChannel(c Channel) frontend.LiveTVChannel {
	return frontend.LiveTVChannel{
		ID:            c.ID,
		Number:        c.Number,
		Name:          c.Name,
		Kind:          c.Kind,
		Enabled:       c.Enabled,
		Logo:          c.Logo,
		LogoMatched:   c.LogoMatched,
		Rating:        c.Rating,
		SourceID:      c.SourceID,
		GuideID:       c.GuideID,
		GuideSourceID: c.GuideSourceID,
		Group:         c.Group,
		Personal:      c.Personal != nil,
	}
}

// FromFrontendChannel converts a frontend boundary channel to a Channel.
func FromFrontendChannel(c frontend.LiveTVChannel) Channel {
	ch := Channel{
		ID:            c.ID,
		Number:        c.Number,
		Name:          c.Name,
		Kind:          c.Kind,
		Enabled:       c.Enabled,
		Logo:          c.Logo,
		LogoMatched:   c.LogoMatched,
		Rating:        c.Rating,
		SourceID:      c.SourceID,
		GuideID:       c.GuideID,
		GuideSourceID: c.GuideSourceID,
		Group:         c.Group,
	}
	if c.Personal {
		ch.Personal = &PersonalRecipe{}
	}
	return ch
}

func toFrontendChannels(cs []Channel) []frontend.LiveTVChannel {
	out := make([]frontend.LiveTVChannel, len(cs))
	for i := range cs {
		out[i] = ToFrontendChannel(cs[i])
	}
	return out
}

func toFrontendItem(item Item) frontend.LiveTVItem {
	return frontend.LiveTVItem{
		Key:          item.Key,
		Title:        item.Title,
		EpisodeTitle: item.EpisodeTitle,
		ShowTitle:    item.ShowTitle,
		Summary:      item.Summary,
		Rating:       item.Rating,
		Thumb:        item.Thumb,
		Art:          item.Art,
		Genres:       item.Genres,
		GUID:         item.GUID,
		ShowGUID:     item.ShowGUID,
		MediaType:    item.MediaType,
		Premiere:     item.Premiere,
		Live:         item.Live,
		Season:       item.Season,
		Episode:      item.Episode,
		ReleasedMS:   item.ReleasedMS,
	}
}

func toFrontendProgramme(p Programme) frontend.LiveTVProgramme {
	return frontend.LiveTVProgramme{
		ID:        p.ID,
		ChannelID: p.ChannelID,
		Start:     p.Start,
		End:       p.End,
		Lens:      p.Lens,
		Item:      toFrontendItem(p.Item),
	}
}

func toFrontendProgrammes(ps []Programme) []frontend.LiveTVProgramme {
	out := make([]frontend.LiveTVProgramme, len(ps))
	for i := range ps {
		out[i] = toFrontendProgramme(ps[i])
	}
	return out
}

func toFrontendSessionInfo(info SessionInfo) frontend.LiveTVSessionInfo {
	out := frontend.LiveTVSessionInfo{
		ID:             info.ID,
		SubscriptionID: info.SubscriptionID,
		ActivityID:     info.ActivityID,
		MediaInfo:      info.MediaInfo,
		Channel:        ToFrontendChannel(info.Channel),
		StartedAt:      info.StartedAt,
	}
	if info.Programme != nil {
		prog := toFrontendProgramme(*info.Programme)
		out.Programme = &prog
	}
	return out
}

type liveTVHost struct {
	s *Service
}

func (h liveTVHost) VisibleTo(ctx context.Context, u *frontend.User) bool {
	return h.s.VisibleTo(ctx, u)
}

func (h liveTVHost) ServeXMLTV(w http.ResponseWriter, r *http.Request, u *frontend.User) {
	h.s.ServeXMLTV(w, r, u)
}

func (h liveTVHost) Stop(ctx context.Context, id string, u *frontend.User) error {
	return h.s.Stop(ctx, id, u)
}

func (h liveTVHost) ServeDASHPart(w http.ResponseWriter, r *http.Request, id string, u *frontend.User, rep, n int, init bool) {
	h.s.ServeDASHPart(w, r, id, u, rep, n, init)
}

func (h liveTVHost) ServePlaylist(w http.ResponseWriter, r *http.Request, id string, u *frontend.User) {
	h.s.ServePlaylist(w, r, id, u)
}

func (h liveTVHost) ServeRelaySegment(w http.ResponseWriter, r *http.Request, id string, n int, u *frontend.User) {
	h.s.ServeRelaySegment(w, r, id, n, u)
}

func (h liveTVHost) ServeResource(w http.ResponseWriter, r *http.Request, id, resource string, u *frontend.User) {
	h.s.ServeResource(w, r, id, resource, u)
}

func (h liveTVHost) AllowedChannels(ctx context.Context, u *frontend.User) ([]frontend.LiveTVChannel, error) {
	cs, err := h.s.AllowedChannels(ctx, u)
	if err != nil {
		return nil, err
	}
	return toFrontendChannels(cs), nil
}

func (h liveTVHost) AllowedChannel(ctx context.Context, u *frontend.User, id string) (frontend.LiveTVChannel, error) {
	c, err := h.s.AllowedChannel(ctx, u, id)
	if err != nil {
		return frontend.LiveTVChannel{}, err
	}
	return ToFrontendChannel(c), nil
}

func (h liveTVHost) Channel(ctx context.Context, id string) (frontend.LiveTVChannel, error) {
	c, err := h.s.Channel(ctx, id)
	if err != nil {
		return frontend.LiveTVChannel{}, err
	}
	return ToFrontendChannel(c), nil
}

func (h liveTVHost) ChannelForViewer(ctx context.Context, u *frontend.User, id string) (frontend.LiveTVChannel, error) {
	c, err := h.s.ChannelForViewer(ctx, u, id)
	if err != nil {
		return frontend.LiveTVChannel{}, err
	}
	return ToFrontendChannel(c), nil
}

func (h liveTVHost) Channels(ctx context.Context) ([]frontend.LiveTVChannel, error) {
	cs, err := h.s.Channels(ctx)
	if err != nil {
		return nil, err
	}
	return toFrontendChannels(cs), nil
}

func (h liveTVHost) ChannelPermitted(ctx context.Context, u *frontend.User, id string) (bool, error) {
	return h.s.ChannelPermitted(ctx, u, id)
}

func (h liveTVHost) LineupChangedFor(u *frontend.User) time.Time {
	return h.s.LineupChangedFor(u)
}

func (h liveTVHost) GuideForChannels(ctx context.Context, u *frontend.User, from, until time.Time, channelIDs []string) ([]frontend.LiveTVProgramme, error) {
	ps, err := h.s.GuideForChannels(ctx, u, from, until, channelIDs)
	if err != nil {
		return nil, err
	}
	return toFrontendProgrammes(ps), nil
}

func (h liveTVHost) Guide(ctx context.Context, u *frontend.User, from, until time.Time) ([]frontend.LiveTVProgramme, error) {
	ps, err := h.s.Guide(ctx, u, from, until)
	if err != nil {
		return nil, err
	}
	return toFrontendProgrammes(ps), nil
}

func (h liveTVHost) GuideChannelLogo(ctx context.Context, c frontend.LiveTVChannel) string {
	return h.s.GuideChannelLogo(ctx, FromFrontendChannel(c))
}

func (h liveTVHost) TuneWithSession(ctx context.Context, u *frontend.User, id string, caps frontend.ClientCaps, sessionID string) (frontend.LiveTVSessionInfo, error) {
	info, err := h.s.TuneWithSession(ctx, u, id, caps, sessionID)
	if err != nil {
		return frontend.LiveTVSessionInfo{}, err
	}
	return toFrontendSessionInfo(info), nil
}

func (h liveTVHost) Sessions(ctx context.Context, u *frontend.User) []frontend.LiveTVSessionInfo {
	infos := h.s.Sessions(ctx, u)
	out := make([]frontend.LiveTVSessionInfo, len(infos))
	for i := range infos {
		out[i] = toFrontendSessionInfo(infos[i])
	}
	return out
}

func (h liveTVHost) Session(ctx context.Context, id string, u *frontend.User) (frontend.LiveTVSessionInfo, error) {
	info, err := h.s.Session(ctx, id, u)
	if err != nil {
		return frontend.LiveTVSessionInfo{}, err
	}
	return toFrontendSessionInfo(info), nil
}

func (h liveTVHost) ServeDASH(w http.ResponseWriter, r *http.Request, id string, u *frontend.User) error {
	return h.s.ServeDASH(w, r, id, u)
}

func (h liveTVHost) ServeHTTP(w http.ResponseWriter, r *http.Request, id string, u *frontend.User) error {
	return h.s.ServeHTTP(w, r, id, u)
}

func (h liveTVHost) ViewingSessions() []*frontend.PlaySession {
	return h.s.ViewingSessions()
}

func (h liveTVHost) NoteClientActivity() {
	h.s.NoteClientActivity()
}

func (h liveTVHost) AuthenticateMediaGrant(r *http.Request, active func(context.Context, int64) *frontend.User) (*frontend.User, bool) {
	return h.s.AuthenticateMediaGrant(r, active)
}
