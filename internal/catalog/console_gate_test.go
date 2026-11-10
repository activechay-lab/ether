package catalog

import (
	"context"
	"github.com/braedonsaunders/ether/internal/core"
	"testing"
)

func TestConsoleReleaseGateUsesCurrentPolicyGeneration(t *testing.T) {
	c := largeCatalogue(t, 3)
	ctx := context.Background()
	run := func(gate string, want int) {
		t.Helper()
		p, err := c.Browse(ctx, core.BrowseQuery{Filter: core.Filter{Type: core.TypeMovie, ReleaseGate: gate}, Limit: 10})
		if err != nil || p.Total != want {
			t.Fatalf("%s total%d want%d err%v", gate, p.Total, want, err)
		}
	}
	// No availability service means no evidence, with no dependency on its tables.
	run("unknown", 3)
	run("admitted", 0)
	if _, err := c.db.ExecContext(ctx, `CREATE TABLE availability_hidden_current(kind TEXT PRIMARY KEY,seq BIGINT NOT NULL,policy TEXT NOT NULL); CREATE TABLE availability_hidden(kind TEXT NOT NULL,rating_key BIGINT NOT NULL,from_seq BIGINT NOT NULL,to_seq BIGINT NOT NULL,PRIMARY KEY(kind,rating_key,from_seq)); INSERT INTO availability_hidden_current VALUES('movie',1,'policy-one')`); err != nil {
		t.Fatal(err)
	}
	// MovieKey(2) is hidden at seq 1 only; MovieKey(3) from seq 2 on.
	if _, err := c.db.ExecContext(ctx, `INSERT INTO availability_hidden VALUES('movie',?,1,2),('movie',?,2,9223372036854775807)`, int64(core.MovieKey(2)), int64(core.MovieKey(3))); err != nil {
		t.Fatal(err)
	}
	policy := "policy-one"
	c.SetConsoleGatePolicies(func() (string, string) { return policy, "show-policy" })
	c.ConsoleGateChanged()
	run("held", 1)
	run("admitted", 2)
	run("unknown", 0)
	// Only the published seq counts; intervals closed before it are inert.
	if _, err := c.db.ExecContext(ctx, `UPDATE availability_hidden_current SET seq=2`); err != nil {
		t.Fatal(err)
	}
	c.ConsoleGateChanged()
	p, err := c.Browse(ctx, core.BrowseQuery{Filter: core.Filter{Type: core.TypeMovie, ReleaseGate: "held"}, Limit: 10})
	if err != nil || len(p.Items) != 1 || p.Items[0].Key != core.MovieKey(3) {
		t.Fatalf("seq %+v err%v", p, err)
	}
	policy = "policy-two"
	run("held", 0)
	run("admitted", 0)
	run("unknown", 3)
}

// A hidden-set publication re-keys release-gate results only. Ordinary
// filters carry the generations they read, so their cached pages (and every
// other catalogue cache) survive it: on a large library a publication
// during browsing otherwise turned every listing into a full recount.
func TestConsoleGateChangeKeepsOrdinaryCaches(t *testing.T) {
	c := largeCatalogue(t, 3)
	plain := core.Filter{Type: core.TypeMovie}
	gated := core.Filter{Type: core.TypeMovie, ReleaseGate: "held"}
	gen, plainKey, gatedKey := c.writeGen.Load(), c.consoleAvailabilityCacheKey(plain), c.consoleAvailabilityCacheKey(gated)
	c.ConsoleGateChanged()
	if c.writeGen.Load() != gen {
		t.Fatal("a hidden-set publication invalidated the whole catalogue")
	}
	if c.consoleAvailabilityCacheKey(plain) != plainKey {
		t.Fatal("an ordinary filter was re-keyed")
	}
	if c.consoleAvailabilityCacheKey(gated) == gatedKey {
		t.Fatal("a release-gate filter kept its key across a publication")
	}
}
