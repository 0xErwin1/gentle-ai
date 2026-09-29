//go:build linux

package shellinstaller

import (
	"errors"
	"fmt"
	"io"
	"os"

	"golang.org/x/sys/unix"
)

const linuxJournalRecordMaxBytes = 1 << 20

// linuxJournalPublishRecord publishes bounded bytes without replacing an
// existing name, but grants no authority until a caller independently reopens
// and verifies the record. It requires an empty per-transaction root; any
// existing entry quarantines later publication. The anonymous O_TMPFILE inode
// is linked through its procfs FD path, with no named-temporary fallback.
// Fsync requests durability but cannot prove survival through every storage
// layer or prevent same-UID mutation after this function returns.
func linuxJournalPublishRecord(root *LinuxJournalRoot, name string, data []byte) error {
	if root == nil || !linuxJournalRecordName(name) || len(data) == 0 || len(data) > linuxJournalRecordMaxBytes {
		return fmt.Errorf("invalid bounded Linux journal record")
	}
	content := append([]byte(nil), data...)

	root.closeMu.Lock()
	defer root.closeMu.Unlock()
	if err := root.validateForPrepareLocked(); err != nil {
		return err
	}

	dir, err := linuxJournalOpenDirectory(root)
	if err != nil {
		return err
	}
	defer dir.Close()
	if err := linuxJournalRequireEmptyDirectory(root); err != nil {
		return err
	}

	if err := root.validateForPrepareLocked(); err != nil {
		return err
	}
	tempFD, err := unix.Openat(root.fd, ".", unix.O_TMPFILE|unix.O_RDWR|unix.O_CLOEXEC, 0o600)
	if err != nil {
		return fmt.Errorf("create anonymous journal record without fallback: %w", err)
	}
	defer unix.Close(tempFD)
	if err := unix.Fchmod(tempFD, 0o600); err != nil {
		return fmt.Errorf("make anonymous journal record private: %w", err)
	}
	if _, err := linuxJournalRecordFact(tempFD, 0, 0o600, 0); err != nil {
		return fmt.Errorf("verify new anonymous journal record: %w", err)
	}

	remaining := content
	for len(remaining) > 0 {
		if err := root.validateForPrepareLocked(); err != nil {
			return err
		}
		n, writeErr := unix.Write(tempFD, remaining)
		if n > 0 {
			remaining = remaining[n:]
		}
		if writeErr == unix.EINTR {
			continue
		}
		if writeErr != nil {
			return fmt.Errorf("write anonymous journal record: %w", writeErr)
		}
		if n == 0 {
			return fmt.Errorf("short write of anonymous journal record")
		}
	}
	if err := root.validateForPrepareLocked(); err != nil {
		return err
	}
	if err := unix.Fchmod(tempFD, 0o400); err != nil {
		return fmt.Errorf("make journal record read-only: %w", err)
	}
	if err := unix.Fsync(tempFD); err != nil {
		return fmt.Errorf("sync anonymous journal record: %w", err)
	}
	if _, err := linuxJournalRecordFact(tempFD, uint64(len(content)), 0o400, 0); err != nil {
		return fmt.Errorf("verify synced anonymous journal record: %w", err)
	}
	if err := root.validateForPrepareLocked(); err != nil {
		return err
	}
	if err := linuxJournalRequireEmptyDirectory(root); err != nil {
		return err
	}

	// linkat follows this process-owned procfs FD link to the already verified
	// inode. The destination remains rooted at the held directory descriptor;
	// linkat does not replace an existing destination.
	procFDPath := fmt.Sprintf("/proc/self/fd/%d", tempFD)
	if err := unix.Linkat(unix.AT_FDCWD, procFDPath, root.fd, name, unix.AT_SYMLINK_FOLLOW); err != nil {
		return fmt.Errorf("link verified anonymous journal record without replacement: %w", err)
	}
	if err := root.validateForPrepareLocked(); err != nil {
		return err
	}
	if _, err := linuxJournalRecordFact(tempFD, uint64(len(content)), 0o400, 1); err != nil {
		return fmt.Errorf("verify linked journal record descriptor: %w", err)
	}
	if err := unix.Fsync(int(dir.Fd())); err != nil {
		return fmt.Errorf("sync journal directory after publication: %w", err)
	}
	if err := root.validateForPrepareLocked(); err != nil {
		return err
	}
	if err := linuxJournalVerifyPublishedEntry(root, name, tempFD, uint64(len(content))); err != nil {
		return err
	}
	return nil
}

