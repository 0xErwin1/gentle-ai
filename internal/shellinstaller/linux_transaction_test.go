package shellinstaller

import (
	"fmt"
	"testing"
)

// Shape-only caller data: these are not physical, persisted or authenticated objects.
func journalTestObject(n uint64) LinuxJournalObject {
	return LinuxJournalObject{Dev: n + 1, Ino: n + 101, MountID: n + 201,
		SHA256: fmt.Sprintf("%064x", n), Exists: true}
}

func journalTestObjects(base uint64) (objects [4]LinuxJournalObject) {
	for i := range objects {
		objects[i] = journalTestObject(base + uint64(i))
	}
	return objects
}

func journalTestDigest(n uint64) string {
	return fmt.Sprintf("%064x", n)
}

func journalTestBegin(t *testing.T, mode TerminalEntryPoint) LinuxTransactionData {
	t.Helper()
	var prior [4]LinuxJournalObject
	instanceID := ""
	if mode == TerminalEntryPointPi {
		prior, instanceID = journalTestObjects(10), "synthetic-existing-pi-instance"
	}
	s, demand := BeginLinuxTransaction(mode, instanceID, prior)
	if s.Kind != LinuxJournalNotAuthorized || s.Phase != LinuxJournalNeedsPersistedReceipt ||
		demand.Kind != "persist-preimage-journal" || !demand.RequiresProtectedHost ||
		s.AppliedCount != 0 || s.JournalClaimSHA256 != "" {
		t.Fatalf("begin must demand protected persistence before effects: state=%+v demand=%+v", s, demand)
	}
	return s
}

func journalTestReported(t *testing.T, mode TerminalEntryPoint, count int) (LinuxTransactionData, [4]LinuxJournalObject) {
	t.Helper()
	s := ReportLinuxJournal(journalTestBegin(t, mode), journalTestDigest(999))
	if s.Kind != LinuxJournalNotAuthorized || s.Phase != LinuxJournalClaimedReceipt {
		t.Fatalf("receipt remains unverified data: %+v", s)
	}
	posts := journalTestObjects(100)
	for i := 0; i < count; i++ {
		claim := ""
		if mode == TerminalEntryPointGentleShell {
			claim = journalTestDigest(500 + uint64(i))
		}
		s = ReportLinuxPostimage(s, s.Actions[i], posts[i], claim)
		if s.Kind != LinuxJournalNotAuthorized || s.Phase != LinuxJournalPostimageData ||
			s.AppliedCount != uint8(i+1) {
			t.Fatalf("postimage %d must remain data: %+v", i, s)
		}
	}
	return s, posts
}

func TestLinuxJournalFixedModesAndReverseSelection(t *testing.T) {
	cases := []struct {
		name    string
		mode    TerminalEntryPoint
		actions [4]LinuxJournalAction
		inverse LinuxInverseAction
	}{
		{"dedicated create-only", TerminalEntryPointGentleShell,
			[4]LinuxJournalAction{LinuxJournalHome, LinuxJournalPackage, LinuxJournalExecutable, LinuxJournalSettings},
			LinuxInverseDeleteCreated},
		{"existing Pi takeover", TerminalEntryPointPi,
			[4]LinuxJournalAction{LinuxJournalPackage, LinuxJournalExecutable, LinuxJournalLink, LinuxJournalSettings},
			LinuxInverseRestorePi},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			s := journalTestBegin(t, tt.mode)
			if s.Actions != tt.actions || NextLinuxJournalDemand(s).Kind != "quarantine" {
				t.Fatalf("actions must be fixed and blocked before receipt: %+v", s)
			}
			s = ReportLinuxJournal(s, journalTestDigest(999)) // syntactically forged claim
			posts := journalTestObjects(100)
			for i, action := range tt.actions {
				demand := NextLinuxJournalDemand(s)
				if demand.Kind != "request-mode-scoped-effect-data" || demand.Action != action ||
					!demand.RequiresProtectedHost || !demand.RequiresPersistedJournal {
					t.Fatalf("step %d cannot request an unchecked or reordered effect: %+v", i, demand)
				}
				claim := ""
				if tt.mode == TerminalEntryPointGentleShell {
					claim = journalTestDigest(500 + uint64(i))
				}
				s = ReportLinuxPostimage(s, action, posts[i], claim)
				if s.Kind != LinuxJournalNotAuthorized || s.Phase != LinuxJournalPostimageData ||
					s.AppliedCount != uint8(i+1) {
					t.Fatalf("step %d did not retain refusal/data: %+v", i, s)
				}
			}
			if NextLinuxJournalDemand(s).Kind != "quarantine" {
				t.Fatal("fifth effect must never be selected")
			}
			p := SelectLinuxInverse(s, posts, false)
			if p.Kind != LinuxJournalNotAuthorized || p.Quarantined || !p.RequiresHost || p.Count != 4 {
				t.Fatalf("complete shape-only report must select conditional host work, not authorize: %+v", p)
			}
			for j, got := range p.ReverseActions {
				i := 3 - j
				if got.Action != tt.inverse || got.JournalAction != tt.actions[i] ||
					got.ExpectedPostimage != posts[i] {
					t.Fatalf("inverse %d is not exact reverse CAS: %+v", j, got)
				}
				if tt.mode == TerminalEntryPointPi {
					if got.RestorePreimage != journalTestObject(10+uint64(i)) ||
						got.ExistingInstanceID != "synthetic-existing-pi-instance" || got.CreatedClaimSHA256 != "" {
						t.Fatalf("takeover inverse lost prior instance or claimed a creation: %+v", got)
					}
				} else if got.RestorePreimage != (LinuxJournalObject{}) || got.ExistingInstanceID != "" ||
					got.CreatedClaimSHA256 != journalTestDigest(500+uint64(i)) {
					t.Fatalf("separate inverse claimed restoration or lost create data: %+v", got)
				}
			}
		})
	}
}

