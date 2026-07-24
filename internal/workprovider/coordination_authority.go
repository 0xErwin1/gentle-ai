package workprovider

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sort"

	"github.com/gentleman-programming/gentle-ai/internal/reviewtransaction"
	"github.com/gentleman-programming/gentle-ai/internal/workrun"
)

const (
	ForecastAuthorityRecordSchema    = "gentle-ai.forecast-authority-record/v1"
	DispositionAuthorityRecordSchema = "gentle-ai.disposition-authority-record/v1"
	RouteSelectionRecordSchema       = "gentle-ai.route-selection-record/v1"

	forecastAuthorityDigestDomain    = "gentle-ai.forecast-authority-record-digest/v1"
	dispositionAuthorityDigestDomain = "gentle-ai.disposition-authority-record-digest/v1"
	routeSelectionDigestDomain       = "gentle-ai.route-selection-record-digest/v1"
)

var (
	ErrCoordinationAuthorityConflict = errors.New(
		"coordination authority slot conflicts with an existing publication",
	)
	ErrCoordinationAuthorityCorrupt = errors.New(
		"coordination authority repository is corrupt",
	)
	ErrCoordinationAuthorityStale = errors.New(
		"coordination authority no longer has live owner preimages",
	)
	ErrCoordinationAuthorityCrossRun = errors.New(
		"coordination authority belongs to another work run",
	)
	ErrPlanAuthorityUnavailable = errors.New(
		"RAR plan authority resolver is unavailable",
	)
)

// RARPlanAuthorityPort is the minimum RAR seam needed by coordination.
// The provider stores only immutable RAR bindings and resolves this complete
// preimage on every forecast or disposition read.
type RARPlanAuthorityPort interface {
	ResolvePlan(context.Context, string) (reviewtransaction.RARPlanAuthority, error)
}

// ForecastAuthorityPublication contains an owner observation plus the exact
// implementation handoff and RAR plan revision it observed. The handoff and
// RAR preimages are validation inputs; they are not copied into the record.
type ForecastAuthorityPublication struct {
	WorkRunID       string
	Handoff         workrun.ImplementationHandoff
	PlanRevisionRef string
	Availability    workrun.ForecastAvailability
	DiagnosticRefs  []string
}

// ForecastAuthorityRecord is the provider-owned immutable observation.
// Applicability, registry, and plan preimages remain exclusively in RAR.
type ForecastAuthorityRecord struct {
	Schema              string                       `json:"schema"`
	AuthorityRef        string                       `json:"authority_ref"`
	RepositoryIdentity  string                       `json:"repository_identity"`
	WorkRunID           string                       `json:"work_run_id"`
	HandoffDigest       string                       `json:"handoff_digest"`
	CandidateRef        string                       `json:"candidate_ref"`
	PlanRevisionRef     string                       `json:"plan_revision_ref"`
	ApplicabilityDigest string                       `json:"applicability_digest"`
	PlanRegistryDigest  string                       `json:"plan_registry_digest"`
	PlanDigest          string                       `json:"plan_digest"`
	Availability        workrun.ForecastAvailability `json:"availability"`
	DiagnosticRefs      []string                     `json:"diagnostic_refs"`
}

// DispositionAuthorityPublication binds a human/owner decision to the exact
// WorkRun forecast already recorded for the handoff and RAR plan.
type DispositionAuthorityPublication struct {
	WorkRunID      string
	Handoff        workrun.ImplementationHandoff
	Forecast       workrun.VerificationForecast
	Kind           workrun.VerificationDispositionKind
	AssumptionsRef string
	ActorRef       string
	RunnerRef      string
}

