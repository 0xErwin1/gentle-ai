package workrun

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
)

const (
	ImplementationRouteDecisionSchemaV1 = "gentle-ai.implementation-route-decision/v1"
	ImplementationRoutingRulesV1        = "gentle-ai.implementation-routing-rules/v1"
)

var (
	ErrInvalidRoutingFacts      = errors.New("invalid implementation routing facts")
	ErrInsufficientRoutingFacts = errors.New("insufficient implementation routing facts")
)

type ReadIntent string

const (
	ReadIntentNone              ReadIntent = "none"
	ReadIntentDecideOrVerify    ReadIntent = "decide_or_verify"
	ReadIntentExploreUnderstand ReadIntent = "explore_or_understand"
	ReadIntentPrepareWrite      ReadIntent = "prepare_write"
)

type WriteIntent string

const (
	WriteIntentNone             WriteIntent = "none"
	WriteIntentAtomicMechanical WriteIntent = "atomic_mechanical"
	WriteIntentAnalytical       WriteIntent = "analytical"
)

type DurablePlanningReason string

const (
	PlanningArchitecture    DurablePlanningReason = "architecture"
	PlanningCrossContext    DurablePlanningReason = "cross_context"
	PlanningMigration       DurablePlanningReason = "migration"
	PlanningMultiWorkUnit   DurablePlanningReason = "multi_work_unit"
	PlanningProductDecision DurablePlanningReason = "product_decision"
	PlanningRollback        DurablePlanningReason = "rollback"
	PlanningSecurity        DurablePlanningReason = "security"
)

type RouteDecisionReason string

const (
	RouteReasonBroadResearch             RouteDecisionReason = "broad_research"
	RouteReasonExplicitSDD               RouteDecisionReason = "explicit_sdd_request"
	RouteReasonFourOrMoreFiles           RouteDecisionReason = "four_or_more_files_to_understand"
	RouteReasonOneMechanicalFile         RouteDecisionReason = "one_mechanical_already_understood_file"
	RouteReasonOneToThreeDecisionFiles   RouteDecisionReason = "one_to_three_files_to_decide_or_verify"
	RouteReasonReadingPreparesWrite      RouteDecisionReason = "reading_prepares_write"
	RouteReasonTwoOrMoreNontrivialWrites RouteDecisionReason = "two_or_more_nontrivial_write_files"
	RouteReasonPlanningArchitecture      RouteDecisionReason = "planning_architecture"
	RouteReasonPlanningCrossContext      RouteDecisionReason = "planning_cross_context"
	RouteReasonPlanningMigration         RouteDecisionReason = "planning_migration"
	RouteReasonPlanningMultiWorkUnit     RouteDecisionReason = "planning_multi_work_unit"
	RouteReasonPlanningProductDecision   RouteDecisionReason = "planning_product_decision"
	RouteReasonPlanningRollback          RouteDecisionReason = "planning_rollback"
	RouteReasonPlanningSecurity          RouteDecisionReason = "planning_security"
)

// ImplementationRouteInput contains context-management facts only. Safety and
// risk deliberately do not participate in implementation-route selection.
type ImplementationRouteInput struct {
	ReadIntent            ReadIntent
	ReadFileCount         int
	WriteIntent           WriteIntent
	WriteFileCount        int
	BroadResearch         bool
	DurablePlanning       []DurablePlanningReason
	ExplicitSDDRequestRef string
}

// ImplementationRouteDecision records the planning decision before any
// lifecycle is created. propose_sdd is intentionally not an executable route.
type ImplementationRouteDecision struct {
	Schema                string                `json:"schema"`
	RulesVersion          string                `json:"rules_version"`
	Decision              RouteDecision         `json:"decision"`
	Reasons               []RouteDecisionReason `json:"reasons"`
	ExplicitSDDRequestRef string                `json:"explicit_sdd_request_ref,omitempty"`
	Digest                string                `json:"digest"`
}

