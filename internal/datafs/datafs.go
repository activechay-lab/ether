// Package datafs classifies the filesystem under Ether's data folder and
// checks, in that folder, the semantics the payload store and SQLite rely on.
//
// The payload store proves a crashed process is gone by taking the
// exclusive lock it held on a witness file. That proof needs locks that
// really exclude and are released when the holder dies, stable file
// identity, in-place rewrite with fsync, and atomic rename and remove. A
// known local filesystem has all of them. A network filesystem cannot give
// the first across machines, whatever a local test shows. Anything else is
// probed.
package datafs

import (
	"strings"
)

// Class is what Ether knows about a filesystem before probing it.
type Class uint8

const (
	// Unknown is not recognised; Probe decides.
	Unknown Class = iota
	// Local is a known filesystem on a disk in this machine.
	Local
	// Network is shared with other machines (NFS, SMB, Ceph, sshfs...).
	// Locks there do not exclude another machine.
	Network
)

func (c Class) String() string {
	switch c {
	case Local:
		return "local"
	case Network:
		return "network"
	}
	return "unknown"
}

// Info describes the filesystem holding a folder.
type Info struct {
	Path string
	// Type names the filesystem: the mount type from /proc/self/mountinfo
	// (fuse.shfs, nfs4, virtiofs), the BSD/macOS type name, the Windows
	// volume format, or the statfs magic in hex when nothing names it.
	Type  string
	Class Class
	// Kind is the cheap identity Kind returns. A later Kind that differs
	// means the folder now lives on another filesystem.
	Kind string
}

// Inspect classifies the filesystem holding dir. The error is the raw
// statfs error (ENOENT, EACCES...) when dir cannot be examined.
func Inspect(dir string) (Info, error) {
	kind, name, class, err := inspect(dir)
	if err != nil {
		return Info{}, err
	}
	return Info{Path: dir, Type: name, Class: class, Kind: kind}, nil
}

// Kind is a cheap identity of the filesystem holding dir (one statfs on
// Unix), for re-checking that it has not changed since Inspect.
func Kind(dir string) (string, error) {
	return statKind(dir)
}

// SQLiteUnsafe reports why SQLite cannot keep a database consistent on
// this filesystem, or "" when it can. SQLite needs POSIX locks and the
// shared memory of its write-ahead log, which network filesystems do not
// provide reliably; Unraid user shares (fuse.shfs) are the documented
// source of corrupted app databases on Unraid.
func SQLiteUnsafe(info Info) string {
	switch {
	case info.Type == "fuse.shfs":
		return "SQLite databases are not safe on Unraid user shares"
	case info.Class == Network:
		return "SQLite databases are not safe on network filesystems"
	}
	return ""
}

// HintKind classifies what to change for a filesystem Ether cannot fully
// trust: "unraid", "docker-desktop", "network", "fuse", or "" when there is
// nothing specific to say. The console translates by kind.
func HintKind(fstype string) string {
	switch {
	case fstype == "fuse.shfs":
		return "unraid"
	case dockerDesktopShare(fstype):
		return "docker-desktop"
	case strings.HasPrefix(fstype, "nfs"), fstype == "cifs", fstype == "smb3", fstype == "smbfs", fstype == "SMB share", fstype == "network drive", networkName(fstype):
		return "network"
	case strings.HasPrefix(fstype, "fuse"):
		return "fuse"
	}
	return ""
}

// Hint says, in English for logs and startup errors, what to change for a
// filesystem Ether cannot fully trust, or "" (#259).
func Hint(fstype string) string {
	switch HintKind(fstype) {
	case "unraid":
		return "Ether's data folder is on an Unraid user share (/mnt/user, fuse.shfs), which does not give the file locking Ether's database and cache need. " +
			"Map it to the pool or disk path instead, for example /mnt/cache/appdata/ether rather than /mnt/user/appdata/ether"
	case "docker-desktop":
		return "Ether's data folder is a Docker Desktop bind mount (" + fstype + "). Use a named volume instead, for example -v ether-config:/config"
	case "network":
		return "Ether's data folder is on a network share (" + fstype + "). Put it on a disk in this machine"
	case "fuse":
		return "Ether's data folder is on a FUSE filesystem (" + fstype + "). Put it on a disk in this machine"
	}
	return ""
}

// dockerDesktopShare is a host folder shared into the Docker Desktop VM.
func dockerDesktopShare(fstype string) bool {
	switch fstype {
	case "virtiofs", "9p", "fakeowner", "fuse.grpcfuse", "grpcfuse", "fuse.osxfs", "osxfs":
		return true
	}
	return false
}

// networkName reports mount type names that are shared with other machines.
func networkName(name string) bool {
	if strings.HasPrefix(name, "nfs") {
		return true
	}
	switch name {
	case "cifs", "smb3", "smbfs", "smb", "ncpfs", "ceph", "glusterfs", "afs", "coda", "gfs2", "ocfs2", "lustre", "gpfs", "beegfs", "orangefs", "davfs", "webdav", "afpfs":
		return true
	}
	sub, ok := strings.CutPrefix(name, "fuse.")
	if !ok {
		return false
	}
	switch sub {
	case "sshfs", "rclone", "s3fs", "glusterfs", "ceph-fuse", "cephfs", "gcsfuse", "goofys", "juicefs", "seaweedfs", "lizardfs", "mfs", "moosefs",
		"blobfuse", "blobfuse2", "smbnetfs", "curlftpfs", "davfs", "davfs2", "geesefs", "mountpoint-s3", "beegfs", "wekafs":
		return true
	}
	return false
}

// Usage reports the size and the bytes free to this process on the volume
// holding path.
func Usage(path string) (total, free int64, err error) { return usage(path) }
