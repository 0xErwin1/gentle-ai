package workprovider

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
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
	padDeliveryClaimSchema            = "gentle-ai.pad-delivery-claim/v1"
	padDeliveryIndeterminateSchema    = "gentle-ai.pad-delivery-indeterminate/v1"
	padDeliveryTerminalEvidenceSchema = "gentle-ai.pad-delivery-terminal-evidence/v1"
	padDeliveryTerminalBindingSchema  = "gentle-ai.pad-delivery-terminal-binding/v1"
	padDeliveryStoreVersion           = "v1"
	padDeliveryLockTimeout            = 2 * time.Second
	padDeliveryLockPoll               = 10 * time.Millisecond
)

type padDeliveryTerminalEvidenceKind string

const (
	padDeliveryTerminalEvidenceBranchCAS padDeliveryTerminalEvidenceKind = "branch_cas_receipt"
	padDeliveryTerminalEvidencePRMerge   padDeliveryTerminalEvidenceKind = "pull_request_merge_receipt"
)

// padDeliveryTerminalEvidence is the complete provider observation from which
// the public ExecutionResult is derived. EvidenceRef is the SHA-256 of the
// exact canonical bytes of this object; it is therefore both immutable and
// resolvable rather than a bare, self-asserted hash.
type padDeliveryTerminalEvidence struct {
	Schema                  string                          `json:"schema"`
	CommandRef              string                          `json:"command_ref"`
	RepositoryRef           string                          `json:"repository_ref"`
	ClaimRef                string                          `json:"claim_ref"`
	Kind                    padDeliveryTerminalEvidenceKind `json:"kind"`
	CompletedAt             int64                           `json:"completed_at"`
	BranchCASReceipt        *HostingBranchCASReceipt        `json:"branch_cas_receipt,omitempty"`
	PullRequestMergeReceipt *HostingPullRequestMergeReceipt `json:"pull_request_merge_receipt,omitempty"`
}

func (evidence padDeliveryTerminalEvidence) executionResult(
	command deliveryadmission.ExecutionCommand,
	commandRef string,
	claim padDeliveryClaim,
	claimRef string,
	evidenceRef string,
) (deliveryadmission.ExecutionResult, error) {
	if evidence.Schema != padDeliveryTerminalEvidenceSchema ||
		evidence.CommandRef != commandRef ||
		evidence.RepositoryRef != command.Destination.RepositoryRef ||
		evidence.ClaimRef != claimRef ||
		evidence.CompletedAt < claim.ClaimedAt ||
		!validPADImmutableRef(evidenceRef) {
		return deliveryadmission.ExecutionResult{}, errors.New(
			"PAD delivery terminal evidence does not bind the exact command",
		)
	}
	if err := claim.Validate(command, commandRef); err != nil {
		return deliveryadmission.ExecutionResult{}, err
	}

	outcome := deliveryadmission.ExecutionFailed
	deliveryRef := ""
	switch evidence.Kind {
	case padDeliveryTerminalEvidenceBranchCAS:
		if evidence.BranchCASReceipt == nil ||
			evidence.PullRequestMergeReceipt != nil ||
			command.Mechanism != deliveryadmission.MechanismFastForwardOnly {
			return deliveryadmission.ExecutionResult{}, errors.New(
				"invalid PAD branch CAS terminal evidence",
			)
		}
		request := HostingBranchCASRequest{
			CommandRef:           commandRef,
			RepositoryRef:        command.Destination.RepositoryRef,
			HostingRepositoryRef: claim.HostingRepositoryRef,
			TargetRef:            command.Destination.TargetRef,
			ExpectedRevision:     claim.ExpectedRevision,
			CandidateRevision:    claim.CandidateRevision,
			ExecutionExpiresAt:   command.ExecutionExpiresAt,
		}
		if err := evidence.BranchCASReceipt.Validate(request); err != nil {
			return deliveryadmission.ExecutionResult{}, err
		}
		switch evidence.BranchCASReceipt.Outcome {
		case HostingEffectApplied, HostingEffectAlreadyApplied:
			outcome = deliveryadmission.ExecutionSucceeded
			deliveryRef = evidence.BranchCASReceipt.DeliveryRef
		case HostingEffectConflict, HostingEffectRejected:
		default:
			return deliveryadmission.ExecutionResult{}, errors.New(
				"invalid PAD branch CAS terminal outcome",
			)
		}
	case padDeliveryTerminalEvidencePRMerge:
		if evidence.PullRequestMergeReceipt == nil ||
			evidence.BranchCASReceipt != nil ||
			command.Mechanism != deliveryadmission.MechanismPullRequest {
			return deliveryadmission.ExecutionResult{}, errors.New(
				"invalid PAD pull-request terminal evidence",
			)
		}
		request := HostingPullRequestMergeRequest{
			CommandRef:           commandRef,
			RepositoryRef:        command.Destination.RepositoryRef,
			HostingRepositoryRef: claim.HostingRepositoryRef,
			TargetRef:            command.Destination.TargetRef,
			PullRequestRef:       command.PullRequestRef,
			ExpectedBaseRevision: claim.ExpectedRevision,
			ExpectedHeadRevision: claim.CandidateRevision,
			ExecutionExpiresAt:   command.ExecutionExpiresAt,
		}
		if err := evidence.PullRequestMergeReceipt.Validate(request); err != nil {
			return deliveryadmission.ExecutionResult{}, err
		}
		switch evidence.PullRequestMergeReceipt.Outcome {
		case HostingEffectApplied, HostingEffectAlreadyApplied:
			outcome = deliveryadmission.ExecutionSucceeded
			deliveryRef = evidence.PullRequestMergeReceipt.DeliveryRef
		case HostingEffectConflict, HostingEffectRejected:
		default:
			return deliveryadmission.ExecutionResult{}, errors.New(
				"invalid PAD pull-request terminal outcome",
			)
		}
	default:
		return deliveryadmission.ExecutionResult{}, errors.New(
			"unknown PAD delivery terminal evidence kind",
		)
	}

	result := deliveryadmission.ExecutionResult{
		Schema:           deliveryadmission.ExecutionResultContractV1,
		CommandRef:       commandRef,
		AuthorizationRef: command.AuthorizationRef,
		Route:            command.Route,
		Candidate:        command.Candidate,
		Outcome:          outcome,
		DeliveryRef:      deliveryRef,
		EvidenceRef:      evidenceRef,
		CompletedAt:      evidence.CompletedAt,
	}
	return result, result.Validate(command)
}