func DecideImplementationRoute(input ImplementationRouteInput) (ImplementationRouteDecision, error) {
	if input.ReadIntent == "" {
		input.ReadIntent = ReadIntentNone
	}
	if input.WriteIntent == "" {
		input.WriteIntent = WriteIntentNone
	}
	if err := ValidateImplementationRouteInput(input); err != nil {
		return ImplementationRouteDecision{}, err
	}

	reasons := make([]RouteDecisionReason, 0, 8)
	for _, reason := range input.DurablePlanning {
		reasons = append(reasons, routeReasonForPlanning(reason))
	}
	if input.ExplicitSDDRequestRef != "" {
		reasons = append(reasons, RouteReasonExplicitSDD)
	}
	if len(reasons) > 0 {
		return newImplementationRouteDecision(RouteDecisionProposeSDD, reasons, input.ExplicitSDDRequestRef)
	}

	if input.ReadIntent == ReadIntentPrepareWrite {
		reasons = append(reasons, RouteReasonReadingPreparesWrite)
	}
	if input.ReadIntent == ReadIntentExploreUnderstand && input.ReadFileCount >= 4 {
		reasons = append(reasons, RouteReasonFourOrMoreFiles)
	}
	if input.WriteIntent == WriteIntentAnalytical && input.WriteFileCount >= 2 {
		reasons = append(reasons, RouteReasonTwoOrMoreNontrivialWrites)
	}
	if input.BroadResearch {
		reasons = append(reasons, RouteReasonBroadResearch)
	}
	if len(reasons) > 0 {
		return newImplementationRouteDecision(RouteDecisionDelegatedDirect, reasons, "")
	}
	if input.WriteIntent == WriteIntentAnalytical {
		return ImplementationRouteDecision{}, fmt.Errorf(
			"%w: a one-file analytical write is neither declared mechanical nor delegation-required",
			ErrInsufficientRoutingFacts,
		)
	}

	if input.ReadIntent == ReadIntentDecideOrVerify && input.ReadFileCount >= 1 && input.ReadFileCount <= 3 {
		reasons = append(reasons, RouteReasonOneToThreeDecisionFiles)
	}
	if input.WriteIntent == WriteIntentAtomicMechanical && input.WriteFileCount == 1 {
		reasons = append(reasons, RouteReasonOneMechanicalFile)
	}
	if len(reasons) == 0 {
		return ImplementationRouteDecision{}, fmt.Errorf(
			"%w: facts match neither a bounded inline action nor a mandatory delegation rule",
			ErrInsufficientRoutingFacts,
		)
	}
	return newImplementationRouteDecision(RouteDecisionDirectInline, reasons, "")
}

func (decision ImplementationRouteDecision) Validate() error {
	if decision.Schema != ImplementationRouteDecisionSchemaV1 ||
		decision.RulesVersion != ImplementationRoutingRulesV1 {
		return errors.New("unsupported implementation route decision schema or rules")
	}
	if len(decision.Reasons) == 0 || !canonicalRouteReasons(decision.Reasons) {
		return errors.New("implementation route decision reasons must be non-empty and canonical")
	}
	switch decision.Decision {
	case RouteDecisionDirectInline:
		if decision.ExplicitSDDRequestRef != "" || !onlyDirectReasons(decision.Reasons) {
			return errors.New("direct implementation decision contains incompatible reasons")
		}
	case RouteDecisionDelegatedDirect:
		if decision.ExplicitSDDRequestRef != "" || !onlyDelegatedReasons(decision.Reasons) {
			return errors.New("delegated implementation decision contains incompatible reasons")
		}
	case RouteDecisionProposeSDD:
		if !onlyProposalReasons(decision.Reasons) {
			return errors.New("SDD proposal contains incompatible reasons")
		}
		if decision.ExplicitSDDRequestRef != "" && !validSHA256Ref(decision.ExplicitSDDRequestRef) {
			return errors.New("explicit SDD request must be an immutable SHA-256 reference")
		}
	default:
		return fmt.Errorf("unsupported implementation route decision %q", decision.Decision)
	}
	want, err := implementationRouteDecisionDigest(decision)
	if err != nil {
		return err
	}
	if decision.Digest != want {
		return errors.New("implementation route decision digest does not match its canonical content")
	}
	return nil
}

