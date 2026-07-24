package workprovider

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/gentleman-programming/gentle-ai/internal/deliveryadmission"
	"github.com/gentleman-programming/gentle-ai/internal/reviewtransaction"
)

const (
	padDeliveryClaimSchema         = "gentle-ai.pad-delivery-claim/v1"
	padDeliveryIndeterminateSchema = "gentle-ai.pad-delivery-indeterminate/v1"
	padDeliveryStoreVersion        = "v1"
	padDeliveryLockTimeout         = 2 * time.Second
	padDeliveryLockPoll            = 10 * time.Millisecond
)

type padDeliveryIndeterminate struct {
	Schema     string `json:"schema"`
	CommandRef string `json:"command_ref"`
	Reason     string `json:"reason"`
}

func (record padDeliveryIndeterminate) Validate(commandRef string) error {
	if record.Schema != padDeliveryIndeterminateSchema ||
		record.CommandRef != commandRef ||
		record.Reason != "claimed_effect_outcome_unknown" {
		return errors.New("invalid PAD delivery indeterminate record")
	}
	return nil
}

type padDeliveryClaim struct {
	Schema                 string                  `json:"schema"`
	CommandRef             string                  `json:"command_ref"`
	RepositoryRef          string                  `json:"repository_ref"`
	HostingRepositoryRef   string                  `json:"hosting_repository_ref"`
	Route                  deliveryadmission.Route `json:"route"`
	PullRequestRef         string                  `json:"pull_request_ref,omitempty"`
	CandidateRef           string                  `json:"candidate_ref"`
	ExpectedRemoteIdentity string                  `json:"expected_remote_identity"`
	CandidateRevision      string                  `json:"candidate_revision"`
	ExpectedRevision       string                  `json:"expected_revision"`
	ExecutionExpiresAt     int64                   `json:"execution_expires_at"`
	ClaimedAt              int64                   `json:"claimed_at"`
}

func newPADDeliveryClaim(
	command deliveryadmission.ExecutionCommand,
	commandRef string,
	hostingRepositoryRef string,
	candidateRevision string,
	expectedRevision string,
	claimedAt int64,
) (padDeliveryClaim, error) {
	claim := padDeliveryClaim{
		Schema:                 padDeliveryClaimSchema,
		CommandRef:             commandRef,
		RepositoryRef:          command.Destination.RepositoryRef,
		HostingRepositoryRef:   hostingRepositoryRef,
		Route:                  command.Route,
		PullRequestRef:         command.PullRequestRef,
		CandidateRef:           command.Candidate.Ref,
		ExpectedRemoteIdentity: command.ExpectedRemoteRevision,
		CandidateRevision:      candidateRevision,
		ExpectedRevision:       expectedRevision,
		ExecutionExpiresAt:     command.ExecutionExpiresAt,
		ClaimedAt:              claimedAt,
	}
	return claim, claim.Validate(command, commandRef)
}

func (claim padDeliveryClaim) Validate(
	command deliveryadmission.ExecutionCommand,
	commandRef string,
) error {
	if claim.Schema != padDeliveryClaimSchema ||
		claim.CommandRef != commandRef ||
		claim.RepositoryRef != command.Destination.RepositoryRef ||
		!validPADHostingToken(claim.HostingRepositoryRef) ||
		claim.Route != command.Route ||
		claim.PullRequestRef != command.PullRequestRef ||
		claim.CandidateRef != command.Candidate.Ref ||
		claim.ExpectedRemoteIdentity != command.ExpectedRemoteRevision ||
		!validPADGitRevision(claim.CandidateRevision) ||
		!validPADGitRevision(claim.ExpectedRevision) ||
		claim.ExecutionExpiresAt != command.ExecutionExpiresAt ||
		claim.ClaimedAt <= 0 ||
		claim.ClaimedAt >= claim.ExecutionExpiresAt {
		return errors.New("PAD delivery claim does not bind the exact command")
	}
	return nil
}

type padDeliveryResultStore struct {
	authority *PADRepositoryAuthority
	root      string
}

func openPADDeliveryResultStore(
	ctx context.Context,
	authority *PADRepositoryAuthority,
) (*padDeliveryResultStore, error) {
	if authority == nil || authority.identity.gitCommonDir == "" {
		return nil, errors.New("PAD delivery result store requires repository authority")
	}
	if _, err := authority.ResolveDeliveryRepository(ctx, authority.RepositoryRef()); err != nil {
		return nil, err
	}
	root := filepath.Join(
		authority.identity.gitCommonDir,
		"gentle-ai",
		"work-provider",
		"pad-delivery",
		padDeliveryStoreVersion,
		"repositories",
		authority.identity.lease.StorageKey(),
	)
	if err := ensurePADDeliveryStoreDirectories(authority.identity.gitCommonDir, root); err != nil {
		return nil, fmt.Errorf("create PAD delivery result store: %w", err)
	}
	return &padDeliveryResultStore{authority: authority, root: root}, nil
}

