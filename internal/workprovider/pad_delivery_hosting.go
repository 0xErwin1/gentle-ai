package workprovider

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/gentleman-programming/gentle-ai/internal/deliveryadmission"
)

const (
	PADGitBindingSchema              = "gentle-ai.pad-git-binding/v1"
	HostingDeliveryObservationSchema = "gentle-ai.hosting-delivery-observation/v1"
	HostingBranchCASReceiptSchema    = "gentle-ai.hosting-branch-cas-receipt/v1"
	HostingPullRequestReceiptSchema  = "gentle-ai.hosting-pull-request-receipt/v1"
)

var (
	ErrPADHostingUnavailable    = errors.New("PAD hosting authority is unavailable")
	ErrPADHostingCorrupt        = errors.New("PAD hosting authority returned an invalid binding")
	ErrPADDeliveryConflict      = errors.New("PAD delivery destination changed")
	ErrPADDeliveryIndeterminate = errors.New(
		"PAD delivery effect is indeterminate",
	)
)

type HostingProtectionState string

const (
	HostingProtectionPermitted HostingProtectionState = "permitted"
	HostingProtectionDenied    HostingProtectionState = "denied"
)

type HostingChecksState string

const (
	HostingChecksNotApplicable HostingChecksState = "not_applicable"
	HostingChecksPassed        HostingChecksState = "passed"
	HostingChecksPending       HostingChecksState = "pending"
	HostingChecksFailed        HostingChecksState = "failed"
)

type HostingPullRequestState string

const (
	HostingPullRequestNotApplicable HostingPullRequestState = "not_applicable"
	HostingPullRequestOpen          HostingPullRequestState = "open"
	HostingPullRequestMerged        HostingPullRequestState = "merged"
	HostingPullRequestClosed        HostingPullRequestState = "closed"
)

type HostingEffectOutcome string

const (
	HostingEffectApplied        HostingEffectOutcome = "applied"
	HostingEffectAlreadyApplied HostingEffectOutcome = "already_applied"
	HostingEffectConflict       HostingEffectOutcome = "conflict"
	HostingEffectRejected       HostingEffectOutcome = "rejected"
)

// PADGitBinding is an owner-controlled semantic bridge from PAD's opaque
// candidate/snapshot identities to exact Git and hosting objects.
type PADGitBinding struct {
	Schema                 string
	Candidate              deliveryadmission.CandidateBinding
	Destination            deliveryadmission.DestinationBinding
	Mechanism              deliveryadmission.Mechanism
	HostingRepositoryRef   string
	CandidateRevision      string
	ExpectedRemoteRevision string
	PullRequestRef         string
}

func (binding PADGitBinding) Validate(
	candidate deliveryadmission.CandidateBinding,
	destination deliveryadmission.DestinationBinding,
	mechanism deliveryadmission.Mechanism,
) error {
	if binding.Schema != PADGitBindingSchema ||
		binding.Candidate != candidate ||
		binding.Destination != destination ||
		binding.Mechanism != mechanism ||
		!validPADCandidateBinding(candidate) ||
		!validPADDestinationBinding(destination) ||
		!validPADHostingToken(binding.HostingRepositoryRef) ||
		!validPADGitRevision(binding.CandidateRevision) ||
		!validPADGitRevision(binding.ExpectedRemoteRevision) {
		return fmt.Errorf("%w: invalid owner Git binding", ErrPADHostingCorrupt)
	}
	switch mechanism {
	case deliveryadmission.MechanismFastForwardOnly:
		if binding.PullRequestRef != "" {
			return fmt.Errorf("%w: direct Git binding carries a PR", ErrPADHostingCorrupt)
		}
	case deliveryadmission.MechanismPullRequest:
		if !validPADHostingToken(binding.PullRequestRef) {
			return fmt.Errorf("%w: PR Git binding lacks exact PR ref", ErrPADHostingCorrupt)
		}
	default:
		return fmt.Errorf("%w: unknown Git binding mechanism", ErrPADHostingCorrupt)
	}
	return nil
}

type PADGitBindingAuthority interface {
	ResolvePADGitBinding(
		context.Context,
		deliveryadmission.CandidateBinding,
		deliveryadmission.DestinationBinding,
		deliveryadmission.Mechanism,
	) (PADGitBinding, error)
}

