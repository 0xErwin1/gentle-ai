package workrun

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/gentleman-programming/gentle-ai/internal/hostruntime"
	"github.com/gentleman-programming/gentle-ai/internal/reviewtransaction"
)

const (
	WorkRunStateSchemaV1             = "gentle-ai.work-run-state/v1"
	workRunRecordSchemaV1            = "gentle-ai.work-run-record/v1"
	workRunRepositoryBindingSchemaV1 = "gentle-ai.work-run-repository-binding/v1"

	workOperationStart              = "run/start"
	workOperationAcceptSDD          = "route/accept-sdd"
	workOperationReroute            = "route/reroute"
	workOperationBindSDD            = "route/bind-sdd"
	workOperationBindHandoff        = "implementation/bind-handoff"
	workOperationReplanVerification = "verification/replan-correction"
	workOperationRecordForecast     = "verification/record-forecast"
	workOperationRecordDisposition  = "verification/record-disposition"
	workOperationBeginVerification  = "verification/begin"
	workOperationClaimLaunch        = "verification/claim-launch"
	workOperationBindResult         = "verification/bind-result"
	workOperationStopMutation       = "verification/stop-mutated"
	workOperationBindReview         = "review/bind-receipt"
	workOperationBindDeliveryRoute  = "delivery/bind-route-reevaluation"
	workOperationBindDelivery       = "delivery/bind-authorization"

	maximumWorkRunRecordBytes  = 1 << 20
	maximumWorkRunChainRecords = 10_000
)

var (
	ErrWorkRunNotStarted         = errors.New("work run has not started")
	ErrWorkRunAlreadyStarted     = errors.New("work run already started")
	ErrWorkRunRevisionConflict   = errors.New("work run revision conflict")
	ErrWorkRunRequestConflict    = errors.New("work run request identifier was reused with different inputs")
	ErrWorkRunConcurrentUpdate   = errors.New("work run is concurrently updated")
	ErrWorkRunInvalidTransition  = errors.New("invalid work run transition")
	ErrWorkRunRoutePending       = errors.New("work run implementation route is pending")
	ErrVerificationReserved      = errors.New("verification slot is already reserved")
	ErrVerificationLaunchClaimed = errors.New("verification reservation launch is already claimed")

	workRunIDPattern         = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{0,127}$`)
	workRequestIDPattern     = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{0,127}$`)
	workRunStorageKeyPattern = regexp.MustCompile(`^[0-9a-f]{64}$`)
)

type RevisionConflictError struct {
	Expected string
	Current  string
}

func (err *RevisionConflictError) Error() string {
	return fmt.Sprintf("%v: expected %q, current %q", ErrWorkRunRevisionConflict, err.Expected, err.Current)
}

func (err *RevisionConflictError) Unwrap() error { return ErrWorkRunRevisionConflict }

// PublicationError means HEAD was replaced but directory durability could not
// be confirmed. Replaying the exact request is safe and consumes no ordinal.
type PublicationError struct {
	Revision  string
	Committed bool
	Cause     error
}

func (err *PublicationError) Error() string {
	return fmt.Sprintf("work run publication for %s requires exact replay: %v", err.Revision, err.Cause)
}

func (err *PublicationError) Unwrap() error { return err.Cause }

// WorkRunState is a route-neutral coordination projection. Referenced owner
// artifacts retain authority; this journal records only their immutable refs
// and the ordering of accepted application-level transitions.
type WorkRunState struct {
	Schema                          string                      `json:"schema"`
	WorkRunID                       string                      `json:"work_run_id"`
	Revision                        string                      `json:"revision,omitempty"`
	Started                         bool                        `json:"started"`
	RouteDecision                   ImplementationRouteDecision `json:"route_decision"`
	ImplementationRoute             ImplementationRoute         `json:"implementation_route,omitempty"`
	RouteAcceptanceRef              string                      `json:"route_acceptance_ref,omitempty"`
	SDDRunRef                       string                      `json:"sdd_run_ref,omitempty"`
	DeliveryIntentRef               string                      `json:"delivery_intent_ref"`
	Handoff                         *ImplementationHandoff      `json:"handoff,omitempty"`
	VerificationReplan              *VerificationReplan         `json:"verification_replan,omitempty"`
	Forecast                        *VerificationForecast       `json:"forecast,omitempty"`
	Disposition                     *VerificationDisposition    `json:"disposition,omitempty"`
	Reservations                    []VerificationReservation   `json:"reservations"`
	LaunchClaims                    []VerificationLaunchClaim   `json:"launch_claims"`
	NextOrdinal                     int                         `json:"next_ordinal"`
	VerificationResultRef           string                      `json:"verification_result_ref,omitempty"`
	PostVerificationSnapshotRef     string                      `json:"post_verification_snapshot_ref,omitempty"`
	CorrectionImpactClosureRef      string                      `json:"correction_impact_closure_ref,omitempty"`
	ReusableVerificationObligations []string                    `json:"reusable_verification_obligations"`
	VerificationStop                *VerificationStop           `json:"verification_stop,omitempty"`
	ReviewReceiptRef                string                      `json:"review_receipt_ref,omitempty"`
	DeliveryRouteReevaluationRef    string                      `json:"delivery_route_reevaluation_ref,omitempty"`
	DeliveryAuthorizationRef        string                      `json:"delivery_authorization_ref,omitempty"`
}

type WorkRunStore struct {
	Dir            string
	Repo           string
	WorkRunID      string
	commonDir      string
	repositoryDir  string
	canonicalDir   string
	storageKey     string
	boundWorkRunID string
	lease          *reviewtransaction.RepositoryIdentityLease
	evidence       EvidencePort
	authority      AuthorityPorts
}

type workRunRepositoryBinding struct {
	Schema         string `json:"schema"`
	StorageKey     string `json:"storage_key"`
	RepositoryRef  string `json:"repository_ref"`
	RepositoryRoot string `json:"repository_root"`
	GitCommonDir   string `json:"git_common_dir"`
	GitDir         string `json:"git_dir"`
}

type StartRequest struct {
	ExpectedRevision  string                      `json:"expected_revision"`
	RequestID         string                      `json:"request_id"`
	RouteDecision     ImplementationRouteDecision `json:"route_decision"`
	DeliveryIntentRef string                      `json:"delivery_intent_ref"`
}

type AcceptSDDRequest struct {
	ExpectedRevision string `json:"expected_revision"`
	RequestID        string `json:"request_id"`
	AcceptanceRef    string `json:"acceptance_ref"`
}

type RerouteRequest struct {
	ExpectedRevision string                      `json:"expected_revision"`
	RequestID        string                      `json:"request_id"`
	OwnerDecisionRef string                      `json:"owner_decision_ref"`
	RouteDecision    ImplementationRouteDecision `json:"route_decision"`
}

type BindSDDRunRequest struct {
	ExpectedRevision string `json:"expected_revision"`
	RequestID        string `json:"request_id"`
	SDDRunRef        string `json:"sdd_run_ref"`
}

type BindImplementationHandoffRequest struct {
	ExpectedRevision string                `json:"expected_revision"`
	RequestID        string                `json:"request_id"`
	Handoff          ImplementationHandoff `json:"handoff"`
}

type ReplanVerificationAfterCorrectionRequest struct {
	ExpectedRevision string                `json:"expected_revision"`
	RequestID        string                `json:"request_id"`
	CorrectedHandoff ImplementationHandoff `json:"corrected_handoff"`
}

type RecordVerificationForecastRequest struct {
	ExpectedRevision string                    `json:"expected_revision"`
	RequestID        string                    `json:"request_id"`
	Input            VerificationForecastInput `json:"input"`
}

type RecordVerificationDispositionRequest struct {
	ExpectedRevision string                      `json:"expected_revision"`
	RequestID        string                      `json:"request_id"`
	Kind             VerificationDispositionKind `json:"kind"`
	AssumptionsRef   string                      `json:"assumptions_ref"`
	ActorRef         string                      `json:"actor_ref"`
	DecisionRef      string                      `json:"decision_ref"`
	RunnerRef        string                      `json:"runner_ref,omitempty"`
}

type BeginRequest struct {
	ExpectedRevision string                                      `json:"expected_revision"`
	RequestID        string                                      `json:"request_id"`
	Applicability    reviewtransaction.VerificationApplicability `json:"applicability"`
	Registry         reviewtransaction.VerificationPlanRegistry  `json:"registry"`
	Plan             reviewtransaction.VerificationPlan          `json:"plan"`
	ActionTicketRef  string                                      `json:"action_ticket_ref"`
}

type BeginOutcome struct {
	State      WorkRunState
	Capability *hostruntime.LaunchCapability
}

type BindVerificationResultRequest struct {
	ExpectedRevision string                                      `json:"expected_revision"`
	RequestID        string                                      `json:"request_id"`
	Applicability    reviewtransaction.VerificationApplicability `json:"applicability"`
	Registry         reviewtransaction.VerificationPlanRegistry  `json:"registry"`
	Plan             reviewtransaction.VerificationPlan          `json:"plan"`
	Result           reviewtransaction.VerificationResultRef     `json:"result"`
}

type BindReviewReceiptRequest struct {
	ExpectedRevision string `json:"expected_revision"`
	RequestID        string `json:"request_id"`
	ReviewReceiptRef string `json:"review_receipt_ref"`
}

type BindDeliveryAuthorizationRequest struct {
	ExpectedRevision string `json:"expected_revision"`
	RequestID        string `json:"request_id"`
	AuthorizationRef string `json:"authorization_ref"`
}

type BindDeliveryRouteReevaluationRequest struct {
	ExpectedRevision string `json:"expected_revision"`
	RequestID        string `json:"request_id"`
	ReevaluationRef  string `json:"reevaluation_ref"`
}

type workStartEvent struct {
	RouteDecision     ImplementationRouteDecision `json:"route_decision"`
	DeliveryIntentRef string                      `json:"delivery_intent_ref"`
}

type workAcceptSDDEvent struct {
	AcceptanceRef string `json:"acceptance_ref"`
}

type workRerouteEvent struct {
	PreviousDecisionDigest string                      `json:"previous_decision_digest"`
	OwnerDecisionRef       string                      `json:"owner_decision_ref"`
	RouteDecision          ImplementationRouteDecision `json:"route_decision"`
}

type workBindSDDEvent struct {
	SDDRunRef string `json:"sdd_run_ref"`
}

type workBindReviewEvent struct {
	ReviewReceiptRef string `json:"review_receipt_ref"`
}

type workBindResultEvent struct {
	ResultRef                   string            `json:"result_ref"`
	PostVerificationSnapshotRef string            `json:"post_verification_snapshot_ref,omitempty"`
	CorrectionImpactClosureRef  string            `json:"correction_impact_closure_ref,omitempty"`
	ReusableObligations         []string          `json:"reusable_obligations,omitempty"`
	Stop                        *VerificationStop `json:"stop,omitempty"`
}

type workVerificationReplanEvent struct {
	Handoff ImplementationHandoff `json:"handoff"`
	Replan  VerificationReplan    `json:"replan"`
}

type workStopMutationEvent struct {
	Stop VerificationStop `json:"stop"`
}

type workBindDeliveryAuthorizationEvent struct {
	AuthorizationRef string `json:"authorization_ref"`
}

type workBindDeliveryRouteEvent struct {
	ReevaluationRef         string `json:"reevaluation_ref"`
	SourceDeliveryIntentRef string `json:"source_delivery_intent_ref"`
	TargetDeliveryIntentRef string `json:"target_delivery_intent_ref"`
}

type workRunRecord struct {
	Schema           string `json:"schema"`
	WorkRunID        string `json:"work_run_id"`
	PreviousRevision string `json:"previous_revision"`
	Operation        string `json:"operation"`
	RequestID        string `json:"request_id"`
	RequestDigest    string `json:"request_digest"`

	Start         *workStartEvent                     `json:"start,omitempty"`
	AcceptSDD     *workAcceptSDDEvent                 `json:"accept_sdd,omitempty"`
	Reroute       *workRerouteEvent                   `json:"reroute,omitempty"`
	BindSDD       *workBindSDDEvent                   `json:"bind_sdd,omitempty"`
	Handoff       *ImplementationHandoff              `json:"handoff,omitempty"`
	Replan        *workVerificationReplanEvent        `json:"replan,omitempty"`
	Forecast      *VerificationForecast               `json:"forecast,omitempty"`
	Disposition   *VerificationDisposition            `json:"disposition,omitempty"`
	Reservation   *VerificationReservation            `json:"reservation,omitempty"`
	Launch        *VerificationLaunchClaim            `json:"launch,omitempty"`
	Result        *workBindResultEvent                `json:"result,omitempty"`
	StopMutation  *workStopMutationEvent              `json:"stop_mutation,omitempty"`
	Review        *workBindReviewEvent                `json:"review,omitempty"`
	DeliveryRoute *workBindDeliveryRouteEvent         `json:"delivery_route,omitempty"`
	Delivery      *workBindDeliveryAuthorizationEvent `json:"delivery,omitempty"`
}

type workRequestReceipt struct {
	Digest   string
	Revision string
}

type workReplay struct {
	State    WorkRunState
	Requests map[string]workRequestReceipt
	States   map[string]WorkRunState
}

func OpenWorkRunStore(ctx context.Context, repo, workRunID string) (WorkRunStore, error) {
	if ctx == nil {
		return WorkRunStore{}, errors.New("work run context is nil")
	}
	if !workRunIDPattern.MatchString(workRunID) {
		return WorkRunStore{}, errors.New("invalid work run identifier")
	}
	lease, err := reviewtransaction.OpenRepositoryIdentityLease(ctx, repo)
	if err != nil {
		return WorkRunStore{}, err
	}
	return OpenWorkRunStoreWithRepositoryIdentityLease(ctx, lease, workRunID)
}

