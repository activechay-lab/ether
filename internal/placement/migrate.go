package placement

import "github.com/braedonsaunders/ether/internal/store"

func init() {
	store.Register("placement", 1, `
 CREATE TABLE placement_pools (scope TEXT PRIMARY KEY, role TEXT NOT NULL, capacity BIGINT NOT NULL CHECK(capacity >= 0));
 CREATE TABLE placement_targets (node TEXT PRIMARY KEY, boot TEXT NOT NULL, roles TEXT NOT NULL, scopes TEXT NOT NULL);
 CREATE TABLE placement_sessions (session TEXT PRIMARY KEY, generation BIGINT NOT NULL, operation TEXT NOT NULL, fingerprint TEXT NOT NULL, controller TEXT NOT NULL, controller_boot TEXT NOT NULL, node TEXT NOT NULL, boot TEXT NOT NULL, role TEXT NOT NULL, claims TEXT NOT NULL, initial_deadline BIGINT NOT NULL, deadline BIGINT NOT NULL, retired INTEGER NOT NULL);
 CREATE TABLE placement_claims (session TEXT NOT NULL, scope TEXT NOT NULL, units BIGINT NOT NULL CHECK(units > 0), PRIMARY KEY(session, scope), FOREIGN KEY(session) REFERENCES placement_sessions(session), FOREIGN KEY(scope) REFERENCES placement_pools(scope));
 CREATE INDEX placement_claims_scope ON placement_claims(scope);
 `)
}
