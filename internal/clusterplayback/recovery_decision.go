package clusterplayback

import "github.com/braedonsaunders/ether/internal/store"

func init() {
	store.Register("clusterplayback", 7, `
CREATE TABLE clusterplayback_recovery_decisions (
 cluster TEXT NOT NULL, session TEXT NOT NULL, generation BIGINT NOT NULL,
 decision TEXT NOT NULL, PRIMARY KEY(cluster,session,generation));
CREATE TABLE clusterplayback_recovery_events (
 cluster TEXT NOT NULL, ordinal BIGINT NOT NULL, session TEXT NOT NULL, generation BIGINT NOT NULL,
 event TEXT NOT NULL, PRIMARY KEY(cluster,ordinal), UNIQUE(cluster,session,generation));
`)
}
