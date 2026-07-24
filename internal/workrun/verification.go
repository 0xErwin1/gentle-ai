package workrun

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/gentleman-programming/gentle-ai/internal/reviewtransaction"
)

const (
	ImplementationHandoffSchemaV1   = "gentle-ai.implementation-handoff/v1"
	ImplementationHandoffSchemaV2   = "gentle-ai.implementation-handoff/v2"
	VerificationForecastSchemaV1    = "gentle-ai.verification-forecast/v1"
	VerificationDispositionSchemaV1 = "gentle-ai.verification-disposition/v1"
	VerificationReservationSchemaV1 = "gentle-ai.verification-reservation/v1"
	VerificationLaunchClaimSchemaV1 = "gentle-ai.verification-launch-claim/v1"
	VerificationReplanSchemaV1      = "gentle-ai.verification-replan/v1"
	VerificationStopSchemaV1        = "gentle-ai.verification-stop/v1"
)

type ForecastAvailability string

const (
	ForecastAvailable   ForecastAvailability = "available"
	ForecastNotRequired ForecastAvailability = "not_required"
	ForecastPartial     ForecastAvailability = "partial"
	ForecastUnavailable ForecastAvailability = "unavailable"
	ForecastUnknown     ForecastAvailability = "unknown"
)

type VerificationDispositionKind string

const (
	DispositionRun            VerificationDispositionKind = "run"
	DispositionDefer          VerificationDispositionKind = "defer"
	DispositionReduceScope    VerificationDispositionKind = "reduce_scope"
	DispositionDeferredRunner VerificationDispositionKind = "deferred_runner"
)

// ImplementationHandoff is route-neutral. It carries the exact normalized
// subject and immutable owner references, never route-specific task state.
type ImplementationHandoff struct {
	Schema                 string                                `json:"schema"`
	Route                  ImplementationRoute                   `json:"route"`
	ScopeDigest            string                                `json:"scope_digest"`
	Subject                reviewtransaction.VerificationSubject `json:"subject"`
	CandidateRef           string                                `json:"candidate_ref"`
	MutationCompletionRef  string                                `json:"mutation_completion_ref,omitempty"`
	DeclaredObligationRefs []string                              `json:"declared_obligation_refs"`
	EvidenceRefs           []string                              `json:"evidence_refs"`
	SDDRunRef              string                                `json:"sdd_run_ref,omitempty"`
	Digest                 string                                `json:"digest"`
}

func NewImplementationHandoff(
	route ImplementationRoute,
	scopeDigest string,
	subject reviewtransaction.VerificationSubject,
	declaredObligationRefs []string,
	evidenceRefs []string,
	sddRunRef string,
	mutationCompletionRef ...string,
) (ImplementationHandoff, error) {
	declared, err := canonicalSHA256Refs(declaredObligationRefs)
	if err != nil {
		return ImplementationHandoff{}, fmt.Errorf("canonicalize declared obligations: %w", err)
	}
	evidence, err := canonicalSHA256Refs(evidenceRefs)
	if err != nil {
		return ImplementationHandoff{}, fmt.Errorf("canonicalize implementation evidence: %w", err)
	}
	completionRef := ""
	if len(mutationCompletionRef) > 1 {
		return ImplementationHandoff{}, errors.New(
			"implementation handoff accepts exactly one mutation completion reference",
		)
	}
	if len(mutationCompletionRef) == 1 {
		completionRef = mutationCompletionRef[0]
	}
	handoff := ImplementationHandoff{
		Schema: ImplementationHandoffSchemaV2, Route: route, ScopeDigest: scopeDigest,
		Subject: subject, CandidateRef: candidateRefForSubject(subject),
		MutationCompletionRef:  completionRef,
		DeclaredObligationRefs: declared, EvidenceRefs: evidence, SDDRunRef: sddRunRef,
	}
	digest, err := implementationHandoffDigest(handoff)
	if err != nil {
		return ImplementationHandoff{}, err
	}
	handoff.Digest = digest
	if err := handoff.Validate(); err != nil {
		return ImplementationHandoff{}, err
	}
	return handoff, nil
}

