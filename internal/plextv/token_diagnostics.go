package plextv

import (
	"context"
	"errors"
	"reflect"
	"sort"

	"github.com/braedonsaunders/ether/internal/config"
)

// TokenDiagnostics deliberately excludes tokens, device names, machine
// identifiers, addresses and connections. SourceTitle is account display metadata. Status and change-only logs are safe for support bundles.
type TokenDiagnostics struct {
	ClaimResponseShapes      []string `json:"claimResponseShapes,omitempty"`
	OwnerCredentialAvailable bool     `json:"ownerCredentialAvailable"`
	OwnerResourcesStatus     int      `json:"ownerResourcesStatus"`
	TokenPresent             bool     `json:"tokenPresent"`
	ResourcesStatus          int      `json:"resourcesStatus"`
	MatchingDevices          int      `json:"matchingDevices"`
	SameNameServers          int      `json:"sameNameServers"`
	TokenMatchesDevice       bool     `json:"tokenMatchesDevice"`
	Provides                 string   `json:"provides,omitempty"`
	Owned                    bool     `json:"owned"`
	Product                  string   `json:"product,omitempty"`
	SourceTitle              string   `json:"sourceTitle,omitempty"`
	SourceTitlePresent       bool     `json:"sourceTitlePresent"`
}

func (r *Registrar) diagnoseServerToken(ctx context.Context, cfg config.Config, ownerToken string) {
	d := TokenDiagnostics{TokenPresent: cfg.Plex.Token != ""}
	r.client.diagnosticShapes.Range(func(key, value any) bool {
		d.ClaimResponseShapes = append(d.ClaimResponseShapes, key.(string))
		return true
	})
	sort.Strings(d.ClaimResponseShapes)
	// The rejected persisted credential is the only available credential on
	// migrated installs. Do not pretend it is a separately retained owner token.
	resources, err := r.client.Resources(ctx, cfg.Plex.Token)
	if ownerToken != "" {
		d.OwnerCredentialAvailable = true
		ownerResources, ownerErr := r.client.Resources(ctx, ownerToken)
		d.OwnerResourcesStatus = resourceStatus(ownerErr)
		// Account resources carry the device access token. Prefer this view
		// for identity and duplicate checks while retaining server-token status.
		d.ResourcesStatus = resourceStatus(err)
		resources, err = ownerResources, ownerErr
	}
	if err == nil {
		if !d.OwnerCredentialAvailable {
			d.ResourcesStatus = 200
		}
		for _, device := range resources {
			if device.ClientIdentifier == cfg.Server.MachineIdentifier {
				d.MatchingDevices++
				d.Provides, d.Owned, d.Product = device.Provides, device.Owned, device.Product
				d.SourceTitlePresent = device.SourceTitle != ""
				d.SourceTitle = device.SourceTitle
				d.TokenMatchesDevice = d.TokenMatchesDevice || device.AccessToken == cfg.Plex.Token
			}
			if device.ProvidesServer() && device.Name == cfg.Server.FriendlyName && device.ClientIdentifier != cfg.Server.MachineIdentifier {
				d.SameNameServers++
			}
		}
	} else {
		var api *APIError
		if errors.As(err, &api) {
			if !d.OwnerCredentialAvailable {
				d.ResourcesStatus = api.Status
			}
		}
	}
	r.mu.Lock()
	changed := !reflect.DeepEqual(r.st.TokenDiagnostics, &d)
	r.st.TokenDiagnostics = &d
	r.mu.Unlock()
	if changed {
		r.log.Warn("plex.tv requires server token: registration diagnostics", "diagnostics", d, "separateOwnerCredentialAvailable", d.OwnerCredentialAvailable)
	}
}

func resourceStatus(err error) int {
	if err == nil {
		return 200
	}
	var api *APIError
	if errors.As(err, &api) {
		return api.Status
	}
	return 0
}
