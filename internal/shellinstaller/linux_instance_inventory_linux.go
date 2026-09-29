//go:build linux

package shellinstaller

import (
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	"golang.org/x/sys/unix"
)

// LinuxInventoryPaths select observations, never authority or process identity.
type LinuxInventoryPaths struct {
	ExistingPiExecutable string
	ExistingPiHome       string
	SeparateExecutable   string
	SeparateHome         string
}

// LinuxObservedObject is point-in-time DATA, never a durable instance pin.
type LinuxObservedObject struct {
	Dev           uint64
	Ino           uint64
	MountID       uint64
	Mode          uint32
	Exists        bool
	ParentDev     uint64 // populated for a direct absent proposed child only
	ParentIno     uint64
	ParentMountID uint64
	MissingLeaf   string // exact observed-absent child name, not an object ID
	Absent        bool
	CrossedMount  bool // observed transition, never disjointness proof
}
type LinuxInventoryRefusalKind string

const LinuxInventoryNotAuthorized LinuxInventoryRefusalKind = "not-authorized"
type LinuxInventoryReject uint32

type LinuxInstanceRole string

const (
	LinuxInstanceRolePi         LinuxInstanceRole = "pi"
	LinuxInstanceRoleGentleShell LinuxInstanceRole = "gentle-shell"
)

// LinuxPhysicalObjectID is a canonical descriptor-observed object tuple.
// It is point-in-time data, not authority for consent, CAS, or effects.
type LinuxPhysicalObjectID struct {
	Dev     uint64
	Ino     uint64
	MountID uint64
	Type    uint32
}

// LinuxPhysicalInstanceID combines both physical objects with an explicit role.
// It is derived only by the Linux observer; it is neither caller input nor a hash.
type LinuxPhysicalInstanceID struct {
	Role       LinuxInstanceRole
	Executable LinuxPhysicalObjectID
	Home       LinuxPhysicalObjectID
}

const (
	RejectInventoryMode LinuxInventoryReject = 1 << iota
	RejectInventoryChannel
	RejectInventoryMainSource
	RejectInventoryPath
	RejectInventorySymlink
	RejectInventoryMount
	RejectInventoryType
	RejectInventoryAlias
	RejectInventoryDrift
)
// LinuxInstanceInventory refuses even if Rejected is zero.
type LinuxInstanceInventory struct {
	Kind                 LinuxInventoryRefusalKind
	EntryPoint           TerminalEntryPoint
	Rejected             LinuxInventoryReject
	ExistingPiExecutable LinuxObservedObject
	ExistingPiHome       LinuxObservedObject
	SeparateExecutable   LinuxObservedObject
	SeparateHome         LinuxObservedObject
	ExistingPiInstanceID *LinuxPhysicalInstanceID
	SeparateInstanceID   *LinuxPhysicalInstanceID
}

type linuxInventoryWalk struct {
	fds         []int
	facts       []LinuxObservedObject // root first, then each path component
	missing     bool
	missingLeaf string
}

func (w *linuxInventoryWalk) close() {
	for i := len(w.fds) - 1; i >= 0; i-- {
		_ = unix.Close(w.fds[i])
	}
	w.fds = nil
}

