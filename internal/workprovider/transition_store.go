package workprovider

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"path/filepath"
	"strings"

	"github.com/gentleman-programming/gentle-ai/internal/reviewtransaction"
	"github.com/gentleman-programming/gentle-ai/internal/workrun"
)

const (
	transitionIndexSchema         = "gentle-ai.verification-transition-index/v1"
	transitionClaimSchema         = "gentle-ai.verification-transition-claim/v1"
	transitioncompletionSchema    = "gentle-ai.verification-transition-completion/v1"
	transitionIndeterminateSchema = "gentle-ai.verification-transition-indeterminate/v1"
)

var (
	ErrTransitionAuthorityConflict = errors.New(
		"verification transition authority conflicts with an existing revision slot",
	)
	ErrTransitionAuthorityCorrupt = errors.New(
		"verification transition authority store is corrupt",
	)
	ErrTransitionIndeterminate = errors.New(
		"verification transition launch outcome is indeterminate",
	)
)

// TransitionPhase is durable provider state, not a public workflow state.
// Pending may execute once; claimed is recoverable only after inspecting the
// WorkRun launch claim and EPD; completed replays one exact public receipt.
type TransitionPhase string

const (
	TransitionPending       TransitionPhase = "pending"
	TransitionClaimed       TransitionPhase = "claimed"
	TransitionCompleted     TransitionPhase = "completed"
	TransitionIndeterminate TransitionPhase = "indeterminate"
)

type transitionIndex struct {
	Schema             string `json:"schema"`
	RepositoryIdentity string `json:"repository_identity"`
	WorkRunID          string `json:"work_run_id"`
	ExpectedRevision   string `json:"expected_revision"`
	AuthorizationRef   string `json:"authorization_ref"`
}

func (index transitionIndex) Validate() error {
	if index.Schema != transitionIndexSchema ||
		!sha256AuthorityRefPattern.MatchString(index.RepositoryIdentity) ||
		!workRunIDPattern.MatchString(index.WorkRunID) ||
		!sha256AuthorityRefPattern.MatchString(index.ExpectedRevision) ||
		!sha256AuthorityRefPattern.MatchString(index.AuthorizationRef) {
		return errors.New("verification transition index is invalid")
	}
	return nil
}

type transitionClaim struct {
	Schema           string `json:"schema"`
	AuthorizationRef string `json:"authorization_ref"`
	WorkRunID        string `json:"work_run_id"`
	ExpectedRevision string `json:"expected_revision"`
	ClaimedAt        int64  `json:"claimed_at"`
}

func (claim transitionClaim) Validate(
	authorization VerificationTransitionAuthorization,
) error {
	if claim.Schema != transitionClaimSchema ||
		claim.AuthorizationRef != authorization.AuthorizationRef ||
		claim.WorkRunID != authorization.WorkRunID ||
		claim.ExpectedRevision != authorization.ExpectedRevision ||
		claim.ClaimedAt <= 0 {
		return errors.New("verification transition claim is invalid")
	}
	return nil
}

type transitioncompletion struct {
	Schema           string                   `json:"schema"`
	AuthorizationRef string                   `json:"authorization_ref"`
	EvidenceRef      string                   `json:"evidence_ref"`
	Transition       workrun.WorkTransitionV1 `json:"transition"`
}

func (completion transitioncompletion) Validate(
	authorization VerificationTransitionAuthorization,
) error {
	if completion.Schema != transitioncompletionSchema ||
		completion.AuthorizationRef != authorization.AuthorizationRef ||
		!sha256AuthorityRefPattern.MatchString(completion.EvidenceRef) {
		return errors.New("verification transition completion is invalid")
	}
	if err := completion.Transition.Validate(); err != nil {
		return err
	}
	if completion.Transition.WorkRunID != authorization.WorkRunID ||
		completion.Transition.PreviousRevision != authorization.ExpectedRevision ||
		completion.Transition.AppliedAuthorizationRef != authorization.AuthorizationRef {
		return errors.New("verification transition completion does not bind its authorization")
	}
	return nil
}

type transitionIndeterminate struct {
	Schema           string `json:"schema"`
	AuthorizationRef string `json:"authorization_ref"`
	WorkRunID        string `json:"work_run_id"`
	Reason           string `json:"reason"`
}