// DispositionAuthorityRecord never grants process-launch authority by itself.
// It records only the exact owner decision consumed by WorkRun.
type DispositionAuthorityRecord struct {
	Schema               string                              `json:"schema"`
	AuthorityRef         string                              `json:"authority_ref"`
	RepositoryIdentity   string                              `json:"repository_identity"`
	WorkRunID            string                              `json:"work_run_id"`
	ForecastDigest       string                              `json:"forecast_digest"`
	ForecastAuthorityRef string                              `json:"forecast_authority_ref"`
	HandoffDigest        string                              `json:"handoff_digest"`
	CandidateRef         string                              `json:"candidate_ref"`
	PlanRevisionRef      string                              `json:"plan_revision_ref"`
	AssumptionsRef       string                              `json:"assumptions_ref"`
	Kind                 workrun.VerificationDispositionKind `json:"kind"`
	ActorRef             string                              `json:"actor_ref"`
	RunnerRef            string                              `json:"runner_ref,omitempty"`
}

// RouteSelectionPublication is a provider-owned choice for one pending route
// decision. Direct reroutes bind the selected decision digest; SDD acceptance
// binds the pending decision only, matching WorkRun's established contract.
type RouteSelectionPublication struct {
	WorkRunID              string
	PendingDecisionDigest  string
	SelectedRoute          workrun.ImplementationRoute
	SelectedDecisionDigest string
}

type RouteSelectionRecord struct {
	Schema                 string                      `json:"schema"`
	AuthorityRef           string                      `json:"authority_ref"`
	RepositoryIdentity     string                      `json:"repository_identity"`
	WorkRunID              string                      `json:"work_run_id"`
	PendingDecisionDigest  string                      `json:"pending_decision_digest"`
	SelectedRoute          workrun.ImplementationRoute `json:"selected_route"`
	SelectedDecisionDigest string                      `json:"selected_decision_digest,omitempty"`
}

func newForecastAuthorityRecord(
	repositoryIdentity string,
	request ForecastAuthorityPublication,
	plan reviewtransaction.RARPlanAuthority,
) (ForecastAuthorityRecord, error) {
	if err := validateCoordinationWorkRunID(request.WorkRunID); err != nil {
		return ForecastAuthorityRecord{}, err
	}
	if err := request.Handoff.Validate(); err != nil {
		return ForecastAuthorityRecord{}, fmt.Errorf("validate forecast handoff: %w", err)
	}
	if err := validateResolvedPlanAuthority(
		plan,
		repositoryIdentity,
		request.PlanRevisionRef,
	); err != nil {
		return ForecastAuthorityRecord{}, err
	}
	if request.Handoff.Subject != plan.Subject ||
		request.Handoff.CandidateRef != plan.Subject.SnapshotIdentity {
		return ForecastAuthorityRecord{}, errors.New(
			"forecast handoff does not bind the exact RAR plan subject",
		)
	}
	diagnostics, err := canonicalCoordinationRefs(request.DiagnosticRefs)
	if err != nil || !equalCoordinationStrings(diagnostics, request.DiagnosticRefs) {
		if err != nil {
			return ForecastAuthorityRecord{}, err
		}
		return ForecastAuthorityRecord{}, errors.New(
			"forecast diagnostic references must already be canonical",
		)
	}
	if err := validateForecastAvailability(
		request.Availability,
		diagnostics,
		plan.Plan,
	); err != nil {
		return ForecastAuthorityRecord{}, err
	}
	record := ForecastAuthorityRecord{
		Schema:              ForecastAuthorityRecordSchema,
		RepositoryIdentity:  repositoryIdentity,
		WorkRunID:           request.WorkRunID,
		HandoffDigest:       request.Handoff.Digest,
		CandidateRef:        request.Handoff.CandidateRef,
		PlanRevisionRef:     plan.AuthorityRef,
		ApplicabilityDigest: plan.Applicability.Digest,
		PlanRegistryDigest:  plan.Registry.Digest,
		PlanDigest:          plan.Plan.Digest,
		Availability:        request.Availability,
		DiagnosticRefs:      diagnostics,
	}
	ref, err := forecastAuthorityRecordDigest(record)
	if err != nil {
		return ForecastAuthorityRecord{}, err
	}
	record.AuthorityRef = ref
	if err := record.Validate(); err != nil {
		return ForecastAuthorityRecord{}, err
	}
	return record, nil
}

