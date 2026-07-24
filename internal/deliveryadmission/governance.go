package deliveryadmission

import (
	"context"
	"fmt"
)

const (
	GovernanceDecisionContractV1 = "gentle-ai.delivery-governance-decision/v1"
	ResidualRiskContractV1       = "gentle-ai.delivery-residual-risk/v1"
)

type GovernanceDecisionKind string

const (
	GovernanceApprovedIssue GovernanceDecisionKind = "approved_issue"
	GovernanceEmergency     GovernanceDecisionKind = "emergency"
	GovernanceException     GovernanceDecisionKind = "exception"
)

func (kind GovernanceDecisionKind) valid() bool {
	return kind == GovernanceApprovedIssue || kind == GovernanceEmergency ||
		kind == GovernanceException
}

// ResidualRisk is an immutable owner-authored preimage. SubjectRef is an intent
// for initial emergency admission and a delivery decision for an exception.
type ResidualRisk struct {
	Schema      string             `json:"schema"`
	RiskID      string             `json:"riskId"`
	SubjectRef  string             `json:"subjectRef"`
	Route       Route              `json:"route"`
	Candidate   *CandidateBinding  `json:"candidate,omitempty"`
	Destination DestinationBinding `json:"destination"`
	PolicyRef   string             `json:"policyRef"`
	Policy      PolicyBinding      `json:"policy"`
	Description string             `json:"description"`
	ExpiresAt   int64              `json:"expiresAt"`
}

func (risk ResidualRisk) Validate() error {
	if risk.Schema != ResidualRiskContractV1 || !risk.Route.valid() || risk.ExpiresAt <= 0 {
		return fmt.Errorf("%w: residual-risk header", ErrInvalid)
	}
	if err := validateToken("residualRisk.riskId", risk.RiskID); err != nil {
		return err
	}
	if err := validateDigest("residualRisk.subjectRef", risk.SubjectRef); err != nil {
		return err
	}
	if err := risk.Destination.validate(); err != nil {
		return err
	}
	if err := validateDigest("residualRisk.policyRef", risk.PolicyRef); err != nil {
		return err
	}
	if err := risk.Policy.validate(); err != nil {
		return err
	}
	if err := validateText("residualRisk.description", risk.Description); err != nil {
		return err
	}
	if risk.Candidate != nil {
		if err := risk.Candidate.validate(); err != nil {
			return err
		}
	}
	return nil
}

func (risk ResidualRisk) Ref() (string, error) {
	return contentRef(risk, risk.Validate())
}

// GovernanceDecision is the owner-authenticated route-specific decision. A
// caller submits only its immutable reference; it cannot author approval,
// emergency, or exception facts in an admission request.
type GovernanceDecision struct {
	Schema             string                 `json:"schema"`
	Kind               GovernanceDecisionKind `json:"kind"`
	DecisionID         string                 `json:"decisionId"`
	SubjectRef         string                 `json:"subjectRef"`
	Route              Route                  `json:"route"`
	RepositoryRef      string                 `json:"repositoryRef"`
	Candidate          *CandidateBinding      `json:"candidate,omitempty"`
	Destination        DestinationBinding     `json:"destination"`
	PolicyRef          string                 `json:"policyRef"`
	Policy             PolicyBinding          `json:"policy"`
	AuthorityRef       string                 `json:"authorityRef"`
	SecondAuthorityRef string                 `json:"secondAuthorityRef,omitempty"`
	ApprovedIssueRef   string                 `json:"approvedIssueRef,omitempty"`
	Reason             string                 `json:"reason,omitempty"`
	ResidualRiskRef    string                 `json:"residualRiskRef,omitempty"`
	ExpiresAt          int64                  `json:"expiresAt"`
}

