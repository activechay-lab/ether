package remote

import ()

// URIs extracts the URI of each connection.
func URIs(cs []Connection) []string {
	out := make([]string, len(cs))
	for i, c := range cs {
		out[i] = c.URI
	}
	return out
}
