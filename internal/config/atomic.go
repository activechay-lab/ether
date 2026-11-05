package config

import (
	"github.com/braedonsaunders/ether/internal/atomicfile"
	"os"
)

// syncDirAfterRename is the production post-rename step: fsync dir so the
// rename itself is durable. It is a no-op on Windows, where directory fsync
// is unsupported and rename is already journalled by NTFS.
func syncDirAfterRename(dir string) error {
	return atomicfile.SyncDir(dir)
}

// writeFileAtomic replaces path with data so that a crash or power loss
// leaves either the old or the new file, never a truncated one: it writes a
// uniquely named temp file in the same directory (concurrent writers or a
// stale temp from a crash cannot collide), fsyncs it and renames it over
// path, then best-effort fsyncs the directory via syncDirAfterRename — a
// failure there only warns, since the rename already committed the new file.
func writeFileAtomic(path string, data []byte, perm os.FileMode) error {
	return writeFileAtomicWithDirSync(path, data, perm, syncDirAfterRename)
}

// writeFileAtomicWithDirSync is writeFileAtomic with an injected post-rename
// dir-sync step (Store.syncDir). The injected step replaces
// syncDirAfterRename entirely, so tests exercise it on every platform,
// including Windows, without changing production Windows behaviour.
func writeFileAtomicWithDirSync(path string, data []byte, perm os.FileMode, syncDir func(string) error) error {
	return atomicfile.WriteWithDirSync(path, data, perm, syncDir)
}