// OpenWorkRunStoreWithRepositoryIdentityLease retains one owner-resolved
// exact-worktree lease so production composition can share a single Git
// identity snapshot across adjacent authority stores.
func OpenWorkRunStoreWithRepositoryIdentityLease(
	ctx context.Context,
	lease *reviewtransaction.RepositoryIdentityLease,
	workRunID string,
) (WorkRunStore, error) {
	if ctx == nil {
		return WorkRunStore{}, errors.New("work run context is nil")
	}
	if err := ctx.Err(); err != nil {
		return WorkRunStore{}, err
	}
	if !workRunIDPattern.MatchString(workRunID) {
		return WorkRunStore{}, errors.New("invalid work run identifier")
	}
	if lease == nil {
		return WorkRunStore{}, errors.New("work run repository identity lease is unavailable")
	}
	identity := lease.Identity()
	storageKey := lease.StorageKey()
	if !workRunStorageKeyPattern.MatchString(storageKey) ||
		identity.RepositoryRef != "sha256:"+storageKey {
		return WorkRunStore{}, errors.New("invalid WorkRun repository identity lease")
	}
	repositoryDir := filepath.Join(
		identity.GitCommonDir,
		"gentle-ai",
		"work-runs",
		"v1",
		"repositories",
		storageKey,
	)
	dir := filepath.Join(repositoryDir, workRunID)
	store := WorkRunStore{
		Dir: dir, Repo: identity.RepositoryRoot, WorkRunID: workRunID,
		commonDir: identity.GitCommonDir, repositoryDir: repositoryDir,
		canonicalDir: dir, storageKey: storageKey, boundWorkRunID: workRunID,
		lease: lease,
	}
	if err := store.validateContext(ctx); err != nil {
		return WorkRunStore{}, err
	}
	return store, nil
}

// WithEvidencePort binds an EPD adapter without coupling WorkRun to the
// provider's opaque admission-seal representation.
func (store WorkRunStore) WithEvidencePort(port EvidencePort) WorkRunStore {
	store.evidence = port
	return store
}

// WithAuthorityPorts wires provider-owned resolvers. The store never treats a
// hash-shaped caller value as proof that an owner artifact exists.
func (store WorkRunStore) WithAuthorityPorts(ports AuthorityPorts) WorkRunStore {
	store.authority = ports
	return store
}

// RepositoryRef returns the stable exact-worktree identity captured by the
// retained lease. Callers must not treat it as a substitute for live store
// validation performed by every read and mutation.
func (store WorkRunStore) RepositoryRef() string {
	if store.lease == nil {
		return ""
	}
	identity := store.lease.Identity()
	if store.validateCanonicalLocation() != nil ||
		store.Repo != identity.RepositoryRoot ||
		store.commonDir != identity.GitCommonDir ||
		store.lease.StorageKey() != store.storageKey ||
		identity.RepositoryRef != "sha256:"+store.storageKey {
		return ""
	}
	return identity.RepositoryRef
}

// Status is read-only. Opening or reading a missing run never creates its
// authority directory.
func (store WorkRunStore) Status() (WorkRunState, error) {
	replay, err := store.load(context.Background())
	if err != nil {
		return WorkRunState{}, err
	}
	if !replay.State.Started {
		return WorkRunState{}, ErrWorkRunNotStarted
	}
	return cloneWorkRunState(replay.State), nil
}

func (store WorkRunStore) Start(ctx context.Context, request StartRequest) (WorkRunState, error) {
	if request.ExpectedRevision != "" {
		return WorkRunState{}, errors.New("new work run expected revision must be empty")
	}
	if err := validateRequestID(request.RequestID); err != nil {
		return WorkRunState{}, err
	}
	if err := request.RouteDecision.Validate(); err != nil {
		return WorkRunState{}, err
	}
	if !validSHA256Ref(request.DeliveryIntentRef) {
		return WorkRunState{}, errors.New("work run start requires an immutable delivery intent reference")
	}
	if store.authority.PAD == nil {
		return WorkRunState{}, ErrAuthorityPortUnavailable
	}
	intent, err := store.authority.PAD.ResolveDeliveryIntent(ctx, request.DeliveryIntentRef)
	if err != nil {
		return WorkRunState{}, fmt.Errorf("resolve PAD delivery intent: %w", err)
	}
	if err := intent.Validate(request.DeliveryIntentRef); err != nil {
		return WorkRunState{}, err
	}
	if request.RouteDecision.ExplicitSDDRequestRef != "" {
		if store.authority.ExplicitSDDRequest == nil {
			return WorkRunState{}, ErrAuthorityPortUnavailable
		}
		explicitRequest, resolveErr :=
			store.authority.ExplicitSDDRequest.ResolveExplicitSDDRequest(
				ctx,
				request.RouteDecision.ExplicitSDDRequestRef,
			)
		if resolveErr != nil {
			return WorkRunState{}, fmt.Errorf("resolve explicit SDD request: %w", resolveErr)
		}
		if err := explicitRequest.Validate(
			request.RouteDecision.ExplicitSDDRequestRef,
			store.WorkRunID,
			request.DeliveryIntentRef,
		); err != nil {
			return WorkRunState{}, err
		}
	}
	digest, err := digestValue("gentle-ai.work-run-start-request/v1", request)
	if err != nil {
		return WorkRunState{}, err
	}
	return store.mutate(ctx, request.ExpectedRevision, request.RequestID, digest, func(replay workReplay) (workRunRecord, error) {
		if replay.State.Started {
			return workRunRecord{}, ErrWorkRunAlreadyStarted
		}
		event := workStartEvent{
			RouteDecision:     request.RouteDecision,
			DeliveryIntentRef: request.DeliveryIntentRef,
		}
		return workRunRecord{Operation: workOperationStart, Start: &event}, nil
	})
}

func (store WorkRunStore) AcceptSDD(ctx context.Context, request AcceptSDDRequest) (WorkRunState, error) {
	if err := validateMutationEnvelope(request.ExpectedRevision, request.RequestID); err != nil {
		return WorkRunState{}, err
	}
	if !validSHA256Ref(request.AcceptanceRef) {
		return WorkRunState{}, errors.New("SDD acceptance requires an immutable owner-decision reference")
	}
	digest, err := digestValue("gentle-ai.work-run-accept-sdd-request/v1", request)
	if err != nil {
		return WorkRunState{}, err
	}
	return store.mutate(ctx, request.ExpectedRevision, request.RequestID, digest, func(replay workReplay) (workRunRecord, error) {
		if store.authority.Route == nil {
			return workRunRecord{}, ErrAuthorityPortUnavailable
		}
		selection, err := store.authority.Route.ResolveRouteSelection(ctx, request.AcceptanceRef)
		if err != nil {
			return workRunRecord{}, fmt.Errorf("resolve SDD acceptance: %w", err)
		}
		if err := selection.Validate(
			request.AcceptanceRef,
			replay.State.RouteDecision.Digest,
			ImplementationRouteSDD,
			"",
		); err != nil {
			return workRunRecord{}, err
		}
		event := workAcceptSDDEvent{AcceptanceRef: request.AcceptanceRef}
		return workRunRecord{Operation: workOperationAcceptSDD, AcceptSDD: &event}, nil
	})
}

// Reroute records a new owner-approved decision after a proposal was declined.
// It never converts the existing propose_sdd decision into direct execution.
func (store WorkRunStore) Reroute(ctx context.Context, request RerouteRequest) (WorkRunState, error) {
	if err := validateMutationEnvelope(request.ExpectedRevision, request.RequestID); err != nil {
		return WorkRunState{}, err
	}
	if !validSHA256Ref(request.OwnerDecisionRef) {
		return WorkRunState{}, errors.New("reroute requires an immutable owner decision reference")
	}
	if err := request.RouteDecision.Validate(); err != nil {
		return WorkRunState{}, err
	}
	if request.RouteDecision.Decision != RouteDecisionDirectInline &&
		request.RouteDecision.Decision != RouteDecisionDelegatedDirect {
		return WorkRunState{}, errors.New("reroute must select a fresh direct or delegated decision")
	}
	digest, err := digestValue("gentle-ai.work-run-reroute-request/v1", request)
	if err != nil {
		return WorkRunState{}, err
	}
	return store.mutate(ctx, request.ExpectedRevision, request.RequestID, digest, func(replay workReplay) (workRunRecord, error) {
		if store.authority.Route == nil {
			return workRunRecord{}, ErrAuthorityPortUnavailable
		}
		selectedRoute := ImplementationRouteDirectInline
		if request.RouteDecision.Decision == RouteDecisionDelegatedDirect {
			selectedRoute = ImplementationRouteDelegatedDirect
		}
		selection, err := store.authority.Route.ResolveRouteSelection(ctx, request.OwnerDecisionRef)
		if err != nil {
			return workRunRecord{}, fmt.Errorf("resolve implementation reroute: %w", err)
		}
		if err := selection.Validate(
			request.OwnerDecisionRef,
			replay.State.RouteDecision.Digest,
			selectedRoute,
			request.RouteDecision.Digest,
		); err != nil {
			return workRunRecord{}, err
		}
		event := workRerouteEvent{
			PreviousDecisionDigest: replay.State.RouteDecision.Digest,
			OwnerDecisionRef:       request.OwnerDecisionRef, RouteDecision: request.RouteDecision,
		}
		return workRunRecord{Operation: workOperationReroute, Reroute: &event}, nil
	})
}

func (store WorkRunStore) BindSDDRun(ctx context.Context, request BindSDDRunRequest) (WorkRunState, error) {
	if err := validateMutationEnvelope(request.ExpectedRevision, request.RequestID); err != nil {
		return WorkRunState{}, err
	}
	if !validOpaqueRef(request.SDDRunRef) {
		return WorkRunState{}, errors.New("invalid SDD run reference")
	}
	digest, err := digestValue("gentle-ai.work-run-bind-sdd-request/v1", request)
	if err != nil {
		return WorkRunState{}, err
	}
	return store.mutate(ctx, request.ExpectedRevision, request.RequestID, digest, func(replay workReplay) (workRunRecord, error) {
		if !replay.State.Started ||
			replay.State.ImplementationRoute != ImplementationRouteSDD ||
			!validSHA256Ref(replay.State.RouteAcceptanceRef) ||
			replay.State.SDDRunRef != "" ||
			replay.State.Handoff != nil {
			return workRunRecord{}, fmt.Errorf(
				"%w: accepted SDD route is not ready for binding",
				ErrWorkRunInvalidTransition,
			)
		}
		if store.authority.SDD == nil {
			return workRunRecord{}, ErrAuthorityPortUnavailable
		}
		run, err := store.authority.SDD.ResolveRun(ctx, request.SDDRunRef)
		if err != nil {
			return workRunRecord{}, fmt.Errorf("resolve accepted SDD run: %w", err)
		}
		if err := run.Validate(
			request.SDDRunRef,
			replay.State.WorkRunID,
			replay.State.RouteAcceptanceRef,
		); err != nil {
			return workRunRecord{}, err
		}
		event := workBindSDDEvent{SDDRunRef: request.SDDRunRef}
		return workRunRecord{Operation: workOperationBindSDD, BindSDD: &event}, nil
	})
}

func (store WorkRunStore) BindImplementationHandoff(
	ctx context.Context,
	request BindImplementationHandoffRequest,
) (WorkRunState, error) {
	if err := validateMutationEnvelope(request.ExpectedRevision, request.RequestID); err != nil {
		return WorkRunState{}, err
	}
	if err := request.Handoff.Validate(); err != nil {
		return WorkRunState{}, err
	}
	if request.Handoff.Schema != ImplementationHandoffSchemaV2 {
		return WorkRunState{}, errors.New(
			"new implementation handoff requires owner-issued mutation completion",
		)
	}
	if _, err := store.resolveLiveMutationCompletion(
		ctx,
		request.Handoff,
	); err != nil {
		return WorkRunState{}, err
	}
	digest, err := digestValue("gentle-ai.work-run-bind-handoff-request/v1", request)
	if err != nil {
		return WorkRunState{}, err
	}
	return store.mutate(ctx, request.ExpectedRevision, request.RequestID, digest, func(replay workReplay) (workRunRecord, error) {
		event := request.Handoff
		return workRunRecord{Operation: workOperationBindHandoff, Handoff: &event}, nil
	})
}

// ReplanVerificationAfterCorrection replaces only the active verification
// subject after the native review kernel has consumed its one correction
// attempt. Route, SDD identity, reservation history, launch history, and
// ordinal budget remain in the same WorkRun.
func (store WorkRunStore) ReplanVerificationAfterCorrection(
	ctx context.Context,
	request ReplanVerificationAfterCorrectionRequest,
) (WorkRunState, error) {
	if err := validateMutationEnvelope(
		request.ExpectedRevision,
		request.RequestID,
	); err != nil {
		return WorkRunState{}, err
	}
	if err := request.CorrectedHandoff.Validate(); err != nil {
		return WorkRunState{}, err
	}
	if request.CorrectedHandoff.Schema != ImplementationHandoffSchemaV2 {
		return WorkRunState{}, errors.New(
			"corrected handoff requires owner-issued mutation completion",
		)
	}
	if _, err := store.resolveLiveMutationCompletion(
		ctx,
		request.CorrectedHandoff,
	); err != nil {
		return WorkRunState{}, err
	}
	digest, err := digestValue(
		"gentle-ai.work-run-replan-verification-request/v1",
		request,
	)
	if err != nil {
		return WorkRunState{}, err
	}
	return store.mutate(
		ctx,
		request.ExpectedRevision,
		request.RequestID,
		digest,
		func(replay workReplay) (workRunRecord, error) {
			state := replay.State
			if state.Handoff == nil ||
				state.Forecast == nil ||
				state.VerificationReplan != nil ||
				state.VerificationResultRef != "" ||
				state.PostVerificationSnapshotRef != "" ||
				state.VerificationStop != nil ||
				state.ReviewReceiptRef != "" ||
				state.DeliveryAuthorizationRef != "" {
				return workRunRecord{}, fmt.Errorf(
					"%w: verification is not eligible for correction replanning",
					ErrWorkRunInvalidTransition,
				)
			}
			corrected := request.CorrectedHandoff
			if corrected.Route != state.Handoff.Route ||
				corrected.Route != state.ImplementationRoute ||
				corrected.ScopeDigest != state.Handoff.ScopeDigest ||
				corrected.CandidateRef == state.Handoff.CandidateRef ||
				corrected.Subject == state.Handoff.Subject ||
				corrected.SDDRunRef != state.Handoff.SDDRunRef {
				return workRunRecord{}, errors.New(
					"corrected verification handoff changes route, scope, SDD identity, or keeps the old subject",
				)
			}
			replan, err := newVerificationReplan(
				1,
				*state.Handoff,
				*state.Forecast,
				corrected,
			)
			if err != nil {
				return workRunRecord{}, err
			}
			event := workVerificationReplanEvent{
				Handoff: corrected,
				Replan:  replan,
			}
			return workRunRecord{
				Operation: workOperationReplanVerification,
				Replan:    &event,
			}, nil
		},
	)
}

