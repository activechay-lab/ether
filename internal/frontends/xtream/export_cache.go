package xtream

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"strconv"

	"github.com/braedonsaunders/ether/internal/frontend"
	"github.com/braedonsaunders/ether/internal/frontend/kit/listcache"
)

// listRequest is l's kind listing for r as the listing cache keys it: one
// slot per list kind, category, media origin and viewer, stamped with the
// experience's export policy for each listed view and the host's policy
// generations. ok is false when this host cannot stamp listings exactly;
// the request then encodes its own.
func (i *instance) listRequest(r *http.Request, l login, kind string) (listcache.Request, bool) {
	policy, ok := i.host.Experience.(frontend.LibraryExportPolicy)
	if _, export := i.host.Experience.(frontend.LibraryExporter); i.exports == nil || !ok || !export || i.host.PageFreshness == nil || !l.hasBase {
		return listcache.Request{}, false
	}
	renewer, renews := i.host.ImageGrants.(frontend.ImagePageRenewer)
	if _, signs := i.host.ImageGrants.(frontend.ImagePageSigner); signs && !renews {
		// A listing outlives its grants; it is kept only if they can be re-armed.
		return listcache.Request{}, false
	}
	cat := r.URL.Query().Get("category_id")
	if cat == "0" {
		cat = ""
	}
	views, err := i.host.Experience.Views(r.Context(), l.viewer)
	if err != nil {
		return listcache.Request{}, false
	}
	fresh := i.host.PageFreshness()
	state := listcache.State{Catalog: fresh.Catalog, At: i.exports.Now()}
	h := sha256.New()
	fmt.Fprintf(h, "%d|%d|%d|%d|%d\n", fresh.Config, fresh.Accounts, fresh.Safety, fresh.Hidden, fresh.Images)
	for _, v := range views {
		if string(v.Type) != kind || cat != "" && cat != v.ID {
			continue
		}
		p, err := policy.ExportPolicy(r.Context(), l.viewer, l.device, v.ID)
		if err != nil {
			return listcache.Request{}, false
		}
		fmt.Fprintf(h, "%s\x00%s\n", v.ID, p.Stamp)
		state.Judged = state.Judged || p.TitleJudged
	}
	state.Stamp = hex.EncodeToString(h.Sum(nil))
	// The request outlives nothing it hands the build: its URL and headers
	// are copied and the build gets maps and a context of its own.
	req, bl := r.Clone(context.Background()), l.detached()
	out := listcache.Request{Slot: kind + "\x00" + cat + "\x00" + l.base + "\x00" + strconv.FormatInt(l.viewer.ID, 10), State: state, Owner: l.device.ID,
		Build: func(ctx context.Context, e *listcache.Entry) error {
			bl.keep = e.KeepImages
			_, err := i.writeList(req.WithContext(ctx), bl, kind, e)
			return err
		}}
	if renews {
		out.Renew = func(ctx context.Context, e *listcache.Entry) error {
			return listcache.RenewImages(ctx, e, renewer, l.password, l.device)
		}
	}
	return out, true
}

// cachedList answers a list request from the listing cache.
func (i *instance) cachedList(w http.ResponseWriter, r *http.Request, l login, kind string) bool {
	req, ok := i.listRequest(r, l, kind)
	return ok && i.exports.Serve(w, r, req)
}

// prepareLists builds l's listings of kinds behind the response, ahead of
// the list requests an app makes after signing in or listing categories.
func (i *instance) prepareLists(r *http.Request, l login, kinds ...string) {
	if i.exports == nil {
		return
	}
	req, l := r.Clone(context.Background()), l.detached()
	q := req.URL.Query()
	q.Del("category_id")
	req.URL.RawQuery = q.Encode()
	i.exports.Go(func(ctx context.Context) {
		req := req.WithContext(ctx)
		l.base, l.hasBase = i.base(req), true
		for _, kind := range kinds {
			if list, ok := i.listRequest(req, l, kind); ok {
				i.exports.Prepare(list)
			}
		}
	})
}