// padDeliveryTerminalBinding is the single-assignment commit point for one
// command. The referenced claim, evidence, and result are independently
// content-addressed and revalidated on every replay.
type padDeliveryTerminalBinding struct {
	Schema        string `json:"schema"`
	CommandRef    string `json:"command_ref"`
	RepositoryRef string `json:"repository_ref"`
	ClaimRef      string `json:"claim_ref"`
	EvidenceRef   string `json:"evidence_ref"`
	ResultRef     string `json:"result_ref"`
}

func (binding padDeliveryTerminalBinding) Validate(
	command deliveryadmission.ExecutionCommand,
	commandRef string,
) error {
	if binding.Schema != padDeliveryTerminalBindingSchema ||
		binding.CommandRef != commandRef ||
		binding.RepositoryRef != command.Destination.RepositoryRef ||
		!validPADImmutableRef(binding.ClaimRef) ||
		!validPADImmutableRef(binding.EvidenceRef) ||
		!validPADImmutableRef(binding.ResultRef) {
		return errors.New(
			"PAD delivery terminal binding does not bind the exact command",
		)
	}
	return nil
}

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
		filepath.Join(root, "terminal-bindings"),
		filepath.Join(root, "terminal-evidence"),
		filepath.Join(root, "terminal-results"),
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
	bindingPayload, err := readPrivateCoordinationFile(
		store.terminalBindingPath(commandRef),
	)
	if errors.Is(err, fs.ErrNotExist) {
		if _, legacyErr := readPrivateCoordinationFile(
			store.legacyResultPath(commandRef),
		); legacyErr == nil {
			return deliveryadmission.ExecutionResult{}, false,
				padDeliveryTerminalIntegrityError(
					"legacy terminal result has no content-addressed binding",
					nil,
				)
		} else if !errors.Is(legacyErr, fs.ErrNotExist) {
			return deliveryadmission.ExecutionResult{}, false,
				padDeliveryTerminalIntegrityError(
					"inspect legacy terminal result",
					legacyErr,
				)
		}
		return deliveryadmission.ExecutionResult{}, false, nil
	}
	if err != nil {
		return deliveryadmission.ExecutionResult{}, false,
			padDeliveryTerminalIntegrityError("read terminal binding", err)
	}
	var binding padDeliveryTerminalBinding
	if err := decodeStrictCoordinationJSON(bindingPayload, &binding); err != nil {
		return deliveryadmission.ExecutionResult{}, false,
			padDeliveryTerminalIntegrityError("decode terminal binding", err)
	}
	canonicalBinding, err := canonicalCoordinationPayload(binding)
	if err != nil || !bytes.Equal(bindingPayload, canonicalBinding) {
		return deliveryadmission.ExecutionResult{}, false,
			padDeliveryTerminalIntegrityError(
				"terminal binding is not canonical",
				err,
			)
	}
	if err := binding.Validate(command, commandRef); err != nil {
		return deliveryadmission.ExecutionResult{}, false,
			padDeliveryTerminalIntegrityError("validate terminal binding", err)
	}

	claim, exists, err := store.readClaim(command, commandRef)
	if err != nil {
		return deliveryadmission.ExecutionResult{}, false,
			padDeliveryTerminalIntegrityError("resolve terminal claim", err)
	}
	if !exists {
		return deliveryadmission.ExecutionResult{}, false,
			padDeliveryTerminalIntegrityError("terminal claim is missing", nil)
	}
	claimPayload, err := canonicalCoordinationPayload(claim)
	if err != nil {
		return deliveryadmission.ExecutionResult{}, false,
			padDeliveryTerminalIntegrityError("canonicalize terminal claim", err)
	}
	claimRef := padDeliveryContentRef(claimPayload)
	if binding.ClaimRef != claimRef {
		return deliveryadmission.ExecutionResult{}, false,
			padDeliveryTerminalIntegrityError(
				"terminal binding references another claim",
				nil,
			)
	}

	evidencePayload, err := readPrivateCoordinationFile(
		store.terminalEvidencePath(binding.EvidenceRef),
	)
	if err != nil {
		return deliveryadmission.ExecutionResult{}, false,
			padDeliveryTerminalIntegrityError("read terminal evidence object", err)
	}
	var evidence padDeliveryTerminalEvidence
	if err := decodeStrictCoordinationJSON(evidencePayload, &evidence); err != nil {
		return deliveryadmission.ExecutionResult{}, false,
			padDeliveryTerminalIntegrityError("decode terminal evidence object", err)
	}
	canonicalEvidence, err := canonicalCoordinationPayload(evidence)
	if err != nil ||
		!bytes.Equal(evidencePayload, canonicalEvidence) ||
		padDeliveryContentRef(evidencePayload) != binding.EvidenceRef {
		return deliveryadmission.ExecutionResult{}, false,
			padDeliveryTerminalIntegrityError(
				"terminal evidence object does not match its reference",
				err,
			)
	}

	resultPayload, err := readPrivateCoordinationFile(
		store.terminalResultPath(binding.ResultRef),
	)
	if err != nil {
		return deliveryadmission.ExecutionResult{}, false,
			padDeliveryTerminalIntegrityError("read terminal result object", err)
	}
	var result deliveryadmission.ExecutionResult
	if err := decodeStrictCoordinationJSON(resultPayload, &result); err != nil {
		return deliveryadmission.ExecutionResult{}, false,
			padDeliveryTerminalIntegrityError("decode terminal result object", err)
	}
	canonicalResult, err := canonicalCoordinationPayload(result)
	if err != nil || !bytes.Equal(resultPayload, canonicalResult) {
		return deliveryadmission.ExecutionResult{}, false,
			padDeliveryTerminalIntegrityError(
				"terminal result object does not match its reference",
				err,
			)
	}
	resultRef, err := result.Ref(command)
	if err != nil || resultRef != binding.ResultRef {
		return deliveryadmission.ExecutionResult{}, false,
			padDeliveryTerminalIntegrityError(
				"terminal result object does not match its reference",
				err,
			)
	}
	if err := result.Validate(command); err != nil {
		return deliveryadmission.ExecutionResult{}, false,
			padDeliveryTerminalIntegrityError("validate terminal result object", err)
	}
	derived, err := evidence.executionResult(
		command,
		commandRef,
		claim,
		claimRef,
		binding.EvidenceRef,
	)
	if err != nil {
		return deliveryadmission.ExecutionResult{}, false,
			padDeliveryTerminalIntegrityError(
				"derive terminal result from evidence",
				err,
			)
	}
	if result != derived || result.EvidenceRef != binding.EvidenceRef {
		return deliveryadmission.ExecutionResult{}, false,
			padDeliveryTerminalIntegrityError(
				"terminal result does not equal its evidence-derived result",
				nil,
			)
	}
	return result, true, nil
}