func (store WorkRunStore) RecordVerificationForecast(
	ctx context.Context,
	request RecordVerificationForecastRequest,
) (WorkRunState, error) {
	if err := validateMutationEnvelope(request.ExpectedRevision, request.RequestID); err != nil {
		return WorkRunState{}, err
	}
	if request.Input.WorkRunID != store.WorkRunID {
		return WorkRunState{}, errors.New("verification forecast work run does not match store")
	}
	if store.authority.Verification == nil {
		return WorkRunState{}, ErrAuthorityPortUnavailable
	}
	ownerForecast, err := store.authority.Verification.ResolveForecast(
		ctx,
		request.Input.AvailabilityRef,
	)
	if err != nil {
		return WorkRunState{}, fmt.Errorf("resolve owner verification forecast: %w", err)
	}
	if err := ownerForecast.MatchesInput(request.Input); err != nil {
		return WorkRunState{}, err
	}
	forecast, err := newVerificationForecast(request.Input)
	if err != nil {
		return WorkRunState{}, err
	}
	if err := forecast.MatchesPlan(request.Input.Applicability, request.Input.Registry, request.Input.Plan); err != nil {
		return WorkRunState{}, err
	}
	digest, err := digestValue("gentle-ai.work-run-record-forecast-request/v1", request)
	if err != nil {
		return WorkRunState{}, err
	}
	return store.mutate(ctx, request.ExpectedRevision, request.RequestID, digest, func(replay workReplay) (workRunRecord, error) {
		event := forecast
		return workRunRecord{Operation: workOperationRecordForecast, Forecast: &event}, nil
	})
}

func (store WorkRunStore) RecordVerificationDisposition(
	ctx context.Context,
	request RecordVerificationDispositionRequest,
) (WorkRunState, error) {
	if err := validateMutationEnvelope(request.ExpectedRevision, request.RequestID); err != nil {
		return WorkRunState{}, err
	}
	digest, err := digestValue("gentle-ai.work-run-record-disposition-request/v1", request)
	if err != nil {
		return WorkRunState{}, err
	}
	return store.mutate(ctx, request.ExpectedRevision, request.RequestID, digest, func(replay workReplay) (workRunRecord, error) {
		if replay.State.Forecast == nil {
			return workRunRecord{}, fmt.Errorf("%w: verification forecast is missing", ErrWorkRunInvalidTransition)
		}
		if store.authority.Verification == nil {
			return workRunRecord{}, ErrAuthorityPortUnavailable
		}
		ownerDisposition, err := store.authority.Verification.ResolveDisposition(
			ctx,
			request.DecisionRef,
		)
		if err != nil {
			return workRunRecord{}, fmt.Errorf("resolve owner verification disposition: %w", err)
		}
		if err := ownerDisposition.Validate(*replay.State.Forecast); err != nil {
			return workRunRecord{}, err
		}
		if ownerDisposition.Kind != request.Kind ||
			ownerDisposition.AssumptionsRef != request.AssumptionsRef ||
			ownerDisposition.ActorRef != request.ActorRef ||
			ownerDisposition.RunnerRef != request.RunnerRef {
			return workRunRecord{}, fmt.Errorf(
				"%w: verification disposition",
				ErrAuthorityBindingMismatch,
			)
		}
		disposition, err := newVerificationDisposition(
			*replay.State.Forecast, request.Kind, request.AssumptionsRef,
			request.ActorRef, request.DecisionRef, request.RunnerRef,
		)
		if err != nil {
			return workRunRecord{}, err
		}
		return workRunRecord{Operation: workOperationRecordDisposition, Disposition: &disposition}, nil
	})
}

// Begin atomically reserves the exact ticket, subject, plan,
// consent/automatic decision, availability observation, slot, and ordinal.
// Callers may launch HCR only after this method succeeds.
func (store WorkRunStore) Begin(
	ctx context.Context,
	request BeginRequest,
) (BeginOutcome, error) {
	if err := validateMutationEnvelope(request.ExpectedRevision, request.RequestID); err != nil {
		return BeginOutcome{}, err
	}
	if !validSHA256Ref(request.ActionTicketRef) {
		return BeginOutcome{}, errors.New("verification begin requires an immutable action ticket reference")
	}
	if err := reviewtransaction.ValidateVerificationPlan(request.Applicability, request.Registry, request.Plan); err != nil {
		return BeginOutcome{}, err
	}
	if store.evidence == nil {
		return BeginOutcome{}, ErrEvidencePortUnavailable
	}
	if store.authority.Verification == nil || store.authority.Launch == nil {
		return BeginOutcome{}, ErrAuthorityPortUnavailable
	}
	digest, err := digestValue("gentle-ai.work-run-begin-verification-request/v1", request)
	if err != nil {
		return BeginOutcome{}, err
	}
	state, err := store.mutate(ctx, request.ExpectedRevision, request.RequestID, digest, func(replay workReplay) (workRunRecord, error) {
		state := replay.State
		if state.VerificationResultRef != "" {
			return workRunRecord{}, fmt.Errorf(
				"%w: verification result is already terminal",
				ErrWorkRunInvalidTransition,
			)
		}
		if state.VerificationStop != nil {
			return workRunRecord{}, fmt.Errorf(
				"%w: verification is already stopped",
				ErrWorkRunInvalidTransition,
			)
		}
		if state.Handoff == nil {
			return workRunRecord{}, fmt.Errorf(
				"%w: implementation handoff is missing",
				ErrWorkRunInvalidTransition,
			)
		}
		if _, err := store.resolveLiveMutationCompletion(
			ctx,
			*state.Handoff,
		); err != nil {
			return workRunRecord{}, err
		}
		ticket, err := store.evidence.ReadActionTicket(ctx, request.ActionTicketRef)
		if err != nil {
			return workRunRecord{}, fmt.Errorf("read verification action ticket: %w", err)
		}
		if err := ticket.Validate(); err != nil {
			return workRunRecord{}, fmt.Errorf("validate verification action ticket: %w", err)
		}
		slotBindingRef := ticket.SlotBindingRef
		if state.Forecast == nil || state.Disposition == nil {
			return workRunRecord{}, fmt.Errorf("%w: forecast and disposition are required before begin", ErrWorkRunInvalidTransition)
		}
		ownerForecast, err := store.authority.Verification.ResolveForecast(
			ctx,
			state.Forecast.AvailabilityRef,
		)
		if err != nil {
			return workRunRecord{}, fmt.Errorf("resolve owner verification plan: %w", err)
		}
		if err := ownerForecast.MatchesInput(VerificationForecastInput{
			WorkRunID: state.WorkRunID, Handoff: *state.Handoff,
			Applicability: request.Applicability, Registry: request.Registry, Plan: request.Plan,
			PlanRevisionRef: state.Forecast.PlanRevisionRef,
			Availability:    state.Forecast.Availability,
			AvailabilityRef: state.Forecast.AvailabilityRef,
			DiagnosticRefs:  state.Forecast.DiagnosticRefs,
		}); err != nil {
			return workRunRecord{}, err
		}
		if err := state.Forecast.MatchesPlan(request.Applicability, request.Registry, request.Plan); err != nil {
			return workRunRecord{}, err
		}
		if err := state.Disposition.ValidateFor(*state.Forecast); err != nil {
			return workRunRecord{}, err
		}
		if state.Disposition.Kind != DispositionRun {
			return workRunRecord{}, fmt.Errorf("%w: disposition does not authorize launch", ErrWorkRunInvalidTransition)
		}
		obligation, ok := verificationObligationByID(request.Plan, ticket.Slot)
		if !ok {
			return workRunRecord{}, errors.New("action ticket slot is not an exact planned obligation")
		}
		if ticket.TicketRef != request.ActionTicketRef ||
			ticket.SubjectRef != state.Forecast.SubjectRef ||
			ticket.CandidateRef != state.Forecast.CandidateRef ||
			ticket.VerificationContextRef != state.Forecast.Digest ||
			ticket.ExpectedRevision != state.Forecast.PlanRevisionRef ||
			ticket.Capability != obligation.CapabilityRef {
			return workRunRecord{}, errors.New("action ticket does not bind the exact forecast and planned obligation")
		}
		for _, reservation := range state.Reservations {
			if reservation.ActionTicketRef == ticket.TicketRef ||
				reservation.SlotBindingRef == slotBindingRef ||
				reservation.ForecastDigest == state.Forecast.Digest &&
					reservation.Slot == ticket.Slot {
				return workRunRecord{}, ErrVerificationReserved
			}
		}
		reservation, err := newVerificationReservation(
			*state.Forecast, *state.Disposition, obligation.Cost,
			state.WorkRunID, state.Revision, ticket.TicketRef, slotBindingRef,
			ticket.Slot, ticket.Capability, state.NextOrdinal,
		)
		if err != nil {
			return workRunRecord{}, err
		}
		return workRunRecord{Operation: workOperationBeginVerification, Reservation: &reservation}, nil
	})
	if err != nil {
		return BeginOutcome{}, err
	}

	reservation, ok := reservationForTicket(state.Reservations, request.ActionTicketRef)
	if !ok {
		return BeginOutcome{State: state}, errors.New("committed verification reservation is missing")
	}
	liveState, err := store.Status()
	if err != nil {
		return BeginOutcome{State: state}, err
	}
	liveReservation, ok := reservationByRef(
		liveState.Reservations,
		reservation.ReservationRef,
	)
	if !ok ||
		liveReservation.ActionTicketRef != reservation.ActionTicketRef ||
		liveReservation.SlotBindingRef != reservation.SlotBindingRef {
		return BeginOutcome{State: state}, errors.New(
			"live WorkRun no longer contains the exact committed verification reservation",
		)
	}
	if liveState.VerificationResultRef != "" {
		return BeginOutcome{State: state}, fmt.Errorf(
			"%w: verification result is already terminal",
			ErrWorkRunInvalidTransition,
		)
	}
	if launchClaimExists(liveState.LaunchClaims, reservation.ReservationRef) {
		return BeginOutcome{State: state}, ErrVerificationLaunchClaimed
	}
	ticket, err := store.evidence.ReadActionTicket(ctx, request.ActionTicketRef)
	if err != nil {
		return BeginOutcome{State: state}, fmt.Errorf("re-read verification action ticket: %w", err)
	}
	if err := ticket.Validate(); err != nil {
		return BeginOutcome{State: state}, err
	}
	if state.ImplementationRoute == ImplementationRouteSDD {
		if store.authority.SDD == nil {
			return BeginOutcome{State: state}, ErrAuthorityPortUnavailable
		}
		binding := SDDReservationBinding{
			WorkRunID: state.WorkRunID, WorkRunRevision: reservation.ExpectedWorkRevision,
			SDDRunRef: state.SDDRunRef, ReservationRef: reservation.ReservationRef,
			ActionTicketRef: reservation.ActionTicketRef,
		}
		if err := binding.Validate(); err != nil {
			return BeginOutcome{State: state}, err
		}
		if err := store.authority.SDD.BindVerificationReservation(ctx, binding); err != nil {
			return BeginOutcome{State: state}, fmt.Errorf("bind reservation into SDD attempt: %w", err)
		}
	}
	launchBinding := hostruntime.LaunchBinding{
		Schema: hostruntime.LaunchBindingSchemaV1, WorkRunID: state.WorkRunID,
		ReservationRef: reservation.ReservationRef, ActionTicketRef: ticket.TicketRef,
		RequestDigest: ticket.HostRequestDigest,
	}
	claimRequestID, err := newLaunchClaimRequestID()
	if err != nil {
		return BeginOutcome{State: state}, err
	}
	capability, err := store.authority.Launch.ActivateLaunch(
		ctx,
		launchBinding,
		func(claimContext context.Context) error {
			return store.claimLaunch(claimContext, launchBinding, claimRequestID)
		},
	)
	if err != nil {
		return BeginOutcome{State: state}, fmt.Errorf("activate HCR launch capability: %w", err)
	}
	return BeginOutcome{State: state, Capability: capability}, nil
}