// A no-follow, no-read walk cannot prevent hostile automount or later rename.
func linuxInventoryWalkPath(path string, wantDirectory bool, allowMissingLeaf bool) (linuxInventoryWalk, error) {
	var w linuxInventoryWalk
	if !filepath.IsAbs(path) || path == "/" || len(path) > 4096 ||
		filepath.Clean(path) != path || strings.ContainsRune(path, '\x00') {
		return w, fmt.Errorf("invalid absolute inventory path")
	}
	parts := strings.Split(strings.TrimPrefix(path, "/"), "/")
	if len(parts) == 0 || len(parts) > 32 {
		return w, fmt.Errorf("inventory path has too many components")
	}
	for _, part := range parts {
		if part == "" || part == "." || part == ".." || len(part) > 255 {
			return w, fmt.Errorf("invalid inventory path component")
		}
	}
	root, err := unix.Open("/", unix.O_PATH|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		return w, err
	}
	w.fds = append(w.fds, root)
	rootFact, err := linuxInventoryFact(root)
	if err != nil {
		return w, err // caller closes the retained root descriptor
	}
	w.facts = append(w.facts, rootFact)
	for i, part := range parts {
		last := i == len(parts)-1
		flags := unix.O_PATH | unix.O_NOFOLLOW | unix.O_CLOEXEC
		if !last || wantDirectory {
			flags |= unix.O_DIRECTORY
		}
		parent := w.fds[len(w.fds)-1]
		fd, openErr := unix.Openat(parent, part, flags, 0)
		if openErr != nil {
			if last && allowMissingLeaf && errors.Is(openErr, unix.ENOENT) {
				var absent unix.Statx_t
				if err := unix.Statx(parent, part, unix.AT_SYMLINK_NOFOLLOW|unix.AT_NO_AUTOMOUNT,
					unix.STATX_BASIC_STATS|unix.STATX_MNT_ID, &absent); errors.Is(err, unix.ENOENT) {
					w.missing = true
					w.missingLeaf = part
					return w, nil
				}
			}
			return w, openErr
		}
		w.fds = append(w.fds, fd)
		fact, err := linuxInventoryFact(fd)
		if err != nil {
			return w, err
		}
		if fact.Mode&unix.S_IFMT == unix.S_IFLNK {
			return w, fmt.Errorf("symlink inventory component")
		}
		if !last && fact.Mode&unix.S_IFMT != unix.S_IFDIR {
			return w, fmt.Errorf("nondirectory inventory ancestor")
		}
		w.facts = append(w.facts, fact)
		if err := linuxInventoryNameStill(parent, part, fact); err != nil {
			return w, err
		}
	}
	leaf := w.facts[len(w.facts)-1]
	if wantDirectory && leaf.Mode&unix.S_IFMT != unix.S_IFDIR {
		return w, fmt.Errorf("home is not a directory")
	}
	if !wantDirectory && leaf.Mode&unix.S_IFMT != unix.S_IFREG {
		return w, fmt.Errorf("executable is not a regular object")
	}
	return w, nil
}

func linuxInventoryFact(fd int) (LinuxObservedObject, error) {
	var st unix.Stat_t
	if err := unix.Fstat(fd, &st); err != nil {
		return LinuxObservedObject{}, err
	}
	var sx unix.Statx_t
	if err := unix.Statx(fd, "", unix.AT_EMPTY_PATH|unix.AT_NO_AUTOMOUNT,
		unix.STATX_BASIC_STATS|unix.STATX_MNT_ID, &sx); err != nil {
		return LinuxObservedObject{}, err
	}
	const required = unix.STATX_TYPE | unix.STATX_MODE | unix.STATX_INO | unix.STATX_MNT_ID
	if sx.Mask&uint32(required) != uint32(required) || sx.Mnt_id == 0 ||
		sx.Ino != st.Ino || unix.Mkdev(sx.Dev_major, sx.Dev_minor) != uint64(st.Dev) ||
		uint32(sx.Mode)&unix.S_IFMT != st.Mode&unix.S_IFMT {
		return LinuxObservedObject{}, fmt.Errorf("unresolved mount or object identity")
	}
	return LinuxObservedObject{Dev: uint64(st.Dev), Ino: st.Ino,
		MountID: sx.Mnt_id, Mode: uint32(st.Mode), Exists: true}, nil
}

func linuxInventoryNameStill(parent int, name string, opened LinuxObservedObject) error {
	var sx unix.Statx_t
	if err := unix.Statx(parent, name, unix.AT_SYMLINK_NOFOLLOW|unix.AT_NO_AUTOMOUNT,
		unix.STATX_BASIC_STATS|unix.STATX_MNT_ID, &sx); err != nil {
		return err
	}
	const required = unix.STATX_TYPE | unix.STATX_MODE | unix.STATX_INO | unix.STATX_MNT_ID
	if sx.Mask&uint32(required) != uint32(required) || sx.Mnt_id != opened.MountID ||
		sx.Ino != opened.Ino || unix.Mkdev(sx.Dev_major, sx.Dev_minor) != opened.Dev ||
		uint32(sx.Mode)&unix.S_IFMT != opened.Mode&unix.S_IFMT {
		return fmt.Errorf("inventory name changed during descriptor walk")
	}
	return nil
}

