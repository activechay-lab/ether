package frontend

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
)

// AppPasswordIdentity authenticates only a dedicated, revocable app password.
// The host applies its shared, verified-peer limiter to failed authentication.
type AppPasswordIdentity interface {
	AuthenticateAppPassword(context.Context, string, string) (*Viewer, error)
}

// XtreamUsername is a generated login name, independent of household sign-in.
// Its input is a random app password; neither value reveals a viewer identifier.
func XtreamUsername(password string) string {
	sum := sha256.Sum256([]byte(password))
	return "xt_" + hex.EncodeToString(sum[:12])
}

// LibraryExportPage is a bounded, policy-filtered export batch. Next advances
// over raw candidates, including those hidden by policy, so no prefix is rescanned.
type LibraryExportPage struct {
	Items []Title
	Next  RatingKey
	Done  bool
}
type LibraryExporter interface {
	ExportLibraryPage(context.Context, *Viewer, Device, string, RatingKey, int) (LibraryExportPage, error)
}

// AppConnectionStatus reports durable login leases, including other nodes.
type AppConnectionStatus interface {
	AppConnections(context.Context, string) (int, error)
}

// AppConnectionAdmission lets an authenticated primary URL switch its one
// playback slot, while old segment URLs cannot steal it back.
type AppConnectionAdmission interface {
	PrepareAppConnection(*http.Request, string, PlanRequest) error
}
