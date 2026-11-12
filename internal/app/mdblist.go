package app

import "github.com/braedonsaunders/ether/internal/mdblist"

// mdblistService builds the per-viewer MDBList sync service. Each viewer
// supplies their own API key in the console; there is no app-level credential.
func (a *App) mdblistService() mdblist.Service {
	return mdblist.Service{Tokens: mdblist.TokenStore{DB: a.db, Sealer: a.cfg}, State: a.state}
}