func (decision GovernanceDecision) Validate() error {
	if decision.Schema != GovernanceDecisionContractV1 || !decision.Kind.valid() ||
		!decision.Route.valid() || decision.ExpiresAt <= 0 {
		return fmt.Errorf("%w: governance-decision header", ErrInvalid)
	}
	if err := validateToken("governance.decisionId", decision.DecisionID); err != nil {
		return err
	}
	if err := validateDigest("governance.subjectRef", decision.SubjectRef); err != nil {
		return err
	}
	if err := validateToken("governance.repositoryRef", decision.RepositoryRef); err != nil {
		return err
	}
	if err := decision.Destination.validate(); err != nil {
		return err
	}
	if err := validateDigest("governance.policyRef", decision.PolicyRef); err != nil {
		return err
	}
	if err := decision.Policy.validate(); err != nil {
		return err
	}
	if err := validateDigest("governance.authorityRef", decision.AuthorityRef); err != nil {
		return err
	}
	if decision.SecondAuthorityRef != "" {
		if err := validateDigest("governance.secondAuthorityRef", decision.SecondAuthorityRef); err != nil {
			return err
		}
	}
	switch decision.Kind {
	case GovernanceApprovedIssue:
		if decision.Route != RoutePRWithIssue || decision.Candidate != nil ||
			decision.SecondAuthorityRef != "" ||
			decision.Reason != "" || decision.ResidualRiskRef != "" {
			return fmt.Errorf("%w: approved-issue governance shape", ErrInvalid)
		}
		return validateToken("governance.approvedIssueRef", decision.ApprovedIssueRef)
	case GovernanceEmergency:
		if decision.Route != RouteEmergency || decision.Candidate != nil ||
			decision.ApprovedIssueRef != "" {
			return fmt.Errorf("%w: emergency governance shape", ErrInvalid)
		}
	case GovernanceException:
		if decision.Candidate == nil || decision.ApprovedIssueRef != "" {
			return fmt.Errorf("%w: exception governance shape", ErrInvalid)
		}
		if err := decision.Candidate.validate(); err != nil {
			return err
		}
	}
	if err := validateText("governance.reason", decision.Reason); err != nil {
		return err
	}
	return validateDigest("governance.residualRiskRef", decision.ResidualRiskRef)
}

func (decision GovernanceDecision) Ref() (string, error) {
	return contentRef(decision, decision.Validate())
}

func resolveGovernance(
	ctx context.Context,
	resolver TrustedResolver,
	ref string,
) (GovernanceDecision, error) {
	if resolver == nil {
		return GovernanceDecision{}, fmt.Errorf("%w: missing resolver", ErrUntrustedPreimage)
	}
	return resolveExact(
		ref, resolver.ResolveGovernanceDecision, ctx,
		func(value GovernanceDecision) (string, error) { return value.Ref() },
	)
}

func resolveResidualRisk(
	ctx context.Context,
	resolver TrustedResolver,
	ref string,
) (ResidualRisk, error) {
	if resolver == nil {
		return ResidualRisk{}, fmt.Errorf("%w: missing resolver", ErrUntrustedPreimage)
	}
	return resolveExact(
		ref, resolver.ResolveResidualRisk, ctx,
		func(value ResidualRisk) (string, error) { return value.Ref() },
	)
}

func validateGovernanceRisk(
	ctx context.Context,
	resolver TrustedResolver,
	governance GovernanceDecision,
	now int64,
) (ResidualRisk, error) {
	risk, err := resolveResidualRisk(ctx, resolver, governance.ResidualRiskRef)
	if err != nil {
		return ResidualRisk{}, err
	}
	if risk.SubjectRef != governance.SubjectRef || risk.Route != governance.Route ||
		!sameOptionalCandidate(risk.Candidate, governance.Candidate) ||
		risk.Destination != governance.Destination ||
		risk.PolicyRef != governance.PolicyRef || risk.Policy != governance.Policy ||
		now >= risk.ExpiresAt {
		return ResidualRisk{}, fmt.Errorf("%w: residual-risk governance binding/window", ErrBindingMismatch)
	}
	return risk, nil
}

func sameOptionalCandidate(left, right *CandidateBinding) bool {
	if left == nil || right == nil {
		return left == nil && right == nil
	}
	return *left == *right
}
