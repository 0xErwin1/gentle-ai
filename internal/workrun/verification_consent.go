package workrun

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/gentleman-programming/gentle-ai/internal/reviewtransaction"
)

const (
	WorkAdvanceContractV2              = "gentle-ai.work-advance/v2"
	WorkVerificationDecideContractV1   = "gentle-ai.work-verification-decide/v1"
	VerificationDecisionPromptSchemaV1 = "gentle-ai.verification-decision-prompt/v1"
)

type VerificationDecisionChoice string

const (
	VerificationDecisionRun            VerificationDecisionChoice = "run"
	VerificationDecisionDefer          VerificationDecisionChoice = "defer"
	VerificationDecisionReduceScope    VerificationDecisionChoice = "reduce_scope"
	VerificationDecisionDeferredRunner VerificationDecisionChoice = "deferred_runner"
)

// VerificationDecisionPromptV1 is the immutable owner-authored checkpoint
// projected by work-advance/v2. The caller may echo only PromptRef and one
// offered Choice. It never carries actor, decision, runner, command, or launch
// authority.
type VerificationDecisionPromptV1 struct {
	Schema           string                             `json:"schema"`
	Contract         string                             `json:"contract"`
	Operation        string                             `json:"operation"`
	PromptRef        string                             `json:"promptRef"`
	WorkRunID        string                             `json:"workRunId"`
	ExpectedRevision string                             `json:"expectedRevision"`
	ForecastRef      string                             `json:"forecastRef"`
	AssumptionsRef   string                             `json:"assumptionsRef"`
	Cost             reviewtransaction.VerificationCost `json:"cost"`
	Choices          []VerificationDecisionChoice       `json:"choices"`
}

type VerificationDecisionPromptRequest struct {
	WorkRunID        string
	ExpectedRevision string
	Forecast         VerificationForecast
	AssumptionsRef   string
	Choices          []VerificationDecisionChoice
}

func NewVerificationDecisionPrompt(
	request VerificationDecisionPromptRequest,
) (VerificationDecisionPromptV1, error) {
	if err := request.Forecast.Validate(); err != nil {
		return VerificationDecisionPromptV1{}, err
	}
	if request.Forecast.MaximumCost == nil {
		return VerificationDecisionPromptV1{}, errors.New(
			"verification decision prompt requires an applicable forecast cost",
		)
	}
	prompt := VerificationDecisionPromptV1{
		Schema:           VerificationDecisionPromptSchemaV1,
		Contract:         WorkVerificationDecideContractV1,
		Operation:        "decide",
		WorkRunID:        request.WorkRunID,
		ExpectedRevision: request.ExpectedRevision,
		ForecastRef:      request.Forecast.Digest,
		AssumptionsRef:   request.AssumptionsRef,
		Cost:             *request.Forecast.MaximumCost,
		Choices:          append([]VerificationDecisionChoice{}, request.Choices...),
	}
	ref, err := verificationDecisionPromptRef(prompt)
	if err != nil {
		return VerificationDecisionPromptV1{}, err
	}
	prompt.PromptRef = ref
	if err := prompt.ValidateFor(request.Forecast); err != nil {
		return VerificationDecisionPromptV1{}, err
	}
	return prompt, nil
}

func (prompt VerificationDecisionPromptV1) Validate() error {
	if prompt.Schema != VerificationDecisionPromptSchemaV1 ||
		prompt.Contract != WorkVerificationDecideContractV1 ||
		prompt.Operation != "decide" {
		return errors.New("verification decision prompt has an unsupported contract")
	}
	if !workRunIDPattern.MatchString(prompt.WorkRunID) ||
		!validSHA256Ref(prompt.PromptRef) ||
		!validSHA256Ref(prompt.ExpectedRevision) ||
		!validSHA256Ref(prompt.ForecastRef) ||
		!validSHA256Ref(prompt.AssumptionsRef) {
		return errors.New("verification decision prompt has invalid immutable bindings")
	}
	switch prompt.Cost {
	case reviewtransaction.VerificationCostQuick,
		reviewtransaction.VerificationCostLong,
		reviewtransaction.VerificationCostVeryLong,
		reviewtransaction.VerificationCostUnknown:
	default:
		return fmt.Errorf(
			"verification decision prompt cannot expose cost %q",
			prompt.Cost,
		)
	}
	if !canonicalVerificationChoices(prompt.Choices) {
		return errors.New("verification decision prompt choices must be canonical")
	}
	if !containsVerificationChoice(prompt.Choices, VerificationDecisionDefer) ||
		!containsVerificationChoice(
			prompt.Choices,
			VerificationDecisionReduceScope,
		) {
		return errors.New(
			"verification decision prompt must preserve defer and reduce-scope escapes",
		)
	}
	switch prompt.Cost {
	case reviewtransaction.VerificationCostQuick,
		reviewtransaction.VerificationCostLong,
		reviewtransaction.VerificationCostVeryLong:
		if !containsVerificationChoice(
			prompt.Choices,
			VerificationDecisionRun,
		) {
			return errors.New(
				"consent-required known-cost verification must offer run",
			)
		}
		if prompt.Cost == reviewtransaction.VerificationCostQuick &&
			containsVerificationChoice(
				prompt.Choices,
				VerificationDecisionDeferredRunner,
			) {
			return errors.New(
				"quick verification cannot offer a deferred runner",
			)
		}
	case reviewtransaction.VerificationCostUnknown:
		if containsVerificationChoice(
			prompt.Choices,
			VerificationDecisionRun,
		) || containsVerificationChoice(
			prompt.Choices,
			VerificationDecisionDeferredRunner,
		) {
			return errors.New(
				"unknown-cost verification cannot offer launch authority",
			)
		}
	}
	want, err := verificationDecisionPromptRef(prompt)
	if err != nil {
		return err
	}
	if prompt.PromptRef != want {
		return errors.New(
			"verification decision prompt ref does not match its canonical content",
		)
	}
	return nil
}