func (record transitionIndeterminate) Validate(
	authorization VerificationTransitionAuthorization,
) error {
	if record.Schema != transitionIndeterminateSchema ||
		record.AuthorizationRef != authorization.AuthorizationRef ||
		record.WorkRunID != authorization.WorkRunID ||
		record.Reason != "launch_claim_without_admitted_evidence" {
		return errors.New("verification transition indeterminate record is invalid")
	}
	return nil
}

// TransitionAuthorityStore is bound to one exact Git identity and WorkRun.
// Reads never create storage. Publications are immutable; the only mutable
// lifecycle is represented by monotonic marker files under a kernel lock.
type TransitionAuthorityStore struct {
	identity  coordinationRepositoryIdentity
	root      string
	workRunID string
}

func OpenTransitionAuthorityStore(
	ctx context.Context,
	repo string,
	workRunID string,
) (*TransitionAuthorityStore, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if !workRunIDPattern.MatchString(workRunID) {
		return nil, errors.New("verification transition work run identifier is invalid")
	}
	identity, err := resolveCoordinationRepositoryIdentity(ctx, repo)
	if err != nil {
		return nil, fmt.Errorf("resolve transition repository identity: %w", err)
	}
	root := filepath.Join(
		identity.gitCommonDir,
		"gentle-ai",
		"work-provider",
		"coordination-authority",
		coordinationRootVersion,
	)
	return &TransitionAuthorityStore{
		identity: identity, root: root, workRunID: workRunID,
	}, nil
}

// PublishAuthorization publishes one owner-issued authorization for exactly
// one WorkRun revision. A different authorization for that slot conflicts.
func (store *TransitionAuthorityStore) PublishAuthorization(
	ctx context.Context,
	authorization VerificationTransitionAuthorization,
) error {
	if err := store.validateContext(ctx); err != nil {
		return err
	}
	if err := store.validateAuthorization(authorization); err != nil {
		return err
	}
	if err := store.ensureWriteDirectories(authorization.AuthorizationRef); err != nil {
		return err
	}
	objectPayload, err := canonicalCoordinationPayload(authorization)
	if err != nil {
		return err
	}
	if err := publishPrivateCoordinationImmutable(
		store.objectPath(authorization.AuthorizationRef),
		objectPayload,
	); err != nil {
		return err
	}
	index := transitionIndex{
		Schema:             transitionIndexSchema,
		RepositoryIdentity: store.identity.repositoryIdentity,
		WorkRunID:          store.workRunID,
		ExpectedRevision:   authorization.ExpectedRevision,
		AuthorizationRef:   authorization.AuthorizationRef,
	}
	indexPayload, err := canonicalCoordinationPayload(index)
	if err != nil {
		return err
	}
	if err := publishPrivateCoordinationImmutable(
		store.slotPath(authorization.ExpectedRevision),
		indexPayload,
	); err != nil {
		if errors.Is(err, ErrCoordinationAuthorityConflict) {
			return ErrTransitionAuthorityConflict
		}
		return err
	}
	return nil
}

// CurrentAuthorization returns only an already-issued, live and unclaimed
// authorization for the exact current revision. Polling cannot mint authority.
func (store *TransitionAuthorityStore) CurrentAuthorization(
	ctx context.Context,
	revision string,
	now int64,
) (*VerificationTransitionAuthorization, error) {
	if err := store.validateContext(ctx); err != nil {
		return nil, err
	}
	if !sha256AuthorityRefPattern.MatchString(revision) {
		return nil, errors.New("verification transition revision is invalid")
	}
	index, err := store.readIndex(revision)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	authorization, err := store.readAuthorization(index.AuthorizationRef)
	if err != nil {
		return nil, err
	}
	if authorization.ExpectedRevision != revision {
		return nil, ErrTransitionAuthorityCorrupt
	}
	phase, _, err := store.state(authorization)
	if err != nil {
		return nil, err
	}
	if phase != TransitionPending ||
		now < authorization.IssuedAt ||
		now >= authorization.ExpiresAt {
		return nil, nil
	}
	copy := authorization
	return &copy, nil
}

