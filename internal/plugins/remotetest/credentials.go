// Package remotetest supplies remote plugin transport fixtures for tests.
package remotetest

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"errors"
)

var ErrUnauthorized = errors.New("test credential rejected")

// Credentials resolves credentials from a fixed map of
// credential to plugin id. It exists for tests, fixtures and the
// conformance kit; production wiring uses identity_grants through
// WS7. Lookups hash the presented credential and compare in constant
// time so map iteration order does not leak through timing.
type Credentials map[string]string

// ResolvePluginCredential implements CredentialResolver.
func (s Credentials) ResolvePluginCredential(_ context.Context, credential string) (string, error) {
	if credential == "" {
		return "", ErrUnauthorized
	}
	want := sha256.Sum256([]byte(credential))
	var found string
	for cred, id := range s {
		h := sha256.Sum256([]byte(cred))
		if subtle.ConstantTimeCompare(want[:], h[:]) == 1 {
			found = id
		}
	}
	if found == "" {
		return "", ErrUnauthorized
	}
	return found, nil
}