func (record ForecastAuthorityRecord) Validate() error {
	if record.Schema != ForecastAuthorityRecordSchema {
		return errors.New("unsupported forecast authority record schema")
	}
	if err := validateCoordinationRecordIdentity(
		record.AuthorityRef,
		record.RepositoryIdentity,
		record.WorkRunID,
	); err != nil {
		return err
	}
	for name, ref := range map[string]string{
		"handoff digest":       record.HandoffDigest,
		"candidate ref":        record.CandidateRef,
		"plan revision ref":    record.PlanRevisionRef,
		"applicability digest": record.ApplicabilityDigest,
		"plan registry digest": record.PlanRegistryDigest,
		"plan digest":          record.PlanDigest,
	} {
		if !validCoordinationRef(ref) {
			return fmt.Errorf("forecast authority %s is invalid", name)
		}
	}
	if !canonicalCoordinationRefsEqual(record.DiagnosticRefs) {
		return errors.New("forecast authority diagnostics are not canonical")
	}
	switch record.Availability {
	case workrun.ForecastAvailable, workrun.ForecastNotRequired,
		workrun.ForecastPartial, workrun.ForecastUnavailable,
		workrun.ForecastUnknown:
	default:
		return fmt.Errorf(
			"unsupported forecast availability %q",
			record.Availability,
		)
	}
	want, err := forecastAuthorityRecordDigest(record)
	if err != nil {
		return err
	}
	if record.AuthorityRef != want {
		return errors.New(
			"forecast authority ref does not match canonical content",
		)
	}
	return nil
}

func newDispositionAuthorityRecord(
	repositoryIdentity string,
	request DispositionAuthorityPublication,
	forecastRecord ForecastAuthorityRecord,
	forecastAuthority workrun.VerificationForecastAuthority,
) (DispositionAuthorityRecord, error) {
	if err := validateCoordinationWorkRunID(request.WorkRunID); err != nil {
		return DispositionAuthorityRecord{}, err
	}
	if err := request.Handoff.Validate(); err != nil {
		return DispositionAuthorityRecord{}, fmt.Errorf(
			"validate disposition handoff: %w",
			err,
		)
	}
	if err := request.Forecast.Validate(); err != nil {
		return DispositionAuthorityRecord{}, fmt.Errorf(
			"validate disposition forecast: %w",
			err,
		)
	}
	if forecastRecord.WorkRunID != request.WorkRunID ||
		request.Forecast.WorkRunID != request.WorkRunID ||
		forecastRecord.HandoffDigest != request.Handoff.Digest ||
		request.Forecast.HandoffDigest != request.Handoff.Digest ||
		forecastRecord.CandidateRef != request.Handoff.CandidateRef ||
		request.Forecast.CandidateRef != request.Handoff.CandidateRef ||
		request.Forecast.AvailabilityRef != forecastRecord.AuthorityRef ||
		request.Forecast.PlanRevisionRef != forecastRecord.PlanRevisionRef {
		return DispositionAuthorityRecord{}, errors.New(
			"disposition does not bind the published forecast, handoff, and work run",
		)
	}
	if err := request.Forecast.MatchesPlan(
		forecastAuthority.Applicability,
		forecastAuthority.Registry,
		forecastAuthority.Plan,
	); err != nil {
		return DispositionAuthorityRecord{}, fmt.Errorf(
			"match disposition forecast to live RAR plan: %w",
			err,
		)
	}
	if err := forecastAuthority.MatchesInput(workrun.VerificationForecastInput{
		WorkRunID:       request.WorkRunID,
		Handoff:         request.Handoff,
		Applicability:   forecastAuthority.Applicability,
		Registry:        forecastAuthority.Registry,
		Plan:            forecastAuthority.Plan,
		PlanRevisionRef: forecastRecord.PlanRevisionRef,
		Availability:    forecastRecord.Availability,
		AvailabilityRef: forecastRecord.AuthorityRef,
		DiagnosticRefs:  cloneCoordinationStrings(forecastRecord.DiagnosticRefs),
	}); err != nil {
		return DispositionAuthorityRecord{}, fmt.Errorf(
			"match disposition to forecast authority: %w",
			err,
		)
	}
	record := DispositionAuthorityRecord{
		Schema:               DispositionAuthorityRecordSchema,
		RepositoryIdentity:   repositoryIdentity,
		WorkRunID:            request.WorkRunID,
		ForecastDigest:       request.Forecast.Digest,
		ForecastAuthorityRef: forecastRecord.AuthorityRef,
		HandoffDigest:        request.Handoff.Digest,
		CandidateRef:         request.Handoff.CandidateRef,
		PlanRevisionRef:      forecastRecord.PlanRevisionRef,
		AssumptionsRef:       request.AssumptionsRef,
		Kind:                 request.Kind,
		ActorRef:             request.ActorRef,
		RunnerRef:            request.RunnerRef,
	}
	ref, err := dispositionAuthorityRecordDigest(record)
	if err != nil {
		return DispositionAuthorityRecord{}, err
	}
	record.AuthorityRef = ref
	if err := record.Validate(); err != nil {
		return DispositionAuthorityRecord{}, err
	}
	authority := record.dispositionAuthority()
	if err := authority.Validate(request.Forecast); err != nil {
		return DispositionAuthorityRecord{}, err
	}
	return record, nil
}