func newImplementationRouteDecision(
	decision RouteDecision,
	reasons []RouteDecisionReason,
	explicitSDDRequestRef string,
) (ImplementationRouteDecision, error) {
	reasons = append([]RouteDecisionReason(nil), reasons...)
	sort.Slice(reasons, func(i, j int) bool { return reasons[i] < reasons[j] })
	reasons = compactRouteReasons(reasons)
	result := ImplementationRouteDecision{
		Schema: ImplementationRouteDecisionSchemaV1, RulesVersion: ImplementationRoutingRulesV1,
		Decision: decision, Reasons: reasons, ExplicitSDDRequestRef: explicitSDDRequestRef,
	}
	digest, err := implementationRouteDecisionDigest(result)
	if err != nil {
		return ImplementationRouteDecision{}, err
	}
	result.Digest = digest
	if err := result.Validate(); err != nil {
		return ImplementationRouteDecision{}, err
	}
	return result, nil
}

// ValidateImplementationRouteInput validates owner-typed routing facts without
// requiring them to select an executable write route. This distinction lets a
// trusted intake classify an exact no-write outcome before any managed
// lifecycle exists.
func ValidateImplementationRouteInput(input ImplementationRouteInput) error {
	if input.ReadIntent == "" {
		input.ReadIntent = ReadIntentNone
	}
	if input.WriteIntent == "" {
		input.WriteIntent = WriteIntentNone
	}
	if input.ReadFileCount < 0 || input.WriteFileCount < 0 ||
		input.ReadFileCount > 100_000 || input.WriteFileCount > 100_000 {
		return fmt.Errorf("%w: file counts are outside supported bounds", ErrInvalidRoutingFacts)
	}
	switch input.ReadIntent {
	case ReadIntentNone:
		if input.ReadFileCount != 0 {
			return fmt.Errorf("%w: read count requires a read intent", ErrInvalidRoutingFacts)
		}
	case ReadIntentDecideOrVerify:
		if input.ReadFileCount < 1 || input.ReadFileCount > 3 {
			return fmt.Errorf(
				"%w: decide-or-verify reads must contain exactly 1-3 files",
				ErrInvalidRoutingFacts,
			)
		}
	case ReadIntentExploreUnderstand:
		if input.ReadFileCount < 4 {
			return fmt.Errorf(
				"%w: explore-or-understand delegation starts at 4 files",
				ErrInvalidRoutingFacts,
			)
		}
	case ReadIntentPrepareWrite:
		if input.ReadFileCount < 1 {
			return fmt.Errorf("%w: preparation reads require at least one file", ErrInvalidRoutingFacts)
		}
	default:
		return fmt.Errorf("%w: unsupported read intent %q", ErrInvalidRoutingFacts, input.ReadIntent)
	}
	switch input.WriteIntent {
	case WriteIntentNone:
		if input.WriteFileCount != 0 {
			return fmt.Errorf("%w: write count requires a write intent", ErrInvalidRoutingFacts)
		}
	case WriteIntentAtomicMechanical:
		if input.WriteFileCount != 1 {
			return fmt.Errorf(
				"%w: atomic mechanical work is exactly one already-understood file",
				ErrInvalidRoutingFacts,
			)
		}
	case WriteIntentAnalytical:
		if input.WriteFileCount < 1 {
			return fmt.Errorf("%w: analytical work requires at least one file", ErrInvalidRoutingFacts)
		}
	default:
		return fmt.Errorf("%w: unsupported write intent %q", ErrInvalidRoutingFacts, input.WriteIntent)
	}
	if input.ExplicitSDDRequestRef != "" && !validSHA256Ref(input.ExplicitSDDRequestRef) {
		return fmt.Errorf("%w: explicit SDD request must be an immutable SHA-256 reference", ErrInvalidRoutingFacts)
	}
	planning := append([]DurablePlanningReason(nil), input.DurablePlanning...)
	sort.Slice(planning, func(i, j int) bool { return planning[i] < planning[j] })
	for index, reason := range planning {
		if !validPlanningReason(reason) {
			return fmt.Errorf("%w: unsupported durable planning reason %q", ErrInvalidRoutingFacts, reason)
		}
		if index > 0 && planning[index-1] == reason {
			return fmt.Errorf("%w: duplicate durable planning reason %q", ErrInvalidRoutingFacts, reason)
		}
	}
	return nil
}