func (handoff ImplementationHandoff) Validate() error {
	switch handoff.Schema {
	case ImplementationHandoffSchemaV1:
		if handoff.MutationCompletionRef != "" {
			return errors.New(
				"legacy implementation handoff cannot carry a mutation completion reference",
			)
		}
	case ImplementationHandoffSchemaV2:
		if !validSHA256Ref(handoff.MutationCompletionRef) {
			return errors.New(
				"implementation handoff requires an immutable mutation completion reference",
			)
		}
	default:
		return errors.New("unsupported implementation handoff schema")
	}
	if !validSHA256Ref(handoff.ScopeDigest) ||
		!validSHA256Ref(handoff.CandidateRef) {
		return errors.New(
			"implementation handoff requires immutable scope and candidate references",
		)
	}
	if err := handoff.Subject.Validate(); err != nil {
		return err
	}
	if handoff.CandidateRef != candidateRefForSubject(handoff.Subject) {
		return errors.New("implementation handoff candidate does not match its exact subject")
	}
	if !isCanonicalSHA256Refs(handoff.DeclaredObligationRefs) ||
		!isCanonicalSHA256Refs(handoff.EvidenceRefs) {
		return errors.New("implementation handoff reference arrays must be canonical")
	}
	switch handoff.Route {
	case ImplementationRouteDirectInline, ImplementationRouteDelegatedDirect:
		if handoff.SDDRunRef != "" {
			return errors.New("direct implementation handoff cannot bind an SDD run")
		}
	case ImplementationRouteSDD:
		if !validOpaqueRef(handoff.SDDRunRef) {
			return errors.New("SDD implementation handoff requires an accepted SDD run reference")
		}
	default:
		return fmt.Errorf("unsupported implementation handoff route %q", handoff.Route)
	}
	var (
		want string
		err  error
	)
	if handoff.Schema == ImplementationHandoffSchemaV1 {
		want, err = implementationHandoffDigestV1(handoff)
	} else {
		want, err = implementationHandoffDigest(handoff)
	}
	if err != nil {
		return err
	}
	if handoff.Digest != want {
		return errors.New("implementation handoff digest does not match its canonical content")
	}
	return nil
}

// VerificationReplan records the single correction-driven replacement of the
// active verification subject. It preserves the implementation route and
// points back to the prior forecast rather than starting another workflow.
type VerificationReplan struct {
	Schema                      string `json:"schema"`
	Ordinal                     int    `json:"ordinal"`
	PreviousHandoffDigest       string `json:"previous_handoff_digest"`
	PreviousForecastDigest      string `json:"previous_forecast_digest"`
	OriginalApplicabilityDigest string `json:"original_applicability_digest"`
	OriginalRegistryDigest      string `json:"original_registry_digest"`
	OriginalPlanDigest          string `json:"original_plan_digest"`
	OriginalAvailabilityRef     string `json:"original_availability_ref"`
	CorrectedHandoffDigest      string `json:"corrected_handoff_digest"`
	MutationCompletionRef       string `json:"mutation_completion_ref"`
	Digest                      string `json:"digest"`
}

func newVerificationReplan(
	ordinal int,
	previousHandoff ImplementationHandoff,
	previousForecast VerificationForecast,
	correctedHandoff ImplementationHandoff,
) (VerificationReplan, error) {
	replan := VerificationReplan{
		Schema:                      VerificationReplanSchemaV1,
		Ordinal:                     ordinal,
		PreviousHandoffDigest:       previousHandoff.Digest,
		PreviousForecastDigest:      previousForecast.Digest,
		OriginalApplicabilityDigest: previousForecast.ApplicabilityDigest,
		OriginalRegistryDigest:      previousForecast.RegistryDigest,
		OriginalPlanDigest:          previousForecast.PlanDigest,
		OriginalAvailabilityRef:     previousForecast.AvailabilityRef,
		CorrectedHandoffDigest:      correctedHandoff.Digest,
		MutationCompletionRef:       correctedHandoff.MutationCompletionRef,
	}
	digest, err := verificationReplanDigest(replan)
	if err != nil {
		return VerificationReplan{}, err
	}
	replan.Digest = digest
	if err := replan.Validate(); err != nil {
		return VerificationReplan{}, err
	}
	return replan, nil
}

func (replan VerificationReplan) Validate() error {
	if replan.Schema != VerificationReplanSchemaV1 ||
		replan.Ordinal < 1 ||
		replan.Ordinal > reviewtransaction.MaxCompactCorrectionAttempts {
		return errors.New("verification replan exceeds the native correction bound")
	}
	for _, ref := range []string{
		replan.PreviousHandoffDigest,
		replan.PreviousForecastDigest,
		replan.OriginalApplicabilityDigest,
		replan.OriginalRegistryDigest,
		replan.OriginalPlanDigest,
		replan.OriginalAvailabilityRef,
		replan.CorrectedHandoffDigest,
		replan.MutationCompletionRef,
		replan.Digest,
	} {
		if !validSHA256Ref(ref) {
			return errors.New(
				"verification replan has an invalid immutable reference",
			)
		}
	}
	if replan.PreviousHandoffDigest == replan.CorrectedHandoffDigest {
		return errors.New("verification replan requires a corrected handoff")
	}
	want, err := verificationReplanDigest(replan)
	if err != nil {
		return err
	}
	if replan.Digest != want {
		return errors.New(
			"verification replan digest does not match its canonical content",
		)
	}
	return nil
}

type VerificationStopReason string

const (
	VerificationStopFailed  VerificationStopReason = "failed"
	VerificationStopMutated VerificationStopReason = "mutated"
)

