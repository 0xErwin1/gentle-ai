package workprovider

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"reflect"

	"github.com/gentleman-programming/gentle-ai/internal/reviewtransaction"
	"github.com/gentleman-programming/gentle-ai/internal/workrun"
)

const (
	productiveVerificationPromptSchema   = "gentle-ai.productive-verification-prompt/v1"
	productiveVerificationDecisionSchema = "gentle-ai.productive-verification-decision/v1"
	productiveVerificationReceiptSchema  = "gentle-ai.productive-verification-receipt/v1"
	productiveVerificationResumeSchema   = "gentle-ai.productive-verification-resume/v1"
)

type productiveVerificationPromptRecord struct {
	Schema         string                               `json:"schema"`
	RepositoryRef  string                               `json:"repositoryRef"`
	WorkRunID      string                               `json:"workRunId"`
	SourceRevision string                               `json:"sourceRevision"`
	Prompt         workrun.VerificationDecisionPromptV1 `json:"prompt"`
}

type productiveVerificationDecisionRecord struct {
	Schema         string                             `json:"schema"`
	RepositoryRef  string                             `json:"repositoryRef"`
	WorkRunID      string                             `json:"workRunId"`
	PromptRef      string                             `json:"promptRef"`
	Choice         workrun.VerificationDecisionChoice `json:"choice"`
	DispositionRef string                             `json:"dispositionRef"`
}

// productiveVerificationReceiptRecord preserves the exact public projection
// of C. Later owner progress may move the live WorkRun to D, but replaying the
// same consent request must not combine that historical mutation with D.
type productiveVerificationReceiptRecord struct {
	Schema        string                             `json:"schema"`
	RepositoryRef string                             `json:"repositoryRef"`
	WorkRunID     string                             `json:"workRunId"`
	PromptRef     string                             `json:"promptRef"`
	Choice        workrun.VerificationDecisionChoice `json:"choice"`
	Receipt       workrun.WorkVerificationDecideV1   `json:"receipt"`
}

// productiveVerificationResumeRecord is the single owner-authenticated
// identity for the post-consent convergence attempt. Replaying ResumeRevision
// may recover that attempt, but no other public revision can start it.
type productiveVerificationResumeRecord struct {
	Schema         string `json:"schema"`
	RepositoryRef  string `json:"repositoryRef"`
	WorkRunID      string `json:"workRunId"`
	SourceRevision string `json:"sourceRevision"`
	ResumeRevision string `json:"resumeRevision"`
	PromptRef      string `json:"promptRef"`
	ForecastRef    string `json:"forecastRef"`
	DispositionRef string `json:"dispositionRef"`
}

type productiveVerificationCheckpointError struct {
	advance workrun.WorkAdvanceV2
}

func (err *productiveVerificationCheckpointError) Error() string {
	return "productive verification yielded an owner decision checkpoint"
}

func checkpointProductiveVerification(
	ctx context.Context,
	store productiveAdvanceStore,
	coordinator *OwnerCoordinator,
	expectedRevision string,
	state workrun.WorkRunState,
	preparation productiveVerificationPreparation,
) (workrun.WorkAdvanceV2, error) {
	if state.Forecast == nil ||
		state.Disposition != nil ||
		!productiveVerificationNeedsDecision(preparation) {
		return workrun.WorkAdvanceV2{}, errors.New(
			"productive verification is not awaiting owner consent",
		)
	}
	assumptionsRef, err := productiveVerificationAssumptionsRef(preparation)
	if err != nil {
		return workrun.WorkAdvanceV2{}, err
	}
	prompt, err := workrun.NewVerificationDecisionPrompt(
		workrun.VerificationDecisionPromptRequest{
			WorkRunID:        state.WorkRunID,
			ExpectedRevision: state.Revision,
			Forecast:         *state.Forecast,
			AssumptionsRef:   assumptionsRef,
			Choices: productiveVerificationPromptChoices(
				*state.Forecast,
			),
		},
	)
	if err != nil {
		return workrun.WorkAdvanceV2{}, err
	}
	if err := store.publishVerificationPrompt(
		ctx,
		expectedRevision,
		prompt,
	); err != nil {
		return workrun.WorkAdvanceV2{}, err
	}
	status, err := coordinator.work.PublicStatus(ctx)
	if err != nil {
		return workrun.WorkAdvanceV2{}, err
	}
	advance := workrun.WorkAdvanceV2{
		Schema:               workrun.WorkAdvanceContractV2,
		Contract:             workrun.WorkAdvanceContractV2,
		PreviousRevision:     expectedRevision,
		Status:               status,
		VerificationDecision: &prompt,
	}
	if err := advance.Validate(); err != nil {
		return workrun.WorkAdvanceV2{}, err
	}
	return advance, nil
}