func existingPADDeliveryResultStore(
	ctx context.Context,
	authority *PADRepositoryAuthority,
) (*padDeliveryResultStore, error) {
	if authority == nil || authority.identity.gitCommonDir == "" {
		return nil, errors.New(
			"PAD delivery result store requires repository authority",
		)
	}
	if _, err := authority.ResolveDeliveryRepository(
		ctx,
		authority.RepositoryRef(),
	); err != nil {
		return nil, err
	}
	store := &padDeliveryResultStore{
		authority: authority,
		root: filepath.Join(
			authority.identity.gitCommonDir,
			"gentle-ai",
			"work-provider",
			"pad-delivery",
			padDeliveryStoreVersion,
			"repositories",
			authority.identity.lease.StorageKey(),
		),
	}
	if err := store.validate(ctx); err != nil {
		return nil, err
	}
	return store, nil
}

func ensurePADDeliveryStoreDirectories(commonDir string, root string) error {
	if err := validateSharedCoordinationDirectory(commonDir); err != nil {
		return err
	}
	gentleRoot := filepath.Join(commonDir, "gentle-ai")
	if _, err := os.Lstat(gentleRoot); errors.Is(err, fs.ErrNotExist) {
		if err := os.Mkdir(gentleRoot, 0o755); err != nil && !errors.Is(err, fs.ErrExist) {
			return err
		}
		if err := reviewtransaction.SyncReviewDirectory(commonDir); err != nil {
			return err
		}
	} else if err != nil {
		return err
	}
	if err := validateSharedCoordinationDirectory(gentleRoot); err != nil {
		return err
	}
	workProviderRoot := filepath.Join(gentleRoot, "work-provider")
	created, err := createPrivateCoordinationDirectory(workProviderRoot)
	if err != nil {
		return err
	}
	if created {
		if err := reviewtransaction.SyncReviewDirectory(gentleRoot); err != nil {
			return err
		}
	}
	for _, directory := range []string{
		root,
		filepath.Join(root, "claims"),
		filepath.Join(root, "indeterminate"),
		filepath.Join(root, "locks"),
		filepath.Join(root, "results"),
	} {
		if err := ensurePrivateCoordinationDirectoryTree(
			workProviderRoot,
			directory,
			true,
		); err != nil {
			return err
		}
	}
	return nil
}

