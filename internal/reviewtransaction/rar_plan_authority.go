package reviewtransaction

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"reflect"
)

const (
	RARPlanAuthoritySchema = "gentle-ai.rar-plan-authority/v1"

	rarPlanAuthorityDigestDomain = "gentle-ai.rar-plan-authority-digest/v1"
)

// RARPlanAuthority is the complete RAR-owned pre-execution plan preimage.
// AuthorityRef is also the PlanRevisionRef consumed by WorkRun. Repository
// identity and the full Snapshot are retained because the smaller Subject is
// intentionally insufficient to revalidate a live workspace by itself.
type RARPlanAuthority struct {
	Schema             string                    `json:"schema"`
	AuthorityRef       string                    `json:"authority_ref"`
	RepositoryIdentity string                    `json:"repository_identity"`
	Snapshot           Snapshot                  `json:"snapshot"`
	Subject            VerificationSubject       `json:"subject"`
	Applicability      VerificationApplicability `json:"applicability"`
	Registry           VerificationPlanRegistry  `json:"registry"`
	Plan               VerificationPlan          `json:"plan"`
}

// Validate proves canonical self-consistency. Only ResolvePlan additionally
// proves that the bound repository identity and exact Snapshot are still live.
// PolicyHash is owned by the emitted registry/plan contract; this package does
// not invent a second external policy owner.
func (authority RARPlanAuthority) Validate() error {
	if authority.Schema != RARPlanAuthoritySchema ||
		!validSHA256(authority.AuthorityRef) ||
		!validSHA256(authority.RepositoryIdentity) {
		return errors.New("invalid RAR plan authority identity")
	}
	subject, err := VerificationSubjectFromSnapshot(authority.Snapshot)
	if err != nil {
		return fmt.Errorf("validate RAR plan snapshot subject: %w", err)
	}
	if subject != authority.Subject ||
		authority.Applicability.Subject != authority.Subject ||
		authority.Plan.Subject != authority.Subject {
		return errors.New("RAR plan authority does not bind one exact snapshot subject")
	}
	if err := ValidateVerificationPlan(
		authority.Applicability,
		authority.Registry,
		authority.Plan,
	); err != nil {
		return fmt.Errorf("validate RAR plan contracts: %w", err)
	}
	want, err := rarPlanAuthorityDigest(authority)
	if err != nil {
		return err
	}
	if authority.AuthorityRef != want {
		return errors.New("RAR plan authority ref does not match its canonical content")
	}
	return nil
}

// RARPlanPublication contains only the exact frozen snapshot and the complete
// RAR verification-plan contracts. It deliberately has no result, outcome,
// candidate-head, or caller-selected storage field.
type RARPlanPublication struct {
	Snapshot      Snapshot
	Applicability VerificationApplicability
	Registry      VerificationPlanRegistry
	Plan          VerificationPlan
}