// VerificationStop is coordination state, not a copied verification result.
// Failed results retain their owner result ref; mutation stops retain only the
// attempted result ref and both snapshot identities.
type VerificationStop struct {
	Schema              string                 `json:"schema"`
	Reason              VerificationStopReason `json:"reason"`
	ResultRef           string                 `json:"result_ref"`
	ExpectedSnapshotRef string                 `json:"expected_snapshot_ref"`
	ObservedSnapshotRef string                 `json:"observed_snapshot_ref"`
	Digest              string                 `json:"digest"`
}

func newVerificationStop(
	reason VerificationStopReason,
	resultRef string,
	expectedSnapshotRef string,
	observedSnapshotRef string,
) (VerificationStop, error) {
	stop := VerificationStop{
		Schema: VerificationStopSchemaV1, Reason: reason, ResultRef: resultRef,
		ExpectedSnapshotRef: expectedSnapshotRef,
		ObservedSnapshotRef: observedSnapshotRef,
	}
	digest, err := verificationStopDigest(stop)
	if err != nil {
		return VerificationStop{}, err
	}
	stop.Digest = digest
	if err := stop.Validate(); err != nil {
		return VerificationStop{}, err
	}
	return stop, nil
}

func (stop VerificationStop) Validate() error {
	if stop.Schema != VerificationStopSchemaV1 {
		return errors.New("unsupported verification stop schema")
	}
	for _, ref := range []string{
		stop.ResultRef,
		stop.ExpectedSnapshotRef,
		stop.ObservedSnapshotRef,
		stop.Digest,
	} {
		if !validSHA256Ref(ref) {
			return errors.New(
				"verification stop has an invalid immutable reference",
			)
		}
	}
	switch stop.Reason {
	case VerificationStopFailed:
		if stop.ExpectedSnapshotRef != stop.ObservedSnapshotRef {
			return errors.New(
				"failed verification stop requires an unchanged subject",
			)
		}
	case VerificationStopMutated:
		if stop.ExpectedSnapshotRef == stop.ObservedSnapshotRef {
			return errors.New(
				"mutated verification stop requires distinct subjects",
			)
		}
	default:
		return fmt.Errorf("unsupported verification stop reason %q", stop.Reason)
	}
	want, err := verificationStopDigest(stop)
	if err != nil {
		return err
	}
	if stop.Digest != want {
		return errors.New(
			"verification stop digest does not match its canonical content",
		)
	}
	return nil
}

type VerificationForecastInput struct {
	WorkRunID       string                                      `json:"work_run_id"`
	Handoff         ImplementationHandoff                       `json:"handoff"`
	Applicability   reviewtransaction.VerificationApplicability `json:"applicability"`
	Registry        reviewtransaction.VerificationPlanRegistry  `json:"registry"`
	Plan            reviewtransaction.VerificationPlan          `json:"plan"`
	PlanRevisionRef string                                      `json:"plan_revision_ref"`
	Availability    ForecastAvailability                        `json:"availability"`
	AvailabilityRef string                                      `json:"availability_ref"`
	RequiresConsent bool                                        `json:"requires_consent,omitempty"`
	DiagnosticRefs  []string                                    `json:"diagnostic_refs"`
}

// VerificationForecast binds exact RAR facts to an availability observation.
// RequiresConsent is an owner fact sealed into the digest; neither it nor the
// forecast grants launch authority.
type VerificationForecast struct {
	Schema              string                              `json:"schema"`
	WorkRunID           string                              `json:"work_run_id"`
	HandoffDigest       string                              `json:"handoff_digest"`
	SubjectRef          string                              `json:"subject_ref"`
	CandidateRef        string                              `json:"candidate_ref"`
	ApplicabilityDigest string                              `json:"applicability_digest"`
	RegistryDigest      string                              `json:"registry_digest"`
	PlanDigest          string                              `json:"plan_digest"`
	PlanRevisionRef     string                              `json:"plan_revision_ref"`
	PlanPreimageRef     string                              `json:"plan_preimage_ref"`
	MaximumCost         *reviewtransaction.VerificationCost `json:"maximum_cost,omitempty"`
	Availability        ForecastAvailability                `json:"availability"`
	AvailabilityRef     string                              `json:"availability_ref"`
	RequiresConsent     bool                                `json:"requires_consent,omitempty"`
	CapabilityRefs      []string                            `json:"capability_refs"`
	DiagnosticRefs      []string                            `json:"diagnostic_refs"`
	Digest              string                              `json:"digest"`
}