func (record DispositionAuthorityRecord) Validate() error {
	if record.Schema != DispositionAuthorityRecordSchema {
		return errors.New("unsupported disposition authority record schema")
	}
	if err := validateCoordinationRecordIdentity(
		record.AuthorityRef,
		record.RepositoryIdentity,
		record.WorkRunID,
	); err != nil {
		return err
	}
	for name, ref := range map[string]string{
		"forecast digest":        record.ForecastDigest,
		"forecast authority ref": record.ForecastAuthorityRef,
		"handoff digest":         record.HandoffDigest,
		"candidate ref":          record.CandidateRef,
		"plan revision ref":      record.PlanRevisionRef,
		"assumptions ref":        record.AssumptionsRef,
		"actor ref":              record.ActorRef,
	} {
		if !validCoordinationRef(ref) {
			return fmt.Errorf("disposition authority %s is invalid", name)
		}
	}
	switch record.Kind {
	case workrun.DispositionRun, workrun.DispositionDefer,
		workrun.DispositionReduceScope:
		if record.RunnerRef != "" {
			return errors.New(
				"disposition runner is valid only for deferred_runner",
			)
		}
	case workrun.DispositionDeferredRunner:
		if !validCoordinationRef(record.RunnerRef) {
			return errors.New(
				"deferred-runner disposition requires an immutable runner ref",
			)
		}
	default:
		return fmt.Errorf("unsupported verification disposition %q", record.Kind)
	}
	want, err := dispositionAuthorityRecordDigest(record)
	if err != nil {
		return err
	}
	if record.AuthorityRef != want {
		return errors.New(
			"disposition authority ref does not match canonical content",
		)
	}
	return nil
}

func (record DispositionAuthorityRecord) dispositionAuthority() workrun.VerificationDispositionAuthority {
	return workrun.VerificationDispositionAuthority{
		DecisionRef:    record.AuthorityRef,
		ForecastDigest: record.ForecastDigest,
		AssumptionsRef: record.AssumptionsRef,
		Kind:           record.Kind,
		ActorRef:       record.ActorRef,
		RunnerRef:      record.RunnerRef,
	}
}