func implementationRouteDecisionDigest(decision ImplementationRouteDecision) (string, error) {
	preimage := decision
	preimage.Digest = ""
	payload, err := json.Marshal(preimage)
	if err != nil {
		return "", err
	}
	hash := sha256.New()
	_, _ = hash.Write([]byte("gentle-ai.implementation-route-decision-digest/v1"))
	_, _ = hash.Write([]byte{0})
	_, _ = hash.Write(payload)
	return "sha256:" + hex.EncodeToString(hash.Sum(nil)), nil
}

func validPlanningReason(reason DurablePlanningReason) bool {
	switch reason {
	case PlanningArchitecture, PlanningCrossContext, PlanningMigration, PlanningMultiWorkUnit,
		PlanningProductDecision, PlanningRollback, PlanningSecurity:
		return true
	default:
		return false
	}
}

func routeReasonForPlanning(reason DurablePlanningReason) RouteDecisionReason {
	switch reason {
	case PlanningArchitecture:
		return RouteReasonPlanningArchitecture
	case PlanningCrossContext:
		return RouteReasonPlanningCrossContext
	case PlanningMigration:
		return RouteReasonPlanningMigration
	case PlanningMultiWorkUnit:
		return RouteReasonPlanningMultiWorkUnit
	case PlanningProductDecision:
		return RouteReasonPlanningProductDecision
	case PlanningRollback:
		return RouteReasonPlanningRollback
	case PlanningSecurity:
		return RouteReasonPlanningSecurity
	default:
		panic("routeReasonForPlanning called with invalid reason")
	}
}

func canonicalRouteReasons(reasons []RouteDecisionReason) bool {
	for index, reason := range reasons {
		if reason == "" || index > 0 && reasons[index-1] >= reason {
			return false
		}
	}
	return true
}

func compactRouteReasons(reasons []RouteDecisionReason) []RouteDecisionReason {
	result := reasons[:0]
	for _, reason := range reasons {
		if len(result) == 0 || result[len(result)-1] != reason {
			result = append(result, reason)
		}
	}
	if result == nil {
		return []RouteDecisionReason{}
	}
	return result
}

func onlyDirectReasons(reasons []RouteDecisionReason) bool {
	for _, reason := range reasons {
		if reason != RouteReasonOneMechanicalFile && reason != RouteReasonOneToThreeDecisionFiles {
			return false
		}
	}
	return true
}

func onlyDelegatedReasons(reasons []RouteDecisionReason) bool {
	for _, reason := range reasons {
		switch reason {
		case RouteReasonBroadResearch, RouteReasonFourOrMoreFiles,
			RouteReasonReadingPreparesWrite, RouteReasonTwoOrMoreNontrivialWrites:
		default:
			return false
		}
	}
	return true
}

func onlyProposalReasons(reasons []RouteDecisionReason) bool {
	for _, reason := range reasons {
		switch reason {
		case RouteReasonExplicitSDD, RouteReasonPlanningArchitecture, RouteReasonPlanningCrossContext,
			RouteReasonPlanningMigration, RouteReasonPlanningMultiWorkUnit, RouteReasonPlanningProductDecision,
			RouteReasonPlanningRollback, RouteReasonPlanningSecurity:
		default:
			return false
		}
	}
	return true
}