func TestLinuxJournalInvalidBeginningsAndReceiptOrder(t *testing.T) {
	prior := journalTestObjects(10)
	cases := []struct {
		name     string
		mode     TerminalEntryPoint
		instance string
		preimage [4]LinuxJournalObject
	}{
		{"zero mode is not takeover", "", "", [4]LinuxJournalObject{}},
		{"unknown mode", "unexpected", "", [4]LinuxJournalObject{}},
		{"dedicated rejects takeover instance", TerminalEntryPointGentleShell, "forged-pi", [4]LinuxJournalObject{}},
		{"dedicated rejects prior Pi objects", TerminalEntryPointGentleShell, "", prior},
		{"takeover needs exact instance", TerminalEntryPointPi, "", prior},
		{"takeover needs all valid preimages", TerminalEntryPointPi, "pi", [4]LinuxJournalObject{}},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			s, demand := BeginLinuxTransaction(tt.mode, tt.instance, tt.preimage)
			if s.Kind != LinuxJournalNotAuthorized || s.Phase != LinuxJournalQuarantined ||
				demand.Kind != "quarantine" || NextLinuxJournalDemand(s).Kind != "quarantine" {
				t.Fatalf("invalid beginning must quarantine: state=%+v demand=%+v", s, demand)
			}
		})
	}
	for _, mode := range []TerminalEntryPoint{TerminalEntryPointGentleShell, TerminalEntryPointPi} {
		t.Run(string(mode), func(t *testing.T) {
			before := journalTestBegin(t, mode)
			if NextLinuxJournalDemand(before).Kind != "quarantine" ||
				SelectLinuxInverse(before, journalTestObjects(100), false).Quarantined != true {
				t.Fatal("no effect/inverse before a persisted-journal claim")
			}
			claim := ""
			if mode == TerminalEntryPointGentleShell {
				claim = journalTestDigest(500)
			}
			if wrongOrder := ReportLinuxPostimage(before, before.Actions[0], journalTestObject(100), claim); wrongOrder.Phase != LinuxJournalQuarantined {
				t.Fatal("postimage before receipt must quarantine")
			}
			for _, bad := range []string{"", "ABC", fmt.Sprintf("%064X", uint64(0xabcdef))} {
				if got := ReportLinuxJournal(before, bad); got.Phase != LinuxJournalQuarantined {
					t.Fatalf("invalid journal shape %q accepted", bad)
				}
			}
		})
	}
}

