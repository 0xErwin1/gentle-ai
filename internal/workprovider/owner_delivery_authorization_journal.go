package workprovider

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"path/filepath"
	"strings"

	"github.com/gentleman-programming/gentle-ai/internal/deliveryadmission"
	"github.com/gentleman-programming/gentle-ai/internal/workrun"
)

const (
	ownerPADAuthorizationJournalSchema = "gentle-ai.owner-pad-authorization-journal/v1"
	ownerPADAuthorizationJournalDomain = "gentle-ai.owner-pad-authorization-journal-digest/v1"
	ownerPADAuthorizationSlotDomain    = "gentle-ai.owner-pad-authorization-slot/v1"
)

var ErrPADDeliveryAuthorizationPrepared = errors.New(
	"PAD delivery authorization is already durably prepared",
)

var ErrPADDeliveryAuthorizationPreparedExpired = errors.New(
	"prepared PAD delivery authorization expired before WorkRun binding",
)

// ownerPADAuthorizationJournal is the durable prepare record for the
// cross-store PAD-publication -> WorkRun-binding transaction. One repository
// and WorkRun has exactly one slot, so a crash cannot choose a new decision,
// issuer, trusted time, or authorization ref on retry.
type ownerPADAuthorizationJournal struct {
	Schema                       string                                `json:"schema"`
	JournalRef                   string                                `json:"journal_ref"`
	SlotRef                      string                                `json:"slot_ref"`
	RepositoryIdentity           string                                `json:"repository_identity"`
	WorkRunID                    string                                `json:"work_run_id"`
	PreparedRevision             string                                `json:"prepared_revision"`
	DeliveryIntentRef            string                                `json:"delivery_intent_ref"`
	DeliveryRouteReevaluationRef string                                `json:"delivery_route_reevaluation_ref,omitempty"`
	CandidateRef                 string                                `json:"candidate_ref"`
	VerificationResultRef        string                                `json:"verification_result_ref"`
	ReviewReceiptRef             string                                `json:"review_receipt_ref"`
	DecisionRef                  string                                `json:"decision_ref"`
	Disposition                  deliveryadmission.DeliveryDisposition `json:"disposition"`
	AuthorityRef                 string                                `json:"authority_ref"`
	HumanDecisionRef             string                                `json:"human_decision_ref,omitempty"`
	Nonce                        string                                `json:"nonce"`
	IssuedAt                     int64                                 `json:"issued_at"`
	ExpiresAt                    int64                                 `json:"expires_at"`
	AuthorizationRef             string                                `json:"authorization_ref"`
}

type ownerPADAuthorizationCandidate struct {
	normal    *deliveryadmission.DeliveryAuthorization
	exception *deliveryadmission.DeliveryExceptionAuthorization
}

func (candidate ownerPADAuthorizationCandidate) Binding() deliveryadmission.AuthorizationBinding {
	if candidate.normal != nil {
		return candidate.normal.Binding
	}
	if candidate.exception != nil {
		return candidate.exception.Binding
	}
	return deliveryadmission.AuthorizationBinding{}
}

func (candidate ownerPADAuthorizationCandidate) Ref() (string, error) {
	if candidate.normal != nil && candidate.exception == nil {
		return candidate.normal.Ref()
	}
	if candidate.exception != nil && candidate.normal == nil {
		return candidate.exception.Ref()
	}
	return "", errors.New("owner PAD authorization candidate kind is invalid")
}

func (candidate ownerPADAuthorizationCandidate) Publish(
	ctx context.Context,
	repository ownerPADAuthorizationRepository,
) (string, error) {
	if candidate.normal != nil && candidate.exception == nil {
		return repository.PublishAuthorization(ctx, *candidate.normal)
	}
	if candidate.exception != nil && candidate.normal == nil {
		return repository.PublishExceptionAuthorization(
			ctx,
			*candidate.exception,
		)
	}
	return "", errors.New("owner PAD authorization candidate kind is invalid")
}

type ownerPinnedAuthorizationResolver struct {
	deliveryadmission.TrustedResolver
	issuedAt int64
}

func (resolver ownerPinnedAuthorizationResolver) CurrentUnix(
	ctx context.Context,
) (int64, error) {
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	if resolver.issuedAt <= 0 {
		return 0, errors.New("owner PAD authorization pinned time is invalid")
	}
	return resolver.issuedAt, nil
}