func newVerificationForecast(input VerificationForecastInput) (VerificationForecast, error) {
	if err := input.Handoff.Validate(); err != nil {
		return VerificationForecast{}, err
	}
	if err := reviewtransaction.ValidateVerificationPlan(input.Applicability, input.Registry, input.Plan); err != nil {
		return VerificationForecast{}, err
	}
	if input.Handoff.Subject != input.Plan.Subject ||
		input.Handoff.CandidateRef != candidateRefForSubject(input.Plan.Subject) {
		return VerificationForecast{}, errors.New("verification forecast does not bind the handoff subject")
	}
	requirements := make([]string, 0, len(input.Plan.Obligations))
	capabilities := make([]string, 0, len(input.Plan.Obligations))
	for _, obligation := range input.Plan.Obligations {
		requirements = append(requirements, obligation.RequirementRef)
		capabilities = append(capabilities, obligation.CapabilityRef)
	}
	requirements, err := canonicalSHA256Refs(requirements)
	if err != nil {
		return VerificationForecast{}, err
	}
	if !equalStrings(requirements, input.Handoff.DeclaredObligationRefs) {
		return VerificationForecast{}, errors.New("verification plan differs from handoff declared obligations")
	}
	capabilities = canonicalIdentifiers(capabilities)
	diagnostics, err := canonicalSHA256Refs(input.DiagnosticRefs)
	if err != nil {
		return VerificationForecast{}, fmt.Errorf("canonicalize forecast diagnostics: %w", err)
	}
	maximumCost := maximumVerificationCost(input.Plan.Obligations)
	forecast := VerificationForecast{
		Schema: VerificationForecastSchemaV1, WorkRunID: input.WorkRunID,
		HandoffDigest: input.Handoff.Digest,
		SubjectRef:    input.Applicability.Digest, CandidateRef: input.Handoff.CandidateRef,
		ApplicabilityDigest: input.Applicability.Digest, RegistryDigest: input.Registry.Digest,
		PlanDigest: input.Plan.Digest, PlanRevisionRef: input.PlanRevisionRef,
		PlanPreimageRef: input.Plan.Digest, MaximumCost: maximumCost,
		Availability: input.Availability, AvailabilityRef: input.AvailabilityRef,
		RequiresConsent: input.RequiresConsent,
		CapabilityRefs:  capabilities, DiagnosticRefs: diagnostics,
	}
	digest, err := verificationForecastDigest(forecast)
	if err != nil {
		return VerificationForecast{}, err
	}
	forecast.Digest = digest
	if err := forecast.Validate(); err != nil {
		return VerificationForecast{}, err
	}
	return forecast, nil
}

func (forecast VerificationForecast) Validate() error {
	if forecast.Schema != VerificationForecastSchemaV1 {
		return errors.New("unsupported verification forecast schema")
	}
	if !workRunIDPattern.MatchString(forecast.WorkRunID) {
		return errors.New("verification forecast has an invalid work run identifier")
	}
	for name, ref := range map[string]string{
		"handoffDigest": forecast.HandoffDigest, "subjectRef": forecast.SubjectRef,
		"candidateRef": forecast.CandidateRef, "applicabilityDigest": forecast.ApplicabilityDigest,
		"registryDigest": forecast.RegistryDigest, "planDigest": forecast.PlanDigest,
		"planRevisionRef": forecast.PlanRevisionRef, "planPreimageRef": forecast.PlanPreimageRef,
		"availabilityRef": forecast.AvailabilityRef,
	} {
		if !validSHA256Ref(ref) {
			return fmt.Errorf("verification forecast %s must be an immutable SHA-256 reference", name)
		}
	}
	if forecast.PlanPreimageRef != forecast.PlanDigest {
		return errors.New("verification forecast plan preimage must be the exact plan digest")
	}
	if !canonicalIdentifiersEqual(forecast.CapabilityRefs) {
		return fmt.Errorf("verification forecast capabilities must be canonical: %#v", forecast.CapabilityRefs)
	}
	if !isCanonicalSHA256Refs(forecast.DiagnosticRefs) {
		return fmt.Errorf("verification forecast diagnostics must be canonical: %#v", forecast.DiagnosticRefs)
	}
	switch forecast.Availability {
	case ForecastAvailable:
		if forecast.MaximumCost == nil {
			return errors.New("available verification requires an applicable cost")
		}
		if len(forecast.DiagnosticRefs) != 0 {
			return errors.New("available verification cannot carry blocker diagnostics")
		}
	case ForecastNotRequired:
		if forecast.MaximumCost != nil || len(forecast.CapabilityRefs) != 0 {
			return errors.New("not-required verification cannot carry executable work")
		}
	case ForecastPartial, ForecastUnavailable, ForecastUnknown:
		if len(forecast.DiagnosticRefs) == 0 {
			return errors.New("non-available verification requires a typed diagnostic reference")
		}
	default:
		return fmt.Errorf("unsupported verification forecast availability %q", forecast.Availability)
	}
	if forecast.RequiresConsent &&
		forecast.Availability != ForecastAvailable {
		return errors.New(
			"verification consent requires an available forecast",
		)
	}
	if forecast.MaximumCost != nil && !validVerificationCost(*forecast.MaximumCost) {
		return fmt.Errorf("unsupported verification forecast cost %q", *forecast.MaximumCost)
	}
	want, err := verificationForecastDigest(forecast)
	if err != nil {
		return err
	}
	if forecast.Digest != want {
		return errors.New("verification forecast digest does not match its canonical content")
	}
	return nil
}