func (runtimeOutcome *productiveRuntimeOutcome) replayVerificationCheckpoint(
	ctx context.Context,
	workRunID string,
	expectedRevision string,
) (workrun.WorkAdvanceV2, bool, error) {
	if err := runtimeOutcome.validate(ctx); err != nil {
		return workrun.WorkAdvanceV2{}, false, err
	}
	factory, err := NewProductionOwnerCoordinatorFactory(
		ctx,
		runtimeOutcome.repo,
		runtimeOutcome.agent,
		runtimeOutcome.activation,
	)
	if err != nil {
		return workrun.WorkAdvanceV2{}, false, err
	}
	store, err := existingProductiveAdvanceStore(factory.lease, workRunID)
	if err != nil {
		return workrun.WorkAdvanceV2{}, false, err
	}
	if err := store.validate(ctx); err != nil {
		if os.IsNotExist(err) {
			return workrun.WorkAdvanceV2{}, false, nil
		}
		return workrun.WorkAdvanceV2{}, false, err
	}
	lock, err := acquireProductiveAdvanceOwnerLock(ctx, store)
	if err != nil {
		return workrun.WorkAdvanceV2{}, false, err
	}
	defer lock.Release()
	authority, err := store.startAuthority(ctx)
	if err != nil {
		return workrun.WorkAdvanceV2{}, false, err
	}
	coordinator, err := factory.openForProductiveRecovery(ctx, workRunID)
	if err != nil {
		return workrun.WorkAdvanceV2{}, false, err
	}
	state, err := coordinator.Status(ctx)
	if err != nil {
		return workrun.WorkAdvanceV2{}, false, err
	}
	if state.DeliveryIntentRef != authority.DeliveryIntentRef {
		return workrun.WorkAdvanceV2{}, false, ErrProviderResultMismatch
	}
	status, err := coordinator.work.PublicStatus(ctx)
	if err != nil {
		return workrun.WorkAdvanceV2{}, false, err
	}
	checkpoint, ok, err := store.verificationCheckpoint(
		ctx,
		expectedRevision,
		state,
		status,
	)
	if err != nil || ok {
		return checkpoint, ok, err
	}
	if !productiveVerificationCheckpointPending(
		expectedRevision,
		state,
	) {
		return workrun.WorkAdvanceV2{}, false, nil
	}
	preparation, candidate, found, err := store.verificationIntent(
		ctx,
		expectedRevision,
		state.Handoff.CandidateRef,
	)
	if err != nil || !found {
		return workrun.WorkAdvanceV2{}, false, err
	}
	planAuthority, err := coordinator.rar.ResolvePlan(
		ctx,
		state.Forecast.PlanRevisionRef,
	)
	if err != nil {
		return workrun.WorkAdvanceV2{}, false, err
	}
	if !reviewtransaction.SnapshotsEqualExact(
		planAuthority.Snapshot,
		candidate,
	) ||
		!reflect.DeepEqual(
			planAuthority.Applicability,
			preparation.Applicability,
		) ||
		!reflect.DeepEqual(planAuthority.Registry, preparation.Registry) ||
		!reflect.DeepEqual(planAuthority.Plan, preparation.Plan) {
		return workrun.WorkAdvanceV2{}, false, errors.New(
			"productive verification intent differs from RAR plan authority",
		)
	}
	if err := validateProductiveVerificationIntentForCandidate(
		ctx,
		factory,
		candidate,
		planAuthority.Plan.PolicyHash,
		preparation,
	); err != nil {
		return workrun.WorkAdvanceV2{}, false, err
	}
	if err := validateProductivePreparedHandoff(
		state.Handoff,
		candidate,
		preparation.Plan,
	); err != nil {
		return workrun.WorkAdvanceV2{}, false, err
	}
	if err := validateProductivePreparedForecast(
		ctx,
		coordinator,
		state,
		planAuthority,
		preparation,
	); err != nil {
		return workrun.WorkAdvanceV2{}, false, err
	}
	recovered, err := checkpointProductiveVerification(
		ctx,
		store,
		coordinator,
		expectedRevision,
		state,
		preparation,
	)
	if err != nil {
		return workrun.WorkAdvanceV2{}, false, err
	}
	return recovered, true, nil
}

func (store productiveAdvanceStore) publishVerificationPrompt(
	ctx context.Context,
	sourceRevision string,
	prompt workrun.VerificationDecisionPromptV1,
) error {
	record := productiveVerificationPromptRecord{
		Schema:         productiveVerificationPromptSchema,
		RepositoryRef:  store.lease.Identity().RepositoryRef,
		WorkRunID:      store.workRunID,
		SourceRevision: sourceRevision,
		Prompt:         prompt,
	}
	if err := store.validateVerificationPromptRecord(record); err != nil {
		return err
	}
	if err := store.publishJSON(
		ctx,
		productiveVerificationCheckpointName(sourceRevision),
		record,
	); err != nil {
		return err
	}
	return store.publishJSON(
		ctx,
		productiveVerificationPromptName(prompt.PromptRef),
		record,
	)
}