type PADGitBindingTransport interface {
	ResolvePADGitBinding(
		context.Context,
		deliveryadmission.CandidateBinding,
		deliveryadmission.DestinationBinding,
		deliveryadmission.Mechanism,
	) (PADGitBinding, error)
}

// TransportPADGitBindingAuthority validates bindings returned by an injected
// owner candidate/hosting catalog. It never infers HEAD, origin, or a PR.
type TransportPADGitBindingAuthority struct {
	transport PADGitBindingTransport
}

func NewTransportPADGitBindingAuthority(
	transport PADGitBindingTransport,
) (*TransportPADGitBindingAuthority, error) {
	if transport == nil {
		return nil, fmt.Errorf("%w: missing owner Git binding transport", ErrPADHostingUnavailable)
	}
	return &TransportPADGitBindingAuthority{transport: transport}, nil
}

func (authority *TransportPADGitBindingAuthority) ResolvePADGitBinding(
	ctx context.Context,
	candidate deliveryadmission.CandidateBinding,
	destination deliveryadmission.DestinationBinding,
	mechanism deliveryadmission.Mechanism,
) (PADGitBinding, error) {
	if authority == nil || authority.transport == nil {
		return PADGitBinding{}, ErrPADHostingUnavailable
	}
	binding, err := authority.transport.ResolvePADGitBinding(
		ctx,
		candidate,
		destination,
		mechanism,
	)
	if err != nil {
		return PADGitBinding{}, fmt.Errorf("%w: resolve owner Git binding: %w", ErrPADHostingUnavailable, err)
	}
	if err := binding.Validate(candidate, destination, mechanism); err != nil {
		return PADGitBinding{}, err
	}
	return binding, nil
}

// HostingObservationRequest contains only immutable PAD/Git identities. It has
// no caller-authored policy booleans or provider endpoint/argv selection.
type HostingObservationRequest struct {
	Binding        PADGitBinding
	Mechanism      deliveryadmission.Mechanism
	PullRequestRef string
}

func (request HostingObservationRequest) Validate() error {
	if err := request.Binding.Validate(
		request.Binding.Candidate,
		request.Binding.Destination,
		request.Mechanism,
	); err != nil {
		return fmt.Errorf("%w: invalid hosting observation request identity", ErrPADHostingCorrupt)
	}
	switch request.Mechanism {
	case deliveryadmission.MechanismFastForwardOnly:
		if request.PullRequestRef != "" || request.Binding.PullRequestRef != "" {
			return fmt.Errorf("%w: direct observation carries a pull request", ErrPADHostingCorrupt)
		}
	case deliveryadmission.MechanismPullRequest:
		if request.PullRequestRef != request.Binding.PullRequestRef {
			return fmt.Errorf("%w: pull-request observation ref mismatch", ErrPADHostingCorrupt)
		}
	default:
		return fmt.Errorf("%w: unknown hosting mechanism", ErrPADHostingCorrupt)
	}
	return nil
}

// HostingDeliveryObservation is provider-owned current state. States are typed
// enums rather than caller-supplied success booleans.
type HostingDeliveryObservation struct {
	Schema                  string
	Request                 HostingObservationRequest
	PullRequestRef          string
	CandidateRevision       string
	ExpectedRemoteRevision  string
	RemoteIdentity          string
	RemoteRevision          string
	ProtectionState         HostingProtectionState
	ProtectionRevision      string
	PullRequestState        HostingPullRequestState
	PullRequestHeadRevision string
	PullRequestBaseRevision string
	RequiredChecksState     HostingChecksState
	RequiredChecksRevision  string
	DeliveryRef             string
}

