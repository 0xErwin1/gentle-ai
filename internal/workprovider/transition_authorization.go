package workprovider

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"

	"github.com/gentleman-programming/gentle-ai/internal/workrun"
)

const VerificationTransitionAuthorizationSchemaV1 = "gentle-ai.verification-transition-authorization/v1"

var sha256AuthorityRefPattern = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)

// VerificationTransitionAuthorization is the immutable owner preimage for the
// sole public common-work mutation. It authorizes one exact WorkRun.Begin; argv
// and environment remain exclusively in the referenced EPD ActionTicket.
type VerificationTransitionAuthorization struct {
	Schema                     string             `json:"schema"`
	AuthorizationRef           string             `json:"authorizationRef"`
	WorkRunID                  string             `json:"workRunId"`
	ExpectedRevision           string             `json:"expectedRevision"`
	CandidateRef               string             `json:"candidateRef"`
	ActionTicketRef            string             `json:"actionTicketRef"`
	ApplicableAuthorizationRef string             `json:"applicableAuthorizationRef"`
	Class                      AuthorizationClass `json:"class"`
	IssuedAt                   int64              `json:"issuedAt"`
	ExpiresAt                  int64              `json:"expiresAt"`
}

type VerificationTransitionAuthorizationRequest struct {
	WorkRunID                  string
	ExpectedRevision           string
	CandidateRef               string
	ActionTicketRef            string
	ApplicableAuthorizationRef string
	Class                      AuthorizationClass
	IssuedAt                   int64
	ExpiresAt                  int64
}

func NewVerificationTransitionAuthorization(
	request VerificationTransitionAuthorizationRequest,
) (VerificationTransitionAuthorization, error) {
	authorization := VerificationTransitionAuthorization{
		Schema:    VerificationTransitionAuthorizationSchemaV1,
		WorkRunID: request.WorkRunID, ExpectedRevision: request.ExpectedRevision,
		CandidateRef: request.CandidateRef, ActionTicketRef: request.ActionTicketRef,
		ApplicableAuthorizationRef: request.ApplicableAuthorizationRef,
		Class:                      request.Class, IssuedAt: request.IssuedAt, ExpiresAt: request.ExpiresAt,
	}
	ref, err := verificationTransitionAuthorizationRef(authorization)
	if err != nil {
		return VerificationTransitionAuthorization{}, err
	}
	authorization.AuthorizationRef = ref
	if err := authorization.Validate(); err != nil {
		return VerificationTransitionAuthorization{}, err
	}
	return authorization, nil
}

func (authorization VerificationTransitionAuthorization) Validate() error {
	if authorization.Schema != VerificationTransitionAuthorizationSchemaV1 {
		return errors.New("unsupported verification transition authorization schema")
	}
	if !workRunIDPattern.MatchString(authorization.WorkRunID) {
		return errors.New("verification transition authorization work run is not canonical")
	}
	for name, ref := range map[string]string{
		"authorizationRef":           authorization.AuthorizationRef,
		"expectedRevision":           authorization.ExpectedRevision,
		"candidateRef":               authorization.CandidateRef,
		"actionTicketRef":            authorization.ActionTicketRef,
		"applicableAuthorizationRef": authorization.ApplicableAuthorizationRef,
	} {
		if !sha256AuthorityRefPattern.MatchString(ref) {
			return fmt.Errorf(
				"verification transition authorization %s must be a lowercase SHA-256 ref",
				name,
			)
		}
	}
	if authorization.IssuedAt <= 0 ||
		authorization.ExpiresAt <= authorization.IssuedAt {
		return errors.New("verification transition authorization has an invalid window")
	}
	switch authorization.Class {
	case AuthorizationGeneral, AuthorizationRecovery, AuthorizationTerminalization:
	default:
		return fmt.Errorf(
			"unsupported verification transition authorization class %q",
			authorization.Class,
		)
	}
	want, err := verificationTransitionAuthorizationRef(authorization)
	if err != nil {
		return err
	}
	if authorization.AuthorizationRef != want {
		return errors.New(
			"verification transition authorization ref does not match its canonical content",
		)
	}
	return nil
}

func (authorization VerificationTransitionAuthorization) Resolved() (
	ResolvedAuthorization,
	error,
) {
	if err := authorization.Validate(); err != nil {
		return ResolvedAuthorization{}, err
	}
	resolved := ResolvedAuthorization{
		WorkRunID:                  authorization.WorkRunID,
		AuthorizationRef:           authorization.AuthorizationRef,
		ExpectedRevision:           authorization.ExpectedRevision,
		CandidateRef:               authorization.CandidateRef,
		ActionTicketRef:            authorization.ActionTicketRef,
		ApplicableAuthorizationRef: authorization.ApplicableAuthorizationRef,
		Class:                      authorization.Class,
	}
	return resolved, resolved.Validate()
}

func (authorization VerificationTransitionAuthorization) PublicTransition() (
	workrun.AuthorizedTransitionV1,
	error,
) {
	if _, err := authorization.Resolved(); err != nil {
		return workrun.AuthorizedTransitionV1{}, err
	}
	return workrun.AuthorizedTransitionV1{
		Contract: workrun.WorkTransitionContractV1, Operation: "apply",
		AuthorizationRef:           authorization.AuthorizationRef,
		ExpectedRevision:           authorization.ExpectedRevision,
		CandidateRef:               authorization.CandidateRef,
		ActionTicketRef:            authorization.ActionTicketRef,
		ApplicableAuthorizationRef: authorization.ApplicableAuthorizationRef,
	}, nil
}

func (authorization VerificationTransitionAuthorization) RequestID() (string, error) {
	if err := authorization.Validate(); err != nil {
		return "", err
	}
	return "apply-" + authorization.AuthorizationRef[len("sha256:"):], nil
}

func verificationTransitionAuthorizationRef(
	authorization VerificationTransitionAuthorization,
) (string, error) {
	authorization.AuthorizationRef = ""
	payload, err := json.Marshal(authorization)
	if err != nil {
		return "", err
	}
	hash := sha256.New()
	_, _ = hash.Write([]byte("gentle-ai.verification-transition-authorization-digest/v1"))
	_, _ = hash.Write([]byte{0})
	_, _ = hash.Write(payload)
	return "sha256:" + hex.EncodeToString(hash.Sum(nil)), nil
}