func (prompt VerificationDecisionPromptV1) ValidateFor(
	forecast VerificationForecast,
) error {
	if err := prompt.Validate(); err != nil {
		return err
	}
	if err := forecast.Validate(); err != nil {
		return err
	}
	if forecast.MaximumCost == nil ||
		forecast.Availability != ForecastAvailable ||
		!forecastRequiresExplicitConsent(forecast) ||
		prompt.WorkRunID != forecast.WorkRunID ||
		prompt.ForecastRef != forecast.Digest ||
		prompt.Cost != *forecast.MaximumCost {
		return errors.New(
			"verification decision prompt does not bind the exact available forecast",
		)
	}
	return nil
}

// WorkAdvanceV2 preserves both terminal v1 branches and adds one non-terminal
// durable verification-decision checkpoint. PreviousRevision binds the
// productive call that reached the checkpoint; Status and the prompt expose
// the same current WorkRun CAS used by DecideVerification. The checkpoint
// carries no blocker, delivery result, or executable transition.
type WorkAdvanceV2 struct {
	Schema               string                        `json:"schema"`
	Contract             string                        `json:"contract"`
	PreviousRevision     string                        `json:"previousRevision"`
	Status               WorkStatusV1                  `json:"status"`
	VerificationDecision *VerificationDecisionPromptV1 `json:"verificationDecision,omitempty"`
	DeliveryResultRef    string                        `json:"deliveryResultRef,omitempty"`
	Diagnostic           *WorkAdvanceDiagnosticV1      `json:"diagnostic,omitempty"`
}

func (advance WorkAdvanceV2) Validate() error {
	if advance.Schema != WorkAdvanceContractV2 ||
		advance.Contract != WorkAdvanceContractV2 {
		return errors.New("work advance v2 must use gentle-ai.work-advance/v2")
	}
	if advance.VerificationDecision == nil {
		return (WorkAdvanceV1{
			Schema:            WorkAdvanceContractV1,
			Contract:          WorkAdvanceContractV1,
			PreviousRevision:  advance.PreviousRevision,
			Status:            advance.Status,
			DeliveryResultRef: advance.DeliveryResultRef,
			Diagnostic:        advance.Diagnostic,
		}).Validate()
	}
	if !validSHA256Ref(advance.PreviousRevision) {
		return errors.New("work advance v2 previousRevision must be sha256")
	}
	if err := advance.Status.Validate(); err != nil {
		return err
	}
	if advance.Status.Revision == advance.PreviousRevision {
		return errors.New("work advance v2 checkpoint must expose its current CAS")
	}
	if advance.Status.AuthorizedTransition != nil {
		return errors.New(
			"work advance v2 cannot mix a public transition with driver authority",
		)
	}
	if err := advance.VerificationDecision.Validate(); err != nil {
		return err
	}
	if advance.Status.PublicState != PublicStateNeedsYourDecision ||
		advance.Status.RoutePhase != RoutePhaseImplementationSelected ||
		advance.Status.Verification.Outcome != VerificationPending ||
		len(advance.Status.Verification.ResultRefs) != 0 ||
		advance.Status.DeliveryIntentRef == "" ||
		advance.Status.ReviewReceiptRef != "" ||
		advance.Status.Diagnostic != nil ||
		advance.DeliveryResultRef != "" ||
		advance.Diagnostic != nil ||
		advance.VerificationDecision.WorkRunID != advance.Status.WorkRunID ||
		advance.VerificationDecision.ExpectedRevision !=
			advance.Status.Revision {
		return errors.New(
			"work advance v2 verification checkpoint has mixed or stale authority",
		)
	}
	return nil
}

