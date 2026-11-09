package livetv

import (
	"context"
	"crypto/sha256"
	"fmt"
	"log/slog"
	"strconv"
	"sync"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/markers"
	"github.com/braedonsaunders/ether/internal/transcode"
)

// scheduleBroadcastPlanner snapshots the channel recipe. An edited recipe gets
// a new planner when P4 replaces the channel output after lineup invalidation.
type scheduleBroadcastPlanner struct {
	workerCtx    context.Context
	workerCancel context.CancelFunc
	workerDone   chan struct{}
	requests     chan time.Time
	lastRequest  time.Time
	window       []Programme
	overlayMu    sync.Mutex
	overlayCards map[string]*programmeOverlays
	overlayBug   *videoOverlay
	joinAt       time.Time
	caps         core.ClientCaps
	s            *Service
	channel      Channel
	user         core.User
	format       transcode.RawFormat
	runner       transcode.ContinuousRunner
	mu           sync.Mutex
}

func (p *scheduleBroadcastPlanner) startupLead() time.Duration { return 12 * time.Second }

func (p *scheduleBroadcastPlanner) startPlanner(parent context.Context) {
	p.workerCtx, p.workerCancel = context.WithCancel(parent)
	p.requests = make(chan time.Time, 1)
	p.workerDone = make(chan struct{})
	// Seed audience/rating identity before the first neutral startup frame.
	// These are already materialized time slices; no schedule or availability
	// expansion runs during a tune, even for a large/personal recipe.
	now := time.Now()
	c := p.s.personalChannel(p.workerCtx, p.channel, &p.user)
	p.window, _ = p.s.programmes(p.workerCtx, c, now, now.Add(12*time.Minute))
	go p.planWorker()
}
func (p *scheduleBroadcastPlanner) closePlanner() { p.workerCancel(); <-p.workerDone }
func (p *scheduleBroadcastPlanner) planWorker() {
	defer close(p.workerDone)
	for {
		select {
		case <-p.workerCtx.Done():
			return
		case now := <-p.requests:
			c := p.s.graphicsChannel(p.channel)
			c = p.s.personalChannel(p.workerCtx, c, &p.user)
			var ps []Programme
			if c.Personal != nil {
				ps, _ = p.s.programmes(p.workerCtx, c, now, now.Add(12*time.Minute))
				if welcome, ok := p.s.personalWelcome(c, now); ok {
					ps = append([]Programme{welcome}, ps...)
				}
			} else {
				ps = p.s.broadcastSchedule(p.workerCtx, p.s.plannedChannel(c), now, now.Add(12*time.Minute), true)
			}
			for i := range ps {
				ps[i].policyTag = p.s.programmePolicy(p.workerCtx, ps[i])
			}
			p.mu.Lock()
			p.window = ps
			p.mu.Unlock()
		}
	}
}
func (p *scheduleBroadcastPlanner) plans(now time.Time) (broadcastPlan, *broadcastPlan) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.lastRequest.IsZero() || now.Sub(p.lastRequest) >= time.Second {
		select {
		case p.requests <- now:
			p.lastRequest = now
		default:
		}
	}
	current := broadcastPlan{id: "screen-" + p.channel.ID}
	var next *broadcastPlan
	for _, programme := range p.window {
		if current.id != "screen-"+p.channel.ID && programme.Start.Before(current.end) {
			continue
		}
		plan := p.programme(programme, now)
		if !now.Before(programme.Start) && now.Before(programme.End) {
			current = plan
		} else if programme.Start.After(now) {
			next = &plan
			break
		}
	}
	return current, next
}

// A short ident must not hide the programme after it from prewarming.
func (p *scheduleBroadcastPlanner) prewarmPlans(now time.Time) []broadcastPlan {
	p.mu.Lock()
	defer p.mu.Unlock()
	var plans []broadcastPlan
	inPod := false
	for _, programme := range p.window {
		if !now.Before(programme.Start) && now.Before(programme.End) {
			inPod = programme.Item.Key == 0 && programme.Item.Slate != offAirSlate
			break
		}
	}
	programmePrepared := false
	for _, programme := range p.window {
		if !programme.Start.After(now) {
			continue
		}
		imminent := !programme.Start.After(now.Add(broadcastPrewarm))
		// A pod's first programme is prepared from the beginning of the pod,
		// even when short intervening elements hide it beyond the usual lead.
		if (imminent && len(plans) < 7) || (inPod && !programmePrepared && programme.Item.Key != 0) {
			plans = append(plans, p.programme(programme))
		}
		if programme.Item.Key != 0 {
			programmePrepared = true
		}
	}
	return plans
}