// ResolveAuthorization resolves pending authority only while live. Claimed and
// completed records remain resolvable after expiry solely for safe recovery or
// byte-identical replay. Indeterminate launches never resolve as executable.
func (store *TransitionAuthorityStore) ResolveAuthorization(
	ctx context.Context,
	ref string,
	now int64,
) (VerificationTransitionAuthorization, TransitionPhase, error) {
	if err := store.validateContext(ctx); err != nil {
		return VerificationTransitionAuthorization{}, "", err
	}
	authorization, err := store.readAuthorization(ref)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return VerificationTransitionAuthorization{}, "", ErrAuthorizationNotFound
		}
		return VerificationTransitionAuthorization{}, "", err
	}
	phase, _, err := store.state(authorization)
	if err != nil {
		return VerificationTransitionAuthorization{}, "", err
	}
	switch phase {
	case TransitionPending:
		if now < authorization.IssuedAt ||
			now >= authorization.ExpiresAt {
			return VerificationTransitionAuthorization{}, "", ErrAuthorizationStale
		}
	case TransitionIndeterminate:
		return VerificationTransitionAuthorization{}, phase, ErrTransitionIndeterminate
	}
	return authorization, phase, nil
}

// withAuthorizationLock serializes the complete launch/recovery transaction
// across processes. The callback runs only after repository identity and the
// immutable authorization have been revalidated under the lease.
func (store *TransitionAuthorityStore) withAuthorizationLock(
	ctx context.Context,
	ref string,
	fn func() error,
) error {
	if fn == nil {
		return errors.New("verification transition lock callback is required")
	}
	if err := store.validateContext(ctx); err != nil {
		return err
	}
	authorization, err := store.readAuthorization(ref)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return ErrAuthorizationNotFound
		}
		return err
	}
	if err := store.ensureWriteDirectories(authorization.AuthorizationRef); err != nil {
		return err
	}
	lock, err := reviewtransaction.AcquireAuthorityFileLock(store.lockPath(ref))
	if err != nil {
		return err
	}
	defer lock.Release()
	if err := store.validateContext(ctx); err != nil {
		return err
	}
	reloaded, err := store.readAuthorization(ref)
	if err != nil || reloaded != authorization {
		if err != nil {
			return err
		}
		return ErrTransitionAuthorityCorrupt
	}
	return fn()
}

func (store *TransitionAuthorityStore) markClaimed(
	authorization VerificationTransitionAuthorization,
	claimedAt int64,
) error {
	if err := store.validateAuthorization(authorization); err != nil {
		return err
	}
	phase, _, err := store.state(authorization)
	if err != nil {
		return err
	}
	if phase == TransitionCompleted || phase == TransitionIndeterminate {
		return fmt.Errorf("%w: cannot claim %s transition", ErrTransitionAuthorityConflict, phase)
	}
	if phase == TransitionClaimed {
		return nil
	}
	claim := transitionClaim{
		Schema:           transitionClaimSchema,
		AuthorizationRef: authorization.AuthorizationRef,
		WorkRunID:        authorization.WorkRunID,
		ExpectedRevision: authorization.ExpectedRevision,
		ClaimedAt:        claimedAt,
	}
	if err := claim.Validate(authorization); err != nil {
		return err
	}
	payload, err := canonicalCoordinationPayload(claim)
	if err != nil {
		return err
	}
	return publishPrivateCoordinationImmutable(store.claimPath(authorization.AuthorizationRef), payload)
}

func (store *TransitionAuthorityStore) markCompleted(
	authorization VerificationTransitionAuthorization,
	evidenceRef string,
	transition workrun.WorkTransitionV1,
) error {
	if err := store.validateAuthorization(authorization); err != nil {
		return err
	}
	phase, _, err := store.state(authorization)
	if err != nil {
		return err
	}
	if phase == TransitionPending {
		return errors.New("verification transition must be claimed before completion")
	}
	if phase == TransitionIndeterminate {
		return ErrTransitionIndeterminate
	}
	completion := transitioncompletion{
		Schema:           transitioncompletionSchema,
		AuthorizationRef: authorization.AuthorizationRef,
		EvidenceRef:      evidenceRef,
		Transition:       transition,
	}
	if err := completion.Validate(authorization); err != nil {
		return err
	}
	payload, err := canonicalCoordinationPayload(completion)
	if err != nil {
		return err
	}
	return publishPrivateCoordinationImmutable(
		store.completionPath(authorization.AuthorizationRef),
		payload,
	)
}

func (store *TransitionAuthorityStore) markIndeterminate(
	authorization VerificationTransitionAuthorization,
) error {
	if err := store.validateAuthorization(authorization); err != nil {
		return err
	}
	phase, _, err := store.state(authorization)
	if err != nil {
		return err
	}
	if phase == TransitionCompleted {
		return ErrTransitionAuthorityConflict
	}
	if phase == TransitionPending {
		return errors.New("verification transition must be claimed before indeterminate")
	}
	record := transitionIndeterminate{
		Schema:           transitionIndeterminateSchema,
		AuthorizationRef: authorization.AuthorizationRef,
		WorkRunID:        authorization.WorkRunID,
		Reason:           "launch_claim_without_admitted_evidence",
	}
	if err := record.Validate(authorization); err != nil {
		return err
	}
	payload, err := canonicalCoordinationPayload(record)
	if err != nil {
		return err
	}
	return publishPrivateCoordinationImmutable(
		store.indeterminatePath(authorization.AuthorizationRef),
		payload,
	)
}