func forecastRequiresExplicitConsent(forecast VerificationForecast) bool {
	if forecast.RequiresConsent {
		return true
	}
	if forecast.MaximumCost == nil {
		return false
	}
	switch *forecast.MaximumCost {
	case reviewtransaction.VerificationCostLong,
		reviewtransaction.VerificationCostVeryLong,
		reviewtransaction.VerificationCostUnknown:
		return true
	default:
		return false
	}
}

// MatchesPlan proves the forecast came from the exact current RAR facts.
func (forecast VerificationForecast) MatchesPlan(
	applicability reviewtransaction.VerificationApplicability,
	registry reviewtransaction.VerificationPlanRegistry,
	plan reviewtransaction.VerificationPlan,
) error {
	if err := forecast.Validate(); err != nil {
		return err
	}
	if err := reviewtransaction.ValidateVerificationPlan(applicability, registry, plan); err != nil {
		return err
	}
	if forecast.SubjectRef != applicability.Digest ||
		forecast.ApplicabilityDigest != applicability.Digest ||
		forecast.RegistryDigest != registry.Digest ||
		forecast.PlanDigest != plan.Digest ||
		forecast.PlanPreimageRef != plan.Digest ||
		forecast.CandidateRef != candidateRefForSubject(plan.Subject) {
		return errors.New("verification forecast does not match the exact RAR plan")
	}
	capabilities := make([]string, 0, len(plan.Obligations))
	for _, obligation := range plan.Obligations {
		capabilities = append(capabilities, obligation.CapabilityRef)
	}
	if !equalStrings(forecast.CapabilityRefs, canonicalIdentifiers(capabilities)) {
		return errors.New("verification forecast capabilities differ from the exact plan")
	}
	wantCost := maximumVerificationCost(plan.Obligations)
	if !equalVerificationCost(forecast.MaximumCost, wantCost) {
		return errors.New("verification forecast cost differs from the exact plan")
	}
	switch plan.Applicability {
	case reviewtransaction.VerificationApplicable:
		if forecast.Availability == ForecastNotRequired || forecast.MaximumCost == nil {
			return errors.New("applicable verification cannot be forecast as not required")
		}
	case reviewtransaction.VerificationNotApplicable:
		if forecast.Availability != ForecastNotRequired {
			return errors.New("native not-applicable verification requires a not-required forecast")
		}
	default:
		if forecast.Availability != ForecastUnknown {
			return errors.New("unknown applicability requires an unknown forecast")
		}
	}
	return nil
}

type VerificationDisposition struct {
	Schema         string                      `json:"schema"`
	ForecastDigest string                      `json:"forecast_digest"`
	AssumptionsRef string                      `json:"assumptions_ref"`
	Kind           VerificationDispositionKind `json:"kind"`
	ActorRef       string                      `json:"actor_ref"`
	DecisionRef    string                      `json:"decision_ref"`
	RunnerRef      string                      `json:"runner_ref,omitempty"`
	Digest         string                      `json:"digest"`
}

func newVerificationDisposition(
	forecast VerificationForecast,
	kind VerificationDispositionKind,
	assumptionsRef string,
	actorRef string,
	decisionRef string,
	runnerRef string,
) (VerificationDisposition, error) {
	disposition := VerificationDisposition{
		Schema: VerificationDispositionSchemaV1, ForecastDigest: forecast.Digest,
		AssumptionsRef: assumptionsRef, Kind: kind, ActorRef: actorRef,
		DecisionRef: decisionRef, RunnerRef: runnerRef,
	}
	digest, err := verificationDispositionDigest(disposition)
	if err != nil {
		return VerificationDisposition{}, err
	}
	disposition.Digest = digest
	if err := disposition.ValidateFor(forecast); err != nil {
		return VerificationDisposition{}, err
	}
	return disposition, nil
}

func (disposition VerificationDisposition) Validate() error {
	if disposition.Schema != VerificationDispositionSchemaV1 {
		return errors.New("unsupported verification disposition schema")
	}
	for name, ref := range map[string]string{
		"forecastDigest": disposition.ForecastDigest, "assumptionsRef": disposition.AssumptionsRef,
		"actorRef": disposition.ActorRef, "decisionRef": disposition.DecisionRef,
	} {
		if !validSHA256Ref(ref) {
			return fmt.Errorf("verification disposition %s must be an immutable SHA-256 reference", name)
		}
	}
	switch disposition.Kind {
	case DispositionRun, DispositionDefer, DispositionReduceScope:
		if disposition.RunnerRef != "" {
			return errors.New("verification disposition runner is valid only for deferred_runner")
		}
	case DispositionDeferredRunner:
		if !validSHA256Ref(disposition.RunnerRef) {
			return errors.New("deferred-runner disposition requires an immutable runner reference")
		}
	default:
		return fmt.Errorf("unsupported verification disposition %q", disposition.Kind)
	}
	want, err := verificationDispositionDigest(disposition)
	if err != nil {
		return err
	}
	if disposition.Digest != want {
		return errors.New("verification disposition digest does not match its canonical content")
	}
	return nil
}

