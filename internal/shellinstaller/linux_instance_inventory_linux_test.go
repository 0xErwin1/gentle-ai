//go:build linux

package shellinstaller

import (
	"os"
	"path/filepath"
	"testing"
)

// All paths are synthetic: never inspect the operator's HOME or Pi install.
type inventoryFixture struct {
	root, piExec, piHome, separateExec, separateHome string
}

func newInventoryFixture(t *testing.T) inventoryFixture {
	t.Helper()
	root := t.TempDir()
	f := inventoryFixture{root: root,
		piExec: filepath.Join(root, "pi-file"),
		piHome: filepath.Join(root, "pi-home"),
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
		name string
		channel Channel
		mode TerminalEntryPoint
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

func TestLinuxInventoryRefusesUnsafeSyntheticPaths(t *testing.T) {
	cases := []struct {
		name string
		mode TerminalEntryPoint
		mutate func(*testing.T, inventoryFixture, *LinuxInventoryPaths)
		want LinuxInventoryReject
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
	for _, tt := range []struct {
		object LinuxObservedObject
		leaf string
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

func TestLinuxInventoryDetectsDeterministicReplacement(t *testing.T) {
	f := newInventoryFixture(t)
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
