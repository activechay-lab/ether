package notify

import "github.com/braedonsaunders/ether/internal/extend"

// eventKind is one thing Ether can notify about. Label and description are
// built-in console copy, declared with extend.T so they are translated.
type eventKind struct {
	ID           string
	Label        extend.Text
	Description  extend.Text
	Order        int
	Automatic    bool
	Critical     bool
	ChannelKinds []string
}

var apiOnly = extend.T("Owner API integration only; Ether does not automatically produce this event.")

// builtinEventKinds lists every event kind, in console order. The "test"
// kind is accepted for delivery but is not a selectable rule, so it has no
// descriptor.
func builtinEventKinds() []eventKind {
	return []eventKind{
		{ID: "server.started", Label: extend.T("Server online"), Description: extend.T("This node is listening and ready for connections."), Order: 10, Automatic: true},
		{ID: "server.stopping", Label: extend.T("Server stopping"), Description: extend.T("A normal shutdown or restart has begun."), Order: 20, Automatic: true},
		{ID: "server.crash", Label: extend.T("Server crashed"), Description: extend.T("Independent watcher detects an unexpected process exit; sent immediately, outside the queue."), Order: 30, Automatic: true, Critical: true},
		{ID: "provider.recovered", Label: extend.T("Provider recovered"), Description: extend.T("An unavailable provider responds again."), Order: 40, Automatic: true},
		{ID: "indexer.recovered", Label: extend.T("Indexer recovered"), Description: extend.T("An indexer operation succeeds after an alert."), Order: 50, Automatic: true},
		{ID: "cache.recovered", Label: extend.T("Cache space available"), Description: extend.T("The cache drops below its budget again."), Order: 60, Automatic: true},
		{ID: "playback.start", Label: extend.T("Playback started"), Description: extend.T("A viewer starts a stream."), Order: 70, Automatic: true},
		{ID: "playback.stop", Label: extend.T("Playback ended"), Description: extend.T("A viewer ends a stream."), Order: 80, Automatic: true},
		{ID: "playback.error", Label: extend.T("Playback failed"), Description: extend.T("Playback fails."), Order: 90, Automatic: true, Critical: true},
		{ID: "playback.failover", Label: extend.T("Playback switched source"), Description: extend.T("A stream switches to another source."), Order: 100, Automatic: true},
		{ID: "provider.outage", Label: extend.T("Provider unavailable"), Description: extend.T("A provider enters backoff or rejects authentication."), Order: 110, Automatic: true, Critical: true},
		{ID: "indexer.outage", Label: extend.T("Indexer unavailable"), Description: extend.T("An indexer operation fails; one alert per outage."), Order: 120, Automatic: true, Critical: true},
		{ID: "indexer.budget", Label: extend.T("Indexer budget reached"), Description: extend.T("An indexer reports budget exhaustion."), Order: 130, Automatic: true},
		{ID: "cache.full", Label: extend.T("Cache full"), Description: extend.T("The segment cache reaches its configured budget."), Order: 140, Automatic: true, Critical: true},
		{ID: "cert.renewed", Label: extend.T("Certificate renewed"), Description: apiOnly, Order: 150},
		{ID: "update.available", Label: extend.T("Update available"), Description: extend.T("A new Ether release is published; one alert per version."), Order: 160, Automatic: true},
		{ID: "update.installed", Label: extend.T("Update installed"), Description: extend.T("Ether installed an update by itself and it is running well; one alert per version."), Order: 161, Automatic: true},
		{ID: "update.rolledback", Label: extend.T("Update rolled back"), Description: extend.T("An update did not run properly, so Ether went back to the previous version and will skip it."), Order: 162, Automatic: true, Critical: true},
		{ID: "backup.failed", Label: extend.T("Backup failed"), Description: extend.T("A scheduled backup fails; one alert until a backup succeeds again."), Order: 170, Automatic: true, Critical: true},
		{ID: "user.joined", Label: extend.T("Viewer joined"), Description: apiOnly, Order: 180},
		{ID: "title.available", Label: extend.T("Watchlist title available"), Description: extend.T("A monitored watchlist title becomes available."), Order: 190, Automatic: true},
		{ID: "newsletter.weekly", Label: extend.T("Weekly newsletter"), Description: extend.T("Scheduled viewing summary; email channels only."), Order: 200, Automatic: true, ChannelKinds: []string{"smtp"}},
	}
}

func eventKindIDs() []string {
	kinds := builtinEventKinds()
	out := make([]string, 0, len(kinds)+1)
	for _, k := range kinds {
		out = append(out, k.ID)
	}
	return append(out, "test")
}

func eventKindDef(id string) (eventKind, bool) {
	for _, k := range builtinEventKinds() {
		if k.ID == id {
			return k, true
		}
	}
	return eventKind{}, false
}

// eventAllowsKind reports whether an event may go to a channel kind.
func eventAllowsKind(event, channelKind string) bool {
	def, ok := eventKindDef(event)
	if !ok || len(def.ChannelKinds) == 0 {
		return true
	}
	for _, k := range def.ChannelKinds {
		if k == channelKind {
			return true
		}
	}
	return false
}

// EventKindDescriptors returns the built-in eventKind descriptors for the
// extend registry.
func EventKindDescriptors() []extend.Descriptor {
	kinds := builtinEventKinds()
	out := make([]extend.Descriptor, 0, len(kinds))
	for _, k := range kinds {
		out = append(out, extend.Descriptor{
			Kind: extend.KindEventKind, ID: k.ID, Owner: extend.Builtin,
			Label: k.Label, Hint: k.Description, Order: k.Order,
		}.WithMeta(extend.EventKindMeta{
			Automatic: k.Automatic, Critical: k.Critical, ChannelKinds: k.ChannelKinds,
		}))
	}
	return out
}

func (s *Service) monitorActive() bool {
	s.watchMu.Lock()
	defer s.watchMu.Unlock()
	if s.watchUpdate == nil {
		return false
	}
	select {
	case <-s.monitorDone:
		return false
	default:
		return true
	}
}
