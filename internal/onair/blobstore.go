package onair

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// blobStore keeps On Air asset bytes as content-addressed, immutable files:
// <dir>/<first two hex digits>/<sha256>. A file is written under a temporary
// name, synced, checked against its digest and renamed into place, so a
// file at its final name is always complete and self-validating. Files are
// never rewritten; removing one is the only change.
type blobStore struct{ dir string }

// blobTempPrefix starts every partly written file; sweeps remove old ones.
const blobTempPrefix = ".put-"

func validDigest(d string) bool {
	if len(d) != 64 {
		return false
	}
	for _, c := range d {
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return false
		}
	}
	return true
}

func (b blobStore) path(d string) string { return filepath.Join(b.dir, d[:2], d) }

// Has reports whether the asset's file is present with the expected size
// (size < 0 skips the size check).
func (b blobStore) Has(d string, size int64) bool {
	if !validDigest(d) {
		return false
	}
	st, e := os.Stat(b.path(d))
	return e == nil && st.Mode().IsRegular() && (size < 0 || st.Size() == size)
}

// Open opens the asset's file for reading.
func (b blobStore) Open(d string) (*os.File, error) {
	if !validDigest(d) {
		return nil, fs.ErrNotExist
	}
	return os.Open(b.path(d))
}

// Put stores r's bytes, which must hash to d (d "" accepts any digest and
// reports it). An existing file is kept: identical names hold identical
// bytes. Its modification time is refreshed so an age-guarded sweep does not
// take it while the caller records it.
func (b blobStore) Put(d string, r io.Reader) (string, int64, error) {
	if d != "" && !validDigest(d) {
		return "", 0, fmt.Errorf("invalid asset digest")
	}
	if d != "" && b.Has(d, -1) {
		return d, b.touch(d), nil
	}
	if e := os.MkdirAll(b.dir, 0o750); e != nil {
		return "", 0, e
	}
	f, e := os.CreateTemp(b.dir, blobTempPrefix+"*")
	if e != nil {
		return "", 0, e
	}
	temp := f.Name()
	defer os.Remove(temp)
	h := sha256.New()
	n, e := io.Copy(io.MultiWriter(f, h), r)
	if e == nil {
		e = f.Sync()
	}
	if ce := f.Close(); e == nil {
		e = ce
	}
	if e != nil {
		return "", 0, e
	}
	sum := hex.EncodeToString(h.Sum(nil))
	if d != "" && sum != d {
		return "", 0, fmt.Errorf("asset bytes do not match digest %s", d)
	}
	if e = b.install(temp, sum); e != nil {
		return "", 0, e
	}
	return sum, n, nil
}

// PutFile moves an already written file at src, whose bytes hash to d, into
// the store, falling back to a copy across filesystems. src is consumed.
func (b blobStore) PutFile(d, src string) error {
	defer os.Remove(src)
	if b.Has(d, -1) {
		b.touch(d)
		return nil
	}
	if e := os.MkdirAll(b.dir, 0o750); e != nil {
		return e
	}
	f, e := os.Open(src)
	if e != nil {
		return e
	}
	e = f.Sync()
	f.Close()
	if e == nil && b.install(src, d) == nil {
		return nil
	}
	f, e = os.Open(src)
	if e != nil {
		return e
	}
	defer f.Close()
	_, _, e = b.Put(d, f)
	return e
}

func (b blobStore) install(temp, d string) error {
	dir := filepath.Dir(b.path(d))
	if e := os.MkdirAll(dir, 0o750); e != nil {
		return e
	}
	if e := os.Rename(temp, b.path(d)); e != nil {
		return e
	}
	syncDir(dir)
	return nil
}

func (b blobStore) touch(d string) int64 {
	now := time.Now()
	_ = os.Chtimes(b.path(d), now, now)
	if st, e := os.Stat(b.path(d)); e == nil {
		return st.Size()
	}
	return 0
}

// Remove deletes the asset's file; a missing file is not an error.
func (b blobStore) Remove(d string) error {
	if !validDigest(d) {
		return nil
	}
	if e := os.Remove(b.path(d)); e != nil && !errors.Is(e, fs.ErrNotExist) {
		return e
	}
	return nil
}

// Walk calls fn for every stored digest with its modification time, and
// removes temporary files older than stale.
func (b blobStore) Walk(stale time.Duration, fn func(d string, mod time.Time)) error {
	shards, e := os.ReadDir(b.dir)
	if errors.Is(e, fs.ErrNotExist) {
		return nil
	}
	if e != nil {
		return e
	}
	for _, shard := range shards {
		if !shard.IsDir() {
			if strings.HasPrefix(shard.Name(), blobTempPrefix) {
				if info, e := shard.Info(); e == nil && time.Since(info.ModTime()) > stale {
					_ = os.Remove(filepath.Join(b.dir, shard.Name()))
				}
			}
			continue
		}
		files, e := os.ReadDir(filepath.Join(b.dir, shard.Name()))
		if e != nil {
			continue
		}
		for _, f := range files {
			if !validDigest(f.Name()) || f.Name()[:2] != shard.Name() || !f.Type().IsRegular() {
				continue
			}
			if info, e := f.Info(); e == nil {
				fn(f.Name(), info.ModTime())
			}
		}
	}
	return nil
}

// syncDir makes a rename durable where the platform allows syncing a
// directory (Windows does not; NTFS journals the rename).
func syncDir(dir string) {
	if f, e := os.Open(dir); e == nil {
		_ = f.Sync()
		f.Close()
	}
}