// WorkVerificationDecideV1 is the exact public receipt for one consent
// mutation. It records disposition only. Run exposes one bounded resume
// revision for a later work-advance/v2; exact request replay remains
// idempotent. No branch carries launch or transition authority.
type WorkVerificationDecideV1 struct {
	Schema           string                     `json:"schema"`
	Contract         string                     `json:"contract"`
	WorkRunID        string                     `json:"workRunId"`
	PreviousRevision string                     `json:"previousRevision"`
	PromptRef        string                     `json:"promptRef"`
	ForecastRef      string                     `json:"forecastRef"`
	AssumptionsRef   string                     `json:"assumptionsRef"`
	Choice           VerificationDecisionChoice `json:"choice"`
	DispositionRef   string                     `json:"dispositionRef"`
	Status           WorkStatusV1               `json:"status"`
}

func (decision WorkVerificationDecideV1) Validate() error {
	if decision.Schema != WorkVerificationDecideContractV1 ||
		decision.Contract != WorkVerificationDecideContractV1 {
		return errors.New(
			"work verification decision must use gentle-ai.work-verification-decide/v1",
		)
	}
	if !workRunIDPattern.MatchString(decision.WorkRunID) {
		return errors.New("work verification decision workRunId is invalid")
	}
	for name, ref := range map[string]string{
		"previousRevision": decision.PreviousRevision,
		"promptRef":        decision.PromptRef,
		"forecastRef":      decision.ForecastRef,
		"assumptionsRef":   decision.AssumptionsRef,
		"dispositionRef":   decision.DispositionRef,
	} {
		if !validSHA256Ref(ref) {
			return fmt.Errorf(
				"work verification decision %s must be sha256",
				name,
			)
		}
	}
	if err := decision.Status.Validate(); err != nil {
		return err
	}
	if decision.Status.WorkRunID != decision.WorkRunID ||
		decision.Status.Revision == decision.PreviousRevision ||
		decision.Status.AuthorizedTransition != nil ||
		decision.Status.RoutePhase != RoutePhaseImplementationSelected ||
		decision.Status.Verification.Outcome != VerificationPending ||
		len(decision.Status.Verification.ResultRefs) != 0 ||
		decision.Status.DeliveryIntentRef == "" ||
		decision.Status.ReviewReceiptRef != "" ||
		decision.Status.Diagnostic != nil {
		return errors.New(
			"work verification decision status does not bind its exact mutation",
		)
	}
	kind, err := dispositionKindForChoice(decision.Choice)
	if err != nil {
		return err
	}
	if kind == DispositionRun {
		if decision.Status.PublicState != PublicStateChecking {
			return errors.New(
				"accepted run verification decision must remain checking",
			)
		}
		return nil
	}
	if decision.Status.PublicState != PublicStateNeedsYourDecision {
		return errors.New(
			"non-run verification decision must stop without launching an advance",
		)
	}
	return nil
}

func dispositionKindForChoice(
	choice VerificationDecisionChoice,
) (VerificationDispositionKind, error) {
	switch choice {
	case VerificationDecisionRun:
		return DispositionRun, nil
	case VerificationDecisionDefer:
		return DispositionDefer, nil
	case VerificationDecisionReduceScope:
		return DispositionReduceScope, nil
	case VerificationDecisionDeferredRunner:
		return DispositionDeferredRunner, nil
	default:
		return "", fmt.Errorf(
			"unsupported verification decision choice %q",
			choice,
		)
	}
}

func verificationDecisionPromptRef(
	prompt VerificationDecisionPromptV1,
) (string, error) {
	prompt.PromptRef = ""
	payload, err := json.Marshal(prompt)
	if err != nil {
		return "", err
	}
	hash := sha256.New()
	_, _ = hash.Write(
		[]byte("gentle-ai.verification-decision-prompt-digest/v1"),
	)
	_, _ = hash.Write([]byte{0})
	_, _ = hash.Write(payload)
	return "sha256:" + hex.EncodeToString(hash.Sum(nil)), nil
}

func canonicalVerificationChoices(
	choices []VerificationDecisionChoice,
) bool {
	if len(choices) == 0 {
		return false
	}
	previousOrder := -1
	for _, choice := range choices {
		if _, err := dispositionKindForChoice(choice); err != nil {
			return false
		}
		order := verificationChoiceOrder(choice)
		if order <= previousOrder {
			return false
		}
		previousOrder = order
	}
	return true
}

func verificationChoiceOrder(choice VerificationDecisionChoice) int {
	switch choice {
	case VerificationDecisionRun:
		return 0
	case VerificationDecisionDefer:
		return 1
	case VerificationDecisionReduceScope:
		return 2
	case VerificationDecisionDeferredRunner:
		return 3
	default:
		return 100
	}
}

func containsVerificationChoice(
	choices []VerificationDecisionChoice,
	choice VerificationDecisionChoice,
) bool {
	for _, candidate := range choices {
		if candidate == choice {
			return true
		}
	}
	return false
}