func TestLinuxJournalRejectsWrongActionDuplicateAndCreationClaim(t *testing.T) {
	for _, mode := range []TerminalEntryPoint{TerminalEntryPointGentleShell, TerminalEntryPointPi} {
		t.Run(string(mode), func(t *testing.T) {
			base := ReportLinuxJournal(journalTestBegin(t, mode), journalTestDigest(999))
			claim := ""
			if mode == TerminalEntryPointGentleShell {
				claim = journalTestDigest(500)
			}
			if got := ReportLinuxPostimage(base, base.Actions[1], journalTestObject(100), claim); got.Phase != LinuxJournalQuarantined {
				t.Fatal("wrong next action must quarantine")
			}
			if got := ReportLinuxPostimage(base, base.Actions[0], LinuxJournalObject{}, claim); got.Phase != LinuxJournalQuarantined {
				t.Fatal("zero object must quarantine")
			}
			badClaim := journalTestDigest(500)
			if mode == TerminalEntryPointGentleShell {
				badClaim = ""
			}
			if got := ReportLinuxPostimage(base, base.Actions[0], journalTestObject(100), badClaim); got.Phase != LinuxJournalQuarantined {
				t.Fatal("missing create claim or takeover creation claim must quarantine")
			}
			one, _ := journalTestReported(t, mode, 1)
			duplicate := journalTestObject(101)
			duplicate.Dev, duplicate.Ino = one.AppliedPostimage[0].Dev, one.AppliedPostimage[0].Ino
			if duplicate.MountID == one.AppliedPostimage[0].MountID {
				t.Fatal("fixture must cross mount ID")
			}
			claim = ""
			if mode == TerminalEntryPointGentleShell {
				claim = journalTestDigest(501)
			}
			if got := ReportLinuxPostimage(one, one.Actions[1], duplicate, claim); got.Phase != LinuxJournalQuarantined {
				t.Fatal("same dev+ino across mount IDs must quarantine")
			}
		})
	}
}

func TestLinuxJournalInverseQuarantinesIncompleteOrDriftedClaims(t *testing.T) {
	for _, mode := range []TerminalEntryPoint{TerminalEntryPointGentleShell, TerminalEntryPointPi} {
		t.Run(string(mode), func(t *testing.T) {
			full, posts := journalTestReported(t, mode, 4)
			zero := ReportLinuxJournal(journalTestBegin(t, mode), journalTestDigest(999))
			partial, _ := journalTestReported(t, mode, 2)
			liveDrift := posts
			liveDrift[2].SHA256 = journalTestDigest(9999)
			mountDrift := posts
			mountDrift[2].MountID++
			duplicate := full
			duplicate.AppliedPostimage[1].Dev = duplicate.AppliedPostimage[0].Dev
			duplicate.AppliedPostimage[1].Ino = duplicate.AppliedPostimage[0].Ino
			duplicateLive := duplicate.AppliedPostimage
			wrongActions := full
			wrongActions.Actions[0] = LinuxJournalSettings
			badKind := full
			badKind.Kind = "ready"
			noReceipt := full
			noReceipt.JournalClaimSHA256 = ""
			badInstance := full
			badPreimage := full
			badCreate := full
			if mode == TerminalEntryPointPi {
				badInstance.ExistingInstanceID = ""
				badPreimage.Preimage[1] = LinuxJournalObject{}
				badCreate.CreatedClaimSHA256[0] = journalTestDigest(500)
			} else {
				badInstance.ExistingInstanceID = "forged-pi"
				badPreimage.Preimage[1] = journalTestObject(12)
				badCreate.CreatedClaimSHA256[0] = ""
			}
			cases := []struct {
				name          string
				state         LinuxTransactionData
				live          [4]LinuxJournalObject
				restoreFailed bool
			}{
				{"zero postimages", zero, posts, false},
				{"partial postimages", partial, posts, false},
				{"restore failure", full, posts, true},
				{"live digest drift", full, liveDrift, false},
				{"live mount drift", full, mountDrift, false},
				{"duplicate dev inode across mounts", duplicate, duplicateLive, false},
				{"forged action order", wrongActions, posts, false},
				{"forged authorization kind", badKind, posts, false},
				{"no journal receipt", noReceipt, posts, false},
				{"invalid instance binding", badInstance, posts, false},
				{"invalid preimage", badPreimage, posts, false},
				{"invalid create claim", badCreate, posts, false},
			}
			for _, tt := range cases {
				t.Run(tt.name, func(t *testing.T) {
					p := SelectLinuxInverse(tt.state, tt.live, tt.restoreFailed)
					if p.Kind != LinuxJournalNotAuthorized || !p.Quarantined || !p.RequiresHost || p.Count != 0 {
						t.Fatalf("ambiguous inverse must preserve all without selecting: %+v", p)
					}
				})
			}
		})
	}
}