func (store *padDeliveryResultStore) readIndeterminate(
	commandRef string,
) (bool, error) {
	payload, err := readPrivateCoordinationFile(store.indeterminatePath(commandRef))
	if errors.Is(err, fs.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	var record padDeliveryIndeterminate
	if err := decodeStrictCoordinationJSON(payload, &record); err != nil {
		return false, err
	}
	canonical, err := canonicalCoordinationPayload(record)
	if err != nil || !bytes.Equal(payload, canonical) ||
		record.Validate(commandRef) != nil {
		return false, errors.New("PAD delivery indeterminate record is corrupt")
	}
	return true, nil
}

func (store *padDeliveryResultStore) publishIndeterminate(commandRef string) error {
	record := padDeliveryIndeterminate{
		Schema:     padDeliveryIndeterminateSchema,
		CommandRef: commandRef,
		Reason:     "claimed_effect_outcome_unknown",
	}
	payload, err := canonicalCoordinationPayload(record)
	if err != nil {
		return err
	}
	return publishPrivateCoordinationImmutable(store.indeterminatePath(commandRef), payload)
}

func (store *padDeliveryResultStore) withCommandLock(
	ctx context.Context,
	commandRef string,
	fn func() error,
) error {
	if store == nil || fn == nil || !validPADImmutableRef(commandRef) {
		return errors.New("invalid PAD delivery command lock")
	}
	if err := store.validate(ctx); err != nil {
		return err
	}
	timeout := time.NewTimer(padDeliveryLockTimeout)
	defer timeout.Stop()
	ticker := time.NewTicker(padDeliveryLockPoll)
	defer ticker.Stop()
	for {
		lock, err := reviewtransaction.AcquireAuthorityFileLock(store.lockPath(commandRef))
		if err == nil {
			defer lock.Release()
			if err := store.validate(ctx); err != nil {
				return err
			}
			return fn()
		}
		if !errors.Is(err, reviewtransaction.ErrConcurrentUpdate) {
			return err
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-timeout.C:
			return fmt.Errorf("PAD delivery command lock timed out: %w", err)
		case <-ticker.C:
		}
	}
}

func (store *padDeliveryResultStore) validate(ctx context.Context) error {
	if store == nil || store.authority == nil {
		return errors.New("PAD delivery result store is unavailable")
	}
	root, err := store.authority.ResolveDeliveryRepository(
		ctx,
		store.authority.RepositoryRef(),
	)
	if err != nil {
		return err
	}
	if root != store.authority.identity.repositoryRoot {
		return ErrPADRepositoryAuthorityMismatch
	}
	return validatePrivateCoordinationDirectory(store.root)
}

func (store *padDeliveryResultStore) readClaim(
	command deliveryadmission.ExecutionCommand,
	commandRef string,
) (padDeliveryClaim, bool, error) {
	payload, err := readPrivateCoordinationFile(store.claimPath(commandRef))
	if errors.Is(err, fs.ErrNotExist) {
		return padDeliveryClaim{}, false, nil
	}
	if err != nil {
		return padDeliveryClaim{}, false, err
	}
	var claim padDeliveryClaim
	if err := decodeStrictCoordinationJSON(payload, &claim); err != nil {
		return padDeliveryClaim{}, false, err
	}
	canonical, err := canonicalCoordinationPayload(claim)
	if err != nil || !bytes.Equal(payload, canonical) {
		return padDeliveryClaim{}, false, errors.New("PAD delivery claim is not canonical")
	}
	if err := claim.Validate(command, commandRef); err != nil {
		return padDeliveryClaim{}, false, err
	}
	return claim, true, nil
}

func (store *padDeliveryResultStore) publishClaim(claim padDeliveryClaim) error {
	payload, err := canonicalCoordinationPayload(claim)
	if err != nil {
		return err
	}
	return publishPrivateCoordinationImmutable(store.claimPath(claim.CommandRef), payload)
}

func (store *padDeliveryResultStore) readTerminal(
	command deliveryadmission.ExecutionCommand,
	commandRef string,
) (deliveryadmission.ExecutionResult, bool, error) {
	payload, err := readPrivateCoordinationFile(store.resultPath(commandRef))
	if errors.Is(err, fs.ErrNotExist) {
		return deliveryadmission.ExecutionResult{}, false, nil
	}
	if err != nil {
		return deliveryadmission.ExecutionResult{}, false, err
	}
	var result deliveryadmission.ExecutionResult
	if err := decodeStrictCoordinationJSON(payload, &result); err != nil {
		return deliveryadmission.ExecutionResult{}, false, err
	}
	canonical, err := canonicalCoordinationPayload(result)
	if err != nil || !bytes.Equal(payload, canonical) {
		return deliveryadmission.ExecutionResult{}, false, errors.New(
			"PAD delivery terminal result is not canonical",
		)
	}
	if err := result.Validate(command); err != nil {
		return deliveryadmission.ExecutionResult{}, false, err
	}
	return result, true, nil
}

func (store *padDeliveryResultStore) publishTerminal(
	command deliveryadmission.ExecutionCommand,
	result deliveryadmission.ExecutionResult,
) error {
	commandRef, err := command.Ref()
	if err != nil {
		return err
	}
	if err := result.Validate(command); err != nil {
		return err
	}
	payload, err := canonicalCoordinationPayload(result)
	if err != nil {
		return err
	}
	if err := publishPrivateCoordinationImmutable(store.resultPath(commandRef), payload); err != nil {
		return err
	}
	reloaded, exists, err := store.readTerminal(command, commandRef)
	if err != nil {
		return err
	}
	if !exists || reloaded != result {
		return errors.New("PAD delivery terminal result durability check failed")
	}
	return nil
}

func (store *padDeliveryResultStore) claimPath(ref string) string {
	return filepath.Join(store.root, "claims", padDeliveryRefFilename(ref))
}

func (store *padDeliveryResultStore) lockPath(ref string) string {
	return filepath.Join(store.root, "locks", padDeliveryRefFilename(ref)+".lock")
}

func (store *padDeliveryResultStore) indeterminatePath(ref string) string {
	return filepath.Join(store.root, "indeterminate", padDeliveryRefFilename(ref))
}

func (store *padDeliveryResultStore) resultPath(ref string) string {
	return filepath.Join(store.root, "results", padDeliveryRefFilename(ref))
}

func padDeliveryRefFilename(ref string) string {
	return strings.TrimPrefix(ref, "sha256:") + ".json"
}