func (store WorkRunStore) BindVerificationResult(
	ctx context.Context,
	request BindVerificationResultRequest,
) (WorkRunState, error) {
	if err := validateMutationEnvelope(request.ExpectedRevision, request.RequestID); err != nil {
		return WorkRunState{}, err
	}
	if store.authority.Verification == nil {
		return WorkRunState{}, ErrAuthorityPortUnavailable
	}
	ownerResult, err := store.authority.Verification.ResolveResult(ctx, request.Result.ResultRef)
	if err != nil {
		return WorkRunState{}, fmt.Errorf("resolve owner verification result: %w", err)
	}
	if err := ownerResult.Validate(request.Result.ResultRef); err != nil {
		return WorkRunState{}, err
	}
	if !equalCanonicalValue(ownerResult.Applicability, request.Applicability) ||
		!equalCanonicalValue(ownerResult.Registry, request.Registry) ||
		!equalCanonicalValue(ownerResult.Plan, request.Plan) ||
		!equalCanonicalValue(ownerResult.Result, request.Result) {
		return WorkRunState{}, fmt.Errorf("%w: verification result preimage", ErrAuthorityBindingMismatch)
	}
	digest, err := digestValue("gentle-ai.work-run-bind-result-request/v1", request)
	if err != nil {
		return WorkRunState{}, err
	}
	var mutationErr error
	state, err := store.mutate(
		ctx,
		request.ExpectedRevision,
		request.RequestID,
		digest,
		func(replay workReplay) (workRunRecord, error) {
			state := replay.State
			if state.Forecast == nil || state.Handoff == nil {
				return workRunRecord{}, fmt.Errorf(
					"%w: verification forecast and handoff are missing",
					ErrWorkRunInvalidTransition,
				)
			}
			if state.VerificationResultRef != "" || state.VerificationStop != nil {
				return workRunRecord{}, fmt.Errorf(
					"%w: verification is already terminal",
					ErrWorkRunInvalidTransition,
				)
			}
			if err := state.Forecast.MatchesPlan(
				request.Applicability,
				request.Registry,
				request.Plan,
			); err != nil {
				return workRunRecord{}, err
			}
			completion, err := store.resolveMutationCompletion(
				ctx,
				*state.Handoff,
			)
			if err != nil {
				return workRunRecord{}, err
			}
			completionSubject, err :=
				reviewtransaction.VerificationSubjectFromSnapshot(
					completion.Snapshot,
				)
			if err != nil {
				return workRunRecord{}, err
			}
			if completionSubject != ownerResult.Result.Subject {
				return workRunRecord{}, fmt.Errorf(
					"%w: result subject differs from mutation completion",
					ErrAuthorityBindingMismatch,
				)
			}
			live, resnapshotErr := reviewtransaction.ResnapshotVerificationSubject(
				ctx,
				store.Repo,
				completion.Snapshot,
			)
			if resnapshotErr != nil {
				if !errors.Is(
					resnapshotErr,
					reviewtransaction.ErrVerificationSubjectMutated,
				) {
					return workRunRecord{}, resnapshotErr
				}
				stop, stopErr := newVerificationStop(
					VerificationStopMutated,
					request.Result.ResultRef,
					completion.Snapshot.Identity,
					live.Identity,
				)
				if stopErr != nil {
					return workRunRecord{}, stopErr
				}
				mutationErr = resnapshotErr
				event := workStopMutationEvent{Stop: stop}
				return workRunRecord{
					Operation:    workOperationStopMutation,
					StopMutation: &event,
				}, nil
			}
			if err := store.validateResultEvidence(
				ctx,
				state,
				request.Result,
			); err != nil {
				return workRunRecord{}, err
			}
			closureRef := ""
			reusable := []string{}
			if state.VerificationReplan != nil {
				switch request.Result.Aggregate {
				case reviewtransaction.VerificationAggregateComplete,
					reviewtransaction.VerificationAggregateNotRequired:
					closure, err := store.buildCorrectionImpactClosure(
						ctx,
						*state.VerificationReplan,
						request.Applicability,
						request.Registry,
						request.Plan,
						request.Result,
					)
					if err != nil {
						return workRunRecord{}, err
					}
					closureRef = closure.Digest
					reusable = reusableCorrectionObligations(closure)
				}
			}
			var stop *VerificationStop
			if request.Result.Aggregate ==
				reviewtransaction.VerificationAggregateFailed {
				value, err := newVerificationStop(
					VerificationStopFailed,
					request.Result.ResultRef,
					completion.Snapshot.Identity,
					live.Identity,
				)
				if err != nil {
					return workRunRecord{}, err
				}
				stop = &value
			}
			event := workBindResultEvent{
				ResultRef:                   request.Result.ResultRef,
				PostVerificationSnapshotRef: live.Identity,
				CorrectionImpactClosureRef:  closureRef,
				ReusableObligations:         reusable,
				Stop:                        stop,
			}
			return workRunRecord{
				Operation: workOperationBindResult,
				Result:    &event,
			}, nil
		},
	)
	if err != nil {
		return WorkRunState{}, err
	}
	if state.VerificationStop != nil &&
		state.VerificationStop.Reason == VerificationStopMutated &&
		state.VerificationStop.ResultRef == request.Result.ResultRef {
		if mutationErr != nil {
			return state, mutationErr
		}
		return state, fmt.Errorf(
			"%w: expected %s, observed %s",
			reviewtransaction.ErrVerificationSubjectMutated,
			state.VerificationStop.ExpectedSnapshotRef,
			state.VerificationStop.ObservedSnapshotRef,
		)
	}
	return state, nil
}

func (store WorkRunStore) BindReviewReceipt(
	ctx context.Context,
	request BindReviewReceiptRequest,
) (WorkRunState, error) {
	if err := validateMutationEnvelope(request.ExpectedRevision, request.RequestID); err != nil {
		return WorkRunState{}, err
	}
	if !validSHA256Ref(request.ReviewReceiptRef) {
		return WorkRunState{}, errors.New("review receipt must be an immutable SHA-256 reference")
	}
	if store.authority.Verification == nil {
		return WorkRunState{}, ErrAuthorityPortUnavailable
	}
	digest, err := digestValue("gentle-ai.work-run-bind-review-request/v1", request)
	if err != nil {
		return WorkRunState{}, err
	}
	return store.mutate(ctx, request.ExpectedRevision, request.RequestID, digest, func(replay workReplay) (workRunRecord, error) {
		state := replay.State
		if state.Handoff == nil ||
			state.VerificationResultRef == "" ||
			state.PostVerificationSnapshotRef == "" ||
			state.VerificationStop != nil {
			return workRunRecord{}, fmt.Errorf(
				"%w: exact converged result and candidate are required before review",
				ErrWorkRunInvalidTransition,
			)
		}
		result, err := store.resolveBoundVerificationResult(ctx, state)
		if err != nil {
			return workRunRecord{}, err
		}
		if result.Result.Subject.SnapshotIdentity !=
			state.PostVerificationSnapshotRef {
			return workRunRecord{}, fmt.Errorf(
				"%w: post-verification snapshot",
				ErrAuthorityBindingMismatch,
			)
		}
		if result.Result.Aggregate ==
			reviewtransaction.VerificationAggregateFailed {
			return workRunRecord{}, fmt.Errorf(
				"%w: failed verification stops before review freeze",
				ErrWorkRunInvalidTransition,
			)
		}
		receipt, err := store.authority.Verification.ResolveReviewReceipt(
			ctx,
			request.ReviewReceiptRef,
			state.VerificationResultRef,
		)
		if err != nil {
			return workRunRecord{}, fmt.Errorf("resolve terminal review receipt: %w", err)
		}
		if err := receipt.Validate(
			request.ReviewReceiptRef,
			state.Handoff.CandidateRef,
			state.VerificationResultRef,
		); err != nil {
			return workRunRecord{}, err
		}
		event := workBindReviewEvent{ReviewReceiptRef: request.ReviewReceiptRef}
		return workRunRecord{Operation: workOperationBindReview, Review: &event}, nil
	})
}

func (store WorkRunStore) BindDeliveryAuthorization(
	ctx context.Context,
	request BindDeliveryAuthorizationRequest,
) (WorkRunState, error) {
	if err := validateMutationEnvelope(request.ExpectedRevision, request.RequestID); err != nil {
		return WorkRunState{}, err
	}
	if !validSHA256Ref(request.AuthorizationRef) {
		return WorkRunState{}, errors.New(
			"delivery authorization must be an immutable SHA-256 reference",
		)
	}
	digest, err := digestValue(
		"gentle-ai.work-run-bind-delivery-authorization-request/v1",
		request,
	)
	if err != nil {
		return WorkRunState{}, err
	}
	return store.mutate(
		ctx,
		request.ExpectedRevision,
		request.RequestID,
		digest,
		func(replay workReplay) (workRunRecord, error) {
			state := replay.State
			if state.Handoff == nil ||
				state.VerificationResultRef == "" ||
				state.PostVerificationSnapshotRef == "" ||
				state.VerificationStop != nil ||
				state.ReviewReceiptRef == "" ||
				state.DeliveryAuthorizationRef != "" {
				return workRunRecord{}, fmt.Errorf(
					"%w: terminal candidate, result, and review are required before delivery authorization",
					ErrWorkRunInvalidTransition,
				)
			}
			result, err := store.resolveBoundVerificationResult(ctx, state)
			if err != nil {
				return workRunRecord{}, err
			}
			if store.authority.PAD == nil {
				return workRunRecord{}, ErrAuthorityPortUnavailable
			}
			authorization, err := store.authority.PAD.ResolveLiveDeliveryAuthorization(
				ctx,
				request.AuthorizationRef,
			)
			if err != nil {
				return workRunRecord{}, fmt.Errorf(
					"resolve live PAD delivery authorization: %w",
					err,
				)
			}
			if err := authorization.Validate(
				request.AuthorizationRef,
				state,
			); err != nil {
				return workRunRecord{}, err
			}
			if state.DeliveryRouteReevaluationRef != "" {
				if store.authority.DeliveryRoute == nil {
					return workRunRecord{}, ErrAuthorityPortUnavailable
				}
				reevaluation, err := store.authority.DeliveryRoute.
					ResolveDeliveryRouteReevaluation(
						ctx,
						state.DeliveryRouteReevaluationRef,
					)
				if err != nil {
					return workRunRecord{}, fmt.Errorf(
						"resolve bound PAD delivery route reevaluation: %w",
						err,
					)
				}
				if err := reevaluation.validateTargetBinding(
					state.DeliveryRouteReevaluationRef,
					state,
				); err != nil {
					return workRunRecord{}, err
				}
				if reevaluation.RepositoryRef != store.RepositoryRef() ||
					authorization.DecisionRef != reevaluation.TargetDecisionRef {
					return workRunRecord{}, fmt.Errorf(
						"%w: delivery authorization route decision",
						ErrAuthorityBindingMismatch,
					)
				}
			}
			if err := validateAuthorizedDeliveryAggregate(
				authorization.Kind,
				result.Result.Aggregate,
			); err != nil {
				return workRunRecord{}, err
			}
			event := workBindDeliveryAuthorizationEvent{
				AuthorizationRef: request.AuthorizationRef,
			}
			return workRunRecord{
				Operation: workOperationBindDelivery,
				Delivery:  &event,
			}, nil
		},
	)
}

// BindDeliveryRouteReevaluation advances only PAD's delivery intent after the
// exact terminal content has already been frozen. The implementation route,
// handoff, MMI completion, verification result, and review receipt remain
// untouched.
func (store WorkRunStore) BindDeliveryRouteReevaluation(
	ctx context.Context,
	request BindDeliveryRouteReevaluationRequest,
) (WorkRunState, error) {
	if err := validateMutationEnvelope(request.ExpectedRevision, request.RequestID); err != nil {
		return WorkRunState{}, err
	}
	if !validSHA256Ref(request.ReevaluationRef) {
		return WorkRunState{}, errors.New(
			"delivery route reevaluation must be an immutable SHA-256 reference",
		)
	}
	digest, err := digestValue(
		"gentle-ai.work-run-bind-delivery-route-reevaluation-request/v1",
		request,
	)
	if err != nil {
		return WorkRunState{}, err
	}
	return store.mutate(
		ctx,
		request.ExpectedRevision,
		request.RequestID,
		digest,
		func(replay workReplay) (workRunRecord, error) {
			state := replay.State
			if state.Handoff == nil ||
				state.VerificationResultRef == "" ||
				state.PostVerificationSnapshotRef == "" ||
				state.VerificationStop != nil ||
				state.ReviewReceiptRef == "" ||
				state.DeliveryRouteReevaluationRef != "" ||
				state.DeliveryAuthorizationRef != "" {
				return workRunRecord{}, fmt.Errorf(
					"%w: exact terminal content is required before delivery route reevaluation",
					ErrWorkRunInvalidTransition,
				)
			}
			if store.authority.DeliveryRoute == nil {
				return workRunRecord{}, ErrAuthorityPortUnavailable
			}
			authority, err := store.authority.DeliveryRoute.
				ResolveDeliveryRouteReevaluation(
					ctx,
					request.ReevaluationRef,
				)
			if err != nil {
				return workRunRecord{}, fmt.Errorf(
					"resolve PAD delivery route reevaluation: %w",
					err,
				)
			}
			if err := authority.Validate(request.ReevaluationRef, state); err != nil {
				return workRunRecord{}, err
			}
			if authority.RepositoryRef != store.RepositoryRef() {
				return workRunRecord{}, fmt.Errorf(
					"%w: delivery route reevaluation repository",
					ErrAuthorityBindingMismatch,
				)
			}
			completion, err := store.resolveMutationCompletion(ctx, *state.Handoff)
			if err != nil {
				return workRunRecord{}, err
			}
			if completion.CompletionRef != state.Handoff.MutationCompletionRef ||
				completion.Snapshot.Identity != authority.CandidateRef {
				return workRunRecord{}, fmt.Errorf(
					"%w: delivery route reevaluation MMI completion",
					ErrAuthorityBindingMismatch,
				)
			}
			event := workBindDeliveryRouteEvent{
				ReevaluationRef:         request.ReevaluationRef,
				SourceDeliveryIntentRef: authority.SourceDeliveryIntentRef,
				TargetDeliveryIntentRef: authority.TargetDeliveryIntentRef,
			}
			return workRunRecord{
				Operation:     workOperationBindDeliveryRoute,
				DeliveryRoute: &event,
			}, nil
		},
	)
}

