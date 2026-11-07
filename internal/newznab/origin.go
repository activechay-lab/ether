package newznab

import (
	"fmt"

	"github.com/braedonsaunders/ether/internal/core"
)

// bindReleases records the client that produced these results. Feed/backend
// fields and display names cannot select a different configured trust context.
func (c *Client) bindReleases(results []core.Release) []core.Release {
	if len(results) == 0 {
		return results
	}
	out := append([]core.Release(nil), results...)
	id := c.indexerID()
	for i := range out {
		out[i].IndexerID = id
	}
	return out
}

// feedReleases also namespaces feed GUIDs: different indexers may use the same
// GUID and display name, including when their NZB disk directory is shared.
func (c *Client) feedReleases(results []core.Release) []core.Release {
	out := c.bindReleases(results)
	for i := range out {
		out[i].ID = releaseID(out[i].IndexerID, out[i].GUID, out[i].NZBURL+out[i].Name)
	}
	return out
}

// releaseClient never infers private trust from a feed URL. An unbound
// record must identify one unique configured name; duplicate names fail closed.
func (m *Manager) releaseClient(r core.Release) (*Client, error) {
	var found *Client
	for _, c := range m.Clients() {
		matches := c.indexerID() == r.IndexerID
		if r.IndexerID == "" {
			matches = c.Name() == r.Indexer
		}
		if !matches {
			continue
		}
		if found != nil {
			return nil, fmt.Errorf("newznab: ambiguous originating indexer for release %q", r.Name)
		}
		found = c
	}
	if found == nil {
		return nil, fmt.Errorf("newznab: originating indexer for release %q is no longer configured", r.Name)
	}
	return found, nil
}
