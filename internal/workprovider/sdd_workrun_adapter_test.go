package workprovider

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/gentleman-programming/gentle-ai/internal/workrun"
)

func TestSDDWorkRunAuthorityDoesNotInferOrCreateMissingRun(t *testing.T) {
	repo := initWorkRunAdapterRepository(t)
	authority := SDDWorkRunAuthority{Repo: repo}
	if _, err := authority.ResolveRun(
		context.Background(),
		"missing-change",
	); err == nil {
		t.Fatal("ResolveRun() inferred a missing SDD authority")
	}
	if _, err := os.Stat(filepath.Join(repo, ".git", "gentle-ai")); !os.IsNotExist(err) {
		t.Fatalf("ResolveRun() created SDD authority storage: %v", err)
	}
}

func TestSDDWorkRunAuthorityRejectsReservationBeforeOpeningRepository(t *testing.T) {
	authority := SDDWorkRunAuthority{Repo: "/does/not/exist"}
	if err := authority.BindVerificationReservation(
		context.Background(),
		workrun.SDDReservationBinding{},
	); err == nil {
		t.Fatal("BindVerificationReservation() accepted a malformed owner binding")
	}
}

func TestSDDWorkRunAuthorityFailsClosedWithoutRepository(t *testing.T) {
	authority := SDDWorkRunAuthority{}
	if _, err := authority.ResolveRun(
		context.Background(),
		"change",
	); err == nil {
		t.Fatal("ResolveRun() accepted an empty repository")
	}
}
