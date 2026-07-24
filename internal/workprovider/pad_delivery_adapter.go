package workprovider

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/gentleman-programming/gentle-ai/internal/deliveryadmission"
)

const (
	padDeliveryGateTTL      = 30 * time.Second
	padDeliveryOperationTTL = 15 * time.Second
)

var padGitRevisionPattern = regexp.MustCompile(`^git:([0-9a-f]{40}|[0-9a-f]{64})$`)

type PADGitAncestryState string

const (
	PADGitAncestor    PADGitAncestryState = "ancestor"
	PADGitNotAncestor PADGitAncestryState = "not_ancestor"
)

type PADGitAncestryObservation struct {
	RepositoryRef string
	BaseRevision  string
	HeadRevision  string
	State         PADGitAncestryState
	EvidenceRef   string
}

func (observation PADGitAncestryObservation) Validate(
	repositoryRef string,
	baseRevision string,
	headRevision string,
) error {
	if observation.RepositoryRef != repositoryRef ||
		observation.BaseRevision != baseRevision ||
		observation.HeadRevision != headRevision ||
		(observation.State != PADGitAncestor &&
			observation.State != PADGitNotAncestor) ||
		!validPADImmutableRef(observation.EvidenceRef) {
		return errors.New("PAD Git ancestry observation is invalid")
	}
	return nil
}

// PADGitObjectAuthority is the typed owner seam for local Git object and
// ancestry evidence. Its productive implementation must use HCR or an
// equivalently bounded fixed-operation runtime; PAD never accepts argv.
type PADGitObjectAuthority interface {
	RequireCommit(context.Context, string, string) error
	ObserveAncestry(
		context.Context,
		string,
		string,
		string,
	) (PADGitAncestryObservation, error)
}

type padDeliveryResolution struct {
	observation HostingDeliveryObservation
	binding     PADGitBinding
	ancestry    PADGitAncestryObservation
}

// PADDeliveryAdapter composes the productive PAD ports from explicit owner
// authorities. It accepts no argv, remote URL, force mode, inferred HEAD, or
// caller-authored gate state.
type PADDeliveryAdapter struct {
	authority *PADRepositoryAuthority
	bindings  PADGitBindingAuthority
	git       PADGitObjectAuthority
	hosting   HostingAuthority
	store     *padDeliveryResultStore
	storeMu   sync.Mutex
	now       func() time.Time
}

func NewPADDeliveryAdapter(
	ctx context.Context,
	authority *PADRepositoryAuthority,
	bindings PADGitBindingAuthority,
	git PADGitObjectAuthority,
	hosting HostingAuthority,
) (*PADDeliveryAdapter, error) {
	if authority == nil || bindings == nil || git == nil || hosting == nil {
		return nil, errors.New("PAD delivery adapter requires Git and hosting authorities")
	}
	_, err := authority.ResolveDeliveryRepository(ctx, authority.RepositoryRef())
	if err != nil {
		return nil, err
	}
	return &PADDeliveryAdapter{
		authority: authority,
		bindings:  bindings,
		git:       git,
		hosting:   hosting,
		now:       func() time.Time { return time.Now().UTC() },
	}, nil
}