func issueOwnerPADAuthorizationCandidate(
	ctx context.Context,
	repository ownerPADAuthorizationRepository,
	rar deliveryadmission.RARAuthorityPort,
	decision deliveryadmission.DeliveryDecision,
	request OwnerIssueDeliveryAuthorizationRequest,
	nonce string,
	issuedAt int64,
) (ownerPADAuthorizationCandidate, error) {
	resolver := ownerPinnedAuthorizationResolver{
		TrustedResolver: repository,
		issuedAt:        issuedAt,
	}
	issue := deliveryadmission.AuthorizationIssueRequest{
		DecisionRef:  request.DecisionRef,
		Nonce:        nonce,
		AuthorityRef: decision.AuthorityRef,
	}
	switch decision.Disposition {
	case deliveryadmission.DeliveryAuthorizeNormal:
		if request.HumanDecisionRef != "" {
			return ownerPADAuthorizationCandidate{}, fmt.Errorf(
				"%w: normal delivery cannot consume exception governance",
				deliveryadmission.ErrUnauthorized,
			)
		}
		authorization, err := deliveryadmission.IssueAuthorization(
			ctx,
			resolver,
			rar,
			issue,
		)
		if err != nil {
			return ownerPADAuthorizationCandidate{}, err
		}
		return ownerPADAuthorizationCandidate{normal: &authorization}, nil
	case deliveryadmission.DeliveryRequireException:
		if request.HumanDecisionRef == "" {
			return ownerPADAuthorizationCandidate{}, fmt.Errorf(
				"%w: exception delivery requires owner-issued human governance",
				deliveryadmission.ErrExceptionNotPermitted,
			)
		}
		authorization, err :=
			deliveryadmission.IssueExceptionAuthorization(
				ctx,
				resolver,
				rar,
				deliveryadmission.ExceptionAuthorizationIssueRequest{
					AuthorizationIssueRequest: issue,
					HumanDecisionRef:          request.HumanDecisionRef,
				},
			)
		if err != nil {
			return ownerPADAuthorizationCandidate{}, err
		}
		return ownerPADAuthorizationCandidate{
			exception: &authorization,
		}, nil
	case deliveryadmission.DeliveryBlocked:
		return ownerPADAuthorizationCandidate{}, fmt.Errorf(
			"%w: PAD blocked the exact delivery decision",
			deliveryadmission.ErrUnauthorized,
		)
	default:
		return ownerPADAuthorizationCandidate{}, fmt.Errorf(
			"%w: unsupported PAD delivery disposition %q",
			deliveryadmission.ErrInvalid,
			decision.Disposition,
		)
	}
}

func prepareAndPublishOwnerPADAuthorization(
	ctx context.Context,
	coordinator *OwnerCoordinator,
	repository ownerPADAuthorizationRepository,
	state workrun.WorkRunState,
	decision deliveryadmission.DeliveryDecision,
	request OwnerIssueDeliveryAuthorizationRequest,
	nonce string,
) (string, error) {
	repositoryIdentity := coordinator.coordination.identity.repositoryIdentity
	journal, err := resolveOwnerPADAuthorizationJournal(ctx, coordinator)
	switch {
	case err == nil:
		if err := journal.Matches(
			repositoryIdentity,
			coordinator.work.WorkRunID,
			state,
			decision,
			request,
			nonce,
		); err != nil {
			return "", err
		}
	case errors.Is(err, fs.ErrNotExist):
		issuedAt, nowErr := repository.CurrentUnix(ctx)
		if nowErr != nil {
			return "", nowErr
		}
		candidate, issueErr := issueOwnerPADAuthorizationCandidate(
			ctx,
			repository,
			coordinator.rar,
			decision,
			request,
			nonce,
			issuedAt,
		)
		if issueErr != nil {
			return "", issueErr
		}
		journal, err = newOwnerPADAuthorizationJournal(
			repositoryIdentity,
			coordinator.work.WorkRunID,
			state,
			decision,
			request,
			nonce,
			candidate,
		)
		if err != nil {
			return "", err
		}
		if err := publishOwnerPADAuthorizationJournal(
			ctx,
			coordinator,
			journal,
		); err != nil {
			return "", err
		}
	default:
		return "", err
	}

	live, liveErr := repository.ResolveLiveAuthorization(
		ctx,
		journal.AuthorizationRef,
	)
	if liveErr == nil {
		if err := matchOwnerPADAuthorizationJournalLive(
			journal,
			live,
		); err != nil {
			return "", err
		}
		return journal.AuthorizationRef, nil
	}

	candidate, err := issueOwnerPADAuthorizationCandidate(
		ctx,
		repository,
		coordinator.rar,
		decision,
		request,
		nonce,
		journal.IssuedAt,
	)
	if err != nil {
		return "", err
	}
	candidateRef, err := candidate.Ref()
	if err != nil {
		return "", err
	}
	if candidateRef != journal.AuthorizationRef ||
		candidate.Binding().IssuedAt != journal.IssuedAt ||
		candidate.Binding().ExpiresAt != journal.ExpiresAt {
		return "", fmt.Errorf(
			"%w: prepared PAD authorization cannot be reconstructed exactly",
			ErrCoordinationAuthorityCorrupt,
		)
	}
	if errors.Is(liveErr, deliveryadmission.ErrLiveAuthorizationInactive) {
		return journal.AuthorizationRef, nil
	}
	if !errors.Is(
		liveErr,
		deliveryadmission.ErrTrustedRepositoryUnavailable,
	) {
		return "", liveErr
	}
	now, err := repository.CurrentUnix(ctx)
	if err != nil {
		return "", err
	}
	if now < journal.IssuedAt || now >= journal.ExpiresAt {
		return "", fmt.Errorf(
			"%w: %w",
			ErrPADDeliveryAuthorizationPreparedExpired,
			deliveryadmission.ErrExpired,
		)
	}
	publishedRef, err := candidate.Publish(ctx, repository)
	if err != nil {
		return "", err
	}
	if publishedRef != journal.AuthorizationRef {
		return "", fmt.Errorf(
			"%w: PAD published a different prepared authorization",
			deliveryadmission.ErrTrustedRepositoryCorrupt,
		)
	}
	return publishedRef, nil
}