func (observation HostingDeliveryObservation) Validate(
	request HostingObservationRequest,
) error {
	if observation.Schema != HostingDeliveryObservationSchema ||
		observation.Request != request ||
		observation.CandidateRevision != request.Binding.CandidateRevision ||
		observation.ExpectedRemoteRevision != request.Binding.ExpectedRemoteRevision ||
		!validPADGitRevision(observation.CandidateRevision) ||
		!validPADGitRevision(observation.ExpectedRemoteRevision) ||
		!validPADHostingToken(observation.RemoteIdentity) ||
		!validPADGitRevision(observation.RemoteRevision) ||
		!validPADHostingToken(observation.ProtectionRevision) ||
		(observation.ProtectionState != HostingProtectionPermitted &&
			observation.ProtectionState != HostingProtectionDenied) {
		return fmt.Errorf("%w: invalid hosting observation header", ErrPADHostingCorrupt)
	}
	switch request.Mechanism {
	case deliveryadmission.MechanismFastForwardOnly:
		if observation.PullRequestRef != "" ||
			observation.PullRequestState != HostingPullRequestNotApplicable ||
			observation.PullRequestHeadRevision != "" ||
			observation.PullRequestBaseRevision != "" ||
			observation.RequiredChecksState != HostingChecksNotApplicable ||
			observation.RequiredChecksRevision != "" ||
			observation.DeliveryRef != "" {
			return fmt.Errorf("%w: direct observation carries PR facts", ErrPADHostingCorrupt)
		}
	case deliveryadmission.MechanismPullRequest:
		if observation.PullRequestRef != request.PullRequestRef {
			return fmt.Errorf("%w: hosting observed another pull request", ErrPADHostingCorrupt)
		}
		if observation.PullRequestState != HostingPullRequestOpen &&
			observation.PullRequestState != HostingPullRequestMerged &&
			observation.PullRequestState != HostingPullRequestClosed {
			return fmt.Errorf("%w: invalid pull-request state", ErrPADHostingCorrupt)
		}
		if !validPADGitRevision(observation.PullRequestHeadRevision) ||
			!validPADGitRevision(observation.PullRequestBaseRevision) ||
			!validPADHostingToken(observation.RequiredChecksRevision) {
			return fmt.Errorf("%w: incomplete pull-request observation", ErrPADHostingCorrupt)
		}
		if observation.RequiredChecksState != HostingChecksPassed &&
			observation.RequiredChecksState != HostingChecksPending &&
			observation.RequiredChecksState != HostingChecksFailed {
			return fmt.Errorf("%w: invalid required-checks state", ErrPADHostingCorrupt)
		}
		if observation.PullRequestState == HostingPullRequestMerged {
			if !validPADHostingToken(observation.DeliveryRef) {
				return fmt.Errorf("%w: merged pull request lacks delivery ref", ErrPADHostingCorrupt)
			}
		} else if observation.DeliveryRef != "" {
			return fmt.Errorf("%w: unmerged pull request carries delivery ref", ErrPADHostingCorrupt)
		}
	default:
		return fmt.Errorf("%w: unknown hosting observation mechanism", ErrPADHostingCorrupt)
	}
	return nil
}

type HostingBranchCASRequest struct {
	CommandRef           string
	RepositoryRef        string
	HostingRepositoryRef string
	TargetRef            string
	ExpectedRevision     string
	CandidateRevision    string
	ExecutionExpiresAt   int64
}

func (request HostingBranchCASRequest) Validate() error {
	if !validPADImmutableRef(request.CommandRef) ||
		!validPADImmutableRef(request.RepositoryRef) ||
		!validPADHostingToken(request.HostingRepositoryRef) ||
		!validPADGitBranchRef(request.TargetRef) ||
		!validPADGitRevision(request.ExpectedRevision) ||
		!validPADGitRevision(request.CandidateRevision) ||
		request.ExpectedRevision == request.CandidateRevision ||
		request.ExecutionExpiresAt <= 0 {
		return fmt.Errorf("%w: invalid exact branch CAS request", ErrPADHostingCorrupt)
	}
	return nil
}

type HostingBranchCASReceipt struct {
	Schema           string
	Request          HostingBranchCASRequest
	Outcome          HostingEffectOutcome
	RemoteRevision   string
	DeliveryRef      string
	EvidenceRevision string
}