func (adapter *PADDeliveryAdapter) ProbeLiveGate(
	ctx context.Context,
	request deliveryadmission.LiveGateProbeRequest,
) (deliveryadmission.LiveGateProbeResult, error) {
	if err := adapter.validate(ctx); err != nil {
		return deliveryadmission.LiveGateProbeResult{}, err
	}
	if err := request.Validate(); err != nil {
		return deliveryadmission.LiveGateProbeResult{}, err
	}
	if request.Destination.RepositoryRef != adapter.authority.RepositoryRef() {
		return deliveryadmission.LiveGateProbeResult{}, ErrPADRepositoryAuthorityMismatch
	}
	resolution, err := adapter.resolveCurrent(
		ctx,
		request.Candidate,
		request.Destination,
		request.Mechanism,
		"",
	)
	if err != nil {
		return deliveryadmission.LiveGateProbeResult{}, err
	}
	requestRef, err := request.Ref()
	if err != nil {
		return deliveryadmission.LiveGateProbeResult{}, err
	}
	observedAt, err := adapter.nowUnix()
	if err != nil {
		return deliveryadmission.LiveGateProbeResult{}, err
	}
	observation := resolution.observation
	result := deliveryadmission.LiveGateProbeResult{
		Schema:                 deliveryadmission.LiveGateProbeResultContractV1,
		RequestRef:             requestRef,
		Request:                request,
		ObservedRemoteRevision: observation.RemoteIdentity,
		RemoteFreshnessRef: padDeliveryDigest(
			"gentle-ai.pad-remote-freshness/v1",
			struct {
				Observation HostingDeliveryObservation
				ObservedAt  int64
			}{Observation: observation, ObservedAt: observedAt},
		),
		ProtectionMode:      deliveryadmission.ProtectionRespect,
		ProtectionSatisfied: observation.ProtectionState == HostingProtectionPermitted,
		ProtectionRef: padDeliveryDigest(
			"gentle-ai.pad-protection-observation/v1",
			struct {
				Request  HostingObservationRequest
				State    HostingProtectionState
				Revision string
			}{
				Request:  observation.Request,
				State:    observation.ProtectionState,
				Revision: observation.ProtectionRevision,
			},
		),
		ObservedAt: observedAt,
		ExpiresAt:  observedAt + int64(padDeliveryGateTTL/time.Second),
	}
	switch request.Mechanism {
	case deliveryadmission.MechanismFastForwardOnly:
		result.FastForwardSatisfied = resolution.ancestry.State == PADGitAncestor &&
			observation.CandidateRevision != observation.ExpectedRemoteRevision
		result.FastForwardRef = resolution.ancestry.EvidenceRef
	case deliveryadmission.MechanismPullRequest:
		if observation.PullRequestState != HostingPullRequestOpen {
			return deliveryadmission.LiveGateProbeResult{}, fmt.Errorf(
				"%w: pull request is not open",
				ErrPADDeliveryConflict,
			)
		}
		result.PullRequestRef = observation.PullRequestRef
		result.RequiredChecksSatisfied =
			observation.RequiredChecksState == HostingChecksPassed &&
				observation.PullRequestHeadRevision == observation.CandidateRevision &&
				observation.PullRequestBaseRevision == observation.ExpectedRemoteRevision
		result.RequiredChecksRef = padDeliveryDigest(
			"gentle-ai.pad-required-checks-observation/v1",
			struct {
				Request  HostingObservationRequest
				State    HostingChecksState
				Revision string
				Head     string
				Base     string
			}{
				Request:  observation.Request,
				State:    observation.RequiredChecksState,
				Revision: observation.RequiredChecksRevision,
				Head:     observation.PullRequestHeadRevision,
				Base:     observation.PullRequestBaseRevision,
			},
		)
	}
	if err := result.Validate(); err != nil {
		return deliveryadmission.LiveGateProbeResult{}, err
	}
	return result, nil
}

func (adapter *PADDeliveryAdapter) ExecuteOnce(
	ctx context.Context,
	command deliveryadmission.ExecutionCommand,
) (deliveryadmission.ExecutionResult, error) {
	if err := adapter.validate(ctx); err != nil {
		return deliveryadmission.ExecutionResult{}, err
	}
	if err := command.Validate(); err != nil {
		return deliveryadmission.ExecutionResult{}, err
	}
	if command.Destination.RepositoryRef != adapter.authority.RepositoryRef() {
		return deliveryadmission.ExecutionResult{}, ErrPADRepositoryAuthorityMismatch
	}
	commandRef, err := command.Ref()
	if err != nil {
		return deliveryadmission.ExecutionResult{}, err
	}
	store, err := adapter.ensureStore(ctx)
	if err != nil {
		return deliveryadmission.ExecutionResult{}, err
	}
	var result deliveryadmission.ExecutionResult
	err = store.withCommandLock(ctx, commandRef, func() error {
		terminal, exists, err := store.readTerminal(command, commandRef)
		if err != nil {
			return err
		}
		if exists {
			result = terminal
			return nil
		}
		indeterminate, err := store.readIndeterminate(commandRef)
		if err != nil {
			return err
		}
		if indeterminate {
			return ErrPADDeliveryIndeterminate
		}
		claim, claimed, err := store.readClaim(command, commandRef)
		if err != nil {
			return err
		}
		result, err = adapter.executeLocked(ctx, command, commandRef, claim, claimed)
		return err
	})
	return result, err
}