func (disposition VerificationDisposition) ValidateFor(forecast VerificationForecast) error {
	if err := forecast.Validate(); err != nil {
		return err
	}
	if err := disposition.Validate(); err != nil {
		return err
	}
	if disposition.ForecastDigest != forecast.Digest {
		return errors.New("verification disposition does not bind the current forecast")
	}
	if forecast.Availability == ForecastNotRequired {
		return errors.New("not-required verification cannot create a launch disposition")
	}
	switch disposition.Kind {
	case DispositionRun:
		if forecast.Availability != ForecastAvailable || forecast.MaximumCost == nil ||
			*forecast.MaximumCost == reviewtransaction.VerificationCostUnknown {
			return errors.New("run disposition requires available verification with a known cost")
		}
	case DispositionDeferredRunner:
		if forecast.Availability != ForecastAvailable || forecast.MaximumCost == nil ||
			(*forecast.MaximumCost != reviewtransaction.VerificationCostLong &&
				*forecast.MaximumCost != reviewtransaction.VerificationCostVeryLong) {
			return errors.New("deferred runner requires available long or very-long verification")
		}
	case DispositionDefer, DispositionReduceScope:
		// These choices intentionally authorize no launch.
	}
	return nil
}

type VerificationReservation struct {
	Schema               string                             `json:"schema"`
	ReservationRef       string                             `json:"reservation_ref"`
	WorkRunID            string                             `json:"work_run_id"`
	ExpectedWorkRevision string                             `json:"expected_work_revision"`
	ForecastDigest       string                             `json:"forecast_digest"`
	DispositionDigest    string                             `json:"disposition_digest"`
	PlanRevisionRef      string                             `json:"plan_revision_ref"`
	PlanPreimageRef      string                             `json:"plan_preimage_ref"`
	SubjectRef           string                             `json:"subject_ref"`
	CandidateRef         string                             `json:"candidate_ref"`
	Cost                 reviewtransaction.VerificationCost `json:"cost"`
	Availability         ForecastAvailability               `json:"availability"`
	AvailabilityRef      string                             `json:"availability_ref"`
	ActorRef             string                             `json:"actor_ref"`
	DecisionRef          string                             `json:"decision_ref"`
	ActionTicketRef      string                             `json:"action_ticket_ref"`
	SlotBindingRef       string                             `json:"slot_binding_ref"`
	Slot                 string                             `json:"slot"`
	Capability           string                             `json:"capability"`
	Ordinal              int                                `json:"ordinal"`
}

func newVerificationReservation(
	forecast VerificationForecast,
	disposition VerificationDisposition,
	cost reviewtransaction.VerificationCost,
	workRunID string,
	expectedWorkRevision string,
	actionTicketRef string,
	slotBindingRef string,
	slot string,
	capability string,
	ordinal int,
) (VerificationReservation, error) {
	reservation := VerificationReservation{
		Schema: VerificationReservationSchemaV1, ForecastDigest: forecast.Digest,
		WorkRunID: workRunID, ExpectedWorkRevision: expectedWorkRevision,
		DispositionDigest: disposition.Digest, PlanRevisionRef: forecast.PlanRevisionRef,
		PlanPreimageRef: forecast.PlanPreimageRef, SubjectRef: forecast.SubjectRef,
		CandidateRef: forecast.CandidateRef, Cost: cost, Availability: forecast.Availability,
		AvailabilityRef: forecast.AvailabilityRef, ActorRef: disposition.ActorRef,
		DecisionRef: disposition.DecisionRef, ActionTicketRef: actionTicketRef,
		SlotBindingRef: slotBindingRef, Slot: slot, Capability: capability, Ordinal: ordinal,
	}
	ref, err := verificationReservationDigest(reservation)
	if err != nil {
		return VerificationReservation{}, err
	}
	reservation.ReservationRef = ref
	if err := reservation.Validate(); err != nil {
		return VerificationReservation{}, err
	}
	return reservation, nil
}

