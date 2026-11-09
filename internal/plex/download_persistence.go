package plex

import (
	"context"
	"github.com/braedonsaunders/ether/internal/frontend"
	"github.com/braedonsaunders/ether/internal/sessions"
	"time"
)

func (s *Server) downloadPersistence() sessions.DownloadQueuePersistence {
	var users func(context.Context) ([]frontend.Viewer, error)
	if s.d.Auth != nil {
		users = s.d.Auth.Users
	}
	return sessions.DownloadQueuePersistence{Queue: s.downloads, DB: s.d.DB, Artifacts: s.d.Downloads, Log: s.log, Expiry: func() time.Duration { return s.cfg().Downloads.Expiry.D() }, StorageID: s.downloadStorageID, Users: users}
}
func (s *Server) retainDownload(path string, ready time.Time) {
	s.downloadPersistence().Retain(path, ready)
}
func (s *Server) stageDownloads() {
	if s.d.DB == nil {
		return
	}
	select {
	case s.downloads.Wake <- struct{}{}:
	default:
	}
}
func (s *Server) downloadStorageID() string {
	id := s.cfg().Server.MachineIdentifier
	if s.d.Cluster != nil {
		id += "/" + s.d.Cluster.NodeID()
	}
	return id
}
func (s *Server) saveDownloads() error { return s.downloadPersistence().Save() }
func (s *Server) loadDownloads()       { s.downloadPersistence().Load(s.ctx) }

// writeDownloads runs the queue writer with the dependencies bound when the
// server was built; reading Server fields from the goroutine itself would race
// with anything that configures the server after NewServer returns.
func (s *Server) writeDownloads(p sessions.DownloadQueuePersistence) {
	defer s.wg.Done()
	p.Run(s.ctx)
}
func (s *Server) stopDownloadsForRestart() { s.downloads.StopForRestart() }
