package reviewtransaction

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestCompactRoleResultSlotsPreserveImmutablePublicationSemantics(t *testing.T) {
	revision := "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	target := "sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	for _, test := range []struct {
		name string
		key  compactRoleResultSlotKey
	}{
		{name: "lens", key: compactLensRoleResultSlotKey(0, "review-risk")},
		{name: "refuter", key: compactRefuterRoleResultSlotKey()},
		{name: "targeted validator", key: compactTargetedValidatorRoleResultSlotKey(revision, target)},
	} {
		t.Run(test.name, func(t *testing.T) {
			storeDir := t.TempDir()
			payload := []byte(`{"result":"admitted"}` + "\n")
			if err := publishCompactRoleResultSlot(storeDir, test.key, payload); err != nil {
				t.Fatal(err)
			}
			first, err := ReadCompactRoleResultSlot(storeDir, test.key)
			if err != nil || !first.Occupied || !bytes.Equal(first.Payload, payload) || first.Digest != compactPreservedPayloadDigest(payload) {
				t.Fatalf("first slot = %#v, %v", first, err)
			}
			if err := publishCompactRoleResultSlot(storeDir, test.key, payload); err != nil {
				t.Fatalf("exact replay: %v", err)
			}
			path, err := compactRoleResultSlotPath(storeDir, test.key)
			if err != nil {
				t.Fatal(err)
			}
			if err := os.Remove(path + ".sha256"); err != nil {
				t.Fatal(err)
			}
			if _, err := ReadCompactRoleResultSlot(storeDir, test.key); err == nil {
				t.Fatal("partial publication was accepted")
			}
			if err := publishCompactRoleResultSlot(storeDir, test.key, payload); err != nil {
				t.Fatalf("repair missing digest: %v", err)
			}
			if err := publishCompactRoleResultSlot(storeDir, test.key, []byte(`{"result":"conflict"}`+"\n")); !errors.Is(err, ErrCapturedReviewerResultSlotConflict) {
				t.Fatalf("conflicting publication error = %v", err)
			}
			last, err := ReadCompactRoleResultSlot(storeDir, test.key)
			if err != nil || !bytes.Equal(last.Payload, payload) {
				t.Fatalf("conflicting publication changed bytes: %#v, %v", last, err)
			}
		})
	}
}

func TestCompactRoleResultSlotAcceptsCompatibleRelativeStoreRoot(t *testing.T) {
	workingDirectory, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	storeDir, err := filepath.Rel(workingDirectory, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	payload := []byte(`{"results":[]}` + "\n")
	key := compactRefuterRoleResultSlotKey()
	if err := publishCompactRoleResultSlot(storeDir, key, payload); err != nil {
		t.Fatal(err)
	}
	slot, err := ReadCompactRoleResultSlot(storeDir, key)
	if err != nil || !slot.Occupied || !bytes.Equal(slot.Payload, payload) {
		t.Fatalf("relative-root slot = %#v, %v", slot, err)
	}
}

func TestCompactRefuterCaptureDoesNotMutateReviewAuthority(t *testing.T) {
	fixture := newCompactReviewerCaptureFixture(t, "role-refuter-capture")
	payload := []byte(`{"results":[]}` + "\n")
	before, err := os.ReadFile(fixture.store.StatePath())
	if err != nil {
		t.Fatal(err)
	}
	validations := 0
	request := CompactAdmittedRefuterResultRequest{
		ExpectedRevision: fixture.request.ExpectedRevision,
		TargetIdentity:   fixture.request.TargetIdentity,
		Payload:          payload,
		PreparePublication: func(state CompactState) error {
			validations++
			if state.State != StateReviewing || state.LineageID != fixture.state.LineageID {
				return errors.New("unexpected review authority")
			}
			return nil
		},
	}
	if err := fixture.store.CaptureAdmittedRefuterResult(context.Background(), request); err != nil {
		t.Fatal(err)
	}
	if err := fixture.store.CaptureAdmittedRefuterResult(context.Background(), request); err != nil {
		t.Fatalf("exact replay: %v", err)
	}
	slot, err := ReadCompactRefuterResultSlot(fixture.store.Dir)
	if err != nil || !slot.Occupied || !bytes.Equal(slot.Payload, payload) || validations != 2 {
		t.Fatalf("refuter slot = %#v, validations=%d, err=%v", slot, validations, err)
	}
	after, err := os.ReadFile(fixture.store.StatePath())
	if err != nil || !bytes.Equal(before, after) {
		t.Fatalf("refuter capture mutated review authority: %v", err)
	}
}