func linuxInventorySameObject(a, b LinuxObservedObject) bool {
	// Dev+inode also catches aliases through two bind mounts with distinct IDs.
	return a.Exists && b.Exists && a.Dev == b.Dev && a.Ino == b.Ino
}

func linuxInventoryCrossedMount(w linuxInventoryWalk) bool {
	for i := 1; i < len(w.facts); i++ {
		if w.facts[i].MountID != w.facts[i-1].MountID {
			return true
		}
	}
	return false
}
func linuxInventoryAbsentObject(w linuxInventoryWalk) LinuxObservedObject {
	parent := w.facts[len(w.facts)-1] // held parent FD; no invented child ID
	return LinuxObservedObject{ParentDev: parent.Dev, ParentIno: parent.Ino,
		ParentMountID: parent.MountID, MissingLeaf: w.missingLeaf, Absent: true,
		CrossedMount: linuxInventoryCrossedMount(w)}
}

func linuxInventorySameAbsent(a, b LinuxObservedObject) bool {
	// Without a proved filesystem casefold policy, even distinct pending
	// names under the same physical parent cannot certify disjoint leaves.
	return a.Absent && b.Absent && a.ParentDev == b.ParentDev && a.ParentIno == b.ParentIno
}

func linuxInventoryPathOverlaps(a, b linuxInventoryWalk) bool {
	if len(a.facts) < 2 || len(b.facts) < 2 {
		return false
	}
	// A missing leaf is not its parent; sibling proposals may share one.
	if !a.missing {
		leafA := a.facts[len(a.facts)-1]
		for _, ancestor := range b.facts[1:] {
			if linuxInventorySameObject(leafA, ancestor) {
				return true
			}
		}
	}
	if !b.missing {
		leafB := b.facts[len(b.facts)-1]
		for _, ancestor := range a.facts[1:] {
			if linuxInventorySameObject(leafB, ancestor) {
				return true
			}
		}
	}
	return false
}

func linuxInventoryStable(path string, dir, allowMissing bool, held linuxInventoryWalk) bool {
	second, err := linuxInventoryWalkPath(path, dir, allowMissing)
	defer second.close()
	if err != nil || second.missing != held.missing ||
		second.missingLeaf != held.missingLeaf || len(second.facts) != len(held.facts) {
		return false
	}
	for i := range held.facts {
		if held.facts[i] != second.facts[i] {
			return false
		}
	}
	return true // post-close drift still needs a later recheck
}

func linuxInventoryInstanceID(role LinuxInstanceRole, executable, home LinuxObservedObject) (LinuxPhysicalInstanceID, bool) {
	if !executable.Exists || !home.Exists || executable.Mode&unix.S_IFMT != unix.S_IFREG ||
		home.Mode&unix.S_IFMT != unix.S_IFDIR {
		return LinuxPhysicalInstanceID{}, false
	}
	return LinuxPhysicalInstanceID{Role: role,
		Executable: LinuxPhysicalObjectID{Dev: executable.Dev, Ino: executable.Ino,
			MountID: executable.MountID, Type: executable.Mode & unix.S_IFMT},
		Home: LinuxPhysicalObjectID{Dev: home.Dev, Ino: home.Ino,
			MountID: home.MountID, Type: home.Mode & unix.S_IFMT}}, true
}

func linuxInventoryRejectError(err error) LinuxInventoryReject {
	if err == nil {
		return 0
	}
	if strings.Contains(err.Error(), "symlink") || errors.Is(err, unix.ELOOP) {
		return RejectInventorySymlink
	}
	if strings.Contains(err.Error(), "mount") || errors.Is(err, unix.ENOSYS) {
		return RejectInventoryMount
	}
	if strings.Contains(err.Error(), "directory") || strings.Contains(err.Error(), "regular") {
		return RejectInventoryType
	}
	return RejectInventoryPath
}