func (adapter *PADDeliveryAdapter) executeLocked(
	ctx context.Context,
	command deliveryadmission.ExecutionCommand,
	commandRef string,
	claim padDeliveryClaim,
	claimed bool,
) (deliveryadmission.ExecutionResult, error) {
	if !claimed {
		now, err := adapter.nowUnix()
		if err != nil {
			return deliveryadmission.ExecutionResult{}, err
		}
		if now >= command.ExecutionExpiresAt {
			return deliveryadmission.ExecutionResult{}, deliveryadmission.ErrExpired
		}
	}
	resolution, err := adapter.resolveCurrent(
		ctx,
		command.Candidate,
		command.Destination,
		command.Mechanism,
		command.PullRequestRef,
	)
	if err != nil {
		return deliveryadmission.ExecutionResult{}, err
	}
	if claimed {
		if claim.CandidateRevision != resolution.observation.CandidateRevision ||
			claim.ExpectedRevision != resolution.observation.ExpectedRemoteRevision ||
			claim.HostingRepositoryRef != resolution.binding.HostingRepositoryRef ||
			claim.PullRequestRef != resolution.binding.PullRequestRef {
			if publishErr := adapter.store.publishIndeterminate(commandRef); publishErr != nil {
				return deliveryadmission.ExecutionResult{}, publishErr
			}
			return deliveryadmission.ExecutionResult{}, fmt.Errorf(
				"%w: Git resolution changed after durable claim",
				ErrPADDeliveryIndeterminate,
			)
		}
		if recovered, ok, err := adapter.recoverApplied(
			command,
			commandRef,
			resolution,
		); err != nil || ok {
			return recovered, err
		}
		// A durable claim proves that a previous invocation crossed the
		// first-effect boundary. If current state does not prove the exact
		// effect already landed, retrying could duplicate an asynchronously
		// completed hosting operation. Stop for an explicit recovery decision.
		if err := adapter.store.publishIndeterminate(commandRef); err != nil {
			return deliveryadmission.ExecutionResult{}, err
		}
		return deliveryadmission.ExecutionResult{}, ErrPADDeliveryIndeterminate
	}
	if err := validatePendingDelivery(command, resolution); err != nil {
		return deliveryadmission.ExecutionResult{}, err
	}
	now, err := adapter.nowUnix()
	if err != nil {
		return deliveryadmission.ExecutionResult{}, err
	}
	if now >= command.ExecutionExpiresAt {
		return deliveryadmission.ExecutionResult{}, deliveryadmission.ErrExpired
	}
	if !claimed {
		claim, err = newPADDeliveryClaim(
			command,
			commandRef,
			resolution.binding.HostingRepositoryRef,
			resolution.observation.CandidateRevision,
			resolution.observation.ExpectedRemoteRevision,
			now,
		)
		if err != nil {
			return deliveryadmission.ExecutionResult{}, err
		}
		if err := adapter.store.publishClaim(claim); err != nil {
			return deliveryadmission.ExecutionResult{}, err
		}
	}
	now, err = adapter.nowUnix()
	if err != nil {
		return deliveryadmission.ExecutionResult{}, err
	}
	if now >= command.ExecutionExpiresAt {
		return deliveryadmission.ExecutionResult{}, deliveryadmission.ErrExpired
	}
	result, err := adapter.performEffect(ctx, command, commandRef, claim)
	if err != nil {
		return deliveryadmission.ExecutionResult{}, err
	}
	if err := adapter.store.publishTerminal(command, result); err != nil {
		return deliveryadmission.ExecutionResult{}, err
	}
	return result, nil
}