func (p *scheduleBroadcastPlanner) programme(programme Programme, plannedAt ...time.Time) broadcastPlan {
	element := programme.Item.Slate
	if programme.Item.ReelClipID != "" {
		element = "reel"
	} else if programme.Item.Key != 0 {
		element = "programme"
	}
	plan := broadcastPlan{mediaKey: programme.Item.Key, sourceOffset: programme.sourceOffset, interstitial: interstitial(programme.Item), fallback: programme.graphicsFallback, element: element, policy: programme.policyTag, programme: programme.Item.Key != 0, id: broadcastProgrammeID(programme), start: programme.Start, end: programme.End}
	if interstitial(programme.Item) {
		plan.failed = func(err error) time.Time {
			p.s.recordGraphicsSkip(p.channel, programme, err)
			// Refresh the input decision without changing any schedule boundary.
			select {
			case p.requests <- p.s.o.Now():
			default:
			}
			return programme.End
		}
	}
	plan.open = func(ctx context.Context) (transcode.Decoder, func(), error) {
		c := p.s.graphicsChannel(p.s.personalChannel(ctx, p.channel, &p.user))
		if p.s.o.Playback == nil && programme.Item.Slate == "" {
			return nil, nil, core.ErrNotAvailable
		}
		if programme.Item.ReelClipID != "" {
			var path string
			if reel := c.reelForItem(programme.Item); reel != nil {
				for _, clip := range reel.Clips {
					if clip.ID == programme.Item.ReelClipID {
						path = clip.Path
						break
					}
				}
			}
			if path == "" {
				return nil, nil, core.ErrNotAvailable
			}
			decoder, err := p.runner.StartDecoder(ctx, transcode.DecoderRequest{SessionID: "reel-" + programme.Item.ReelClipID, InputURL: path, Format: p.format})
			return decoder, nil, err
		}
		if programme.Item.Slate != "" {
			if programme.Item.Slate == offAirSlate && (c.Look == nil || !c.Look.OffAir) {
				back := ""
				for _, next := range Schedule(p.s.graphicsChannel(c), programme.End, programme.End.Add(24*time.Hour)) {
					if next.Item.Key != 0 {
						back = next.Start.In(zoneOf(c.Timezone)).Format("3:04 PM")
						break
					}
				}
				return p.s.openSignalDecoder(ctx, p.s.screenRequestWithBack(c, offAirSlate, back), p.format)
			}
			var request core.ClipRequest
			if programme.graphicsRequest != nil {
				request = *programme.graphicsRequest
			} else {
				request = p.s.clipRequest(c, programme)
			}
			decoder, release, err := p.s.openGraphicsDecoder(ctx, p.runner, request, p.format, programme.Item.Slate == offAirSlate)
			if err != nil && programme.Item.Slate == offAirSlate {
				return p.s.openSignalDecoder(ctx, p.s.screenRequest(c, offAirSlate), p.format)
			}
			return decoder, release, err
		}

		candidates := p.s.virtualCandidates(c, programme)
		if programme.Item.BreakReleaseID != "" && len(programme.Item.BreakPointsMS) > 0 {
			candidates = []Programme{programme}
		}
		var last error = core.ErrNotAvailable
		for attempt, candidate := range candidates {
			if attempt >= virtualMaxAttempts {
				break
			}
			if p.s.knownUnavailable(ctx, candidate.Item.Key) {
				continue
			}
			if !p.s.allowed(ctx, &p.user, c, &candidate) {
				continue
			}
			var ps *core.PlaySession
			var err error
			attempt, cancel := context.WithTimeout(ctx, virtualAttemptBudget)
			group := "broadcast-" + c.ID
			if c.Personal != nil {
				group += "-viewer-" + strconv.FormatInt(p.user.ID, 10)
			}
			// Programme inputs overlap while a pod or next airing is prewarmed.
			// They share one channel budget, but each keeps its own playback
			// identity so preparing the next title cannot supersede this one.
			caps := p.caps
			caps.ClientID = "channel-input/" + plan.id
			if candidate.Item.BreakReleaseID != "" && len(candidate.Item.BreakPointsMS) > 0 {
				planned, ok := p.s.o.Playback.(interface {
					PrepareChannelRelease(context.Context, *core.User, core.RatingKey, core.ClientCaps, string, string) (*core.PlaySession, error)
				})
				if !ok {
					cancel()
					last = core.ErrSourceChanged
					continue
				}
				ps, err = planned.PrepareChannelRelease(attempt, &p.user, candidate.Item.Key, caps, group, candidate.Item.BreakReleaseID)
				if err == nil {
					pin, ok := p.s.o.Playback.(core.PlaybackRevisionPin)
					if !ok {
						err = core.ErrSourceChanged
					} else {
						err = pin.PinSourceRevision(attempt, ps.ID, candidate.Item.BreakRevision)
					}
					if err != nil {
						p.s.o.Playback.Stop(ps.ID)
					}
				}
			} else if grouped, ok := p.s.o.Playback.(core.ChannelPreparer); ok {
				ps, err = grouped.PrepareChannel(attempt, &p.user, candidate.Item.Key, caps, group)
			} else {
				ps, err = p.s.o.Playback.Prepare(attempt, &p.user, candidate.Item.Key, caps)
			}
			cancel()
			if err != nil {
				last = err
				continue
			}
			now := p.s.o.Now()
			if len(plannedAt) > 0 && plannedAt[0].After(now) {
				now = plannedAt[0]
			}
			if p.joinAt.After(now) {
				now = p.joinAt
			}
			offset := time.Duration(0)
			if candidate.ID == programme.ID {
				at := now
				if at.Before(programme.Start) {
					at = programme.Start
				}
				offset = programme.Offset(at)
			}
			sourceWall := time.Now()
			// Alternate content occupies this same future airing. Anchoring it to
			// preparation time would discard the parked decoder's opening frames
			// as stale when a long pod or plugin interval finally ends.
			if programme.Start.After(now) {
				sourceWall = sourceWall.Add(programme.Start.Sub(now))
			}
			p.s.o.Logger.Debug("broadcast programme decoder start", "channel", c.ID, "programme", programme.ID, "playback", ps.ID, "offset", offset)
			input := p.s.o.Playback.InputURL(ps.ID)
			seeked := false
			trim := offset
			if index, ok := p.s.o.Playback.(interface {
				IndexedInput(context.Context, string, time.Duration) (string, time.Duration, bool)
			}); ok {
				if url, remaining, ready := index.IndexedInput(ctx, input, offset); ready {
					input = url
					trim = remaining
					seeked = true
				}
			}
			decoder, err := p.runner.StartDecoder(ctx, transcode.DecoderRequest{SessionID: group + "-" + candidate.ID, InputURL: input, Offset: trim, Seeked: seeked, Format: p.format})
			if err != nil {
				p.s.o.Playback.Stop(ps.ID)
				last = err
				continue
			}
			if programme.naturalBreaks && p.s.o.BreakAnalyzer != nil && candidate.Item.Key.Kind() == core.KindEpisode {
				input := markers.PlaybackBreakInput(ps)
				input.Info = core.MediaInfo{Duration: ps.Info.Duration, Size: ps.Info.Size, Estimated: ps.Info.Estimated}
				if input.Revision != "" {
					decoder = &breakTapDecoder{Decoder: decoder, tap: markers.NewBreakTap(ps.Info.Duration), input: input, format: p.format, at: offset, output: p.s.breakObserved}
				}
			}
			return &overlayDecoder{policy: p.s.programmePolicy(ctx, candidate), Decoder: decoder, wallStart: sourceWall, format: p.format, overlays: p.prepareProgrammeOverlays(ctx, programme, ps.Info.Chapters)}, func() { p.s.o.Playback.Stop(ps.ID) }, nil
		}
		return nil, nil, last
	}
	return plan
}

func (p *scheduleBroadcastPlanner) broadcastLogger() *slog.Logger {
	return p.s.o.Logger.With("channel", p.channel.ID)
}

// Runtime preparation and retry identity includes the media and full interval.
// Guide IDs remain unchanged; an edited item can never inherit another input's
// prepared decoder, failure deadline, or latched fallback decision.
func broadcastProgrammeID(p Programme) string {
	identity := fmt.Sprintf("%s/%s/%d/%s/%s/%s/%d/%d/%d", p.SourceID, p.Item.SourceID, p.Item.Key, p.Item.Slate, p.Item.ReelID, p.Item.ReelClipID, p.sourceOffset, p.Start.UnixNano(), p.End.UnixNano())
	hash := sha256.Sum256([]byte(identity))
	return fmt.Sprintf("%x-%s", hash[:8], p.ID)
}