func (reservation VerificationReservation) Validate() error {
	if reservation.Schema != VerificationReservationSchemaV1 {
		return errors.New("unsupported verification reservation schema")
	}
	if !workRunIDPattern.MatchString(reservation.WorkRunID) {
		return errors.New("verification reservation has an invalid work run identifier")
	}
	for name, ref := range map[string]string{
		"reservationRef": reservation.ReservationRef, "forecastDigest": reservation.ForecastDigest,
		"expectedWorkRevision": reservation.ExpectedWorkRevision,
		"dispositionDigest":    reservation.DispositionDigest, "planRevisionRef": reservation.PlanRevisionRef,
		"planPreimageRef": reservation.PlanPreimageRef, "subjectRef": reservation.SubjectRef,
		"candidateRef": reservation.CandidateRef, "availabilityRef": reservation.AvailabilityRef,
		"actorRef": reservation.ActorRef, "decisionRef": reservation.DecisionRef,
		"actionTicketRef": reservation.ActionTicketRef, "slotBindingRef": reservation.SlotBindingRef,
	} {
		if !validSHA256Ref(ref) {
			return fmt.Errorf("verification reservation %s must be an immutable SHA-256 reference", name)
		}
	}
	if reservation.Availability != ForecastAvailable || !validVerificationCost(reservation.Cost) ||
		reservation.Cost == reviewtransaction.VerificationCostUnknown {
		return errors.New("verification reservation requires available work with a known cost")
	}
	if !validIdentifier(reservation.Slot) || !validIdentifier(reservation.Capability) {
		return errors.New("verification reservation slot and capability must be canonical identifiers")
	}
	if reservation.Ordinal < 1 {
		return errors.New("verification reservation ordinal must be positive")
	}
	want, err := verificationReservationDigest(reservation)
	if err != nil {
		return err
	}
	if reservation.ReservationRef != want {
		return errors.New("verification reservation reference does not match its canonical content")
	}
	return nil
}

// VerificationLaunchClaim is coordination state only. It proves that HCR
// consumed a reservation before process creation; it contains no process result
// or verification outcome.
type VerificationLaunchClaim struct {
	Schema            string `json:"schema"`
	ClaimRef          string `json:"claim_ref"`
	ReservationRef    string `json:"reservation_ref"`
	ActionTicketRef   string `json:"action_ticket_ref"`
	HostRequestDigest string `json:"host_request_digest"`
}

func newVerificationLaunchClaim(
	reservationRef string,
	actionTicketRef string,
	hostRequestDigest string,
) (VerificationLaunchClaim, error) {
	claim := VerificationLaunchClaim{
		Schema:         VerificationLaunchClaimSchemaV1,
		ReservationRef: reservationRef, ActionTicketRef: actionTicketRef,
		HostRequestDigest: hostRequestDigest,
	}
	ref, err := verificationLaunchClaimDigest(claim)
	if err != nil {
		return VerificationLaunchClaim{}, err
	}
	claim.ClaimRef = ref
	if err := claim.Validate(); err != nil {
		return VerificationLaunchClaim{}, err
	}
	return claim, nil
}

func (claim VerificationLaunchClaim) Validate() error {
	if claim.Schema != VerificationLaunchClaimSchemaV1 {
		return errors.New("unsupported verification launch claim schema")
	}
	for name, ref := range map[string]string{
		"claimRef": claim.ClaimRef, "reservationRef": claim.ReservationRef,
		"actionTicketRef": claim.ActionTicketRef, "hostRequestDigest": claim.HostRequestDigest,
	} {
		if !validSHA256Ref(ref) {
			return fmt.Errorf("verification launch claim %s must be an immutable SHA-256 reference", name)
		}
	}
	want, err := verificationLaunchClaimDigest(claim)
	if err != nil {
		return err
	}
	if claim.ClaimRef != want {
		return errors.New("verification launch claim reference does not match its canonical content")
	}
	return nil
}

func candidateRefForSubject(subject reviewtransaction.VerificationSubject) string {
	if !validSHA256Ref(subject.SnapshotIdentity) {
		return ""
	}
	return subject.SnapshotIdentity
}

func implementationHandoffDigest(handoff ImplementationHandoff) (string, error) {
	value := handoff
	value.Digest = ""
	return digestValue("gentle-ai.implementation-handoff-digest/v1", value)
}

func implementationHandoffDigestV1(
	handoff ImplementationHandoff,
) (string, error) {
	legacy := struct {
		Schema                 string                                `json:"schema"`
		Route                  ImplementationRoute                   `json:"route"`
		ScopeDigest            string                                `json:"scope_digest"`
		Subject                reviewtransaction.VerificationSubject `json:"subject"`
		CandidateRef           string                                `json:"candidate_ref"`
		DeclaredObligationRefs []string                              `json:"declared_obligation_refs"`
		EvidenceRefs           []string                              `json:"evidence_refs"`
		SDDRunRef              string                                `json:"sdd_run_ref,omitempty"`
		Digest                 string                                `json:"digest"`
	}{
		Schema:                 handoff.Schema,
		Route:                  handoff.Route,
		ScopeDigest:            handoff.ScopeDigest,
		Subject:                handoff.Subject,
		CandidateRef:           handoff.CandidateRef,
		DeclaredObligationRefs: handoff.DeclaredObligationRefs,
		EvidenceRefs:           handoff.EvidenceRefs,
		SDDRunRef:              handoff.SDDRunRef,
	}
	return digestValue("gentle-ai.implementation-handoff-digest/v1", legacy)
}

func verificationForecastDigest(forecast VerificationForecast) (string, error) {
	value := forecast
	value.Digest = ""
	return digestValue("gentle-ai.verification-forecast-digest/v1", value)
}