func (store productiveAdvanceStore) verificationPrompt(
	ctx context.Context,
	promptRef string,
) (workrun.VerificationDecisionPromptV1, error) {
	if !revisionPattern.MatchString(promptRef) {
		return workrun.VerificationDecisionPromptV1{}, errors.New(
			"productive verification prompt reference is invalid",
		)
	}
	var record productiveVerificationPromptRecord
	if err := store.readJSON(
		ctx,
		productiveVerificationPromptName(promptRef),
		&record,
	); err != nil {
		return workrun.VerificationDecisionPromptV1{}, err
	}
	if err := store.validateVerificationPromptRecord(record); err != nil ||
		record.Prompt.PromptRef != promptRef {
		return workrun.VerificationDecisionPromptV1{}, errors.New(
			"productive verification prompt authority is corrupt",
		)
	}
	return record.Prompt, nil
}

func (store productiveAdvanceStore) validateVerificationPromptRecord(
	record productiveVerificationPromptRecord,
) error {
	if store.lease == nil ||
		record.Schema != productiveVerificationPromptSchema ||
		record.RepositoryRef != store.lease.Identity().RepositoryRef ||
		record.WorkRunID != store.workRunID ||
		!revisionPattern.MatchString(record.SourceRevision) ||
		record.SourceRevision == record.Prompt.ExpectedRevision {
		return errors.New("productive verification prompt authority is invalid")
	}
	return record.Prompt.Validate()
}

func (store productiveAdvanceStore) verificationCheckpoint(
	ctx context.Context,
	expectedRevision string,
	state workrun.WorkRunState,
	status workrun.WorkStatusV1,
) (workrun.WorkAdvanceV2, bool, error) {
	if !productiveVerificationCheckpointPending(
		expectedRevision,
		state,
	) {
		return workrun.WorkAdvanceV2{}, false, nil
	}
	var record productiveVerificationPromptRecord
	if err := store.readJSON(
		ctx,
		productiveVerificationCheckpointName(expectedRevision),
		&record,
	); err != nil {
		if os.IsNotExist(err) {
			return workrun.WorkAdvanceV2{}, false, nil
		}
		return workrun.WorkAdvanceV2{}, false, err
	}
	if err := store.validateVerificationPromptRecord(record); err != nil ||
		record.SourceRevision != expectedRevision ||
		record.Prompt.WorkRunID != state.WorkRunID ||
		record.Prompt.ExpectedRevision != state.Revision ||
		record.Prompt.ValidateFor(*state.Forecast) != nil {
		return workrun.WorkAdvanceV2{}, false, errors.New(
			"productive verification checkpoint authority is corrupt",
		)
	}
	// The source-indexed record is published first. Exact replay repairs a
	// crash between that durable boundary and the prompt-ref lookup.
	if err := store.publishJSON(
		ctx,
		productiveVerificationPromptName(record.Prompt.PromptRef),
		record,
	); err != nil {
		return workrun.WorkAdvanceV2{}, false, err
	}
	prompt, err := store.verificationPrompt(ctx, record.Prompt.PromptRef)
	if err != nil {
		return workrun.WorkAdvanceV2{}, false, err
	}
	if !reflect.DeepEqual(prompt, record.Prompt) {
		return workrun.WorkAdvanceV2{}, false, errors.New(
			"productive verification checkpoint lost its prompt lookup",
		)
	}
	advance := workrun.WorkAdvanceV2{
		Schema:               workrun.WorkAdvanceContractV2,
		Contract:             workrun.WorkAdvanceContractV2,
		PreviousRevision:     expectedRevision,
		Status:               status,
		VerificationDecision: &prompt,
	}
	if err := advance.Validate(); err != nil {
		return workrun.WorkAdvanceV2{}, false, err
	}
	return advance, true, nil
}

func productiveVerificationCheckpointPending(
	expectedRevision string,
	state workrun.WorkRunState,
) bool {
	return state.ProductiveAdvanceSourceRevision == expectedRevision &&
		state.ProductiveResumeRevision == "" &&
		state.Handoff != nil &&
		state.Forecast != nil &&
		productiveForecastNeedsDecision(*state.Forecast) &&
		state.Disposition == nil &&
		len(state.Reservations) == 0 &&
		len(state.LaunchClaims) == 0 &&
		state.ProductiveExecutionResultRef == "" &&
		state.ProductiveBlockerRef == "" &&
		state.ProductiveReconciliationRef == "" &&
		state.VerificationResultRef == "" &&
		state.ReviewReceiptRef == "" &&
		state.DeliveryAuthorizationRef == "" &&
		state.DeliveryResultRef == ""
}