func matchOwnerPADAuthorizationJournalLive(
	journal ownerPADAuthorizationJournal,
	live deliveryadmission.LiveAuthorization,
) error {
	wantKind := deliveryadmission.AuthorizationNormal
	if journal.Disposition == deliveryadmission.DeliveryRequireException {
		wantKind = deliveryadmission.AuthorizationException
	}
	binding := live.Binding
	if live.AuthorizationRef != journal.AuthorizationRef ||
		live.Kind != wantKind ||
		binding.DecisionRef != journal.DecisionRef ||
		binding.IntentRef != journal.DeliveryIntentRef ||
		binding.Candidate.Digest != journal.CandidateRef ||
		binding.VerificationResultRef != journal.VerificationResultRef ||
		binding.ReviewReceiptRef != journal.ReviewReceiptRef ||
		binding.AuthorityRef != journal.AuthorityRef ||
		binding.Nonce != journal.Nonce ||
		binding.IssuedAt != journal.IssuedAt ||
		binding.ExpiresAt != journal.ExpiresAt {
		return fmt.Errorf(
			"%w: live PAD authorization differs from its prepared journal",
			deliveryadmission.ErrTrustedRepositoryCorrupt,
		)
	}
	return nil
}

func newOwnerPADAuthorizationJournal(
	repositoryIdentity string,
	workRunID string,
	state workrun.WorkRunState,
	decision deliveryadmission.DeliveryDecision,
	request OwnerIssueDeliveryAuthorizationRequest,
	nonce string,
	candidate ownerPADAuthorizationCandidate,
) (ownerPADAuthorizationJournal, error) {
	slotRef, err := ownerPADAuthorizationSlotRef(
		repositoryIdentity,
		workRunID,
	)
	if err != nil {
		return ownerPADAuthorizationJournal{}, err
	}
	authorizationRef, err := candidate.Ref()
	if err != nil {
		return ownerPADAuthorizationJournal{}, err
	}
	binding := candidate.Binding()
	record := ownerPADAuthorizationJournal{
		Schema:                       ownerPADAuthorizationJournalSchema,
		SlotRef:                      slotRef,
		RepositoryIdentity:           repositoryIdentity,
		WorkRunID:                    workRunID,
		PreparedRevision:             state.Revision,
		DeliveryIntentRef:            state.DeliveryIntentRef,
		DeliveryRouteReevaluationRef: state.DeliveryRouteReevaluationRef,
		CandidateRef:                 state.Handoff.CandidateRef,
		VerificationResultRef:        state.VerificationResultRef,
		ReviewReceiptRef:             state.ReviewReceiptRef,
		DecisionRef:                  request.DecisionRef,
		Disposition:                  decision.Disposition,
		AuthorityRef:                 decision.AuthorityRef,
		HumanDecisionRef:             request.HumanDecisionRef,
		Nonce:                        nonce,
		IssuedAt:                     binding.IssuedAt,
		ExpiresAt:                    binding.ExpiresAt,
		AuthorizationRef:             authorizationRef,
	}
	record.JournalRef, err = ownerPADAuthorizationJournalRef(record)
	if err != nil {
		return ownerPADAuthorizationJournal{}, err
	}
	if err := record.Validate(); err != nil {
		return ownerPADAuthorizationJournal{}, err
	}
	return record, nil
}