func (store *TransitionAuthorityStore) completion(
	authorization VerificationTransitionAuthorization,
) (*workrun.WorkTransitionV1, string, error) {
	phase, completion, err := store.state(authorization)
	if err != nil {
		return nil, "", err
	}
	if phase != TransitionCompleted || completion == nil {
		return nil, "", nil
	}
	transition := completion.Transition
	return &transition, completion.EvidenceRef, nil
}

func (store *TransitionAuthorityStore) state(
	authorization VerificationTransitionAuthorization,
) (TransitionPhase, *transitioncompletion, error) {
	claimExists := false
	if payload, err := readPrivateCoordinationFile(
		store.claimPath(authorization.AuthorizationRef),
	); err == nil {
		var claim transitionClaim
		if decodeStrictCoordinationJSON(payload, &claim) != nil ||
			claim.Validate(authorization) != nil {
			return "", nil, ErrTransitionAuthorityCorrupt
		}
		claimExists = true
	} else if !errors.Is(err, fs.ErrNotExist) {
		return "", nil, err
	}

	var completion *transitioncompletion
	if payload, err := readPrivateCoordinationFile(
		store.completionPath(authorization.AuthorizationRef),
	); err == nil {
		var record transitioncompletion
		if decodeStrictCoordinationJSON(payload, &record) != nil ||
			record.Validate(authorization) != nil {
			return "", nil, ErrTransitionAuthorityCorrupt
		}
		completion = &record
	} else if !errors.Is(err, fs.ErrNotExist) {
		return "", nil, err
	}

	indeterminate := false
	if payload, err := readPrivateCoordinationFile(
		store.indeterminatePath(authorization.AuthorizationRef),
	); err == nil {
		var record transitionIndeterminate
		if decodeStrictCoordinationJSON(payload, &record) != nil ||
			record.Validate(authorization) != nil {
			return "", nil, ErrTransitionAuthorityCorrupt
		}
		indeterminate = true
	} else if !errors.Is(err, fs.ErrNotExist) {
		return "", nil, err
	}

	if completion != nil && indeterminate ||
		(completion != nil || indeterminate) && !claimExists {
		return "", nil, ErrTransitionAuthorityCorrupt
	}
	if completion != nil {
		return TransitionCompleted, completion, nil
	}
	if indeterminate {
		return TransitionIndeterminate, nil, nil
	}
	if claimExists {
		return TransitionClaimed, nil, nil
	}
	return TransitionPending, nil, nil
}

func (store *TransitionAuthorityStore) readIndex(
	revision string,
) (transitionIndex, error) {
	payload, err := readPrivateCoordinationFile(store.slotPath(revision))
	if err != nil {
		return transitionIndex{}, err
	}
	var index transitionIndex
	if decodeStrictCoordinationJSON(payload, &index) != nil ||
		index.Validate() != nil ||
		index.RepositoryIdentity != store.identity.repositoryIdentity ||
		index.WorkRunID != store.workRunID ||
		index.ExpectedRevision != revision {
		return transitionIndex{}, ErrTransitionAuthorityCorrupt
	}
	return index, nil
}

func (store *TransitionAuthorityStore) readAuthorization(
	ref string,
) (VerificationTransitionAuthorization, error) {
	if !sha256AuthorityRefPattern.MatchString(ref) {
		return VerificationTransitionAuthorization{}, ErrAuthorizationNotFound
	}
	if err := ensureCoordinationRepositoryRoot(
		store.identity.gitCommonDir,
		store.root,
		false,
	); err != nil {
		return VerificationTransitionAuthorization{}, err
	}
	payload, err := readPrivateCoordinationFile(store.objectPath(ref))
	if err != nil {
		return VerificationTransitionAuthorization{}, err
	}
	var authorization VerificationTransitionAuthorization
	if decodeStrictCoordinationJSON(payload, &authorization) != nil ||
		authorization.Validate() != nil ||
		authorization.AuthorizationRef != ref ||
		authorization.WorkRunID != store.workRunID {
		return VerificationTransitionAuthorization{}, ErrTransitionAuthorityCorrupt
	}
	index, err := store.readIndex(authorization.ExpectedRevision)
	if err != nil || index.AuthorizationRef != ref {
		if err != nil {
			return VerificationTransitionAuthorization{}, err
		}
		return VerificationTransitionAuthorization{}, ErrTransitionAuthorityCorrupt
	}
	return authorization, nil
}

