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
	if _, err := c.db.ExecContext(ctx, `CREATE TABLE availability_hidden_current(kind TEXT PRIMARY KEY,generation TEXT NOT NULL,policy TEXT NOT NULL); CREATE TABLE availability_hidden(kind TEXT NOT NULL,generation TEXT NOT NULL,rating_key BIGINT NOT NULL,created_at BIGINT NOT NULL,PRIMARY KEY(kind,generation,rating_key)); INSERT INTO availability_hidden_current VALUES('movie','gen-one','policy-one')`); err != nil {
		t.Fatal(err)
	}
	if _, err := c.db.ExecContext(ctx, `INSERT INTO availability_hidden VALUES('movie','gen-one',?,0),('movie','gen-two',?,0)`, int64(core.MovieKey(2)), int64(core.MovieKey(3))); err != nil {
		t.Fatal(err)
	}
	policy := "policy-one"
	c.SetConsoleGatePolicies(func() (string, string) { return policy, "show-policy" })
	c.ConsoleGateChanged()
	run("held", 1)
	run("admitted", 2)
	run("unknown", 0)
	// Only the published generation counts; an old retained generation is inert.
	if _, err := c.db.ExecContext(ctx, `UPDATE availability_hidden_current SET generation='gen-two'`); err != nil {
		t.Fatal(err)
	}
	c.ConsoleGateChanged()
	p, err := c.Browse(ctx, core.BrowseQuery{Filter: core.Filter{Type: core.TypeMovie, ReleaseGate: "held"}, Limit: 10})
	if err != nil || len(p.Items) != 1 || p.Items[0].Key != core.MovieKey(3) {
		t.Fatalf("generation %+v err%v", p, err)
	}
	policy = "policy-two"
	run("held", 0)
	run("admitted", 0)
	run("unknown", 3)
}