// ObserveLinuxInstanceInventory measures objects, never consent or Ready;
// its closed descriptors cannot pin names for a later write or loaded process.
func ObserveLinuxInstanceInventory(profile Profile, paths LinuxInventoryPaths) LinuxInstanceInventory {
	out := LinuxInstanceInventory{Kind: LinuxInventoryNotAuthorized, EntryPoint: profile.TerminalEntryPoint}
	if !profile.Channel.Valid() {
		out.Rejected |= RejectInventoryChannel
	}
	if profile.Channel == ChannelMain {
		out.Rejected |= RejectInventoryMainSource
	}
	if profile.TerminalEntryPoint != TerminalEntryPointPi &&
		profile.TerminalEntryPoint != TerminalEntryPointGentleShell {
		out.Rejected |= RejectInventoryMode
		return out
	}
	if profile.TerminalEntryPoint == TerminalEntryPointPi &&
		(paths.SeparateExecutable != "" || paths.SeparateHome != "") {
		out.Rejected |= RejectInventoryMode
		return out
	}
	if profile.TerminalEntryPoint == TerminalEntryPointGentleShell &&
		(paths.SeparateExecutable == "" || paths.SeparateHome == "") {
		out.Rejected |= RejectInventoryPath
		return out
	}
	piExec, err := linuxInventoryWalkPath(paths.ExistingPiExecutable, false, false)
	defer piExec.close()
	if err != nil {
		out.Rejected |= linuxInventoryRejectError(err)
		return out
	}
	out.ExistingPiExecutable = piExec.facts[len(piExec.facts)-1]
	out.ExistingPiExecutable.CrossedMount = linuxInventoryCrossedMount(piExec)
	piHome, err := linuxInventoryWalkPath(paths.ExistingPiHome, true, false)
	defer piHome.close()
	if err != nil {
		out.Rejected |= linuxInventoryRejectError(err)
		return out
	}
	out.ExistingPiHome = piHome.facts[len(piHome.facts)-1]
	out.ExistingPiHome.CrossedMount = linuxInventoryCrossedMount(piHome)
	if profile.TerminalEntryPoint == TerminalEntryPointGentleShell {
		separateExec, walkErr := linuxInventoryWalkPath(paths.SeparateExecutable, false, true)
		defer separateExec.close()
		if walkErr != nil {
			out.Rejected |= linuxInventoryRejectError(walkErr)
			return out
		}
		if separateExec.missing {
			out.SeparateExecutable = linuxInventoryAbsentObject(separateExec)
		} else {
			out.SeparateExecutable = separateExec.facts[len(separateExec.facts)-1]
			out.SeparateExecutable.CrossedMount = linuxInventoryCrossedMount(separateExec)
		}
		separateHome, walkErr := linuxInventoryWalkPath(paths.SeparateHome, true, true)
		defer separateHome.close()
		if walkErr != nil {
			out.Rejected |= linuxInventoryRejectError(walkErr)
			return out
		}
		if separateHome.missing {
			out.SeparateHome = linuxInventoryAbsentObject(separateHome)
		} else {
			out.SeparateHome = separateHome.facts[len(separateHome.facts)-1]
			out.SeparateHome.CrossedMount = linuxInventoryCrossedMount(separateHome)
		}
		if linuxInventoryPathOverlaps(piExec, separateExec) ||
			linuxInventoryPathOverlaps(piExec, separateHome) ||
			linuxInventoryPathOverlaps(piHome, separateExec) ||
			linuxInventoryPathOverlaps(piHome, separateHome) ||
			linuxInventorySameObject(out.SeparateExecutable, out.SeparateHome) ||
			linuxInventorySameAbsent(out.SeparateExecutable, out.SeparateHome) {
			out.Rejected |= RejectInventoryAlias
		}
		if !linuxInventoryStable(paths.SeparateExecutable, false, true, separateExec) ||
			!linuxInventoryStable(paths.SeparateHome, true, true, separateHome) {
			out.Rejected |= RejectInventoryDrift
		}
	}
	if !linuxInventoryStable(paths.ExistingPiExecutable, false, false, piExec) ||
		!linuxInventoryStable(paths.ExistingPiHome, true, false, piHome) {
		out.Rejected |= RejectInventoryDrift
	}
	if out.Rejected != 0 {
		return out
	}
	if id, ok := linuxInventoryInstanceID(LinuxInstanceRolePi,
		out.ExistingPiExecutable, out.ExistingPiHome); ok {
		out.ExistingPiInstanceID = &id
	}
	if profile.TerminalEntryPoint == TerminalEntryPointGentleShell {
		if id, ok := linuxInventoryInstanceID(LinuxInstanceRoleGentleShell,
			out.SeparateExecutable, out.SeparateHome); ok {
			out.SeparateInstanceID = &id
		}
	}
	return out
}