func (adapter *PADDeliveryAdapter) resolveCurrent(
	ctx context.Context,
	candidate deliveryadmission.CandidateBinding,
	destination deliveryadmission.DestinationBinding,
	mechanism deliveryadmission.Mechanism,
	pullRequestRef string,
) (padDeliveryResolution, error) {
	operationContext, cancel := context.WithTimeout(ctx, padDeliveryOperationTTL)
	defer cancel()
	binding, err := adapter.bindings.ResolvePADGitBinding(
		operationContext,
		candidate,
		destination,
		mechanism,
	)
	if err != nil {
		return padDeliveryResolution{}, err
	}
	if err := binding.Validate(candidate, destination, mechanism); err != nil {
		return padDeliveryResolution{}, err
	}
	if mechanism == deliveryadmission.MechanismPullRequest {
		if pullRequestRef == "" {
			pullRequestRef = binding.PullRequestRef
		} else if pullRequestRef != binding.PullRequestRef {
			return padDeliveryResolution{}, fmt.Errorf(
				"%w: command pull request differs from owner Git binding",
				ErrPADDeliveryConflict,
			)
		}
	}
	request := HostingObservationRequest{
		Binding:        binding,
		Mechanism:      mechanism,
		PullRequestRef: pullRequestRef,
	}
	if err := request.Validate(); err != nil {
		return padDeliveryResolution{}, err
	}
	observation, err := adapter.hosting.ObserveDelivery(operationContext, request)
	if err != nil {
		return padDeliveryResolution{}, err
	}
	if err := observation.Validate(request); err != nil {
		return padDeliveryResolution{}, err
	}
	if err := adapter.git.RequireCommit(
		operationContext,
		destination.RepositoryRef,
		observation.CandidateRevision,
	); err != nil {
		return padDeliveryResolution{}, fmt.Errorf("resolve exact candidate commit: %w", err)
	}
	if err := adapter.git.RequireCommit(
		operationContext,
		destination.RepositoryRef,
		observation.ExpectedRemoteRevision,
	); err != nil {
		return padDeliveryResolution{}, fmt.Errorf("resolve exact expected remote commit: %w", err)
	}
	var ancestry PADGitAncestryObservation
	if mechanism == deliveryadmission.MechanismFastForwardOnly {
		ancestry, err = adapter.git.ObserveAncestry(
			operationContext,
			destination.RepositoryRef,
			observation.ExpectedRemoteRevision,
			observation.CandidateRevision,
		)
		if err != nil {
			return padDeliveryResolution{}, err
		}
		if err := ancestry.Validate(
			destination.RepositoryRef,
			observation.ExpectedRemoteRevision,
			observation.CandidateRevision,
		); err != nil {
			return padDeliveryResolution{}, err
		}
	}
	return padDeliveryResolution{
		observation: observation,
		binding:     binding,
		ancestry:    ancestry,
	}, nil
}

func validatePendingDelivery(
	command deliveryadmission.ExecutionCommand,
	resolution padDeliveryResolution,
) error {
	observation := resolution.observation
	if observation.RemoteIdentity != command.ExpectedRemoteRevision ||
		observation.RemoteRevision != observation.ExpectedRemoteRevision {
		return fmt.Errorf("%w: remote revision is not the exact expected base", ErrPADDeliveryConflict)
	}
	if observation.ProtectionState != HostingProtectionPermitted {
		return fmt.Errorf("%w: current branch protection denies the mechanism", ErrPADDeliveryConflict)
	}
	switch command.Mechanism {
	case deliveryadmission.MechanismFastForwardOnly:
		if resolution.ancestry.State != PADGitAncestor ||
			observation.CandidateRevision == observation.ExpectedRemoteRevision {
			return fmt.Errorf("%w: candidate is not a strict fast-forward", ErrPADDeliveryConflict)
		}
	case deliveryadmission.MechanismPullRequest:
		if observation.PullRequestState != HostingPullRequestOpen ||
			observation.PullRequestHeadRevision != observation.CandidateRevision ||
			observation.PullRequestBaseRevision != observation.ExpectedRemoteRevision ||
			observation.RequiredChecksState != HostingChecksPassed {
			return fmt.Errorf("%w: pull request is not merge-ready", ErrPADDeliveryConflict)
		}
	default:
		return fmt.Errorf("%w: unknown delivery mechanism", ErrPADDeliveryConflict)
	}
	return nil
}