func (record ownerPADAuthorizationJournal) Validate() error {
	if record.Schema != ownerPADAuthorizationJournalSchema {
		return errors.New("unsupported owner PAD authorization journal schema")
	}
	for name, ref := range map[string]string{
		"journal ref":             record.JournalRef,
		"slot ref":                record.SlotRef,
		"repository identity":     record.RepositoryIdentity,
		"prepared revision":       record.PreparedRevision,
		"delivery intent ref":     record.DeliveryIntentRef,
		"candidate ref":           record.CandidateRef,
		"verification result ref": record.VerificationResultRef,
		"review receipt ref":      record.ReviewReceiptRef,
		"decision ref":            record.DecisionRef,
		"authority ref":           record.AuthorityRef,
		"authorization ref":       record.AuthorizationRef,
	} {
		if !validPADImmutableRef(ref) {
			return fmt.Errorf(
				"owner PAD authorization journal %s is invalid",
				name,
			)
		}
	}
	if record.DeliveryRouteReevaluationRef != "" &&
		!validPADImmutableRef(record.DeliveryRouteReevaluationRef) {
		return errors.New(
			"owner PAD authorization journal route reevaluation ref is invalid",
		)
	}
	if record.HumanDecisionRef != "" &&
		!validPADImmutableRef(record.HumanDecisionRef) {
		return errors.New(
			"owner PAD authorization journal human decision ref is invalid",
		)
	}
	if err := validateCoordinationWorkRunID(record.WorkRunID); err != nil {
		return err
	}
	if record.Nonce == "" ||
		record.Nonce != strings.TrimSpace(record.Nonce) ||
		len(record.Nonce) > 512 ||
		record.IssuedAt <= 0 ||
		record.ExpiresAt <= record.IssuedAt {
		return errors.New("owner PAD authorization journal timing or nonce is invalid")
	}
	for _, character := range record.Nonce {
		if character < 0x21 || character > 0x7e {
			return errors.New(
				"owner PAD authorization journal nonce is not printable ASCII",
			)
		}
	}
	switch record.Disposition {
	case deliveryadmission.DeliveryAuthorizeNormal:
		if record.HumanDecisionRef != "" {
			return errors.New(
				"normal owner PAD authorization journal has exception governance",
			)
		}
	case deliveryadmission.DeliveryRequireException:
		if record.HumanDecisionRef == "" {
			return errors.New(
				"exception owner PAD authorization journal lacks human governance",
			)
		}
	default:
		return errors.New(
			"owner PAD authorization journal has a non-authorizing disposition",
		)
	}
	slotRef, err := ownerPADAuthorizationSlotRef(
		record.RepositoryIdentity,
		record.WorkRunID,
	)
	if err != nil || slotRef != record.SlotRef {
		return errors.New("owner PAD authorization journal slot binding differs")
	}
	journalRef, err := ownerPADAuthorizationJournalRef(record)
	if err != nil || journalRef != record.JournalRef {
		return errors.New("owner PAD authorization journal digest differs")
	}
	return nil
}

func (record ownerPADAuthorizationJournal) Matches(
	repositoryIdentity string,
	workRunID string,
	state workrun.WorkRunState,
	decision deliveryadmission.DeliveryDecision,
	request OwnerIssueDeliveryAuthorizationRequest,
	nonce string,
) error {
	if err := record.Validate(); err != nil {
		return err
	}
	if state.Handoff == nil ||
		record.RepositoryIdentity != repositoryIdentity ||
		record.WorkRunID != workRunID ||
		record.PreparedRevision != state.Revision ||
		record.DeliveryIntentRef != state.DeliveryIntentRef ||
		record.DeliveryRouteReevaluationRef !=
			state.DeliveryRouteReevaluationRef ||
		record.CandidateRef != state.Handoff.CandidateRef ||
		record.VerificationResultRef != state.VerificationResultRef ||
		record.ReviewReceiptRef != state.ReviewReceiptRef ||
		record.DecisionRef != request.DecisionRef ||
		record.Disposition != decision.Disposition ||
		record.AuthorityRef != decision.AuthorityRef ||
		record.HumanDecisionRef != request.HumanDecisionRef ||
		record.Nonce != nonce {
		return fmt.Errorf(
			"%w: prepared PAD authorization differs from the exact owner request",
			deliveryadmission.ErrTrustedRepositoryConflict,
		)
	}
	return nil
}

func ownerPADAuthorizationJournalRef(
	record ownerPADAuthorizationJournal,
) (string, error) {
	record.JournalRef = ""
	return coordinationDigest(ownerPADAuthorizationJournalDomain, record)
}