func (receipt HostingBranchCASReceipt) Validate(
	request HostingBranchCASRequest,
) error {
	if receipt.Schema != HostingBranchCASReceiptSchema ||
		receipt.Request != request ||
		!validPADGitRevision(receipt.RemoteRevision) ||
		!validPADHostingToken(receipt.EvidenceRevision) {
		return fmt.Errorf("%w: invalid branch CAS receipt", ErrPADHostingCorrupt)
	}
	switch receipt.Outcome {
	case HostingEffectApplied, HostingEffectAlreadyApplied:
		if receipt.RemoteRevision != request.CandidateRevision ||
			receipt.DeliveryRef != request.CandidateRevision {
			return fmt.Errorf("%w: branch CAS success is not exact", ErrPADHostingCorrupt)
		}
	case HostingEffectConflict, HostingEffectRejected:
		if receipt.DeliveryRef != "" {
			return fmt.Errorf("%w: rejected branch CAS carries delivery ref", ErrPADHostingCorrupt)
		}
	default:
		return fmt.Errorf("%w: invalid branch CAS outcome", ErrPADHostingCorrupt)
	}
	return nil
}

type HostingPullRequestMergeRequest struct {
	CommandRef           string
	RepositoryRef        string
	HostingRepositoryRef string
	TargetRef            string
	PullRequestRef       string
	ExpectedBaseRevision string
	ExpectedHeadRevision string
	ExecutionExpiresAt   int64
}

func (request HostingPullRequestMergeRequest) Validate() error {
	if !validPADImmutableRef(request.CommandRef) ||
		!validPADImmutableRef(request.RepositoryRef) ||
		!validPADHostingToken(request.HostingRepositoryRef) ||
		!validPADGitBranchRef(request.TargetRef) ||
		!validPADHostingToken(request.PullRequestRef) ||
		!validPADGitRevision(request.ExpectedBaseRevision) ||
		!validPADGitRevision(request.ExpectedHeadRevision) ||
		request.ExecutionExpiresAt <= 0 {
		return fmt.Errorf("%w: invalid exact pull-request merge request", ErrPADHostingCorrupt)
	}
	return nil
}

type HostingPullRequestMergeReceipt struct {
	Schema             string
	Request            HostingPullRequestMergeRequest
	Outcome            HostingEffectOutcome
	RemoteRevision     string
	MergedHeadRevision string
	DeliveryRef        string
	EvidenceRevision   string
}

func (receipt HostingPullRequestMergeReceipt) Validate(
	request HostingPullRequestMergeRequest,
) error {
	if receipt.Schema != HostingPullRequestReceiptSchema ||
		receipt.Request != request ||
		!validPADGitRevision(receipt.RemoteRevision) ||
		!validPADHostingToken(receipt.EvidenceRevision) {
		return fmt.Errorf("%w: invalid pull-request merge receipt", ErrPADHostingCorrupt)
	}
	switch receipt.Outcome {
	case HostingEffectApplied, HostingEffectAlreadyApplied:
		if receipt.MergedHeadRevision != request.ExpectedHeadRevision ||
			!validPADHostingToken(receipt.DeliveryRef) {
			return fmt.Errorf("%w: pull-request merge success is not exact", ErrPADHostingCorrupt)
		}
	case HostingEffectConflict, HostingEffectRejected:
		if receipt.MergedHeadRevision != "" || receipt.DeliveryRef != "" {
			return fmt.Errorf("%w: rejected merge carries delivery facts", ErrPADHostingCorrupt)
		}
	default:
		return fmt.Errorf("%w: invalid pull-request merge outcome", ErrPADHostingCorrupt)
	}
	return nil
}

// HostingAuthority is the provider-owned network/API seam consumed by PAD's
// productive adapter. Implementations, not callers, derive protection and
// required-check state from current hosting data.
type HostingAuthority interface {
	ObserveDelivery(
		context.Context,
		HostingObservationRequest,
	) (HostingDeliveryObservation, error)
	CompareAndSwapBranch(
		context.Context,
		HostingBranchCASRequest,
	) (HostingBranchCASReceipt, error)
	MergePullRequest(
		context.Context,
		HostingPullRequestMergeRequest,
	) (HostingPullRequestMergeReceipt, error)
}

