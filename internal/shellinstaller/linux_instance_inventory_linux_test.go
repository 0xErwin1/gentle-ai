//go:build linux

package shellinstaller

import (
	"os"
	"path/filepath"
	"testing"

	"golang.org/x/sys/unix"
)

// All paths are synthetic: never inspect the operator's HOME or Pi install.
type inventoryFixture struct {
	root, piExec, piHome, separateExec, separateHome string
}

func newInventoryFixture(t *testing.T) inventoryFixture {
	t.Helper()
	root := t.TempDir()
	f := inventoryFixture{root: root,
		piExec:       filepath.Join(root, "pi-file"),
		piHome:       filepath.Join(root, "pi-home"),
		separateExec: filepath.Join(root, "dedicated-file"),
		separateHome: filepath.Join(root, "dedicated-home")}
	if err := os.WriteFile(f.piExec, []byte("synthetic pi, never executed"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(f.piHome, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(f.separateExec, []byte("synthetic separate, never executed"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(f.separateHome, 0o700); err != nil {
		t.Fatal(err)
	}
	return f
}

func (f inventoryFixture) paths() LinuxInventoryPaths {
	return LinuxInventoryPaths{ExistingPiExecutable: f.piExec,
		ExistingPiHome: f.piHome, SeparateExecutable: f.separateExec,
		SeparateHome: f.separateHome}
}

func TestLinuxInventoryExplicitModesAndPhysicalObjects(t *testing.T) {
	cases := []struct {
		name       string
		channel    Channel
		mode       TerminalEntryPoint
		wantReject LinuxInventoryReject
	}{
		{"pi Stable", ChannelStable, TerminalEntryPointPi, 0},
		{"separate Stable", ChannelStable, TerminalEntryPointGentleShell, 0},
		{"pi Main still source-stopped", ChannelMain, TerminalEntryPointPi, RejectInventoryMainSource},
		{"zero channel refuses", "", TerminalEntryPointPi, RejectInventoryChannel},
		{"zero mode refuses", ChannelStable, "", RejectInventoryMode},
		{"unknown mode refuses", ChannelStable, "unknown", RejectInventoryMode},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			f := newInventoryFixture(t)
			paths := f.paths()
			if tt.mode == TerminalEntryPointPi || tt.mode == "" {
				paths.SeparateExecutable, paths.SeparateHome = "", ""
			}
			got := ObserveLinuxInstanceInventory(Profile{Channel: tt.channel,
				TerminalEntryPoint: tt.mode}, paths)
			if got.Kind != LinuxInventoryNotAuthorized || got.Rejected != tt.wantReject {
				t.Fatalf("kind=%q rejected=%b, want not-authorized/%b", got.Kind, got.Rejected, tt.wantReject)
			}
			if tt.wantReject == 0 && (got.ExistingPiInstanceID == nil ||
				got.ExistingPiInstanceID.Role != LinuxInstanceRolePi) {
				t.Fatalf("valid inventory must observe the explicit Pi role: %+v", got)
			}
			if tt.mode == TerminalEntryPointPi && got.SeparateInstanceID != nil {
				t.Fatal("explicit Pi mode must not claim a separate instance")
			}
			if got.Rejected != 0 && (got.ExistingPiInstanceID != nil || got.SeparateInstanceID != nil) {
				t.Fatalf("rejected inventory must not publish instance IDs: %+v", got)
			}
			if tt.wantReject == RejectInventoryMode {
				if got.ExistingPiExecutable.Exists || got.ExistingPiHome.Exists {
					t.Fatal("invalid mode must not inventory an existing object")
				}
				return
			}
			if !got.ExistingPiExecutable.Exists || !got.ExistingPiHome.Exists ||
				got.ExistingPiExecutable.MountID == 0 || got.ExistingPiHome.MountID == 0 {
				t.Fatalf("existing synthetic physical objects not observed: %+v", got)
			}
			if tt.mode == TerminalEntryPointGentleShell {
				if !got.SeparateExecutable.Exists || !got.SeparateHome.Exists ||
					got.SeparateExecutable.MountID == 0 || got.SeparateHome.MountID == 0 ||
					(got.SeparateExecutable.Dev == got.ExistingPiExecutable.Dev &&
						got.SeparateExecutable.Ino == got.ExistingPiExecutable.Ino) {
					t.Fatalf("distinct dedicated objects not observed: %+v", got)
				}
			} else if got.SeparateExecutable.Exists || got.SeparateHome.Exists {
				t.Fatal("explicit Pi must not claim separate target objects")
			}
		})
	}
}

func TestLinuxInventoryBuildsCanonicalRoleTaggedInstanceIDs(t *testing.T) {
	f := newInventoryFixture(t)
	first := ObserveLinuxInstanceInventory(Profile{Channel: ChannelStable,
		TerminalEntryPoint: TerminalEntryPointGentleShell}, f.paths())
	again := ObserveLinuxInstanceInventory(Profile{Channel: ChannelStable,
		TerminalEntryPoint: TerminalEntryPointGentleShell}, f.paths())
	if first.Rejected != 0 || first.ExistingPiInstanceID == nil || first.SeparateInstanceID == nil {
		t.Fatalf("complete physical inventories must provide both IDs: first=%+v", first)
	}
	if again.Rejected != 0 || again.ExistingPiInstanceID == nil || again.SeparateInstanceID == nil {
		t.Fatalf("complete repeated physical inventory must provide both IDs: again=%+v", again)
	}
	if *first.ExistingPiInstanceID != *again.ExistingPiInstanceID ||
		*first.SeparateInstanceID != *again.SeparateInstanceID {
		t.Fatalf("same descriptor-observed objects must produce deterministic IDs: first=%+v again=%+v",
			first, again)
	}
	if first.ExistingPiInstanceID.Role != LinuxInstanceRolePi ||
		first.SeparateInstanceID.Role != LinuxInstanceRoleGentleShell {
		t.Fatalf("instance roles must stay explicit and distinct: pi=%+v separate=%+v",
			first.ExistingPiInstanceID, first.SeparateInstanceID)
	}
	piID := first.ExistingPiInstanceID
	if piID.Executable.Type != first.ExistingPiExecutable.Mode&unix.S_IFMT ||
		piID.Executable.Dev != first.ExistingPiExecutable.Dev ||
		piID.Executable.Ino != first.ExistingPiExecutable.Ino ||
		piID.Executable.MountID != first.ExistingPiExecutable.MountID ||
		piID.Home.Type != first.ExistingPiHome.Mode&unix.S_IFMT ||
		piID.Home.Dev != first.ExistingPiHome.Dev || piID.Home.Ino != first.ExistingPiHome.Ino ||
		piID.Home.MountID != first.ExistingPiHome.MountID {
		t.Fatalf("physical ID must preserve descriptor-observed object types and tuples: %+v", piID)
	}
}

func TestLinuxInventoryRefusesUnsafeSyntheticPaths(t *testing.T) {
	cases := []struct {
		name   string
		mode   TerminalEntryPoint
		mutate func(*testing.T, inventoryFixture, *LinuxInventoryPaths)
		want   LinuxInventoryReject
	}{
		{"existing Pi symlink", TerminalEntryPointPi, func(t *testing.T, f inventoryFixture, p *LinuxInventoryPaths) {
			link := filepath.Join(f.root, "pi-link")
			if err := os.Symlink(f.piExec, link); err != nil {
				t.Fatal(err)
			}
			p.ExistingPiExecutable = link
		}, 0}, // kernel may surface ELOOP, ENOTDIR or a no-follow identity mismatch
		{"symlinked ancestor", TerminalEntryPointGentleShell, func(t *testing.T, f inventoryFixture, p *LinuxInventoryPaths) {
			link := filepath.Join(f.root, "parent-link")
			if err := os.Symlink(f.root, link); err != nil {
				t.Fatal(err)
			}
			p.SeparateHome = filepath.Join(link, "dedicated-home")
		}, 0},
		{"missing existing Pi", TerminalEntryPointPi, func(_ *testing.T, f inventoryFixture, p *LinuxInventoryPaths) {
			p.ExistingPiExecutable = filepath.Join(f.root, "missing-pi")
		}, 0}, // current classifier is errno-text dependent; assert refusal, not reason
		{"missing dedicated ancestor", TerminalEntryPointGentleShell, func(_ *testing.T, f inventoryFixture, p *LinuxInventoryPaths) {
			p.SeparateHome = filepath.Join(f.root, "missing-parent", "home")
		}, 0},

		{"separate executable aliases Pi", TerminalEntryPointGentleShell, func(_ *testing.T, f inventoryFixture, p *LinuxInventoryPaths) {
			p.SeparateExecutable = f.piExec
		}, RejectInventoryAlias},
		{"separate home aliases Pi", TerminalEntryPointGentleShell, func(_ *testing.T, f inventoryFixture, p *LinuxInventoryPaths) {
			p.SeparateHome = f.piHome
		}, RejectInventoryAlias},
		{"directory cannot be existing executable", TerminalEntryPointPi, func(_ *testing.T, f inventoryFixture, p *LinuxInventoryPaths) {
			p.ExistingPiExecutable = f.piHome
		}, RejectInventoryType},
		{"file cannot be existing home", TerminalEntryPointPi, func(_ *testing.T, f inventoryFixture, p *LinuxInventoryPaths) {
			p.ExistingPiHome = f.piExec
		}, RejectInventoryType},
		{"pi mode cannot borrow separate targets", TerminalEntryPointPi, func(_ *testing.T, _ inventoryFixture, _ *LinuxInventoryPaths) {}, RejectInventoryMode},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			f := newInventoryFixture(t)
			p := f.paths()
			if tt.mode == TerminalEntryPointPi && tt.want != RejectInventoryMode {
				p.SeparateExecutable, p.SeparateHome = "", ""
			}
			tt.mutate(t, f, &p)
			got := ObserveLinuxInstanceInventory(Profile{Channel: ChannelStable,
				TerminalEntryPoint: tt.mode}, p)
			if got.Kind != LinuxInventoryNotAuthorized || got.Rejected == 0 ||
				(tt.want != 0 && got.Rejected&tt.want == 0) {
				t.Fatalf("unsafe path kind=%q rejected=%b, want nonzero/bit %b", got.Kind, got.Rejected, tt.want)
			}
			if got.ExistingPiInstanceID != nil || got.SeparateInstanceID != nil {
				t.Fatalf("rejected inventory must not publish instance IDs: %+v", got)
			}
		})
	}
}

func TestLinuxInventoryAbsentLeafIsParentDataNotObjectID(t *testing.T) {
	f := newInventoryFixture(t)
	binParent, homeParent := filepath.Join(f.root, "bin-parent"), filepath.Join(f.root, "home-parent")
	for _, dir := range []string{binParent, homeParent} {
		if err := os.Mkdir(dir, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	p := f.paths()
	p.SeparateExecutable = filepath.Join(binParent, "new-bin")
	p.SeparateHome = filepath.Join(homeParent, "new-home")
	got := ObserveLinuxInstanceInventory(Profile{Channel: ChannelStable,
		TerminalEntryPoint: TerminalEntryPointGentleShell}, p)
	if got.Kind != LinuxInventoryNotAuthorized || got.Rejected != 0 {
		t.Fatalf("direct absence must remain data, not authority/error: %+v", got)
	}
	if got.ExistingPiInstanceID == nil || got.SeparateInstanceID != nil {
		t.Fatalf("missing separate targets must retain Pi ID but invent no separate ID: %+v", got)
	}
	for _, tt := range []struct {
		object LinuxObservedObject
		leaf   string
	}{
		{got.SeparateExecutable, "new-bin"}, {got.SeparateHome, "new-home"},
	} {
		o := tt.object
		if !o.Absent || o.Exists || o.Dev != 0 || o.Ino != 0 || o.MountID != 0 ||
			o.ParentDev == 0 || o.ParentIno == 0 || o.ParentMountID == 0 || o.MissingLeaf != tt.leaf {
			t.Fatalf("missing child invented identity or lost parent/name: %+v", o)
		}
	}
	p.SeparateHome = p.SeparateExecutable // exact same pending target
	alias := ObserveLinuxInstanceInventory(Profile{Channel: ChannelStable,
		TerminalEntryPoint: TerminalEntryPointGentleShell}, p)
	if alias.Kind != LinuxInventoryNotAuthorized || alias.Rejected&RejectInventoryAlias == 0 {
		t.Fatalf("identical pending targets must refuse: %+v", alias)
	}
	p.SeparateHome = filepath.Join(binParent, "casefold-ambiguous-other-name")
	casefold := ObserveLinuxInstanceInventory(Profile{Channel: ChannelStable,
		TerminalEntryPoint: TerminalEntryPointGentleShell}, p)
	if casefold.Rejected&RejectInventoryAlias == 0 {
		t.Fatalf("unknown casefold under same parent must refuse: %+v", casefold)
	}
}

// Synthetic facts exercise bind-mount aliases without creating or inspecting mounts.
func TestLinuxInventoryPartialSeparateTargetHasNoInstanceID(t *testing.T) {
	for _, missingExecutable := range []bool{true, false} {
		name := "home absent"
		if missingExecutable {
			name = "executable absent"
		}
		t.Run(name, func(t *testing.T) {
			f := newInventoryFixture(t)
			paths := f.paths()
			missing := filepath.Join(f.root, "future-target")
			if missingExecutable {
				paths.SeparateExecutable = missing
			} else {
				paths.SeparateHome = missing
			}
			got := ObserveLinuxInstanceInventory(Profile{Channel: ChannelStable,
				TerminalEntryPoint: TerminalEntryPointGentleShell}, paths)
			if got.Rejected != 0 || got.SeparateInstanceID != nil {
				t.Fatalf("incomplete separate target cannot have an InstanceID: %+v", got)
			}
			absent := got.SeparateHome
			if missingExecutable {
				absent = got.SeparateExecutable
			}
			if !absent.Absent || absent.Exists || absent.Dev != 0 || absent.Ino != 0 ||
				absent.MountID != 0 || absent.ParentDev == 0 || absent.ParentIno == 0 ||
				absent.ParentMountID == 0 || absent.MissingLeaf != "future-target" {
				t.Fatalf("missing leaf must preserve only accurate parent/name facts: %+v", absent)
			}
		})
	}
}

func TestLinuxInventoryCrossedMountDoesNotProveDisjointness(t *testing.T) {
	root := LinuxObservedObject{Dev: 1, Ino: 2, MountID: 10, Exists: true}
	pi := LinuxObservedObject{Dev: 7, Ino: 8, MountID: 11, Exists: true}
	alias := pi
	alias.MountID = 22
	piWalk := linuxInventoryWalk{facts: []LinuxObservedObject{root, pi}}
	aliasWalk := linuxInventoryWalk{facts: []LinuxObservedObject{root, alias}}
	if !linuxInventoryCrossedMount(piWalk) || !linuxInventoryCrossedMount(aliasWalk) {
		t.Fatal("distinct mount transitions must be observed")
	}
	if !linuxInventorySameObject(pi, alias) || !linuxInventoryPathOverlaps(piWalk, aliasWalk) {
		t.Fatal("different MountIDs must not hide a shared Dev+Ino")
	}
	pending := LinuxObservedObject{Absent: true, ParentDev: pi.Dev, ParentIno: pi.Ino,
		ParentMountID: pi.MountID, MissingLeaf: "new-bin"}
	pendingAlias := pending
	pendingAlias.ParentMountID = alias.MountID
	pendingAlias.MissingLeaf = "other-name"
	if !linuxInventorySameAbsent(pending, pendingAlias) {
		t.Fatal("a shared parent through different MountIDs must remain ambiguous")
	}
}

func TestLinuxInventoryDetectsDeterministicReplacement(t *testing.T) {
	f := newInventoryFixture(t)
	before := ObserveLinuxInstanceInventory(Profile{Channel: ChannelStable,
		TerminalEntryPoint: TerminalEntryPointPi}, LinuxInventoryPaths{
		ExistingPiExecutable: f.piExec, ExistingPiHome: f.piHome})
	if before.Rejected != 0 || before.ExistingPiInstanceID == nil {
		t.Fatalf("initial synthetic Pi identity: %+v", before)
	}
	held, err := linuxInventoryWalkPath(f.piExec, false, false)
	defer held.close()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(f.piExec, filepath.Join(f.root, "retired-pi")); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(f.piExec, []byte("replacement, never executed"), 0o600); err != nil {
		t.Fatal(err)
	}
	if linuxInventoryStable(f.piExec, false, false, held) {
		t.Fatal("held original FD must not match a replaced name")
	}
	after := ObserveLinuxInstanceInventory(Profile{Channel: ChannelStable,
		TerminalEntryPoint: TerminalEntryPointPi}, LinuxInventoryPaths{
		ExistingPiExecutable: f.piExec, ExistingPiHome: f.piHome})
	if after.Rejected != 0 || after.ExistingPiInstanceID == nil ||
		*before.ExistingPiInstanceID == *after.ExistingPiInstanceID {
		t.Fatalf("re-observation must identify a replacement rather than reuse stale data: before=%+v after=%+v",
			before.ExistingPiInstanceID, after.ExistingPiInstanceID)
	}
	missing := filepath.Join(f.root, "pending-child")
	absent, err := linuxInventoryWalkPath(missing, false, true)
	defer absent.close()
	if err != nil || !absent.missing {
		t.Fatalf("pending leaf: missing=%v err=%v", absent.missing, err)
	}
	if err := os.WriteFile(missing, []byte("created after observation"), 0o600); err != nil {
		t.Fatal(err)
	}
	if linuxInventoryStable(missing, false, true, absent) {
		t.Fatal("held absent child must not match a newly created object")
	}
}