// PublishPlan validates the exact live snapshot, constructs its owner
// content-addressed authority, and durably publishes it in the repository's
// private Git-common-dir RAR store. Exact replay is idempotent.
func (repository *RARAuthorityRepository) PublishPlan(
	ctx context.Context,
	request RARPlanPublication,
) (RARPlanAuthority, error) {
	if err := ctx.Err(); err != nil {
		return RARPlanAuthority{}, err
	}
	if err := repository.validateIdentity(ctx); err != nil {
		return RARPlanAuthority{}, err
	}
	subject, err := VerificationSubjectFromSnapshot(request.Snapshot)
	if err != nil {
		return RARPlanAuthority{}, err
	}
	if request.Applicability.Subject != subject || request.Plan.Subject != subject {
		return RARPlanAuthority{}, errors.New("RAR plan publication does not bind the exact snapshot subject")
	}
	if err := ValidateVerificationPlan(
		request.Applicability,
		request.Registry,
		request.Plan,
	); err != nil {
		return RARPlanAuthority{}, fmt.Errorf("validate RAR plan publication: %w", err)
	}
	if err := repository.validateLivePlanSnapshot(ctx, request.Snapshot); err != nil {
		return RARPlanAuthority{}, err
	}
	if err := repository.validateNativePlanContracts(
		ctx,
		request.Snapshot,
		request.Applicability,
		request.Registry,
		request.Plan,
	); err != nil {
		return RARPlanAuthority{}, err
	}

	authority := RARPlanAuthority{
		Schema:             RARPlanAuthoritySchema,
		RepositoryIdentity: repository.identity.RepositoryIdentity,
		Snapshot:           request.Snapshot,
		Subject:            subject,
		Applicability:      request.Applicability,
		Registry:           request.Registry,
		Plan:               request.Plan,
	}
	authority.AuthorityRef, err = rarPlanAuthorityDigest(authority)
	if err != nil {
		return RARPlanAuthority{}, err
	}
	if err := authority.Validate(); err != nil {
		return RARPlanAuthority{}, err
	}
	payload, err := canonicalRARPlanAuthorityPayload(authority)
	if err != nil {
		return RARPlanAuthority{}, err
	}

	if err := repository.validateIdentity(ctx); err != nil {
		return RARPlanAuthority{}, err
	}
	if err := ensureRARRepositoryRoot(repository.identity.GitCommonDir, repository.root, true); err != nil {
		return RARPlanAuthority{}, err
	}
	if err := ensurePrivateRARDirectoryTree(repository.root, repository.planObjectsRoot(), true); err != nil {
		return RARPlanAuthority{}, err
	}
	if err := repository.validateIdentity(ctx); err != nil {
		return RARPlanAuthority{}, err
	}
	lock, err := acquireRARAuthorityLock(ctx, filepath.Join(repository.root, "LOCK"))
	if err != nil {
		return RARPlanAuthority{}, err
	}
	defer lock.release()
	if err := repository.validateIdentity(ctx); err != nil {
		return RARPlanAuthority{}, err
	}
	if err := repository.validateLivePlanSnapshot(ctx, request.Snapshot); err != nil {
		return RARPlanAuthority{}, err
	}
	if err := repository.validateNativePlanContracts(
		ctx,
		request.Snapshot,
		request.Applicability,
		request.Registry,
		request.Plan,
	); err != nil {
		return RARPlanAuthority{}, err
	}
	if err := publishPrivateRARImmutable(repository.planObjectPath(authority.AuthorityRef), payload); err != nil {
		return RARPlanAuthority{}, err
	}
	if err := repository.validateIdentity(ctx); err != nil {
		return RARPlanAuthority{}, err
	}
	// A concurrent workspace mutation cannot turn a stale publication into a
	// successful issuance. The immutable orphan remains harmless and retry-safe.
	if err := repository.validateLivePlanSnapshot(ctx, request.Snapshot); err != nil {
		return RARPlanAuthority{}, err
	}
	if err := repository.validateIdentity(ctx); err != nil {
		return RARPlanAuthority{}, err
	}
	return authority, nil
}

// ResolvePlan resolves only the owner-issued AuthorityRef (PlanRevisionRef),
// then revalidates repository identity and the stored exact live snapshot. It
// accepts no caller result, outcome, policy projection, or candidate head.
func (repository *RARAuthorityRepository) ResolvePlan(
	ctx context.Context,
	authorityRef string,
) (RARPlanAuthority, error) {
	if err := ctx.Err(); err != nil {
		return RARPlanAuthority{}, err
	}
	if !validSHA256(authorityRef) {
		return RARPlanAuthority{}, errors.New("invalid RAR plan authority ref")
	}
	if err := repository.validateIdentity(ctx); err != nil {
		return RARPlanAuthority{}, err
	}
	if err := ensureRARRepositoryRoot(repository.identity.GitCommonDir, repository.root, false); err != nil {
		return RARPlanAuthority{}, err
	}
	if err := ensurePrivateRARDirectoryTree(repository.root, repository.planObjectsRoot(), false); err != nil {
		return RARPlanAuthority{}, err
	}
	payload, err := readPrivateRARFile(repository.planObjectPath(authorityRef))
	if err != nil {
		return RARPlanAuthority{}, err
	}
	authority, err := parseRARPlanAuthority(payload)
	if err != nil {
		return RARPlanAuthority{}, fmt.Errorf("%w: %v", ErrRARAuthorityCorrupt, err)
	}
	if authority.AuthorityRef != authorityRef {
		return RARPlanAuthority{}, fmt.Errorf("%w: plan authority lookup binding mismatch", ErrRARAuthorityCorrupt)
	}
	if authority.RepositoryIdentity != repository.identity.RepositoryIdentity {
		return RARPlanAuthority{}, fmt.Errorf("%w: repository identity changed", ErrRARAuthorityStale)
	}
	if err := repository.validateIdentity(ctx); err != nil {
		return RARPlanAuthority{}, err
	}
	if err := repository.validateLivePlanSnapshot(ctx, authority.Snapshot); err != nil {
		return RARPlanAuthority{}, err
	}
	if err := repository.validateNativePlanContracts(
		ctx,
		authority.Snapshot,
		authority.Applicability,
		authority.Registry,
		authority.Plan,
	); err != nil {
		return RARPlanAuthority{}, err
	}
	if err := repository.validateIdentity(ctx); err != nil {
		return RARPlanAuthority{}, err
	}
	return authority, nil
}