func newRouteSelectionRecord(
	repositoryIdentity string,
	request RouteSelectionPublication,
) (RouteSelectionRecord, error) {
	if err := validateCoordinationWorkRunID(request.WorkRunID); err != nil {
		return RouteSelectionRecord{}, err
	}
	if !validCoordinationRef(request.PendingDecisionDigest) {
		return RouteSelectionRecord{}, errors.New(
			"route selection requires an immutable pending decision digest",
		)
	}
	switch request.SelectedRoute {
	case workrun.ImplementationRouteSDD:
		if request.SelectedDecisionDigest != "" {
			return RouteSelectionRecord{}, errors.New(
				"SDD route selection cannot carry a replacement decision",
			)
		}
	case workrun.ImplementationRouteDirectInline,
		workrun.ImplementationRouteDelegatedDirect:
		if !validCoordinationRef(request.SelectedDecisionDigest) {
			return RouteSelectionRecord{}, errors.New(
				"direct route selection requires the selected decision digest",
			)
		}
	default:
		return RouteSelectionRecord{}, fmt.Errorf(
			"unsupported selected implementation route %q",
			request.SelectedRoute,
		)
	}
	record := RouteSelectionRecord{
		Schema:                 RouteSelectionRecordSchema,
		RepositoryIdentity:     repositoryIdentity,
		WorkRunID:              request.WorkRunID,
		PendingDecisionDigest:  request.PendingDecisionDigest,
		SelectedRoute:          request.SelectedRoute,
		SelectedDecisionDigest: request.SelectedDecisionDigest,
	}
	ref, err := routeSelectionRecordDigest(record)
	if err != nil {
		return RouteSelectionRecord{}, err
	}
	record.AuthorityRef = ref
	if err := record.Validate(); err != nil {
		return RouteSelectionRecord{}, err
	}
	return record, nil
}

func (record RouteSelectionRecord) Validate() error {
	if record.Schema != RouteSelectionRecordSchema {
		return errors.New("unsupported route selection record schema")
	}
	if err := validateCoordinationRecordIdentity(
		record.AuthorityRef,
		record.RepositoryIdentity,
		record.WorkRunID,
	); err != nil {
		return err
	}
	if !validCoordinationRef(record.PendingDecisionDigest) {
		return errors.New("route selection pending decision digest is invalid")
	}
	switch record.SelectedRoute {
	case workrun.ImplementationRouteSDD:
		if record.SelectedDecisionDigest != "" {
			return errors.New(
				"SDD route selection cannot carry a replacement decision",
			)
		}
	case workrun.ImplementationRouteDirectInline,
		workrun.ImplementationRouteDelegatedDirect:
		if !validCoordinationRef(record.SelectedDecisionDigest) {
			return errors.New(
				"direct route selection requires an immutable selected decision digest",
			)
		}
	default:
		return fmt.Errorf(
			"unsupported selected implementation route %q",
			record.SelectedRoute,
		)
	}
	want, err := routeSelectionRecordDigest(record)
	if err != nil {
		return err
	}
	if record.AuthorityRef != want {
		return errors.New(
			"route selection ref does not match canonical content",
		)
	}
	return nil
}

func (record RouteSelectionRecord) routeAuthority() workrun.RouteSelectionAuthority {
	return workrun.RouteSelectionAuthority{
		DecisionRef:            record.AuthorityRef,
		PendingDecisionDigest:  record.PendingDecisionDigest,
		SelectedRoute:          record.SelectedRoute,
		SelectedDecisionDigest: record.SelectedDecisionDigest,
	}
}

func validateResolvedPlanAuthority(
	authority reviewtransaction.RARPlanAuthority,
	repositoryIdentity string,
	planRevisionRef string,
) error {
	if err := authority.Validate(); err != nil {
		return fmt.Errorf("validate RAR plan authority: %w", err)
	}
	if authority.AuthorityRef != planRevisionRef ||
		authority.RepositoryIdentity != repositoryIdentity {
		return errors.New(
			"RAR plan authority does not bind this repository and revision",
		)
	}
	return nil
}

