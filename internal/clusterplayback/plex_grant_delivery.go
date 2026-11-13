package clusterplayback

import "net/http"

func (r *Runtime) ForwardGrantMedia(w http.ResponseWriter, q *http.Request, id string) bool {
	if !r.o.Delivery.GrantEnvelope(q) {
		return false
	}
	return r.ForwardEncodedMedia(w, q, id)
}