// HostingEndpointTransport is the smallest provider-specific implementation
// seam. A GitHub implementation can call REST/GraphQL here without exposing
// endpoint names, arbitrary payloads, force flags, or caller-selected methods
// to PAD. Implementations MUST observe current branch protection and required
// checks, enforce ExecutionExpiresAt with an owner clock immediately before an
// effect, use an exact server-side expected-old -> candidate CAS for direct
// updates, and merge only the exact bound PR/head/base without admin bypass.
type HostingEndpointTransport interface {
	ObserveDelivery(
		context.Context,
		HostingObservationRequest,
	) (HostingDeliveryObservation, error)
	CompareAndSwapBranch(
		context.Context,
		HostingBranchCASRequest,
	) (HostingBranchCASReceipt, error)
	MergePullRequest(
		context.Context,
		HostingPullRequestMergeRequest,
	) (HostingPullRequestMergeReceipt, error)
}

// TransportHostingAuthority is the production validation adapter around an
// injected provider endpoint transport.
type TransportHostingAuthority struct {
	transport HostingEndpointTransport
}

func NewTransportHostingAuthority(
	transport HostingEndpointTransport,
) (*TransportHostingAuthority, error) {
	if transport == nil {
		return nil, fmt.Errorf("%w: missing hosting endpoint transport", ErrPADHostingUnavailable)
	}
	return &TransportHostingAuthority{transport: transport}, nil
}

func (authority *TransportHostingAuthority) ObserveDelivery(
	ctx context.Context,
	request HostingObservationRequest,
) (HostingDeliveryObservation, error) {
	if authority == nil || authority.transport == nil {
		return HostingDeliveryObservation{}, ErrPADHostingUnavailable
	}
	if err := request.Validate(); err != nil {
		return HostingDeliveryObservation{}, err
	}
	observation, err := authority.transport.ObserveDelivery(ctx, request)
	if err != nil {
		return HostingDeliveryObservation{}, fmt.Errorf("%w: observe delivery: %w", ErrPADHostingUnavailable, err)
	}
	if err := observation.Validate(request); err != nil {
		return HostingDeliveryObservation{}, err
	}
	return observation, nil
}

func (authority *TransportHostingAuthority) CompareAndSwapBranch(
	ctx context.Context,
	request HostingBranchCASRequest,
) (HostingBranchCASReceipt, error) {
	if authority == nil || authority.transport == nil {
		return HostingBranchCASReceipt{}, ErrPADHostingUnavailable
	}
	if err := request.Validate(); err != nil {
		return HostingBranchCASReceipt{}, err
	}
	receipt, err := authority.transport.CompareAndSwapBranch(ctx, request)
	if err != nil {
		return HostingBranchCASReceipt{}, fmt.Errorf("%w: branch CAS: %w", ErrPADHostingUnavailable, err)
	}
	if err := receipt.Validate(request); err != nil {
		return HostingBranchCASReceipt{}, err
	}
	return receipt, nil
}

func (authority *TransportHostingAuthority) MergePullRequest(
	ctx context.Context,
	request HostingPullRequestMergeRequest,
) (HostingPullRequestMergeReceipt, error) {
	if authority == nil || authority.transport == nil {
		return HostingPullRequestMergeReceipt{}, ErrPADHostingUnavailable
	}
	if err := request.Validate(); err != nil {
		return HostingPullRequestMergeReceipt{}, err
	}
	receipt, err := authority.transport.MergePullRequest(ctx, request)
	if err != nil {
		return HostingPullRequestMergeReceipt{}, fmt.Errorf("%w: merge pull request: %w", ErrPADHostingUnavailable, err)
	}
	if err := receipt.Validate(request); err != nil {
		return HostingPullRequestMergeReceipt{}, err
	}
	return receipt, nil
}

func validPADHostingToken(value string) bool {
	if value == "" || len(value) > 512 || strings.TrimSpace(value) != value {
		return false
	}
	for _, character := range value {
		if character < 0x21 || character > 0x7e {
			return false
		}
	}
	return true
}

func validPADCandidateBinding(binding deliveryadmission.CandidateBinding) bool {
	return validPADHostingToken(binding.Ref) && validPADImmutableRef(binding.Digest)
}

func validPADDestinationBinding(binding deliveryadmission.DestinationBinding) bool {
	return validPADImmutableRef(binding.RepositoryRef) &&
		validPADGitBranchRef(binding.TargetRef) &&
		validPADHostingToken(binding.ObservedRevision)
}

var (
	_ PADGitBindingAuthority = (*TransportPADGitBindingAuthority)(nil)
	_ HostingAuthority       = (*TransportHostingAuthority)(nil)
)