func (adapter *PADDeliveryAdapter) recoverApplied(
	command deliveryadmission.ExecutionCommand,
	commandRef string,
	resolution padDeliveryResolution,
) (deliveryadmission.ExecutionResult, bool, error) {
	observation := resolution.observation
	switch command.Mechanism {
	case deliveryadmission.MechanismFastForwardOnly:
		if observation.RemoteRevision != observation.CandidateRevision {
			return deliveryadmission.ExecutionResult{}, false, nil
		}
		result, err := adapter.newExecutionResult(
			command,
			commandRef,
			deliveryadmission.ExecutionSucceeded,
			observation.CandidateRevision,
			padDeliveryDigest("gentle-ai.pad-direct-recovery/v1", observation),
		)
		if err != nil {
			return deliveryadmission.ExecutionResult{}, true, err
		}
		if err := adapter.store.publishTerminal(command, result); err != nil {
			return deliveryadmission.ExecutionResult{}, true, err
		}
		return result, true, nil
	case deliveryadmission.MechanismPullRequest:
		if observation.PullRequestState != HostingPullRequestMerged {
			return deliveryadmission.ExecutionResult{}, false, nil
		}
		if observation.PullRequestHeadRevision != observation.CandidateRevision ||
			observation.PullRequestBaseRevision != observation.ExpectedRemoteRevision ||
			!validPADHostingToken(observation.DeliveryRef) {
			if err := adapter.store.publishIndeterminate(commandRef); err != nil {
				return deliveryadmission.ExecutionResult{}, true, err
			}
			return deliveryadmission.ExecutionResult{}, true, fmt.Errorf(
				"%w: merged pull request no longer binds the claimed head and base",
				ErrPADDeliveryIndeterminate,
			)
		}
		result, err := adapter.newExecutionResult(
			command,
			commandRef,
			deliveryadmission.ExecutionSucceeded,
			observation.DeliveryRef,
			padDeliveryDigest("gentle-ai.pad-pr-recovery/v1", observation),
		)
		if err != nil {
			return deliveryadmission.ExecutionResult{}, true, err
		}
		if err := adapter.store.publishTerminal(command, result); err != nil {
			return deliveryadmission.ExecutionResult{}, true, err
		}
		return result, true, nil
	default:
		if err := adapter.store.publishIndeterminate(commandRef); err != nil {
			return deliveryadmission.ExecutionResult{}, true, err
		}
		return deliveryadmission.ExecutionResult{}, true, ErrPADDeliveryIndeterminate
	}
}

func (adapter *PADDeliveryAdapter) performEffect(
	ctx context.Context,
	command deliveryadmission.ExecutionCommand,
	commandRef string,
	claim padDeliveryClaim,
) (deliveryadmission.ExecutionResult, error) {
	operationContext, cancel := context.WithTimeout(ctx, padDeliveryOperationTTL)
	defer cancel()
	switch command.Mechanism {
	case deliveryadmission.MechanismFastForwardOnly:
		request := HostingBranchCASRequest{
			CommandRef:           commandRef,
			RepositoryRef:        command.Destination.RepositoryRef,
			HostingRepositoryRef: claim.HostingRepositoryRef,
			TargetRef:            command.Destination.TargetRef,
			ExpectedRevision:     claim.ExpectedRevision,
			CandidateRevision:    claim.CandidateRevision,
			ExecutionExpiresAt:   command.ExecutionExpiresAt,
		}
		receipt, err := adapter.hosting.CompareAndSwapBranch(operationContext, request)
		if err != nil {
			return deliveryadmission.ExecutionResult{}, err
		}
		if err := receipt.Validate(request); err != nil {
			return deliveryadmission.ExecutionResult{}, err
		}
		outcome := deliveryadmission.ExecutionSucceeded
		deliveryRef := receipt.DeliveryRef
		if receipt.Outcome == HostingEffectConflict ||
			receipt.Outcome == HostingEffectRejected {
			outcome = deliveryadmission.ExecutionFailed
			deliveryRef = ""
		}
		return adapter.newExecutionResult(
			command,
			commandRef,
			outcome,
			deliveryRef,
			padDeliveryDigest("gentle-ai.pad-direct-effect/v1", receipt),
		)
	case deliveryadmission.MechanismPullRequest:
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
		receipt, err := adapter.hosting.MergePullRequest(operationContext, request)
		if err != nil {
			return deliveryadmission.ExecutionResult{}, err
		}
		if err := receipt.Validate(request); err != nil {
			return deliveryadmission.ExecutionResult{}, err
		}
		outcome := deliveryadmission.ExecutionSucceeded
		deliveryRef := receipt.DeliveryRef
		if receipt.Outcome == HostingEffectConflict ||
			receipt.Outcome == HostingEffectRejected {
			outcome = deliveryadmission.ExecutionFailed
			deliveryRef = ""
		}
		return adapter.newExecutionResult(
			command,
			commandRef,
			outcome,
			deliveryRef,
			padDeliveryDigest("gentle-ai.pad-pr-effect/v1", receipt),
		)
	default:
		return deliveryadmission.ExecutionResult{}, ErrPADDeliveryConflict
	}
}