func (store WorkRunStore) mutate(
	ctx context.Context,
	expectedRevision string,
	requestID string,
	requestDigest string,
	build func(workReplay) (workRunRecord, error),
) (WorkRunState, error) {
	if err := ctx.Err(); err != nil {
		return WorkRunState{}, err
	}
	if err := store.validateContext(ctx); err != nil {
		return WorkRunState{}, err
	}
	if err := store.ensureDirectories(ctx); err != nil {
		return WorkRunState{}, err
	}
	if err := store.validateContext(ctx); err != nil {
		return WorkRunState{}, err
	}
	directoryIdentity, err := openWorkRunDirectoryIdentity(store.Dir)
	if err != nil {
		return WorkRunState{}, fmt.Errorf("open work run authority identity: %w", err)
	}
	defer directoryIdentity.Close()
	recordsIdentity, err := openWorkRunDirectoryIdentity(filepath.Join(store.Dir, "records"))
	if err != nil {
		return WorkRunState{}, fmt.Errorf("open work run records identity: %w", err)
	}
	defer recordsIdentity.Close()

	if err := store.validateContext(ctx); err != nil {
		return WorkRunState{}, err
	}
	lock, err := reviewtransaction.AcquireAuthorityFileLock(filepath.Join(store.Dir, "LOCK"))
	if err != nil {
		if errors.Is(err, reviewtransaction.ErrConcurrentUpdate) {
			return WorkRunState{}, fmt.Errorf("%w: %v", ErrWorkRunConcurrentUpdate, err)
		}
		return WorkRunState{}, err
	}
	defer lock.Release()
	if err := store.validateContext(ctx); err != nil {
		return WorkRunState{}, err
	}
	if err := validateWorkRunDirectoryIdentities(directoryIdentity, recordsIdentity); err != nil {
		return WorkRunState{}, err
	}

	replay, err := store.load(ctx)
	if err != nil {
		return WorkRunState{}, err
	}
	if err := validateWorkRunDirectoryIdentities(directoryIdentity, recordsIdentity); err != nil {
		return WorkRunState{}, err
	}
	if receipt, exists := replay.Requests[requestID]; exists {
		if receipt.Digest != requestDigest {
			return WorkRunState{}, ErrWorkRunRequestConflict
		}
		historical, exists := replay.States[receipt.Revision]
		if !exists || historical.Revision != receipt.Revision {
			return WorkRunState{}, errors.New(
				"work run replay receipt has no validated historical state",
			)
		}
		if err := store.syncReplay(ctx); err != nil {
			return WorkRunState{}, &PublicationError{
				Revision: receipt.Revision, Committed: true, Cause: err,
			}
		}
		if err := validateWorkRunDirectoryIdentities(directoryIdentity, recordsIdentity); err != nil {
			return WorkRunState{}, &PublicationError{
				Revision: receipt.Revision, Committed: true, Cause: err,
			}
		}
		if err := store.validateContext(ctx); err != nil {
			return WorkRunState{}, err
		}
		return cloneWorkRunState(historical), nil
	}
	if replay.State.Revision != expectedRevision {
		return WorkRunState{}, &RevisionConflictError{
			Expected: expectedRevision, Current: replay.State.Revision,
		}
	}
	record, err := build(replay)
	if err != nil {
		return WorkRunState{}, err
	}
	record.Schema = workRunRecordSchemaV1
	record.WorkRunID = store.WorkRunID
	record.PreviousRevision = expectedRevision
	record.RequestID = requestID
	record.RequestDigest = requestDigest
	if err := validateWorkRunRecordShape(record); err != nil {
		return WorkRunState{}, err
	}
	candidate := cloneWorkRunState(replay.State)
	if err := applyWorkRunRecord(&candidate, record); err != nil {
		return WorkRunState{}, err
	}
	if err := store.validateContext(ctx); err != nil {
		return WorkRunState{}, err
	}
	return store.commitRecordLocked(ctx, record, directoryIdentity, recordsIdentity)
}

func (store WorkRunStore) commitRecordLocked(
	ctx context.Context,
	record workRunRecord,
	directoryIdentity *workRunDirectoryIdentity,
	recordsIdentity *workRunDirectoryIdentity,
) (WorkRunState, error) {
	revision, payload, err := workRunRecordRevision(record)
	if err != nil {
		return WorkRunState{}, err
	}
	if err := validateWorkRunDirectoryIdentities(directoryIdentity, recordsIdentity); err != nil {
		return WorkRunState{}, err
	}
	if err := store.validateContext(ctx); err != nil {
		return WorkRunState{}, err
	}
	if err := store.publishRecord(revision, payload); err != nil {
		return WorkRunState{}, err
	}
	if err := store.validateContext(ctx); err != nil {
		return WorkRunState{}, fmt.Errorf(
			"WorkRun repository identity changed after immutable record publication: %w",
			err,
		)
	}
	if err := validateWorkRunDirectoryIdentities(directoryIdentity, recordsIdentity); err != nil {
		return WorkRunState{}, fmt.Errorf(
			"work run authority changed after immutable record publication and before HEAD: %w",
			err,
		)
	}
	if err := store.validateContext(ctx); err != nil {
		return WorkRunState{}, err
	}
	if err := store.publishHead(revision); err != nil {
		return WorkRunState{}, err
	}
	if err := store.validateContext(ctx); err != nil {
		return WorkRunState{}, &PublicationError{
			Revision: revision, Committed: true, Cause: err,
		}
	}
	if err := validateWorkRunDirectoryIdentities(directoryIdentity, recordsIdentity); err != nil {
		return WorkRunState{}, &PublicationError{
			Revision: revision, Committed: true, Cause: err,
		}
	}
	if err := reviewtransaction.SyncReviewDirectory(store.Dir); err != nil {
		return WorkRunState{}, &PublicationError{
			Revision: revision, Committed: true,
			Cause: fmt.Errorf("sync work run HEAD directory: %w", err),
		}
	}
	committed, err := store.load(ctx)
	if err != nil {
		return WorkRunState{}, &PublicationError{
			Revision: revision, Committed: true,
			Cause: fmt.Errorf("replay committed work run: %w", err),
		}
	}
	if committed.State.Revision != revision {
		return WorkRunState{}, &PublicationError{
			Revision: revision, Committed: true,
			Cause: errors.New("committed work run did not replay to candidate revision"),
		}
	}
	if err := validateWorkRunDirectoryIdentities(directoryIdentity, recordsIdentity); err != nil {
		return WorkRunState{}, &PublicationError{
			Revision: revision, Committed: true, Cause: err,
		}
	}
	if err := store.validateContext(ctx); err != nil {
		return WorkRunState{}, &PublicationError{
			Revision: revision, Committed: true, Cause: err,
		}
	}
	return cloneWorkRunState(committed.State), nil
}

func validateWorkRunDirectoryIdentities(identities ...*workRunDirectoryIdentity) error {
	for _, identity := range identities {
		if err := identity.Validate(); err != nil {
			return fmt.Errorf("work run authority directory changed during mutation: %w", err)
		}
	}
	return nil
}

func (store WorkRunStore) claimLaunch(
	ctx context.Context,
	binding hostruntime.LaunchBinding,
	requestID string,
) error {
	if err := binding.Validate(); err != nil {
		return err
	}
	if binding.WorkRunID != store.WorkRunID {
		return errors.New("host launch binding work run does not match store")
	}
	if err := validateRequestID(requestID); err != nil {
		return err
	}
	current, err := store.Status()
	if err != nil {
		return err
	}
	if launchClaimExists(current.LaunchClaims, binding.ReservationRef) {
		return ErrVerificationLaunchClaimed
	}
	claim, err := newVerificationLaunchClaim(
		binding.ReservationRef,
		binding.ActionTicketRef,
		binding.RequestDigest,
	)
	if err != nil {
		return err
	}
	requestDigest, err := digestValue("gentle-ai.work-run-claim-launch-request/v1", claim)
	if err != nil {
		return err
	}
	_, err = store.mutate(
		ctx,
		current.Revision,
		requestID,
		requestDigest,
		func(replay workReplay) (workRunRecord, error) {
			reservation, ok := reservationByRef(
				replay.State.Reservations,
				binding.ReservationRef,
			)
			if !ok || reservation.ActionTicketRef != binding.ActionTicketRef {
				return workRunRecord{}, errors.New("host launch does not bind a durable reservation")
			}
			if launchClaimExists(replay.State.LaunchClaims, binding.ReservationRef) {
				return workRunRecord{}, ErrVerificationLaunchClaimed
			}
			event := claim
			return workRunRecord{Operation: workOperationClaimLaunch, Launch: &event}, nil
		},
	)
	return err
}

func newLaunchClaimRequestID() (string, error) {
	var nonce [16]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		return "", fmt.Errorf("generate launch claim request identifier: %w", err)
	}
	return "claim-" + hex.EncodeToString(nonce[:]), nil
}

func (store WorkRunStore) load(
	ctx context.Context,
) (result workReplay, resultErr error) {
	replay := workReplay{
		State: WorkRunState{
			Schema: WorkRunStateSchemaV1, WorkRunID: store.WorkRunID,
			Reservations:                    []VerificationReservation{},
			LaunchClaims:                    []VerificationLaunchClaim{},
			ReusableVerificationObligations: []string{},
			NextOrdinal:                     1,
		},
		Requests: map[string]workRequestReceipt{},
		States:   map[string]WorkRunState{},
	}
	if err := store.validateContext(ctx); err != nil {
		return workReplay{}, err
	}
	defer func() {
		if err := store.validateContext(ctx); err != nil {
			result = workReplay{}
			resultErr = err
		}
	}()
	if err := store.validateExistingAuthorityPath(); err != nil {
		return workReplay{}, err
	}
	head, exists, err := readWorkRunHead(filepath.Join(store.Dir, "HEAD"))
	if err != nil || !exists {
		return replay, err
	}

	type revisionRecord struct {
		revision string
		record   workRunRecord
	}
	reverse := make([]revisionRecord, 0, 16)
	seen := make(map[string]struct{})
	for revision := head; revision != ""; {
		if len(reverse) >= maximumWorkRunChainRecords {
			return workReplay{}, errors.New("work run chain exceeds the bounded record count")
		}
		if _, duplicate := seen[revision]; duplicate {
			return workReplay{}, errors.New("work run chain contains a revision cycle")
		}
		seen[revision] = struct{}{}
		record, err := store.loadRecord(revision)
		if err != nil {
			return workReplay{}, err
		}
		reverse = append(reverse, revisionRecord{revision: revision, record: record})
		revision = record.PreviousRevision
	}

	for index := len(reverse) - 1; index >= 0; index-- {
		entry := reverse[index]
		if entry.record.PreviousRevision != replay.State.Revision {
			return workReplay{}, errors.New("work run chain has a broken predecessor")
		}
		if _, duplicate := replay.Requests[entry.record.RequestID]; duplicate {
			return workReplay{}, errors.New("work run chain contains a duplicate request identifier")
		}
		if err := applyWorkRunRecord(&replay.State, entry.record); err != nil {
			return workReplay{}, fmt.Errorf("apply work run revision %s: %w", entry.revision, err)
		}
		replay.State.Revision = entry.revision
		replay.Requests[entry.record.RequestID] = workRequestReceipt{
			Digest: entry.record.RequestDigest, Revision: entry.revision,
		}
		replay.States[entry.revision] = cloneWorkRunState(replay.State)
	}
	if replay.State.Revision != head {
		return workReplay{}, errors.New("work run replay did not reach HEAD")
	}
	return replay, nil
}

func (store WorkRunStore) validateExistingAuthorityPath() error {
	if err := store.validateCanonicalLocation(); err != nil {
		return err
	}
	relative, err := filepath.Rel(store.commonDir, filepath.Join(store.Dir, "records"))
	if err != nil || relative == "." || relative == ".." || filepath.IsAbs(relative) ||
		strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return errors.New("work run authority escapes the Git common directory")
	}
	current := store.commonDir
	for index, segment := range strings.Split(relative, string(filepath.Separator)) {
		if segment == "" || segment == "." || segment == ".." {
			return errors.New("work run authority contains an invalid path segment")
		}
		current = filepath.Join(current, segment)
		info, err := os.Lstat(current)
		if os.IsNotExist(err) {
			return store.validateExistingRepositoryBinding()
		}
		if err != nil {
			return err
		}
		if workRunPathUnsafe(current, info) || !info.IsDir() {
			return errors.New("work run authority path is not a directory")
		}
		if index > 0 {
			if err := validatePrivateWorkRunDirectory(current); err != nil {
				return fmt.Errorf("validate private work run authority directory: %w", err)
			}
		}
	}
	return store.validateExistingRepositoryBinding()
}

func (store WorkRunStore) validateCanonicalLocation() error {
	if !workRunIDPattern.MatchString(store.WorkRunID) ||
		store.WorkRunID != store.boundWorkRunID {
		return errors.New("work run store has invalid identifier")
	}
	commonDir := filepath.Clean(store.commonDir)
	if commonDir == "." || !filepath.IsAbs(commonDir) ||
		commonDir != store.commonDir ||
		!workRunStorageKeyPattern.MatchString(store.storageKey) {
		return errors.New("work run Git common directory is invalid")
	}
	expectedRepositoryDir := filepath.Join(
		commonDir,
		"gentle-ai",
		"work-runs",
		"v1",
		"repositories",
		store.storageKey,
	)
	expected := filepath.Join(expectedRepositoryDir, store.boundWorkRunID)
	if store.repositoryDir != expectedRepositoryDir ||
		store.canonicalDir != expected ||
		store.Dir != expected ||
		filepath.Clean(store.Dir) != store.Dir {
		return errors.New("work run store directory is not its canonical authority path")
	}
	return nil
}