func (store *TransitionAuthorityStore) validateAuthorization(
	authorization VerificationTransitionAuthorization,
) error {
	if err := authorization.Validate(); err != nil {
		return err
	}
	if authorization.WorkRunID != store.workRunID {
		return ErrAuthorizationMismatch
	}
	return nil
}

func (store *TransitionAuthorityStore) validateContext(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if store == nil || store.root == "" || store.workRunID == "" {
		return errors.New("verification transition authority store is not initialized")
	}
	live, err := resolveCoordinationRepositoryIdentity(ctx, store.identity.repositoryRoot)
	if err != nil {
		return err
	}
	if live.repositoryIdentity != store.identity.repositoryIdentity ||
		!sameCoordinationDirectory(live.repositoryRoot, store.identity.repositoryRoot) ||
		!sameCoordinationDirectory(live.gitCommonDir, store.identity.gitCommonDir) ||
		!sameCoordinationDirectory(live.gitDir, store.identity.gitDir) {
		return ErrAuthorizationStale
	}
	wantRoot := filepath.Join(
		live.gitCommonDir,
		"gentle-ai",
		"work-provider",
		"coordination-authority",
		coordinationRootVersion,
	)
	if filepath.Clean(store.root) != filepath.Clean(wantRoot) {
		return errors.New("verification transition authority root is not canonical")
	}
	return nil
}

func (store *TransitionAuthorityStore) ensureWriteDirectories(ref string) error {
	if err := ensureCoordinationRepositoryRoot(
		store.identity.gitCommonDir,
		store.root,
		true,
	); err != nil {
		return err
	}
	for _, directory := range []string{
		store.objectsRoot(),
		store.slotsRoot(),
		store.stateRoot(),
		store.stateDirectory(ref),
		store.locksRoot(),
	} {
		if err := ensurePrivateCoordinationDirectoryTree(
			store.root,
			directory,
			true,
		); err != nil {
			return err
		}
	}
	return nil
}

func (store *TransitionAuthorityStore) objectsRoot() string {
	return filepath.Join(store.root, "transitions", "objects")
}

func (store *TransitionAuthorityStore) slotsRoot() string {
	return filepath.Join(
		store.root,
		"transitions",
		"slots",
		transitionWorkRunDirectory(store.workRunID),
	)
}

func (store *TransitionAuthorityStore) stateRoot() string {
	return filepath.Join(store.root, "transitions", "state")
}

func (store *TransitionAuthorityStore) locksRoot() string {
	return filepath.Join(store.root, "transitions", "locks")
}

func (store *TransitionAuthorityStore) objectPath(ref string) string {
	return filepath.Join(
		store.objectsRoot(),
		strings.TrimPrefix(ref, "sha256:")+".json",
	)
}

func (store *TransitionAuthorityStore) slotPath(revision string) string {
	return filepath.Join(
		store.slotsRoot(),
		strings.TrimPrefix(revision, "sha256:")+".json",
	)
}

func (store *TransitionAuthorityStore) stateDirectory(ref string) string {
	return filepath.Join(
		store.stateRoot(),
		strings.TrimPrefix(ref, "sha256:"),
	)
}

func (store *TransitionAuthorityStore) claimPath(ref string) string {
	return filepath.Join(store.stateDirectory(ref), "claimed.json")
}

func (store *TransitionAuthorityStore) completionPath(ref string) string {
	return filepath.Join(store.stateDirectory(ref), "completed.json")
}

func (store *TransitionAuthorityStore) indeterminatePath(ref string) string {
	return filepath.Join(store.stateDirectory(ref), "indeterminate.json")
}

func (store *TransitionAuthorityStore) lockPath(ref string) string {
	return filepath.Join(
		store.locksRoot(),
		strings.TrimPrefix(ref, "sha256:")+".lock",
	)
}

func transitionWorkRunDirectory(workRunID string) string {
	sum := sha256.Sum256([]byte("gentle-ai.transition-work-run/v1\x00" + workRunID))
	return hex.EncodeToString(sum[:])
}