func verificationDispositionDigest(disposition VerificationDisposition) (string, error) {
	value := disposition
	value.Digest = ""
	return digestValue("gentle-ai.verification-disposition-digest/v1", value)
}

func verificationReservationDigest(reservation VerificationReservation) (string, error) {
	value := reservation
	value.ReservationRef = ""
	return digestValue("gentle-ai.verification-reservation-digest/v1", value)
}

func verificationLaunchClaimDigest(claim VerificationLaunchClaim) (string, error) {
	value := claim
	value.ClaimRef = ""
	return digestValue("gentle-ai.verification-launch-claim-digest/v1", value)
}

func verificationReplanDigest(replan VerificationReplan) (string, error) {
	value := replan
	value.Digest = ""
	return digestValue("gentle-ai.verification-replan-digest/v1", value)
}

func verificationStopDigest(stop VerificationStop) (string, error) {
	value := stop
	value.Digest = ""
	return digestValue("gentle-ai.verification-stop-digest/v1", value)
}

func digestValue(domain string, value any) (string, error) {
	payload, err := json.Marshal(value)
	if err != nil {
		return "", err
	}
	hash := sha256.New()
	_, _ = hash.Write([]byte(domain))
	_, _ = hash.Write([]byte{0})
	_, _ = hash.Write(payload)
	return "sha256:" + hex.EncodeToString(hash.Sum(nil)), nil
}

func canonicalSHA256Refs(values []string) ([]string, error) {
	result := append([]string(nil), values...)
	if result == nil {
		result = []string{}
	}
	sort.Strings(result)
	for index, value := range result {
		if !validSHA256Ref(value) {
			return nil, fmt.Errorf("invalid immutable SHA-256 reference %q", value)
		}
		if index > 0 && result[index-1] == value {
			return nil, fmt.Errorf("duplicate immutable SHA-256 reference %q", value)
		}
	}
	return result, nil
}

func isCanonicalSHA256Refs(values []string) bool {
	if values == nil {
		return false
	}
	for index, value := range values {
		if !validSHA256Ref(value) || index > 0 && values[index-1] >= value {
			return false
		}
	}
	return true
}

func canonicalIdentifiers(values []string) []string {
	result := append([]string(nil), values...)
	if result == nil {
		result = []string{}
	}
	sort.Strings(result)
	compacted := result[:0]
	for _, value := range result {
		if len(compacted) == 0 || compacted[len(compacted)-1] != value {
			compacted = append(compacted, value)
		}
	}
	return compacted
}

func canonicalIdentifiersEqual(values []string) bool {
	if values == nil {
		return false
	}
	for index, value := range values {
		if !validIdentifier(value) || index > 0 && values[index-1] >= value {
			return false
		}
	}
	return true
}

func validIdentifier(value string) bool {
	if value == "" || value != strings.TrimSpace(value) || len(value) > 128 {
		return false
	}
	for index, char := range value {
		if char >= 'a' && char <= 'z' || char >= '0' && char <= '9' {
			continue
		}
		if index > 0 && strings.ContainsRune("._/-", char) {
			continue
		}
		return false
	}
	return true
}

func validSHA256Ref(value string) bool {
	return workRevisionPattern.MatchString(value)
}

func validOpaqueRef(value string) bool {
	return value != "" && utf8.ValidString(value) &&
		hasCanonicalTextEdges(value) &&
		utf8.RuneCountInString(value) <= 240 &&
		!strings.ContainsAny(value, "\r\n\x00")
}

func validVerificationCost(cost reviewtransaction.VerificationCost) bool {
	switch cost {
	case reviewtransaction.VerificationCostQuick, reviewtransaction.VerificationCostLong,
		reviewtransaction.VerificationCostVeryLong, reviewtransaction.VerificationCostUnknown:
		return true
	default:
		return false
	}
}

func maximumVerificationCost(
	obligations []reviewtransaction.VerificationObligation,
) *reviewtransaction.VerificationCost {
	if len(obligations) == 0 {
		return nil
	}
	maximum := obligations[0].Cost
	for _, obligation := range obligations[1:] {
		if verificationCostRank(obligation.Cost) > verificationCostRank(maximum) {
			maximum = obligation.Cost
		}
	}
	result := maximum
	return &result
}

func verificationCostRank(cost reviewtransaction.VerificationCost) int {
	switch cost {
	case reviewtransaction.VerificationCostQuick:
		return 1
	case reviewtransaction.VerificationCostLong:
		return 2
	case reviewtransaction.VerificationCostVeryLong:
		return 3
	case reviewtransaction.VerificationCostUnknown:
		return 4
	default:
		return 5
	}
}

func equalVerificationCost(
	left *reviewtransaction.VerificationCost,
	right *reviewtransaction.VerificationCost,
) bool {
	if left == nil || right == nil {
		return left == nil && right == nil
	}
	return *left == *right
}

func equalStrings(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}