func validateForecastAvailability(
	availability workrun.ForecastAvailability,
	diagnostics []string,
	plan reviewtransaction.VerificationPlan,
) error {
	switch availability {
	case workrun.ForecastAvailable:
		if plan.Applicability != reviewtransaction.VerificationApplicable ||
			len(plan.Obligations) == 0 {
			return errors.New(
				"available verification requires an applicable RAR plan",
			)
		}
		if len(diagnostics) != 0 {
			return errors.New(
				"available verification cannot carry blocker diagnostics",
			)
		}
	case workrun.ForecastNotRequired:
		if plan.Applicability != reviewtransaction.VerificationNotApplicable ||
			len(plan.Obligations) != 0 {
			return errors.New(
				"not-required verification requires a native not-applicable plan",
			)
		}
	case workrun.ForecastPartial, workrun.ForecastUnavailable:
		if plan.Applicability != reviewtransaction.VerificationApplicable ||
			len(diagnostics) == 0 {
			return errors.New(
				"partial or unavailable verification requires an applicable plan and diagnostics",
			)
		}
	case workrun.ForecastUnknown:
		if plan.Applicability != reviewtransaction.VerificationUnknown ||
			len(diagnostics) == 0 {
			return errors.New(
				"unknown verification requires an unknown plan and diagnostics",
			)
		}
	default:
		return fmt.Errorf("unsupported forecast availability %q", availability)
	}
	return nil
}

func validateCoordinationRecordIdentity(
	authorityRef string,
	repositoryIdentity string,
	workRunID string,
) error {
	if !validCoordinationRef(authorityRef) ||
		!validCoordinationRef(repositoryIdentity) {
		return errors.New("coordination authority identity is invalid")
	}
	return validateCoordinationWorkRunID(workRunID)
}

func validateCoordinationWorkRunID(workRunID string) error {
	if !workRunIDPattern.MatchString(workRunID) {
		return errors.New("coordination authority work run identifier is invalid")
	}
	return nil
}

func forecastAuthorityRecordDigest(record ForecastAuthorityRecord) (string, error) {
	record.AuthorityRef = ""
	return coordinationDigest(forecastAuthorityDigestDomain, record)
}

func dispositionAuthorityRecordDigest(record DispositionAuthorityRecord) (string, error) {
	record.AuthorityRef = ""
	return coordinationDigest(dispositionAuthorityDigestDomain, record)
}

func routeSelectionRecordDigest(record RouteSelectionRecord) (string, error) {
	record.AuthorityRef = ""
	return coordinationDigest(routeSelectionDigestDomain, record)
}

func coordinationDigest(domain string, value any) (string, error) {
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

func canonicalCoordinationPayload(value any) ([]byte, error) {
	payload, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	return append(payload, '\n'), nil
}

func decodeStrictCoordinationJSON(payload []byte, target any) error {
	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return errors.New("coordination authority JSON has trailing content")
	}
	return nil
}

func canonicalCoordinationRefs(values []string) ([]string, error) {
	result := cloneCoordinationStrings(values)
	if result == nil {
		result = []string{}
	}
	sort.Strings(result)
	for index, value := range result {
		if !validCoordinationRef(value) {
			return nil, fmt.Errorf(
				"invalid immutable coordination reference %q",
				value,
			)
		}
		if index > 0 && result[index-1] == value {
			return nil, fmt.Errorf(
				"duplicate immutable coordination reference %q",
				value,
			)
		}
	}
	return result, nil
}

func canonicalCoordinationRefsEqual(values []string) bool {
	if values == nil {
		return false
	}
	for index, value := range values {
		if !validCoordinationRef(value) ||
			index > 0 && values[index-1] >= value {
			return false
		}
	}
	return true
}

func validCoordinationRef(value string) bool {
	return revisionPattern.MatchString(value)
}

func cloneCoordinationStrings(values []string) []string {
	if values == nil {
		return nil
	}
	result := make([]string, len(values))
	copy(result, values)
	return result
}

func equalCoordinationStrings(left, right []string) bool {
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

type forecastDispositionAuthorityPort interface {
	ResolveForecast(
		context.Context,
		string,
	) (workrun.VerificationForecastAuthority, error)
	ResolveDisposition(
		context.Context,
		string,
	) (workrun.VerificationDispositionAuthority, error)
}

var _ forecastDispositionAuthorityPort = (*CoordinationAuthorityStore)(nil)
var _ workrun.RouteAuthorityPort = (*CoordinationAuthorityStore)(nil)