func (store *padDeliveryResultStore) publishTerminal(
	command deliveryadmission.ExecutionCommand,
	evidence padDeliveryTerminalEvidence,
) (deliveryadmission.ExecutionResult, error) {
	commandRef, err := command.Ref()
	if err != nil {
		return deliveryadmission.ExecutionResult{}, err
	}
	claim, exists, err := store.readClaim(command, commandRef)
	if err != nil {
		return deliveryadmission.ExecutionResult{}, err
	}
	if !exists {
		return deliveryadmission.ExecutionResult{}, errors.New(
			"PAD delivery terminal publication requires the durable exact claim",
		)
	}
	claimPayload, err := canonicalCoordinationPayload(claim)
	if err != nil {
		return deliveryadmission.ExecutionResult{}, err
	}
	claimRef := padDeliveryContentRef(claimPayload)
	evidencePayload, err := canonicalCoordinationPayload(evidence)
	if err != nil {
		return deliveryadmission.ExecutionResult{}, err
	}
	evidenceRef := padDeliveryContentRef(evidencePayload)
	result, err := evidence.executionResult(
		command,
		commandRef,
		claim,
		claimRef,
		evidenceRef,
	)
	if err != nil {
		return deliveryadmission.ExecutionResult{}, err
	}
	resultPayload, err := canonicalCoordinationPayload(result)
	if err != nil {
		return deliveryadmission.ExecutionResult{}, err
	}
	resultRef, err := result.Ref(command)
	if err != nil {
		return deliveryadmission.ExecutionResult{}, err
	}
	binding := padDeliveryTerminalBinding{
		Schema:        padDeliveryTerminalBindingSchema,
		CommandRef:    commandRef,
		RepositoryRef: command.Destination.RepositoryRef,
		ClaimRef:      claimRef,
		EvidenceRef:   evidenceRef,
		ResultRef:     resultRef,
	}
	if err := binding.Validate(command, commandRef); err != nil {
		return deliveryadmission.ExecutionResult{}, err
	}
	bindingPayload, err := canonicalCoordinationPayload(binding)
	if err != nil {
		return deliveryadmission.ExecutionResult{}, err
	}
	if err := publishPrivateCoordinationImmutable(
		store.terminalEvidencePath(evidenceRef),
		evidencePayload,
	); err != nil {
		return deliveryadmission.ExecutionResult{}, err
	}
	if err := publishPrivateCoordinationImmutable(
		store.terminalResultPath(resultRef),
		resultPayload,
	); err != nil {
		return deliveryadmission.ExecutionResult{}, err
	}
	if err := publishPrivateCoordinationImmutable(
		store.terminalBindingPath(commandRef),
		bindingPayload,
	); err != nil {
		return deliveryadmission.ExecutionResult{}, err
	}
	reloaded, exists, err := store.readTerminal(command, commandRef)
	if err != nil {
		return deliveryadmission.ExecutionResult{}, err
	}
	if !exists || reloaded != result {
		return deliveryadmission.ExecutionResult{}, errors.New(
			"PAD delivery terminal result durability check failed",
		)
	}
	return result, nil
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

func (store *padDeliveryResultStore) legacyResultPath(ref string) string {
	return filepath.Join(store.root, "results", padDeliveryRefFilename(ref))
}

func (store *padDeliveryResultStore) terminalBindingPath(ref string) string {
	return filepath.Join(store.root, "terminal-bindings", padDeliveryRefFilename(ref))
}

func (store *padDeliveryResultStore) terminalEvidencePath(ref string) string {
	return filepath.Join(store.root, "terminal-evidence", padDeliveryRefFilename(ref))
}

func (store *padDeliveryResultStore) terminalResultPath(ref string) string {
	return filepath.Join(store.root, "terminal-results", padDeliveryRefFilename(ref))
}

func padDeliveryRefFilename(ref string) string {
	return strings.TrimPrefix(ref, "sha256:") + ".json"
}

func padDeliveryContentRef(payload []byte) string {
	digest := sha256.Sum256(payload)
	return "sha256:" + hex.EncodeToString(digest[:])
}

func padDeliveryCanonicalValueRef(value any) (string, error) {
	payload, err := canonicalCoordinationPayload(value)
	if err != nil {
		return "", err
	}
	// deliveryadmission content refs hash canonical JSON, while coordination
	// files add one transport newline after those canonical bytes.
	return padDeliveryContentRef(bytes.TrimSuffix(payload, []byte{'\n'})), nil
}

func padDeliveryTerminalIntegrityError(message string, err error) error {
	if err == nil {
		return fmt.Errorf("%w: %s", ErrPADDeliveryResultCorrupt, message)
	}
	return fmt.Errorf("%w: %s: %v", ErrPADDeliveryResultCorrupt, message, err)
}
