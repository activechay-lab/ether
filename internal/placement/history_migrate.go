package placement

import "github.com/braedonsaunders/ether/internal/store"

func init() {
	store.Register("placement", 3, `
CREATE TABLE placement_history_namespace (id INTEGER PRIMARY KEY CHECK(id=1), cluster TEXT NOT NULL);
CREATE TABLE placement_history_receipts (
 cluster TEXT NOT NULL, session TEXT NOT NULL, generation BIGINT NOT NULL CHECK(generation>0),
 controller TEXT NOT NULL, controller_boot TEXT NOT NULL, node TEXT NOT NULL, boot TEXT NOT NULL,
 receipt TEXT NOT NULL,
 PRIMARY KEY(cluster,session,generation,controller,controller_boot,node,boot)
);
`)
}
