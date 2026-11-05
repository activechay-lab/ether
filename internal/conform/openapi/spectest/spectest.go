// Package spectest loads the pinned protocol specs for tests.
package spectest

import (
	"errors"
	"io/fs"
	"os"
	"sync"
	"testing"

	"github.com/braedonsaunders/ether/internal/conform/openapi"
	"github.com/braedonsaunders/ether/internal/conform/specs"
)

// DirEnv names the directory that holds the fetched specs
// (internal/conform/specs/fetch.sh <dir>). When it is set, a missing spec
// fails the test instead of skipping validation: CI sets it after fetching,
// so validation can never silently drop out there.
const DirEnv = "ETHER_CONFORM_SPECS_DIR"

var testDocs sync.Map // protocol -> *testDoc

type testDoc struct {
	once sync.Once
	data []byte
	doc  *openapi.Document
	err  error
}

// Document returns the parsed, checksum-verified pinned spec for
// protocol. It returns nil only when the spec is not cached and DirEnv is
// unset, so the caller skips schema validation (and says so in the test
// log). Every other problem — a checksum mismatch, an unreadable file, a
// parse error, or a missing spec when DirEnv is set — fails the test.
func Document(tb testing.TB, protocol string) *openapi.Document {
	tb.Helper()
	if d := load(tb, protocol); d != nil {
		return d.doc
	}
	return nil
}

// Bytes returns the raw checksum-verified spec under the same rules as
// Document (it is parsed too, so a malformed spec still fails).
func Bytes(tb testing.TB, protocol string) []byte {
	tb.Helper()
	if d := load(tb, protocol); d != nil {
		return d.data
	}
	return nil
}

func load(tb testing.TB, protocol string) *testDoc {
	tb.Helper()
	v, _ := testDocs.LoadOrStore(protocol, &testDoc{})
	d := v.(*testDoc)
	d.once.Do(func() {
		d.data, d.err = openapi.CachedSpecManifest(specs.Manifest, protocol, os.Getenv(DirEnv))
		if d.err == nil {
			d.doc, d.err = openapi.Parse(d.data)
		}
	})
	if d.err == nil {
		return d
	}
	if errors.Is(d.err, fs.ErrNotExist) && os.Getenv(DirEnv) == "" {
		tb.Logf("%s schema validation skipped: spec not cached (run internal/conform/specs/fetch.sh)", protocol)
		return nil
	}
	tb.Fatalf("%s pinned spec: %v", protocol, d.err)
	return nil
}
