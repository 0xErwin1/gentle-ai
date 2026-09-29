//go:build linux

package shellinstaller

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"sync"

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
	return linuxJournalPublishRecordWithEvidence(root, name, data, nil)
}

func linuxJournalPublishRecordWithEvidence(root *LinuxJournalRoot, name string, data []byte, evidence *linuxJournalRecordMetadata) error {
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
	linkedRecord, err := linuxJournalRecordFact(tempFD, uint64(len(content)), 0o400, 1)
	if err != nil {
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
	if evidence != nil {
		*evidence = linkedRecord
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

// linuxJournalPrepareRecord publishes and then independently reopens and reads
// the exact named record. Its result is a data observation only; it authorizes
// no transaction effect and makes no power-loss durability claim.
func linuxJournalPrepareRecord(root *LinuxJournalRoot, name string, data []byte) (*linuxJournalPreparedRecord, error) {
	if root == nil || !linuxJournalRecordName(name) || len(data) == 0 || len(data) > linuxJournalRecordMaxBytes {
		return nil, fmt.Errorf("invalid bounded Linux journal record")
	}
	content := append([]byte(nil), data...)

	root.closeMu.Lock()
	if err := root.validateForPrepareLocked(); err != nil {
		root.closeMu.Unlock()
		return nil, err
	}
	rootFact, err := linuxJournalRootDescriptorFact(root.fd)
	if err != nil {
		root.closeMu.Unlock()
		return nil, err
	}
	if rootFact.uid != uint32(unix.Geteuid()) || rootFact.mode&0o7777 != 0o700 {
		root.closeMu.Unlock()
		return nil, fmt.Errorf("journal root mode is not exactly private 0700")
	}
	rootFD, err := unix.FcntlInt(uintptr(root.fd), unix.F_DUPFD_CLOEXEC, 0)
	root.closeMu.Unlock()
	if err != nil {
		return nil, fmt.Errorf("pin journal root for readback: %w", err)
	}
	closeRootOnError := true
	defer func() {
		if closeRootOnError {
			_ = unix.Close(rootFD)
		}
	}()

	var evidence linuxJournalRecordMetadata
	if err := linuxJournalPublishRecordWithEvidence(root, name, content, &evidence); err != nil {
		return nil, err
	}
	recordFD, err := linuxJournalOpenVerifiedReadback(rootFD, root.identity, rootFact.mode, name, content, evidence)
	if err != nil {
		return nil, err
	}
	closeRootOnError = false
	return &linuxJournalPreparedRecord{
		rootFD: rootFD, recordFD: recordFD, rootIdentity: root.identity,
		rootMode: rootFact.mode, name: name, content: content, evidence: evidence,
		observation: linuxJournalRecordObservation{name: name, root: root.identity, record: evidence},
	}, nil
}

type linuxJournalRecordObservation struct {
	name   string
	root   LinuxJournalRootIdentity
	record linuxJournalRecordMetadata
}

type linuxJournalPreparedRecord struct {
	rootFD       int
	recordFD     int
	rootIdentity LinuxJournalRootIdentity
	rootMode     uint32
	name         string
	content      []byte
	evidence     linuxJournalRecordMetadata
	observation  linuxJournalRecordObservation
	mu           sync.Mutex
	closed       bool
}

// Observation returns the immutable point-in-time readback result, not effect authority.
func (record *linuxJournalPreparedRecord) Observation() linuxJournalRecordObservation {
	if record == nil {
		return linuxJournalRecordObservation{}
	}
	return record.observation
}

// Verify reopens the named entry and repeats metadata, byte, and root checks.
func (record *linuxJournalPreparedRecord) Verify() error {
	if record == nil {
		return fmt.Errorf("prepared Linux journal record is nil")
	}
	record.mu.Lock()
	defer record.mu.Unlock()
	if record.closed {
		return fmt.Errorf("prepared Linux journal record is closed")
	}
	fd, err := linuxJournalOpenVerifiedReadback(record.rootFD, record.rootIdentity, record.rootMode,
		record.name, record.content, record.evidence)
	if err != nil {
		return err
	}
	pinnedErr := linuxJournalVerifyRecordDescriptor(record.recordFD, record.rootIdentity, record.evidence)
	closeErr := unix.Close(fd)
	if pinnedErr != nil {
		return pinnedErr
	}
	if closeErr != nil {
		return fmt.Errorf("close reopened Linux journal record: %w", closeErr)
	}
	return nil
}

// Close releases the pinned record and root descriptors. It is safe to repeat.
func (record *linuxJournalPreparedRecord) Close() error {
	if record == nil {
		return nil
	}
	record.mu.Lock()
	defer record.mu.Unlock()
	if record.closed {
		return nil
	}
	record.closed = true
	var closeErr error
	if err := unix.Close(record.recordFD); err != nil {
		closeErr = err
	}
	if err := unix.Close(record.rootFD); err != nil && closeErr == nil {
		closeErr = err
	}
	record.recordFD, record.rootFD = -1, -1
	return closeErr
}

func linuxJournalOpenVerifiedReadback(rootFD int, rootIdentity LinuxJournalRootIdentity, rootMode uint32,
	name string, expected []byte, evidence linuxJournalRecordMetadata) (int, error) {
	if !linuxJournalRecordName(name) || len(expected) == 0 || len(expected) > linuxJournalRecordMaxBytes ||
		evidence.size != uint64(len(expected)) {
		return -1, fmt.Errorf("invalid Linux journal readback request")
	}
	if err := linuxJournalCheckReadbackEntries(rootFD, rootIdentity, rootMode, name); err != nil {
		return -1, err
	}
	fd, err := unix.Openat(rootFD, name, unix.O_RDONLY|unix.O_CLOEXEC|unix.O_NOFOLLOW|unix.O_NONBLOCK, 0)
	if err != nil {
		return -1, fmt.Errorf("reopen published Linux journal record without following links: %w", err)
	}
	fail := func(err error) (int, error) {
		_ = unix.Close(fd)
		return -1, err
	}
	if err := linuxJournalVerifyRecordDescriptor(fd, rootIdentity, evidence); err != nil {
		return fail(err)
	}
	if err := linuxJournalPreadExact(fd, expected); err != nil {
		return fail(err)
	}
	if err := linuxJournalVerifyRecordDescriptor(fd, rootIdentity, evidence); err != nil {
		return fail(err)
	}
	if err := linuxJournalCheckReadbackEntries(rootFD, rootIdentity, rootMode, name); err != nil {
		return fail(err)
	}
	if err := linuxJournalVerifyNamedRecord(rootFD, rootIdentity, name, evidence); err != nil {
		return fail(err)
	}
	return fd, nil
}

func linuxJournalVerifyRecordDescriptor(fd int, rootIdentity LinuxJournalRootIdentity, evidence linuxJournalRecordMetadata) error {
	actual, err := linuxJournalRecordFact(fd, evidence.size, 0o400, 1)
	if err != nil {
		return err
	}
	if actual != evidence || actual.identity.Device != rootIdentity.Device || actual.identity.MountID != rootIdentity.MountID {
		return fmt.Errorf("Linux journal record differs from published descriptor evidence or root")
	}
	return nil
}

func linuxJournalVerifyNamedRecord(rootFD int, rootIdentity LinuxJournalRootIdentity, name string,
	evidence linuxJournalRecordMetadata) error {
	fd, err := unix.Openat(rootFD, name, unix.O_RDONLY|unix.O_CLOEXEC|unix.O_NOFOLLOW|unix.O_NONBLOCK, 0)
	if err != nil {
		return fmt.Errorf("reopen named Linux journal record for identity check: %w", err)
	}
	defer unix.Close(fd)
	return linuxJournalVerifyRecordDescriptor(fd, rootIdentity, evidence)
}

func linuxJournalCheckReadbackEntries(rootFD int, rootIdentity LinuxJournalRootIdentity, rootMode uint32, name string) error {
	if err := linuxJournalValidateReadbackRoot(rootFD, rootIdentity, rootMode); err != nil {
		return err
	}
	dirFD, err := unix.Openat(rootFD, ".", unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0)
	if err != nil {
		return fmt.Errorf("open journal root for readback entry check: %w", err)
	}
	dirFact, err := linuxJournalRootDescriptorFact(dirFD)
	if err != nil {
		_ = unix.Close(dirFD)
		return err
	}
	if dirFact.identity != rootIdentity || dirFact.uid != uint32(unix.Geteuid()) || dirFact.mode != rootMode {
		_ = unix.Close(dirFD)
		return fmt.Errorf("journal root metadata changed during readback")
	}
	dir := os.NewFile(uintptr(dirFD), "Linux journal readback root")
	entries, readErr := dir.ReadDir(2)
	closeErr := dir.Close()
	if readErr != nil && !errors.Is(readErr, io.EOF) {
		return fmt.Errorf("read journal root entries during readback: %w", readErr)
	}
	if closeErr != nil {
		return fmt.Errorf("close journal root after readback entry check: %w", closeErr)
	}
	if len(entries) != 1 || entries[0].Name() != name {
		return fmt.Errorf("journal root entry set changed during readback")
	}
	return linuxJournalValidateReadbackRoot(rootFD, rootIdentity, rootMode)
}

func linuxJournalValidateReadbackRoot(rootFD int, identity LinuxJournalRootIdentity, mode uint32) error {
	fact, err := linuxJournalRootDescriptorFact(rootFD)
	if err != nil {
		return err
	}
	if fact.identity != identity || fact.uid != uint32(unix.Geteuid()) || fact.mode != mode || fact.mode&0o077 != 0 {
		return fmt.Errorf("journal root identity, ownership, or mode changed during readback")
	}
	return nil
}

func linuxJournalPreadExact(fd int, expected []byte) error {
	got := make([]byte, len(expected))
	for offset := 0; offset < len(got); {
		n, err := unix.Pread(fd, got[offset:], int64(offset))
		if n > 0 {
			offset += n
		}
		if err == unix.EINTR {
			continue
		}
		if err != nil {
			return fmt.Errorf("pread published Linux journal record: %w", err)
		}
		if n == 0 {
			return io.ErrUnexpectedEOF
		}
	}
	if !bytes.Equal(got, expected) {
		return fmt.Errorf("published Linux journal record bytes differ from requested content")
	}
	return nil
}