func productiveForecastNeedsDecision(
	forecast workrun.VerificationForecast,
) bool {
	if forecast.Availability != workrun.ForecastAvailable ||
		forecast.MaximumCost == nil {
		return false
	}
	if forecast.RequiresConsent {
		return true
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

func (store productiveAdvanceStore) publishVerificationDecision(
	ctx context.Context,
	prompt workrun.VerificationDecisionPromptV1,
	choice workrun.VerificationDecisionChoice,
	dispositionRef string,
) error {
	if !productivePromptOffers(prompt, choice) ||
		!revisionPattern.MatchString(dispositionRef) {
		return errors.New("productive verification decision is invalid")
	}
	record := productiveVerificationDecisionRecord{
		Schema:         productiveVerificationDecisionSchema,
		RepositoryRef:  store.lease.Identity().RepositoryRef,
		WorkRunID:      store.workRunID,
		PromptRef:      prompt.PromptRef,
		Choice:         choice,
		DispositionRef: dispositionRef,
	}
	if err := store.publishJSON(
		ctx,
		productiveVerificationDecisionName(prompt.PromptRef, choice),
		record,
	); err != nil {
		return err
	}
	_, err := store.ResolveVerificationDecision(
		ctx,
		prompt.PromptRef,
		choice,
	)
	return err
}

func (store productiveAdvanceStore) publishVerificationReceipt(
	ctx context.Context,
	receipt workrun.WorkVerificationDecideV1,
) error {
	record := productiveVerificationReceiptRecord{
		Schema:        productiveVerificationReceiptSchema,
		RepositoryRef: store.lease.Identity().RepositoryRef,
		WorkRunID:     store.workRunID,
		PromptRef:     receipt.PromptRef,
		Choice:        receipt.Choice,
		Receipt:       receipt,
	}
	if err := store.validateVerificationReceiptRecord(record); err != nil {
		return err
	}
	return store.publishJSON(
		ctx,
		productiveVerificationReceiptName(
			record.PromptRef,
			record.Choice,
		),
		record,
	)
}

func (store productiveAdvanceStore) verificationReceipt(
	ctx context.Context,
	promptRef string,
	choice workrun.VerificationDecisionChoice,
	start productiveStartAuthority,
	state workrun.WorkRunState,
) (workrun.WorkVerificationDecideV1, bool, error) {
	if !revisionPattern.MatchString(promptRef) {
		return workrun.WorkVerificationDecideV1{}, false, errors.New(
			"productive verification receipt reference is invalid",
		)
	}
	if _, err := productiveDispositionKind(choice); err != nil {
		return workrun.WorkVerificationDecideV1{}, false, err
	}
	var record productiveVerificationReceiptRecord
	err := store.readJSON(
		ctx,
		productiveVerificationReceiptName(promptRef, choice),
		&record,
	)
	if os.IsNotExist(err) {
		return workrun.WorkVerificationDecideV1{}, false, nil
	}
	if err != nil {
		return workrun.WorkVerificationDecideV1{}, false, err
	}
	if err := store.validateVerificationReceiptRecord(record); err != nil ||
		record.PromptRef != promptRef ||
		record.Choice != choice {
		return workrun.WorkVerificationDecideV1{}, false, errors.New(
			"productive verification receipt authority is corrupt",
		)
	}
	if err := store.validateVerificationReceiptReplay(
		ctx,
		record,
		start,
		state,
	); err != nil {
		return workrun.WorkVerificationDecideV1{}, false, err
	}
	return record.Receipt, true, nil
}

func (store productiveAdvanceStore) validateVerificationReceiptRecord(
	record productiveVerificationReceiptRecord,
) error {
	if store.lease == nil ||
		record.Schema != productiveVerificationReceiptSchema ||
		record.RepositoryRef != store.lease.Identity().RepositoryRef ||
		record.WorkRunID != store.workRunID ||
		record.PromptRef != record.Receipt.PromptRef ||
		record.Choice != record.Receipt.Choice ||
		record.Receipt.WorkRunID != store.workRunID {
		return errors.New("productive verification receipt authority is invalid")
	}
	return record.Receipt.Validate()
}

func (store productiveAdvanceStore) validateVerificationReceiptReplay(
	ctx context.Context,
	record productiveVerificationReceiptRecord,
	start productiveStartAuthority,
	state workrun.WorkRunState,
) error {
	prompt, err := store.verificationPrompt(ctx, record.PromptRef)
	if err != nil {
		return err
	}
	authority, err := store.ResolveVerificationDecision(
		ctx,
		record.PromptRef,
		record.Choice,
	)
	if err != nil {
		return err
	}
	source := state
	source.Revision = prompt.ExpectedRevision
	expectedDisposition, err := authority.Validate(source)
	if err != nil {
		return err
	}
	receipt := record.Receipt
	if start.RepositoryRef != record.RepositoryRef ||
		start.WorkRunID != record.WorkRunID ||
		start.DeliveryIntentRef != state.DeliveryIntentRef ||
		state.WorkRunID != record.WorkRunID ||
		state.Forecast == nil ||
		state.Forecast.Digest != prompt.ForecastRef ||
		state.Disposition == nil ||
		!reflect.DeepEqual(*state.Disposition, expectedDisposition) ||
		state.ProductiveResumeRevision != receipt.Status.Revision ||
		receipt.PreviousRevision != prompt.ExpectedRevision ||
		receipt.ForecastRef != prompt.ForecastRef ||
		receipt.AssumptionsRef != prompt.AssumptionsRef ||
		receipt.DispositionRef != authority.DecisionRef ||
		receipt.Status.DeliveryIntentRef != state.DeliveryIntentRef ||
		receipt.Status.RouteDecision != state.RouteDecision.Decision ||
		receipt.Status.ImplementationRoute != state.ImplementationRoute ||
		receipt.Status.SDDRunRef != state.SDDRunRef {
		return errors.New(
			"productive verification receipt does not bind durable decision authority",
		)
	}
	return nil
}

func (store productiveAdvanceStore) ResolveVerificationDecision(
	ctx context.Context,
	promptRef string,
	choice workrun.VerificationDecisionChoice,
) (workrun.VerificationDecisionAuthority, error) {
	if store.coordination == nil {
		return workrun.VerificationDecisionAuthority{}, fmt.Errorf(
			"%w: productive verification decision authority",
			workrun.ErrAuthorityPortUnavailable,
		)
	}
	prompt, err := store.verificationPrompt(ctx, promptRef)
	if err != nil {
		return workrun.VerificationDecisionAuthority{}, err
	}
	if !productivePromptOffers(prompt, choice) {
		return workrun.VerificationDecisionAuthority{}, errors.New(
			"productive verification choice was not offered",
		)
	}
	var record productiveVerificationDecisionRecord
	if err := store.readJSON(
		ctx,
		productiveVerificationDecisionName(promptRef, choice),
		&record,
	); err != nil {
		return workrun.VerificationDecisionAuthority{}, err
	}
	if record.Schema != productiveVerificationDecisionSchema ||
		record.RepositoryRef != store.lease.Identity().RepositoryRef ||
		record.WorkRunID != store.workRunID ||
		record.PromptRef != promptRef ||
		record.Choice != choice ||
		!revisionPattern.MatchString(record.DispositionRef) {
		return workrun.VerificationDecisionAuthority{}, errors.New(
			"productive verification decision authority is corrupt",
		)
	}
	disposition, err := store.coordination.ResolveDisposition(
		ctx,
		record.DispositionRef,
	)
	if err != nil {
		return workrun.VerificationDecisionAuthority{}, err
	}
	kind, err := productiveDispositionKind(choice)
	if err != nil {
		return workrun.VerificationDecisionAuthority{}, err
	}
	if disposition.DecisionRef != record.DispositionRef ||
		disposition.ForecastDigest != prompt.ForecastRef ||
		disposition.AssumptionsRef != prompt.AssumptionsRef ||
		disposition.Kind != kind {
		return workrun.VerificationDecisionAuthority{}, errors.New(
			"productive verification decision does not bind its prompt",
		)
	}
	return workrun.VerificationDecisionAuthority{
		Prompt:      prompt,
		Choice:      choice,
		ActorRef:    disposition.ActorRef,
		DecisionRef: disposition.DecisionRef,
		RunnerRef:   disposition.RunnerRef,
	}, nil
}

func (store productiveAdvanceStore) publishVerificationResume(
	ctx context.Context,
	state workrun.WorkRunState,
	prompt workrun.VerificationDecisionPromptV1,
) error {
	if state.Forecast == nil ||
		state.Disposition == nil ||
		state.Disposition.Kind != workrun.DispositionRun ||
		state.Disposition.ForecastDigest != prompt.ForecastRef ||
		state.Disposition.AssumptionsRef != prompt.AssumptionsRef ||
		!revisionPattern.MatchString(state.ProductiveAdvanceSourceRevision) ||
		!revisionPattern.MatchString(state.Revision) ||
		state.ProductiveResumeRevision != state.Revision {
		return errors.New("productive verification resume authority is invalid")
	}
	record := productiveVerificationResumeRecord{
		Schema:         productiveVerificationResumeSchema,
		RepositoryRef:  store.lease.Identity().RepositoryRef,
		WorkRunID:      store.workRunID,
		SourceRevision: state.ProductiveAdvanceSourceRevision,
		ResumeRevision: state.Revision,
		PromptRef:      prompt.PromptRef,
		ForecastRef:    prompt.ForecastRef,
		DispositionRef: state.Disposition.DecisionRef,
	}
	return store.publishJSON(
		ctx,
		productiveVerificationResumeName(record.ResumeRevision),
		record,
	)
}

func (store productiveAdvanceStore) validateVerificationResume(
	ctx context.Context,
	resumeRevision string,
	state workrun.WorkRunState,
) error {
	if !revisionPattern.MatchString(resumeRevision) {
		return errors.New("productive verification resume revision is invalid")
	}
	var record productiveVerificationResumeRecord
	if err := store.readJSON(
		ctx,
		productiveVerificationResumeName(resumeRevision),
		&record,
	); err != nil {
		return err
	}
	if record.Schema != productiveVerificationResumeSchema ||
		record.RepositoryRef != store.lease.Identity().RepositoryRef ||
		record.WorkRunID != store.workRunID ||
		record.ResumeRevision != resumeRevision ||
		record.SourceRevision != state.ProductiveAdvanceSourceRevision ||
		state.ProductiveResumeRevision != resumeRevision ||
		state.Forecast == nil ||
		state.Disposition == nil ||
		state.Disposition.Kind != workrun.DispositionRun ||
		record.ForecastRef != state.Forecast.Digest ||
		record.DispositionRef != state.Disposition.DecisionRef {
		return errors.New(
			"productive verification resume does not bind the live WorkRun",
		)
	}
	resolved, err := store.ResolveVerificationDecision(
		ctx,
		record.PromptRef,
		workrun.VerificationDecisionRun,
	)
	if err != nil {
		return err
	}
	if resolved.DecisionRef != record.DispositionRef ||
		resolved.Prompt.ForecastRef != record.ForecastRef {
		return errors.New(
			"productive verification resume lost owner decision authority",
		)
	}
	return nil
}

func productiveVerificationAssumptionsRef(
	preparation productiveVerificationPreparation,
) (string, error) {
	value := struct {
		Schema     string                         `json:"schema"`
		PlanDigest string                         `json:"planDigest"`
		Actions    []ProductiveVerificationAction `json:"actions"`
	}{
		Schema:     "gentle-ai.productive-verification-assumptions/v1",
		PlanDigest: preparation.Plan.Digest,
		Actions: append(
			[]ProductiveVerificationAction(nil),
			preparation.Actions...,
		),
	}
	payload, err := json.Marshal(value)
	if err != nil {
		return "", err
	}
	return productiveAdvanceSHA256(
		append(
			[]byte("gentle-ai.productive-verification-assumptions-ref/v1\x00"),
			payload...,
		),
	), nil
}

func productiveVerificationPromptChoices(
	forecast workrun.VerificationForecast,
) []workrun.VerificationDecisionChoice {
	choices := []workrun.VerificationDecisionChoice{
		workrun.VerificationDecisionDefer,
		workrun.VerificationDecisionReduceScope,
	}
	if forecast.MaximumCost != nil &&
		*forecast.MaximumCost != reviewtransaction.VerificationCostUnknown {
		choices = append(
			[]workrun.VerificationDecisionChoice{
				workrun.VerificationDecisionRun,
			},
			choices...,
		)
	}
	return choices
}

func productivePromptOffers(
	prompt workrun.VerificationDecisionPromptV1,
	choice workrun.VerificationDecisionChoice,
) bool {
	for _, offered := range prompt.Choices {
		if offered == choice {
			return true
		}
	}
	return false
}

func productiveDispositionKind(
	choice workrun.VerificationDecisionChoice,
) (workrun.VerificationDispositionKind, error) {
	switch choice {
	case workrun.VerificationDecisionRun:
		return workrun.DispositionRun, nil
	case workrun.VerificationDecisionDefer:
		return workrun.DispositionDefer, nil
	case workrun.VerificationDecisionReduceScope:
		return workrun.DispositionReduceScope, nil
	case workrun.VerificationDecisionDeferredRunner:
		return workrun.DispositionDeferredRunner, nil
	default:
		return "", errors.New("unsupported productive verification choice")
	}
}

func productiveVerificationPromptName(promptRef string) string {
	return "verification-prompt-" +
		productiveAdvanceRevisionKey(promptRef) +
		".json"
}

func productiveVerificationCheckpointName(sourceRevision string) string {
	return "verification-checkpoint-" +
		productiveAdvanceRevisionKey(sourceRevision) +
		".json"
}

func productiveVerificationDecisionName(
	promptRef string,
	choice workrun.VerificationDecisionChoice,
) string {
	return "verification-decision-" +
		productiveAdvanceRevisionKey(
			promptRef+"\x00"+string(choice),
		) +
		".json"
}

func productiveVerificationReceiptName(
	promptRef string,
	choice workrun.VerificationDecisionChoice,
) string {
	return "verification-receipt-" +
		productiveAdvanceRevisionKey(
			promptRef+"\x00"+string(choice),
		) +
		".json"
}

func productiveVerificationResumeName(resumeRevision string) string {
	return "verification-resume-" +
		productiveAdvanceRevisionKey(resumeRevision) +
		".json"
}

func productiveVerificationDecisionReceipt(
	prompt workrun.VerificationDecisionPromptV1,
	choice workrun.VerificationDecisionChoice,
	dispositionRef string,
	state workrun.WorkRunState,
) (workrun.WorkVerificationDecideV1, error) {
	kind, err := productiveDispositionKind(choice)
	if err != nil {
		return workrun.WorkVerificationDecideV1{}, err
	}
	if err := prompt.Validate(); err != nil ||
		!state.Started ||
		state.WorkRunID != prompt.WorkRunID ||
		!revisionPattern.MatchString(state.Revision) ||
		state.Revision == prompt.ExpectedRevision ||
		state.ProductiveResumeRevision != state.Revision ||
		state.Handoff == nil ||
		state.Forecast == nil ||
		state.Forecast.Digest != prompt.ForecastRef ||
		state.Disposition == nil ||
		state.Disposition.Kind != kind ||
		state.Disposition.DecisionRef != dispositionRef ||
		state.Disposition.ForecastDigest != prompt.ForecastRef ||
		state.Disposition.AssumptionsRef != prompt.AssumptionsRef ||
		len(state.Reservations) != 0 ||
		len(state.LaunchClaims) != 0 ||
		state.VerificationResultRef != "" ||
		state.VerificationStop != nil ||
		state.ReviewReceiptRef != "" ||
		state.DeliveryAuthorizationRef != "" ||
		state.DeliveryResultRef != "" {
		return workrun.WorkVerificationDecideV1{}, errors.New(
			"productive verification decision successor is invalid",
		)
	}
	publicState := workrun.PublicStateNeedsYourDecision
	if choice == workrun.VerificationDecisionRun {
		publicState = workrun.PublicStateChecking
	}
	status := workrun.WorkStatusV1{
		Schema:              workrun.WorkStatusContractV1,
		Contract:            workrun.WorkStatusContractV1,
		WorkRunID:           state.WorkRunID,
		Revision:            state.Revision,
		PublicState:         publicState,
		RouteDecision:       state.RouteDecision.Decision,
		RoutePhase:          workrun.RoutePhaseImplementationSelected,
		ImplementationRoute: state.ImplementationRoute,
		SDDRunRef:           state.SDDRunRef,
		Verification: workrun.VerificationSummaryV1{
			Outcome:    workrun.VerificationPending,
			ResultRefs: []string{},
		},
		DeliveryIntentRef: state.DeliveryIntentRef,
	}
	result := workrun.WorkVerificationDecideV1{
		Schema:           workrun.WorkVerificationDecideContractV1,
		Contract:         workrun.WorkVerificationDecideContractV1,
		WorkRunID:        state.WorkRunID,
		PreviousRevision: prompt.ExpectedRevision,
		PromptRef:        prompt.PromptRef,
		ForecastRef:      prompt.ForecastRef,
		AssumptionsRef:   prompt.AssumptionsRef,
		Choice:           choice,
		DispositionRef:   dispositionRef,
		Status:           status,
	}
	if err := result.Validate(); err != nil {
		return workrun.WorkVerificationDecideV1{}, err
	}
	return result, nil
}

var _ workrun.VerificationDecisionAuthorityPort = productiveAdvanceStore{}

func (runtimeOutcome *productiveRuntimeOutcome) DecideVerificationOutcome(
	ctx context.Context,
	workRunID string,
	promptRef string,
	choice workrun.VerificationDecisionChoice,
) (workrun.WorkVerificationDecideV1, error) {
	if !workRunIDPattern.MatchString(workRunID) ||
		!revisionPattern.MatchString(promptRef) {
		return workrun.WorkVerificationDecideV1{}, errors.New(
			"productive verification decision requires exact owner authority",
		)
	}
	if _, err := productiveDispositionKind(choice); err != nil {
		return workrun.WorkVerificationDecideV1{}, err
	}
	start, err := runtimeOutcome.bindWorkRunAgent(ctx, workRunID)
	if err != nil {
		return workrun.WorkVerificationDecideV1{}, err
	}
	factory, err := NewProductionOwnerCoordinatorFactory(
		ctx,
		runtimeOutcome.repo,
		runtimeOutcome.agent,
		runtimeOutcome.activation,
	)
	if err != nil {
		return workrun.WorkVerificationDecideV1{}, err
	}
	store, err := existingProductiveAdvanceStore(factory.lease, workRunID)
	if err != nil {
		return workrun.WorkVerificationDecideV1{}, err
	}
	if err := store.validate(ctx); err != nil {
		return workrun.WorkVerificationDecideV1{}, err
	}
	advanceLock, err := acquireProductiveAdvanceOwnerLock(ctx, store)
	if err != nil {
		return workrun.WorkVerificationDecideV1{}, err
	}
	defer advanceLock.Release()

	storedStart, err := store.startAuthority(ctx)
	if err != nil {
		return workrun.WorkVerificationDecideV1{}, err
	}
	if storedStart.AgentID != start.AgentID ||
		storedStart.DeliveryIntentRef != start.DeliveryIntentRef {
		return workrun.WorkVerificationDecideV1{},
			ErrProductiveRuntimeBindingMismatch
	}
	coordinator, err := factory.openForProductiveRecovery(ctx, workRunID)
	if err != nil {
		return workrun.WorkVerificationDecideV1{}, err
	}
	store.coordination = coordinator.coordination
	state, err := coordinator.Status(ctx)
	if err != nil {
		return workrun.WorkVerificationDecideV1{}, err
	}
	if receipt, exists, err := store.verificationReceipt(
		ctx,
		promptRef,
		choice,
		storedStart,
		state,
	); err != nil {
		return workrun.WorkVerificationDecideV1{}, err
	} else if exists {
		return receipt, nil
	}
	prompt, err := store.verificationPrompt(ctx, promptRef)
	if err != nil {
		return workrun.WorkVerificationDecideV1{}, err
	}
	if !productivePromptOffers(prompt, choice) {
		return workrun.WorkVerificationDecideV1{}, errors.New(
			"productive verification choice was not offered",
		)
	}
	if state.Handoff == nil || state.Forecast == nil {
		return workrun.WorkVerificationDecideV1{}, errors.New(
			"productive verification decision has no durable forecast",
		)
	}
	if err := prompt.ValidateFor(*state.Forecast); err != nil {
		return workrun.WorkVerificationDecideV1{}, err
	}
	kind, err := productiveDispositionKind(choice)
	if err != nil {
		return workrun.WorkVerificationDecideV1{}, err
	}
	actorRef := productiveAdvanceSHA256(
		[]byte(
			"gentle-ai.productive-verification-actor/v1\x00" +
				factory.RepositoryRef(),
		),
	)
	disposition, err := coordinator.coordination.PublishDisposition(
		ctx,
		DispositionAuthorityPublication{
			WorkRunID:      workRunID,
			Handoff:        *state.Handoff,
			Forecast:       *state.Forecast,
			Kind:           kind,
			AssumptionsRef: prompt.AssumptionsRef,
			ActorRef:       actorRef,
		},
	)
	if err != nil {
		return workrun.WorkVerificationDecideV1{}, err
	}
	if err := store.publishVerificationDecision(
		ctx,
		prompt,
		choice,
		disposition.AuthorityRef,
	); err != nil {
		return workrun.WorkVerificationDecideV1{}, err
	}
	requestID, err := ownerRequestID(
		"verification-decision",
		struct {
			PromptRef string
			Choice    workrun.VerificationDecisionChoice
		}{
			PromptRef: promptRef,
			Choice:    choice,
		},
	)
	if err != nil {
		return workrun.WorkVerificationDecideV1{}, err
	}
	decided, err := coordinator.work.DecideVerification(
		ctx,
		workrun.DecideVerificationRequest{
			ExpectedRevision: prompt.ExpectedRevision,
			RequestID:        requestID,
			PromptRef:        promptRef,
			Choice:           choice,
		},
	)
	if err != nil {
		return workrun.WorkVerificationDecideV1{}, err
	}
	if decided.Disposition == nil ||
		decided.Disposition.DecisionRef != disposition.AuthorityRef {
		return workrun.WorkVerificationDecideV1{},
			ErrProviderResultMismatch
	}
	if choice == workrun.VerificationDecisionRun {
		if err := store.publishVerificationResume(
			ctx,
			decided,
			prompt,
		); err != nil {
			return workrun.WorkVerificationDecideV1{}, err
		}
	}
	result, err := productiveVerificationDecisionReceipt(
		prompt,
		choice,
		disposition.AuthorityRef,
		decided,
	)
	if err != nil {
		return workrun.WorkVerificationDecideV1{}, err
	}
	if err := store.publishVerificationReceipt(ctx, result); err != nil {
		return workrun.WorkVerificationDecideV1{}, err
	}
	return result, nil
}