func (store WorkRunStore) validateContext(ctx context.Context) error {
	if ctx == nil {
		return errors.New("work run context is nil")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if store.lease == nil {
		return errors.New("work run repository identity lease is unavailable")
	}
	identity := store.lease.Identity()
	if !workRunStorageKeyPattern.MatchString(store.storageKey) ||
		store.lease.StorageKey() != store.storageKey ||
		identity.RepositoryRef != "sha256:"+store.storageKey ||
		!validSHA256Ref(identity.RepositoryRef) ||
		!cleanAbsoluteWorkRunPath(identity.RepositoryRoot) ||
		!cleanAbsoluteWorkRunPath(identity.GitCommonDir) ||
		!cleanAbsoluteWorkRunPath(identity.GitDir) ||
		store.Repo != identity.RepositoryRoot ||
		store.commonDir != identity.GitCommonDir {
		return errors.New("work run repository identity binding is invalid")
	}
	if err := store.validateCanonicalLocation(); err != nil {
		return err
	}
	if err := store.lease.Validate(ctx); err != nil {
		return fmt.Errorf("validate WorkRun repository identity: %w", err)
	}
	return nil
}

func cleanAbsoluteWorkRunPath(path string) bool {
	return path != "" && filepath.IsAbs(path) && filepath.Clean(path) == path
}

func (store WorkRunStore) expectedRepositoryBinding() (
	workRunRepositoryBinding,
	error,
) {
	if store.lease == nil {
		return workRunRepositoryBinding{}, errors.New(
			"work run repository identity lease is unavailable",
		)
	}
	identity := store.lease.Identity()
	binding := workRunRepositoryBinding{
		Schema: workRunRepositoryBindingSchemaV1, StorageKey: store.storageKey,
		RepositoryRef: identity.RepositoryRef, RepositoryRoot: identity.RepositoryRoot,
		GitCommonDir: identity.GitCommonDir, GitDir: identity.GitDir,
	}
	if err := binding.validate(); err != nil {
		return workRunRepositoryBinding{}, err
	}
	return binding, nil
}

func (binding workRunRepositoryBinding) validate() error {
	if binding.Schema != workRunRepositoryBindingSchemaV1 ||
		!workRunStorageKeyPattern.MatchString(binding.StorageKey) ||
		binding.RepositoryRef != "sha256:"+binding.StorageKey ||
		!validSHA256Ref(binding.RepositoryRef) ||
		!cleanAbsoluteWorkRunPath(binding.RepositoryRoot) ||
		!cleanAbsoluteWorkRunPath(binding.GitCommonDir) ||
		!cleanAbsoluteWorkRunPath(binding.GitDir) {
		return errors.New("work run repository binding is invalid")
	}
	return nil
}

func (store WorkRunStore) validateExistingRepositoryBinding() error {
	info, err := os.Lstat(store.repositoryDir)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	if workRunPathUnsafe(store.repositoryDir, info) || !info.IsDir() {
		return errors.New("work run repository shard is not a directory")
	}
	if err := validatePrivateWorkRunDirectory(store.repositoryDir); err != nil {
		return fmt.Errorf("validate private work run repository shard: %w", err)
	}
	path := filepath.Join(store.repositoryDir, "repository-binding.json")
	payload, err := readBoundedPrivateWorkRunFile(path, maximumWorkRunRecordBytes)
	if err != nil {
		if os.IsNotExist(err) {
			return errors.New("work run repository shard has no immutable binding")
		}
		return fmt.Errorf("read work run repository binding: %w", err)
	}
	binding, err := decodeWorkRunRepositoryBinding(payload)
	if err != nil {
		return err
	}
	expected, err := store.expectedRepositoryBinding()
	if err != nil {
		return err
	}
	if binding != expected {
		return errors.New("work run repository binding does not match its identity lease")
	}
	return nil
}

func decodeWorkRunRepositoryBinding(payload []byte) (
	workRunRepositoryBinding,
	error,
) {
	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.DisallowUnknownFields()
	var binding workRunRepositoryBinding
	if err := decoder.Decode(&binding); err != nil {
		return workRunRepositoryBinding{}, fmt.Errorf(
			"decode work run repository binding: %w",
			err,
		)
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return workRunRepositoryBinding{}, errors.New(
			"work run repository binding contains multiple JSON values",
		)
	}
	if err := binding.validate(); err != nil {
		return workRunRepositoryBinding{}, err
	}
	canonical, err := json.Marshal(binding)
	if err != nil {
		return workRunRepositoryBinding{}, err
	}
	canonical = append(canonical, '\n')
	if !bytes.Equal(payload, canonical) {
		return workRunRepositoryBinding{}, errors.New(
			"work run repository binding is not canonical",
		)
	}
	return binding, nil
}

func applyWorkRunRecord(state *WorkRunState, record workRunRecord) error {
	if err := validateWorkRunRecordShape(record); err != nil {
		return err
	}
	if record.WorkRunID != state.WorkRunID {
		return errors.New("work run record identifier does not match store")
	}
	switch record.Operation {
	case workOperationStart:
		if state.Started || state.Revision != "" {
			return ErrWorkRunAlreadyStarted
		}
		if err := record.Start.RouteDecision.Validate(); err != nil {
			return err
		}
		if !validSHA256Ref(record.Start.DeliveryIntentRef) {
			return errors.New("work run start has invalid delivery intent reference")
		}
		state.Started = true
		state.RouteDecision = record.Start.RouteDecision
		state.DeliveryIntentRef = record.Start.DeliveryIntentRef
		switch record.Start.RouteDecision.Decision {
		case RouteDecisionDirectInline:
			state.ImplementationRoute = ImplementationRouteDirectInline
		case RouteDecisionDelegatedDirect:
			state.ImplementationRoute = ImplementationRouteDelegatedDirect
		case RouteDecisionProposeSDD:
			if record.Start.RouteDecision.ExplicitSDDRequestRef != "" {
				state.ImplementationRoute = ImplementationRouteSDD
				state.RouteAcceptanceRef = record.Start.RouteDecision.ExplicitSDDRequestRef
			}
		}
	case workOperationAcceptSDD:
		if !state.Started || state.RouteDecision.Decision != RouteDecisionProposeSDD ||
			state.ImplementationRoute != "" || state.RouteAcceptanceRef != "" ||
			state.Handoff != nil {
			return fmt.Errorf("%w: SDD proposal is not awaiting acceptance", ErrWorkRunInvalidTransition)
		}
		if !validSHA256Ref(record.AcceptSDD.AcceptanceRef) {
			return errors.New("work run SDD acceptance reference is invalid")
		}
		state.ImplementationRoute = ImplementationRouteSDD
		state.RouteAcceptanceRef = record.AcceptSDD.AcceptanceRef
	case workOperationReroute:
		if !state.Started || state.RouteDecision.Decision != RouteDecisionProposeSDD ||
			state.ImplementationRoute != "" || state.RouteAcceptanceRef != "" ||
			state.Handoff != nil {
			return fmt.Errorf("%w: SDD proposal is not eligible for reroute", ErrWorkRunInvalidTransition)
		}
		if record.Reroute.PreviousDecisionDigest != state.RouteDecision.Digest ||
			!validSHA256Ref(record.Reroute.OwnerDecisionRef) {
			return errors.New("work run reroute does not bind the pending owner decision")
		}
		if err := record.Reroute.RouteDecision.Validate(); err != nil {
			return err
		}
		switch record.Reroute.RouteDecision.Decision {
		case RouteDecisionDirectInline:
			state.ImplementationRoute = ImplementationRouteDirectInline
		case RouteDecisionDelegatedDirect:
			state.ImplementationRoute = ImplementationRouteDelegatedDirect
		default:
			return errors.New("work run reroute must produce an executable non-SDD decision")
		}
		state.RouteDecision = record.Reroute.RouteDecision
		state.RouteAcceptanceRef = record.Reroute.OwnerDecisionRef
	case workOperationBindSDD:
		if !state.Started || state.ImplementationRoute != ImplementationRouteSDD ||
			!validSHA256Ref(state.RouteAcceptanceRef) || state.SDDRunRef != "" ||
			state.Handoff != nil {
			return fmt.Errorf("%w: accepted SDD route is not ready for binding", ErrWorkRunInvalidTransition)
		}
		if !validOpaqueRef(record.BindSDD.SDDRunRef) {
			return errors.New("work run SDD reference is invalid")
		}
		state.SDDRunRef = record.BindSDD.SDDRunRef
	case workOperationBindHandoff:
		if !state.Started || state.ImplementationRoute == "" {
			return ErrWorkRunRoutePending
		}
		if state.Handoff != nil {
			return fmt.Errorf("%w: implementation handoff is already bound", ErrWorkRunInvalidTransition)
		}
		if err := record.Handoff.Validate(); err != nil {
			return err
		}
		if record.Handoff.Route != state.ImplementationRoute {
			return errors.New("implementation handoff route differs from selected route")
		}
		if state.ImplementationRoute == ImplementationRouteSDD {
			if state.SDDRunRef == "" || record.Handoff.SDDRunRef != state.SDDRunRef {
				return errors.New("SDD handoff does not bind the accepted SDD run")
			}
		}
		state.Handoff = cloneHandoff(record.Handoff)
		state.ReusableVerificationObligations = []string{}
	case workOperationReplanVerification:
		if state.Handoff == nil ||
			state.Forecast == nil ||
			state.VerificationReplan != nil ||
			state.VerificationResultRef != "" ||
			state.PostVerificationSnapshotRef != "" ||
			state.VerificationStop != nil ||
			state.ReviewReceiptRef != "" ||
			state.DeliveryAuthorizationRef != "" {
			return fmt.Errorf(
				"%w: verification is not eligible for correction replanning",
				ErrWorkRunInvalidTransition,
			)
		}
		if err := record.Replan.Handoff.Validate(); err != nil {
			return err
		}
		if err := record.Replan.Replan.Validate(); err != nil {
			return err
		}
		if record.Replan.Handoff.Route != state.Handoff.Route ||
			record.Replan.Handoff.ScopeDigest != state.Handoff.ScopeDigest ||
			record.Replan.Handoff.CandidateRef == state.Handoff.CandidateRef ||
			record.Replan.Handoff.SDDRunRef != state.Handoff.SDDRunRef ||
			record.Replan.Replan.PreviousHandoffDigest != state.Handoff.Digest ||
			record.Replan.Replan.PreviousForecastDigest != state.Forecast.Digest ||
			record.Replan.Replan.OriginalApplicabilityDigest !=
				state.Forecast.ApplicabilityDigest ||
			record.Replan.Replan.OriginalRegistryDigest !=
				state.Forecast.RegistryDigest ||
			record.Replan.Replan.OriginalPlanDigest != state.Forecast.PlanDigest ||
			record.Replan.Replan.OriginalAvailabilityRef !=
				state.Forecast.AvailabilityRef ||
			record.Replan.Replan.CorrectedHandoffDigest !=
				record.Replan.Handoff.Digest ||
			record.Replan.Replan.MutationCompletionRef !=
				record.Replan.Handoff.MutationCompletionRef {
			return errors.New(
				"verification replan does not bind the exact prior and corrected subjects",
			)
		}
		state.Handoff = cloneHandoff(&record.Replan.Handoff)
		replan := record.Replan.Replan
		state.VerificationReplan = &replan
		state.Forecast = nil
		state.Disposition = nil
		state.VerificationResultRef = ""
		state.PostVerificationSnapshotRef = ""
		state.CorrectionImpactClosureRef = ""
		state.ReusableVerificationObligations = []string{}
		state.VerificationStop = nil
		state.ReviewReceiptRef = ""
		state.DeliveryAuthorizationRef = ""
	case workOperationRecordForecast:
		if state.Handoff == nil {
			return fmt.Errorf("%w: implementation handoff is missing", ErrWorkRunInvalidTransition)
		}
		if state.Forecast != nil {
			return fmt.Errorf("%w: verification forecast is already bound", ErrWorkRunInvalidTransition)
		}
		if err := record.Forecast.Validate(); err != nil {
			return err
		}
		if record.Forecast.WorkRunID != state.WorkRunID ||
			record.Forecast.HandoffDigest != state.Handoff.Digest ||
			record.Forecast.CandidateRef != state.Handoff.CandidateRef {
			return errors.New("verification forecast does not bind the current handoff")
		}
		state.Forecast = cloneForecast(record.Forecast)
	case workOperationRecordDisposition:
		if state.Forecast == nil ||
			hasReservationForForecast(
				state.Reservations,
				state.Forecast.Digest,
			) ||
			state.VerificationResultRef != "" {
			return fmt.Errorf("%w: forecast is not eligible for a disposition", ErrWorkRunInvalidTransition)
		}
		if err := record.Disposition.ValidateFor(*state.Forecast); err != nil {
			return err
		}
		state.Disposition = cloneDisposition(record.Disposition)
	case workOperationBeginVerification:
		if state.Forecast == nil || state.Disposition == nil ||
			state.VerificationResultRef != "" {
			return fmt.Errorf("%w: verification cannot begin in the current state", ErrWorkRunInvalidTransition)
		}
		if err := record.Reservation.Validate(); err != nil {
			return err
		}
		if record.Reservation.ForecastDigest != state.Forecast.Digest ||
			record.Reservation.DispositionDigest != state.Disposition.Digest ||
			record.Reservation.WorkRunID != state.WorkRunID ||
			record.Reservation.ExpectedWorkRevision != state.Revision ||
			record.Reservation.Ordinal != state.NextOrdinal {
			return errors.New("verification reservation does not bind the current forecast ordinal")
		}
		for _, existing := range state.Reservations {
			if existing.ActionTicketRef == record.Reservation.ActionTicketRef ||
				existing.SlotBindingRef == record.Reservation.SlotBindingRef {
				return ErrVerificationReserved
			}
			if existing.ForecastDigest == state.Forecast.Digest &&
				existing.Slot == record.Reservation.Slot {
				return ErrVerificationReserved
			}
		}
		state.Reservations = append(state.Reservations, *record.Reservation)
		state.NextOrdinal++
	case workOperationClaimLaunch:
		if state.Forecast == nil || state.VerificationResultRef != "" {
			return fmt.Errorf("%w: verification launch is not claimable", ErrWorkRunInvalidTransition)
		}
		if err := record.Launch.Validate(); err != nil {
			return err
		}
		reservation, ok := reservationByRef(state.Reservations, record.Launch.ReservationRef)
		if !ok || reservation.ActionTicketRef != record.Launch.ActionTicketRef {
			return errors.New("verification launch claim does not bind a reservation")
		}
		if launchClaimExists(state.LaunchClaims, record.Launch.ReservationRef) {
			return ErrVerificationLaunchClaimed
		}
		state.LaunchClaims = append(state.LaunchClaims, *record.Launch)
	case workOperationBindResult:
		if state.Forecast == nil ||
			state.VerificationResultRef != "" ||
			state.VerificationStop != nil {
			return fmt.Errorf("%w: verification result is not bindable", ErrWorkRunInvalidTransition)
		}
		if !validSHA256Ref(record.Result.ResultRef) {
			return errors.New("verification result reference is invalid")
		}
		if record.Result.PostVerificationSnapshotRef != "" &&
			!validSHA256Ref(record.Result.PostVerificationSnapshotRef) {
			return errors.New(
				"post-verification snapshot reference is invalid",
			)
		}
		if record.Result.CorrectionImpactClosureRef != "" &&
			!validSHA256Ref(record.Result.CorrectionImpactClosureRef) {
			return errors.New("correction-impact closure reference is invalid")
		}
		if record.Result.ReusableObligations != nil &&
			!equalStrings(
				record.Result.ReusableObligations,
				canonicalIdentifiers(record.Result.ReusableObligations),
			) {
			return errors.New(
				"reusable verification obligations must be canonical",
			)
		}
		if record.Result.Stop != nil {
			if err := record.Result.Stop.Validate(); err != nil {
				return err
			}
			if record.Result.Stop.Reason != VerificationStopFailed ||
				record.Result.Stop.ResultRef != record.Result.ResultRef ||
				record.Result.Stop.ExpectedSnapshotRef !=
					record.Result.PostVerificationSnapshotRef {
				return errors.New(
					"failed verification stop does not bind its exact result",
				)
			}
		}
		state.VerificationResultRef = record.Result.ResultRef
		state.PostVerificationSnapshotRef =
			record.Result.PostVerificationSnapshotRef
		state.CorrectionImpactClosureRef =
			record.Result.CorrectionImpactClosureRef
		state.ReusableVerificationObligations = append(
			[]string{},
			record.Result.ReusableObligations...,
		)
		state.VerificationStop = cloneVerificationStop(record.Result.Stop)
		state.ReviewReceiptRef = ""
		state.DeliveryAuthorizationRef = ""
	case workOperationStopMutation:
		if state.Forecast == nil ||
			state.VerificationResultRef != "" ||
			state.VerificationStop != nil {
			return fmt.Errorf(
				"%w: verification mutation stop is not bindable",
				ErrWorkRunInvalidTransition,
			)
		}
		if err := record.StopMutation.Stop.Validate(); err != nil {
			return err
		}
		if record.StopMutation.Stop.Reason != VerificationStopMutated {
			return errors.New(
				"verification mutation event requires a mutated stop",
			)
		}
		stop := record.StopMutation.Stop
		state.VerificationStop = &stop
		state.VerificationResultRef = ""
		state.PostVerificationSnapshotRef = ""
		state.CorrectionImpactClosureRef = ""
		state.ReusableVerificationObligations = []string{}
		state.ReviewReceiptRef = ""
		state.DeliveryAuthorizationRef = ""
	case workOperationBindReview:
		if state.VerificationResultRef == "" ||
			state.PostVerificationSnapshotRef == "" ||
			state.VerificationStop != nil ||
			state.ReviewReceiptRef != "" {
			return fmt.Errorf("%w: review receipt is not bindable", ErrWorkRunInvalidTransition)
		}
		if !validSHA256Ref(record.Review.ReviewReceiptRef) {
			return errors.New("work run review receipt reference is invalid")
		}
		state.ReviewReceiptRef = record.Review.ReviewReceiptRef
	case workOperationBindDeliveryRoute:
		if state.Handoff == nil ||
			state.VerificationResultRef == "" ||
			state.PostVerificationSnapshotRef == "" ||
			state.VerificationStop != nil ||
			state.ReviewReceiptRef == "" ||
			state.DeliveryRouteReevaluationRef != "" ||
			state.DeliveryAuthorizationRef != "" {
			return fmt.Errorf(
				"%w: delivery route reevaluation is not bindable",
				ErrWorkRunInvalidTransition,
			)
		}
		if !validSHA256Ref(record.DeliveryRoute.ReevaluationRef) ||
			!validSHA256Ref(record.DeliveryRoute.SourceDeliveryIntentRef) ||
			!validSHA256Ref(record.DeliveryRoute.TargetDeliveryIntentRef) ||
			record.DeliveryRoute.SourceDeliveryIntentRef !=
				state.DeliveryIntentRef ||
			record.DeliveryRoute.SourceDeliveryIntentRef ==
				record.DeliveryRoute.TargetDeliveryIntentRef {
			return errors.New(
				"work run delivery route reevaluation has invalid intent lineage",
			)
		}
		state.DeliveryIntentRef =
			record.DeliveryRoute.TargetDeliveryIntentRef
		state.DeliveryRouteReevaluationRef =
			record.DeliveryRoute.ReevaluationRef
	case workOperationBindDelivery:
		if state.Handoff == nil ||
			state.VerificationResultRef == "" ||
			state.PostVerificationSnapshotRef == "" ||
			state.VerificationStop != nil ||
			state.ReviewReceiptRef == "" ||
			state.DeliveryAuthorizationRef != "" {
			return fmt.Errorf(
				"%w: delivery authorization is not bindable",
				ErrWorkRunInvalidTransition,
			)
		}
		if !validSHA256Ref(record.Delivery.AuthorizationRef) {
			return errors.New(
				"work run delivery authorization reference is invalid",
			)
		}
		state.DeliveryAuthorizationRef = record.Delivery.AuthorizationRef
	default:
		return fmt.Errorf("unsupported work run operation %q", record.Operation)
	}
	return nil
}

func validateWorkRunRecordShape(record workRunRecord) error {
	if record.Schema != workRunRecordSchemaV1 {
		return errors.New("unsupported work run record schema")
	}
	if !workRunIDPattern.MatchString(record.WorkRunID) {
		return errors.New("work run record has invalid identifier")
	}
	if record.PreviousRevision != "" && !validSHA256Ref(record.PreviousRevision) {
		return errors.New("work run record has invalid previous revision")
	}
	if err := validateRequestID(record.RequestID); err != nil {
		return err
	}
	if !validSHA256Ref(record.RequestDigest) {
		return errors.New("work run record has invalid request digest")
	}
	fields := []bool{
		record.Start != nil, record.AcceptSDD != nil, record.Reroute != nil,
		record.BindSDD != nil, record.Handoff != nil, record.Replan != nil,
		record.Forecast != nil,
		record.Disposition != nil, record.Reservation != nil, record.Result != nil,
		record.StopMutation != nil, record.Launch != nil, record.Review != nil,
		record.DeliveryRoute != nil, record.Delivery != nil,
	}
	count := 0
	for _, present := range fields {
		if present {
			count++
		}
	}
	if count != 1 {
		return errors.New("work run record must contain exactly one typed event")
	}
	validOperation := record.Operation == workOperationStart && record.Start != nil ||
		record.Operation == workOperationAcceptSDD && record.AcceptSDD != nil ||
		record.Operation == workOperationReroute && record.Reroute != nil ||
		record.Operation == workOperationBindSDD && record.BindSDD != nil ||
		record.Operation == workOperationBindHandoff && record.Handoff != nil ||
		record.Operation == workOperationReplanVerification &&
			record.Replan != nil ||
		record.Operation == workOperationRecordForecast && record.Forecast != nil ||
		record.Operation == workOperationRecordDisposition && record.Disposition != nil ||
		record.Operation == workOperationBeginVerification && record.Reservation != nil ||
		record.Operation == workOperationClaimLaunch && record.Launch != nil ||
		record.Operation == workOperationBindResult && record.Result != nil ||
		record.Operation == workOperationStopMutation &&
			record.StopMutation != nil ||
		record.Operation == workOperationBindReview && record.Review != nil ||
		record.Operation == workOperationBindDeliveryRoute &&
			record.DeliveryRoute != nil ||
		record.Operation == workOperationBindDelivery && record.Delivery != nil
	if !validOperation {
		return errors.New("work run record operation and typed event differ")
	}
	if record.Operation == workOperationStart && record.PreviousRevision != "" {
		return errors.New("work run genesis must not have a previous revision")
	}
	return nil
}

func (store WorkRunStore) resolveMutationCompletion(
	ctx context.Context,
	handoff ImplementationHandoff,
) (MutationCompletionAuthority, error) {
	if store.authority.MutationCompletion == nil {
		return MutationCompletionAuthority{}, ErrAuthorityPortUnavailable
	}
	completion, err := store.authority.MutationCompletion.
		ResolveMutationCompletion(ctx, handoff.MutationCompletionRef)
	if err != nil {
		return MutationCompletionAuthority{}, fmt.Errorf(
			"resolve mutation completion: %w",
			err,
		)
	}
	if err := completion.Validate(
		handoff.MutationCompletionRef,
		store.WorkRunID,
		handoff,
	); err != nil {
		return MutationCompletionAuthority{}, err
	}
	if completion.RepositoryRef != store.RepositoryRef() {
		return MutationCompletionAuthority{}, fmt.Errorf(
			"%w: mutation completion repository",
			ErrAuthorityBindingMismatch,
		)
	}
	return completion, nil
}

func (store WorkRunStore) resolveLiveMutationCompletion(
	ctx context.Context,
	handoff ImplementationHandoff,
) (MutationCompletionAuthority, error) {
	completion, err := store.resolveMutationCompletion(ctx, handoff)
	if err != nil {
		return MutationCompletionAuthority{}, err
	}
	if _, err := reviewtransaction.ResnapshotVerificationSubject(
		ctx,
		store.Repo,
		completion.Snapshot,
	); err != nil {
		return MutationCompletionAuthority{}, err
	}
	return completion, nil
}

func (store WorkRunStore) buildCorrectionImpactClosure(
	ctx context.Context,
	replan VerificationReplan,
	correctedApplicability reviewtransaction.VerificationApplicability,
	correctedRegistry reviewtransaction.VerificationPlanRegistry,
	correctedPlan reviewtransaction.VerificationPlan,
	result reviewtransaction.VerificationResultRef,
) (reviewtransaction.CorrectionImpactClosure, error) {
	if err := replan.Validate(); err != nil {
		return reviewtransaction.CorrectionImpactClosure{}, err
	}
	if store.authority.Verification == nil {
		return reviewtransaction.CorrectionImpactClosure{},
			ErrAuthorityPortUnavailable
	}
	original, err := store.authority.Verification.ResolveForecast(
		ctx,
		replan.OriginalAvailabilityRef,
	)
	if err != nil {
		return reviewtransaction.CorrectionImpactClosure{}, fmt.Errorf(
			"resolve original correction forecast: %w",
			err,
		)
	}
	if err := original.Validate(); err != nil {
		return reviewtransaction.CorrectionImpactClosure{}, err
	}
	if original.AvailabilityRef != replan.OriginalAvailabilityRef ||
		original.Applicability.Digest !=
			replan.OriginalApplicabilityDigest ||
		original.Registry.Digest != replan.OriginalRegistryDigest ||
		original.Plan.Digest != replan.OriginalPlanDigest {
		return reviewtransaction.CorrectionImpactClosure{}, fmt.Errorf(
			"%w: original correction forecast",
			ErrAuthorityBindingMismatch,
		)
	}
	closure, err := reviewtransaction.BuildCorrectionImpactClosure(
		original.Applicability,
		original.Registry,
		original.Plan,
		correctedApplicability,
		correctedRegistry,
		correctedPlan,
		result,
	)
	if err != nil {
		return reviewtransaction.CorrectionImpactClosure{}, err
	}
	return closure, nil
}

// reusableCorrectionObligations derives reuse instead of accepting a caller
// set. Only completed obligations outside the owner-validated rerun closure
// survive. The current conservative RAR closure marks every corrected
// obligation for rerun, so this safely returns an empty set today.
func reusableCorrectionObligations(
	closure reviewtransaction.CorrectionImpactClosure,
) []string {
	reusable := make([]string, 0)
	rerunIndex := 0
	for _, completed := range closure.Result.CompletedObligations {
		for rerunIndex < len(closure.RerunObligations) &&
			closure.RerunObligations[rerunIndex] < completed {
			rerunIndex++
		}
		if rerunIndex < len(closure.RerunObligations) &&
			closure.RerunObligations[rerunIndex] == completed {
			continue
		}
		reusable = append(reusable, completed)
	}
	return reusable
}

func (store WorkRunStore) validateResultEvidence(
	ctx context.Context,
	state WorkRunState,
	result reviewtransaction.VerificationResultRef,
) error {
	if result.Aggregate == reviewtransaction.VerificationAggregateNotRequired {
		if len(result.EvidenceRefs) != 0 ||
			state.Forecast == nil ||
			hasReservationForForecast(
				state.Reservations,
				state.Forecast.Digest,
			) {
			return errors.New("not-required verification cannot consume execution reservations or evidence")
		}
		return nil
	}
	if len(result.EvidenceRefs) == 0 {
		if result.Aggregate == reviewtransaction.VerificationAggregateComplete ||
			result.Aggregate == reviewtransaction.VerificationAggregateFailed ||
			result.Aggregate == reviewtransaction.VerificationAggregatePartial ||
			len(result.CompletedObligations) != 0 {
			return errors.New("verification outcome requires admitted execution evidence")
		}
		return nil
	}
	if store.evidence == nil {
		return ErrEvidencePortUnavailable
	}
	reservations := make(map[string]VerificationReservation, len(state.Reservations))
	for _, reservation := range state.Reservations {
		if state.Forecast == nil ||
			reservation.ForecastDigest != state.Forecast.Digest {
			continue
		}
		reservations[reservation.ActionTicketRef] = reservation
	}
	completeSlots := make(map[string]struct{}, len(result.CompletedObligations))
	for _, evidenceRef := range result.EvidenceRefs {
		execution, err := store.evidence.ReadExecutionEvidence(ctx, evidenceRef)
		if err != nil {
			return fmt.Errorf("read admitted verification evidence %s: %w", evidenceRef, err)
		}
		if err := execution.Validate(); err != nil {
			return fmt.Errorf("validate admitted verification evidence %s: %w", evidenceRef, err)
		}
		reservation, exists := reservations[execution.TicketRef]
		if !exists {
			return errors.New("verification evidence has no successful atomic reservation")
		}
		if !launchClaimExists(state.LaunchClaims, reservation.ReservationRef) {
			return errors.New("verification evidence has no durable pre-launch claim")
		}
		if execution.EvidenceRef != evidenceRef ||
			execution.SlotBindingRef != reservation.SlotBindingRef ||
			execution.SubjectRef != reservation.SubjectRef ||
			execution.CandidateRef != reservation.CandidateRef ||
			execution.VerificationContextRef != reservation.ForecastDigest ||
			execution.ExpectedRevision != reservation.PlanRevisionRef ||
			execution.Slot != reservation.Slot ||
			execution.Capability != reservation.Capability {
			return errors.New("verification evidence does not bind its exact reservation")
		}
		if execution.Complete {
			completeSlots[execution.Slot] = struct{}{}
		}
	}
	for _, obligationID := range result.CompletedObligations {
		if _, complete := completeSlots[obligationID]; !complete {
			return fmt.Errorf("completed obligation %q lacks complete admitted evidence", obligationID)
		}
	}
	return nil
}

func hasReservationForForecast(
	reservations []VerificationReservation,
	forecastDigest string,
) bool {
	for _, reservation := range reservations {
		if reservation.ForecastDigest == forecastDigest {
			return true
		}
	}
	return false
}

func verificationObligationByID(
	plan reviewtransaction.VerificationPlan,
	id string,
) (reviewtransaction.VerificationObligation, bool) {
	for _, obligation := range plan.Obligations {
		if obligation.ID == id {
			return obligation, true
		}
	}
	return reviewtransaction.VerificationObligation{}, false
}

func reservationForTicket(
	reservations []VerificationReservation,
	ticketRef string,
) (VerificationReservation, bool) {
	for _, reservation := range reservations {
		if reservation.ActionTicketRef == ticketRef {
			return reservation, true
		}
	}
	return VerificationReservation{}, false
}

func reservationByRef(
	reservations []VerificationReservation,
	reservationRef string,
) (VerificationReservation, bool) {
	for _, reservation := range reservations {
		if reservation.ReservationRef == reservationRef {
			return reservation, true
		}
	}
	return VerificationReservation{}, false
}

func launchClaimExists(
	claims []VerificationLaunchClaim,
	reservationRef string,
) bool {
	for _, claim := range claims {
		if claim.ReservationRef == reservationRef {
			return true
		}
	}
	return false
}

func equalCanonicalValue(left, right any) bool {
	leftPayload, leftErr := json.Marshal(left)
	rightPayload, rightErr := json.Marshal(right)
	return leftErr == nil && rightErr == nil && bytes.Equal(leftPayload, rightPayload)
}

func cloneWorkRunState(state WorkRunState) WorkRunState {
	result := state
	result.RouteDecision.Reasons = append([]RouteDecisionReason(nil), state.RouteDecision.Reasons...)
	result.Reservations = make([]VerificationReservation, len(state.Reservations))
	copy(result.Reservations, state.Reservations)
	result.LaunchClaims = make([]VerificationLaunchClaim, len(state.LaunchClaims))
	copy(result.LaunchClaims, state.LaunchClaims)
	result.Handoff = cloneHandoff(state.Handoff)
	if state.VerificationReplan != nil {
		replan := *state.VerificationReplan
		result.VerificationReplan = &replan
	}
	result.Forecast = cloneForecast(state.Forecast)
	result.Disposition = cloneDisposition(state.Disposition)
	result.ReusableVerificationObligations =
		cloneStrings(state.ReusableVerificationObligations)
	result.VerificationStop = cloneVerificationStop(state.VerificationStop)
	return result
}

func cloneHandoff(value *ImplementationHandoff) *ImplementationHandoff {
	if value == nil {
		return nil
	}
	result := *value
	result.DeclaredObligationRefs = cloneStrings(value.DeclaredObligationRefs)
	result.EvidenceRefs = cloneStrings(value.EvidenceRefs)
	return &result
}

func cloneForecast(value *VerificationForecast) *VerificationForecast {
	if value == nil {
		return nil
	}
	result := *value
	result.CapabilityRefs = cloneStrings(value.CapabilityRefs)
	result.DiagnosticRefs = cloneStrings(value.DiagnosticRefs)
	if value.MaximumCost != nil {
		cost := *value.MaximumCost
		result.MaximumCost = &cost
	}
	return &result
}

func cloneDisposition(value *VerificationDisposition) *VerificationDisposition {
	if value == nil {
		return nil
	}
	result := *value
	return &result
}

func cloneVerificationStop(value *VerificationStop) *VerificationStop {
	if value == nil {
		return nil
	}
	result := *value
	return &result
}

func cloneStrings(values []string) []string {
	if values == nil {
		return nil
	}
	result := make([]string, len(values))
	copy(result, values)
	return result
}

func validateMutationEnvelope(expectedRevision, requestID string) error {
	if !validSHA256Ref(expectedRevision) {
		return errors.New("work run mutation requires the exact current revision")
	}
	return validateRequestID(requestID)
}

func validateRequestID(requestID string) error {
	if !workRequestIDPattern.MatchString(requestID) {
		return errors.New("invalid work run request identifier")
	}
	return nil
}

func workRunRecordRevision(record workRunRecord) (string, []byte, error) {
	payload, err := json.Marshal(record)
	if err != nil {
		return "", nil, err
	}
	payload = append(payload, '\n')
	sum := sha256.Sum256(payload)
	return "sha256:" + hex.EncodeToString(sum[:]), payload, nil
}

func (store WorkRunStore) ensureDirectories(ctx context.Context) error {
	if err := store.validateContext(ctx); err != nil {
		return err
	}
	if err := store.ensureDirectoryTree(ctx, store.repositoryDir); err != nil {
		return err
	}
	if err := store.validateContext(ctx); err != nil {
		return err
	}
	if err := store.ensureRepositoryBinding(ctx); err != nil {
		return err
	}
	if err := store.validateContext(ctx); err != nil {
		return err
	}
	if err := store.ensureDirectoryTree(ctx, filepath.Join(store.Dir, "records")); err != nil {
		return err
	}
	return store.validateContext(ctx)
}

func (store WorkRunStore) ensureDirectoryTree(
	ctx context.Context,
	target string,
) error {
	relative, err := filepath.Rel(store.commonDir, target)
	if err != nil || relative == "." || relative == ".." || filepath.IsAbs(relative) ||
		strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return errors.New("work run authority escapes the Git common directory")
	}
	current := store.commonDir
	created := make([]string, 0, 6)
	for index, segment := range strings.Split(relative, string(filepath.Separator)) {
		if err := store.validateContext(ctx); err != nil {
			return err
		}
		if segment == "" || segment == "." || segment == ".." {
			return errors.New("work run authority contains an invalid path segment")
		}
		current = filepath.Join(current, segment)
		if index == 0 {
			info, statErr := os.Lstat(current)
			if os.IsNotExist(statErr) {
				if err := os.Mkdir(current, 0o755); err != nil && !os.IsExist(err) {
					return err
				}
				created = append(created, current)
				info, statErr = os.Lstat(current)
			}
			if statErr != nil {
				return statErr
			}
			if workRunPathUnsafe(current, info) || !info.IsDir() {
				return errors.New("shared gentle-ai authority path is not a directory")
			}
			continue
		}
		wasCreated, err := createPrivateWorkRunDirectory(current)
		if err != nil {
			return fmt.Errorf("create private work run authority directory: %w", err)
		}
		if wasCreated {
			created = append(created, current)
		}
		if err := store.validateContext(ctx); err != nil {
			return err
		}
	}
	if filepath.Clean(current) != filepath.Clean(target) {
		return errors.New("work run authority path resolution is inconsistent")
	}
	for _, path := range created {
		if err := reviewtransaction.SyncReviewDirectory(filepath.Dir(path)); err != nil {
			return fmt.Errorf("sync parent of work run authority directory: %w", err)
		}
		if err := store.validateContext(ctx); err != nil {
			return err
		}
	}
	return nil
}