func (adapter *PADDeliveryAdapter) newExecutionResult(
	command deliveryadmission.ExecutionCommand,
	commandRef string,
	outcome deliveryadmission.ExecutionOutcome,
	deliveryRef string,
	evidenceRef string,
) (deliveryadmission.ExecutionResult, error) {
	completedAt, err := adapter.nowUnix()
	if err != nil {
		return deliveryadmission.ExecutionResult{}, err
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
		CompletedAt:      completedAt,
	}
	return result, result.Validate(command)
}

func (adapter *PADDeliveryAdapter) validate(ctx context.Context) error {
	if adapter == nil || adapter.authority == nil || adapter.bindings == nil ||
		adapter.git == nil || adapter.hosting == nil || adapter.now == nil {
		return errors.New("PAD delivery adapter is unavailable")
	}
	_, err := adapter.authority.ResolveDeliveryRepository(
		ctx,
		adapter.authority.RepositoryRef(),
	)
	return err
}

func (adapter *PADDeliveryAdapter) ensureStore(
	ctx context.Context,
) (*padDeliveryResultStore, error) {
	adapter.storeMu.Lock()
	defer adapter.storeMu.Unlock()
	if adapter.store != nil {
		return adapter.store, nil
	}
	store, err := openPADDeliveryResultStore(ctx, adapter.authority)
	if err != nil {
		return nil, err
	}
	adapter.store = store
	return store, nil
}

func (adapter *PADDeliveryAdapter) nowUnix() (int64, error) {
	now := adapter.now().UTC().Unix()
	if now <= 0 {
		return 0, errors.New("PAD delivery owner clock returned non-positive time")
	}
	return now, nil
}

func validPADGitRevision(revision string) bool {
	return padGitRevisionPattern.MatchString(revision)
}

func validPADGitBranchRef(ref string) bool {
	if !strings.HasPrefix(ref, "refs/heads/") ||
		len(ref) > 255 ||
		strings.Contains(ref, "..") ||
		strings.Contains(ref, "@{") ||
		strings.Contains(ref, "//") ||
		strings.HasSuffix(ref, "/") ||
		strings.HasSuffix(ref, ".") ||
		strings.HasSuffix(ref, ".lock") {
		return false
	}
	for _, character := range ref {
		switch character {
		case ' ', '~', '^', ':', '?', '*', '[', '\\':
			return false
		}
		if character < 0x21 || character > 0x7e {
			return false
		}
	}
	return true
}

func padDeliveryDigest(domain string, value any) string {
	payload, _ := json.Marshal(value)
	hash := sha256.New()
	_, _ = hash.Write([]byte(domain))
	_, _ = hash.Write([]byte{0})
	_, _ = hash.Write(payload)
	return "sha256:" + hex.EncodeToString(hash.Sum(nil))
}

var (
	_ deliveryadmission.LiveGateProbePort    = (*PADDeliveryAdapter)(nil)
	_ deliveryadmission.DeliveryExecutorPort = (*PADDeliveryAdapter)(nil)
)