func (repository *RARAuthorityRepository) validateLivePlanSnapshot(
	ctx context.Context,
	snapshot Snapshot,
) error {
	if repository == nil {
		return errors.New("RAR authority repository is not initialized")
	}
	if err := (SnapshotBuilder{Repo: repository.identity.RepositoryRoot}).ValidateLiveSnapshot(ctx, snapshot); err != nil {
		return fmt.Errorf("%w: exact plan snapshot is no longer live: %v", ErrRARAuthorityStale, err)
	}
	return nil
}

func (repository *RARAuthorityRepository) validateNativePlanContracts(
	ctx context.Context,
	snapshot Snapshot,
	applicability VerificationApplicability,
	registry VerificationPlanRegistry,
	plan VerificationPlan,
) error {
	if repository == nil {
		return errors.New("RAR authority repository is not initialized")
	}
	builder := SnapshotBuilder{Repo: repository.identity.RepositoryRoot}
	nativeApplicability, err := builder.ClassifyVerificationApplicability(
		ctx,
		snapshot,
		registry,
		applicability.EvidenceRefs,
	)
	if err != nil {
		return fmt.Errorf("derive native RAR plan applicability: %w", err)
	}
	if !reflect.DeepEqual(nativeApplicability, applicability) {
		return errors.New("RAR plan applicability differs from the native exact-snapshot classification")
	}
	nativePlan, err := BuildVerificationPlan(nativeApplicability, registry)
	if err != nil {
		return fmt.Errorf("derive native RAR verification plan: %w", err)
	}
	if !reflect.DeepEqual(nativePlan, plan) {
		return errors.New("RAR verification plan differs from the complete native registry projection")
	}
	return nil
}

func (repository *RARAuthorityRepository) planObjectsRoot() string {
	return filepath.Join(repository.root, "plan-objects")
}

func (repository *RARAuthorityRepository) planObjectPath(authorityRef string) string {
	return filepath.Join(repository.planObjectsRoot(), hashPathComponent(authorityRef)+".json")
}

func canonicalRARPlanAuthorityPayload(authority RARPlanAuthority) ([]byte, error) {
	if err := authority.Validate(); err != nil {
		return nil, err
	}
	payload, err := json.Marshal(authority)
	if err != nil {
		return nil, err
	}
	return append(payload, '\n'), nil
}

func parseRARPlanAuthority(payload []byte) (RARPlanAuthority, error) {
	var authority RARPlanAuthority
	if err := decodeStrictRARJSON(payload, &authority); err != nil {
		return RARPlanAuthority{}, err
	}
	if err := authority.Validate(); err != nil {
		return RARPlanAuthority{}, err
	}
	canonical, err := canonicalRARPlanAuthorityPayload(authority)
	if err != nil || !bytes.Equal(payload, canonical) {
		return RARPlanAuthority{}, errors.New("RAR plan authority is not canonical")
	}
	return authority, nil
}

func rarPlanAuthorityDigest(authority RARPlanAuthority) (string, error) {
	authority.AuthorityRef = ""
	payload, err := json.Marshal(authority)
	if err != nil {
		return "", err
	}
	hash := sha256.New()
	_, _ = hash.Write([]byte(rarPlanAuthorityDigestDomain))
	_, _ = hash.Write([]byte{0})
	_, _ = hash.Write(payload)
	return "sha256:" + hex.EncodeToString(hash.Sum(nil)), nil
}