func (store WorkRunStore) ensureRepositoryBinding(ctx context.Context) error {
	if err := store.validateContext(ctx); err != nil {
		return err
	}
	expected, err := store.expectedRepositoryBinding()
	if err != nil {
		return err
	}
	payload, err := json.Marshal(expected)
	if err != nil {
		return err
	}
	payload = append(payload, '\n')
	path := filepath.Join(store.repositoryDir, "repository-binding.json")
	existing, err := readBoundedPrivateWorkRunFile(path, maximumWorkRunRecordBytes)
	if err == nil {
		binding, decodeErr := decodeWorkRunRepositoryBinding(existing)
		if decodeErr != nil {
			return decodeErr
		}
		if binding != expected || !bytes.Equal(existing, payload) {
			return errors.New("work run repository binding conflicts")
		}
		return store.validateContext(ctx)
	}
	if !os.IsNotExist(err) {
		return fmt.Errorf("read work run repository binding: %w", err)
	}

	temp, tempPath, err := createPrivateTempWorkRunFile(
		store.repositoryDir,
		".repository-binding-",
	)
	if err != nil {
		return err
	}
	defer os.Remove(tempPath)
	if _, err = temp.Write(payload); err == nil {
		err = temp.Sync()
	}
	if closeErr := temp.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}
	if err := validatePrivateWorkRunFile(tempPath); err != nil {
		return err
	}
	if err := store.validateContext(ctx); err != nil {
		return err
	}
	if err := reviewtransaction.PublishFileNoReplace(tempPath, path); err != nil &&
		!os.IsExist(err) {
		return err
	}
	existing, err = readBoundedPrivateWorkRunFile(path, maximumWorkRunRecordBytes)
	if err != nil {
		return err
	}
	binding, err := decodeWorkRunRepositoryBinding(existing)
	if err != nil {
		return err
	}
	if binding != expected || !bytes.Equal(existing, payload) {
		return errors.New("concurrent work run repository binding conflicts")
	}
	if err := reviewtransaction.SyncReviewDirectory(store.repositoryDir); err != nil {
		return fmt.Errorf("sync work run repository binding: %w", err)
	}
	return store.validateContext(ctx)
}