func ownerPADAuthorizationSlotRef(
	repositoryIdentity string,
	workRunID string,
) (string, error) {
	if !validPADImmutableRef(repositoryIdentity) {
		return "", errors.New(
			"owner PAD authorization slot repository identity is invalid",
		)
	}
	if err := validateCoordinationWorkRunID(workRunID); err != nil {
		return "", err
	}
	return coordinationDigest(ownerPADAuthorizationSlotDomain, struct {
		RepositoryIdentity string `json:"repository_identity"`
		WorkRunID          string `json:"work_run_id"`
	}{
		RepositoryIdentity: repositoryIdentity,
		WorkRunID:          workRunID,
	})
}

func resolveOwnerPADAuthorizationJournal(
	ctx context.Context,
	coordinator *OwnerCoordinator,
) (ownerPADAuthorizationJournal, error) {
	if err := coordinator.coordination.validateRequestContext(
		ctx,
		coordinator.work.WorkRunID,
	); err != nil {
		return ownerPADAuthorizationJournal{}, err
	}
	slotRef, err := ownerPADAuthorizationSlotRef(
		coordinator.coordination.identity.repositoryIdentity,
		coordinator.work.WorkRunID,
	)
	if err != nil {
		return ownerPADAuthorizationJournal{}, err
	}
	path := ownerPADAuthorizationJournalPath(
		coordinator.coordination,
		slotRef,
	)
	payload, err := readPrivateCoordinationFile(path)
	if err != nil {
		return ownerPADAuthorizationJournal{}, err
	}
	var record ownerPADAuthorizationJournal
	if err := decodeStrictCoordinationJSON(payload, &record); err != nil {
		return ownerPADAuthorizationJournal{}, fmt.Errorf(
			"%w: decode owner PAD authorization journal: %v",
			ErrCoordinationAuthorityCorrupt,
			err,
		)
	}
	canonical, err := canonicalCoordinationPayload(record)
	if err != nil ||
		!bytes.Equal(payload, canonical) ||
		record.Validate() != nil ||
		record.SlotRef != slotRef {
		return ownerPADAuthorizationJournal{}, fmt.Errorf(
			"%w: owner PAD authorization journal is invalid",
			ErrCoordinationAuthorityCorrupt,
		)
	}
	return record, nil
}

func publishOwnerPADAuthorizationJournal(
	ctx context.Context,
	coordinator *OwnerCoordinator,
	record ownerPADAuthorizationJournal,
) error {
	if err := coordinator.coordination.validateRequestContext(
		ctx,
		coordinator.work.WorkRunID,
	); err != nil {
		return err
	}
	if err := record.Validate(); err != nil {
		return err
	}
	if record.RepositoryIdentity !=
		coordinator.coordination.identity.repositoryIdentity ||
		record.WorkRunID != coordinator.work.WorkRunID {
		return ErrCoordinationAuthorityCrossRun
	}
	store := coordinator.coordination
	if err := ensureCoordinationRepositoryRoot(
		store.identity.gitCommonDir,
		store.root,
		true,
	); err != nil {
		return err
	}
	root := ownerPADAuthorizationJournalRoot(store)
	if err := ensurePrivateCoordinationDirectoryTree(
		store.root,
		root,
		true,
	); err != nil {
		return err
	}
	payload, err := canonicalCoordinationPayload(record)
	if err != nil {
		return err
	}
	return publishPrivateCoordinationImmutable(
		ownerPADAuthorizationJournalPath(store, record.SlotRef),
		payload,
	)
}

func ownerPADAuthorizationJournalRoot(
	store *CoordinationAuthorityStore,
) string {
	return filepath.Join(store.root, "delivery-authorization-journal")
}

func ownerPADAuthorizationJournalPath(
	store *CoordinationAuthorityStore,
	slotRef string,
) string {
	return filepath.Join(
		ownerPADAuthorizationJournalRoot(store),
		strings.TrimPrefix(slotRef, "sha256:")+".json",
	)
}

// rejectPreparedPADAuthorizationForRoute is called under the common delivery
// owner lock. A prepared authorization survives process failure and therefore
// blocks route mutation until that exact WorkRun issuance is recovered.
func rejectPreparedPADAuthorizationForRoute(
	ctx context.Context,
	coordinator *OwnerCoordinator,
) error {
	record, err := resolveOwnerPADAuthorizationJournal(ctx, coordinator)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	return fmt.Errorf(
		"%w: %s (%s)",
		ErrPADDeliveryAuthorizationPrepared,
		record.AuthorizationRef,
		record.DecisionRef,
	)
}
