//go:build linux

package shellinstaller

import (
	"fmt"
	"path/filepath"
	"strings"
	"sync"

	"golang.org/x/sys/unix"
)

// LinuxJournalRootIdentity describes the journal directory observed through its
// open descriptor. It is point-in-time data, not consent or durable authority.
type LinuxJournalRootIdentity struct {
	Device  uint64
	Inode   uint64
	MountID uint64
}

// LinuxJournalRoot holds the final directory descriptor until Close. The
// descriptor pins the opened object, not its pathname or mutable metadata.
type LinuxJournalRoot struct {
	fd       int
	identity LinuxJournalRootIdentity

	closeMu sync.Mutex
	closed  bool
}

// Identity returns the device, inode, and mount ID read from the opened root
// descriptor. The identity remains available after Close as an observation.
func (root *LinuxJournalRoot) Identity() LinuxJournalRootIdentity {
	if root == nil {
		return LinuxJournalRootIdentity{}
	}
	return root.identity
}

// Close releases the descriptor held by this root. Repeated calls are safe.
func (root *LinuxJournalRoot) Close() error {
	if root == nil {
		return nil
	}
	root.closeMu.Lock()
	defer root.closeMu.Unlock()
	if root.closed {
		return nil
	}
	root.closed = true
	fd := root.fd
	root.fd = -1
	return unix.Close(fd)
}

// OpenLinuxJournalRoot opens one explicit absolute directory using a single
// openat2 resolution from a descriptor for /. It rejects symlinks in every
// component and roots not owned by the effective user or accessible to
// group/world. It creates or changes no filesystem object. If openat2 is not
// available, opening fails closed without a pathname-walk fallback.
//
// The descriptor pins the resolved directory object only. No opener can pin
// the supplied pathname or prevent its ownership/mode from changing after the
// successful open and point-in-time metadata check.
func OpenLinuxJournalRoot(path string) (*LinuxJournalRoot, error) {
	relativePath, err := linuxJournalRootRelativePath(path)
	if err != nil {
		return nil, err
	}

	rootFD, err := unix.Open("/", unix.O_PATH|unix.O_DIRECTORY|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, fmt.Errorf("open journal path root: %w", err)
	}
	defer unix.Close(rootFD)

	fd, err := unix.Openat2(rootFD, relativePath, &unix.OpenHow{
		Flags:   uint64(unix.O_PATH | unix.O_DIRECTORY | unix.O_CLOEXEC),
		Resolve: unix.RESOLVE_BENEATH | unix.RESOLVE_NO_SYMLINKS,
	})
	if err != nil {
		return nil, fmt.Errorf("open journal root with no-symlink resolution: %w", err)
	}
	keepDescriptor := false
	defer func() {
		if !keepDescriptor {
			_ = unix.Close(fd)
		}
	}()

	fact, err := linuxJournalRootDescriptorFact(fd)
	if err != nil {
		return nil, err
	}
	if fact.uid != uint32(unix.Geteuid()) {
		return nil, fmt.Errorf("journal root is not owned by the effective user")
	}
	if fact.mode&0o077 != 0 {
		return nil, fmt.Errorf("journal root is accessible to group or world")
	}

	keepDescriptor = true
	return &LinuxJournalRoot{fd: fd, identity: fact.identity}, nil
}

type linuxJournalRootFact struct {
	identity LinuxJournalRootIdentity
	mode     uint32
	uid      uint32
}

func linuxJournalRootRelativePath(path string) (string, error) {
	if !filepath.IsAbs(path) || path == "/" || len(path) > 4096 ||
		filepath.Clean(path) != path || strings.ContainsRune(path, '\x00') {
		return "", fmt.Errorf("journal root must be a clean absolute path below /")
	}
	parts := strings.Split(strings.TrimPrefix(path, "/"), "/")
	if len(parts) == 0 || len(parts) > 32 {
		return "", fmt.Errorf("journal root path has too many components")
	}
	for _, part := range parts {
		if part == "" || part == "." || part == ".." || len(part) > 255 {
			return "", fmt.Errorf("invalid journal root path component")
		}
	}
	return strings.TrimPrefix(path, "/"), nil
}

func linuxJournalRootDescriptorFact(fd int) (linuxJournalRootFact, error) {
	var sx unix.Statx_t
	if err := unix.Statx(fd, "", unix.AT_EMPTY_PATH|unix.AT_NO_AUTOMOUNT,
		unix.STATX_BASIC_STATS|unix.STATX_MNT_ID, &sx); err != nil {
		return linuxJournalRootFact{}, fmt.Errorf("read opened journal root identity: %w", err)
	}
	const required = unix.STATX_TYPE | unix.STATX_MODE | unix.STATX_UID | unix.STATX_INO | unix.STATX_MNT_ID
	if sx.Mask&uint32(required) != uint32(required) || sx.Mnt_id == 0 || sx.Ino == 0 {
		return linuxJournalRootFact{}, fmt.Errorf("opened journal root has incomplete descriptor identity")
	}
	if uint32(sx.Mode)&unix.S_IFMT != unix.S_IFDIR {
		return linuxJournalRootFact{}, fmt.Errorf("opened journal root is not a directory")
	}
	return linuxJournalRootFact{
		identity: LinuxJournalRootIdentity{
			Device:  unix.Mkdev(sx.Dev_major, sx.Dev_minor),
			Inode:   sx.Ino,
			MountID: sx.Mnt_id,
		},
		mode: uint32(sx.Mode),
		uid:  sx.Uid,
	}, nil
}