func (store WorkRunStore) publishRecord(revision string, payload []byte) error {
	recordsDir := filepath.Join(store.Dir, "records")
	path := filepath.Join(recordsDir, strings.TrimPrefix(revision, "sha256:")+".json")
	temp, tempPath, err := createPrivateTempWorkRunFile(recordsDir, ".record-")
	if err != nil {
		return err
	}
	defer os.Remove(tempPath)
	_, err = temp.Write(payload)
	if err == nil {
		err = temp.Sync()
	}
	if closeErr := temp.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}
	if err := validatePrivateWorkRunFile(tempPath); err != nil {
		return err
	}
	if err := reviewtransaction.PublishFileNoReplace(tempPath, path); err != nil {
		if !os.IsExist(err) {
			return err
		}
		existing, readErr := readBoundedWorkRunFile(path)
		if readErr != nil {
			return readErr
		}
		if !bytes.Equal(existing, payload) {
			return errors.New("existing immutable work run record differs from its revision")
		}
	}
	if err := validatePrivateWorkRunFile(path); err != nil {
		return fmt.Errorf("validate published immutable work run record: %w", err)
	}
	return reviewtransaction.SyncReviewDirectory(recordsDir)
}

func (store WorkRunStore) publishHead(revision string) error {
	temp, tempPath, err := createPrivateTempWorkRunFile(store.Dir, ".head-")
	if err != nil {
		return err
	}
	defer os.Remove(tempPath)
	_, err = temp.WriteString(revision + "\n")
	if err == nil {
		err = temp.Sync()
	}
	if closeErr := temp.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}
	if err := validatePrivateWorkRunFile(tempPath); err != nil {
		return err
	}
	headPath := filepath.Join(store.Dir, "HEAD")
	if err := reviewtransaction.ReplaceFileAtomic(tempPath, headPath); err != nil {
		return err
	}
	return validatePrivateWorkRunFile(headPath)
}

func createPrivateTempWorkRunFile(
	directory string,
	prefix string,
) (*os.File, string, error) {
	for attempt := 0; attempt < 32; attempt++ {
		var nonce [16]byte
		if _, err := rand.Read(nonce[:]); err != nil {
			return nil, "", fmt.Errorf("generate private work run temporary path: %w", err)
		}
		path := filepath.Join(directory, prefix+hex.EncodeToString(nonce[:]))
		file, err := createPrivateWorkRunFile(path)
		if os.IsExist(err) {
			continue
		}
		if err != nil {
			return nil, "", err
		}
		return file, path, nil
	}
	return nil, "", errors.New("could not allocate a private work run temporary path")
}

func (store WorkRunStore) syncReplay(ctx context.Context) error {
	if err := store.validateContext(ctx); err != nil {
		return err
	}
	if err := reviewtransaction.SyncReviewDirectory(filepath.Join(store.Dir, "records")); err != nil {
		return fmt.Errorf("sync immutable work run records: %w", err)
	}
	if err := reviewtransaction.SyncReviewDirectory(store.Dir); err != nil {
		return fmt.Errorf("sync work run HEAD: %w", err)
	}
	return store.validateContext(ctx)
}

func readWorkRunHead(path string) (string, bool, error) {
	payload, err := readBoundedWorkRunFile(path)
	if os.IsNotExist(err) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	if len(payload) != len("sha256:")+64+1 || payload[len(payload)-1] != '\n' {
		return "", true, errors.New("invalid work run HEAD encoding")
	}
	revision := strings.TrimSuffix(string(payload), "\n")
	if !validSHA256Ref(revision) {
		return "", true, errors.New("invalid work run HEAD revision")
	}
	return revision, true, nil
}

func (store WorkRunStore) loadRecord(revision string) (workRunRecord, error) {
	if !validSHA256Ref(revision) {
		return workRunRecord{}, errors.New("invalid work run record revision")
	}
	path := filepath.Join(store.Dir, "records", strings.TrimPrefix(revision, "sha256:")+".json")
	payload, err := readBoundedWorkRunFile(path)
	if err != nil {
		return workRunRecord{}, fmt.Errorf("load work run revision %s: %w", revision, err)
	}
	sum := sha256.Sum256(payload)
	actual := "sha256:" + hex.EncodeToString(sum[:])
	if actual != revision {
		return workRunRecord{}, fmt.Errorf(
			"work run record revision mismatch: expected %s, got %s",
			revision, actual,
		)
	}
	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.DisallowUnknownFields()
	var record workRunRecord
	if err := decoder.Decode(&record); err != nil {
		return workRunRecord{}, fmt.Errorf("decode work run revision %s: %w", revision, err)
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		return workRunRecord{}, errors.New("work run record contains multiple JSON values")
	}
	_, canonical, err := workRunRecordRevision(record)
	if err != nil || !bytes.Equal(payload, canonical) {
		return workRunRecord{}, errors.New("work run record is not canonical")
	}
	if record.WorkRunID != store.WorkRunID {
		return workRunRecord{}, errors.New("work run record identifier does not match store")
	}
	return record, nil
}

func readBoundedWorkRunFile(path string) ([]byte, error) {
	return readBoundedPrivateWorkRunFile(path, maximumWorkRunRecordBytes)
}