func linuxJournalOpenDirectory(root *LinuxJournalRoot) (*os.File, error) {
	fd, err := unix.Openat(root.fd, ".", unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0)
	if err != nil {
		return nil, fmt.Errorf("open journal directory for sync: %w", err)
	}
	dir := os.NewFile(uintptr(fd), "linux journal root")
	dirFact, err := linuxJournalRootDescriptorFact(int(dir.Fd()))
	if err != nil {
		_ = dir.Close()
		return nil, fmt.Errorf("read journal directory descriptor: %w", err)
	}
	if dirFact.identity != root.identity || dirFact.uid != uint32(unix.Geteuid()) || dirFact.mode&0o077 != 0 {
		_ = dir.Close()
		return nil, fmt.Errorf("journal directory descriptor failed identity or privacy check")
	}
	return dir, nil
}

func linuxJournalRequireEmptyDirectory(root *LinuxJournalRoot) error {
	dir, err := linuxJournalOpenDirectory(root)
	if err != nil {
		return err
	}
	defer dir.Close()
	entries, err := dir.ReadDir(1)
	if err != nil && !errors.Is(err, io.EOF) {
		return fmt.Errorf("inspect journal directory: %w", err)
	}
	if len(entries) != 0 {
		return fmt.Errorf("nonempty journal root requires reconciliation")
	}
	return nil
}

func linuxJournalVerifyPublishedEntry(root *LinuxJournalRoot, name string, recordFD int, size uint64) error {
	dir, err := linuxJournalOpenDirectory(root)
	if err != nil {
		return err
	}
	entries, readErr := dir.ReadDir(2)
	closeErr := dir.Close()
	if readErr != nil && !errors.Is(readErr, io.EOF) {
		return fmt.Errorf("recheck journal directory after publication: %w", readErr)
	}
	if closeErr != nil {
		return fmt.Errorf("close journal directory after publication: %w", closeErr)
	}
	if len(entries) != 1 || entries[0].Name() != name {
		return fmt.Errorf("journal root changed during publication; reconciliation is required")
	}

	fd, err := unix.Openat(root.fd, name, unix.O_RDONLY|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0)
	if err != nil {
		return fmt.Errorf("open linked journal record through root descriptor: %w", err)
	}
	defer unix.Close(fd)
	published, err := linuxJournalRecordFact(fd, size, 0o400, 1)
	if err != nil {
		return fmt.Errorf("verify linked journal record: %w", err)
	}
	anonymous, err := linuxJournalRecordFact(recordFD, size, 0o400, 1)
	if err != nil {
		return fmt.Errorf("reverify anonymous journal record: %w", err)
	}
	if published.identity != anonymous.identity {
		return fmt.Errorf("published journal entry does not match verified record descriptor")
	}
	return nil
}

func linuxJournalRecordName(name string) bool {
	if len(name) == 0 || len(name) > 128 || name == "." || name == ".." {
		return false
	}
	for i, c := range []byte(name) {
		valid := c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || (i > 0 && (c == '-' || c == '_' || c == '.'))
		if !valid {
			return false
		}
	}
	return true
}

type linuxJournalRecordMetadata struct {
	identity LinuxJournalRootIdentity
	size     uint64
	links    uint32
	mode     uint32
	uid      uint32
}

func linuxJournalRecordFact(fd int, size uint64, mode uint32, links uint32) (linuxJournalRecordMetadata, error) {
	var sx unix.Statx_t
	if err := unix.Statx(fd, "", unix.AT_EMPTY_PATH|unix.AT_NO_AUTOMOUNT,
		unix.STATX_BASIC_STATS|unix.STATX_MNT_ID, &sx); err != nil {
		return linuxJournalRecordMetadata{}, fmt.Errorf("stat opened journal record: %w", err)
	}
	const required = unix.STATX_TYPE | unix.STATX_MODE | unix.STATX_UID | unix.STATX_INO | unix.STATX_MNT_ID | unix.STATX_SIZE | unix.STATX_NLINK
	if sx.Mask&uint32(required) != uint32(required) || sx.Mnt_id == 0 || sx.Ino == 0 ||
		uint32(sx.Mode)&unix.S_IFMT != unix.S_IFREG || uint64(sx.Size) != size || sx.Nlink != links ||
		uint32(sx.Mode)&0o7777 != mode || sx.Uid != uint32(unix.Geteuid()) {
		return linuxJournalRecordMetadata{}, fmt.Errorf("journal record metadata is incomplete or unsafe")
	}
	return linuxJournalRecordMetadata{
		identity: LinuxJournalRootIdentity{Device: unix.Mkdev(sx.Dev_major, sx.Dev_minor), Inode: sx.Ino, MountID: sx.Mnt_id},
		size:     sx.Size,
		links:    sx.Nlink,
		mode:     uint32(sx.Mode) & 0o7777,
		uid:      sx.Uid,
	}, nil
}
