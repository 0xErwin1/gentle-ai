package workprovider

import (
	"context"
	"errors"
	"io/fs"
	"reflect"

	"github.com/gentleman-programming/gentle-ai/internal/evidence"
	"github.com/gentleman-programming/gentle-ai/internal/reviewtransaction"
	"github.com/gentleman-programming/gentle-ai/internal/workrun"
)

const productiveVerificationIntentSchema = "gentle-ai.productive-verification-intent/v1"

type productiveVerificationIntentRecord struct {
	Schema         string                            `json:"schema"`
	RepositoryRef  string                            `json:"repositoryRef"`
	WorkRunID      string                            `json:"workRunId"`
	SourceRevision string                            `json:"sourceRevision"`
	CandidateRef   string                            `json:"candidateRef"`
	Candidate      reviewtransaction.Snapshot        `json:"candidate"`
	DominantLens   string                            `json:"dominantLens,omitempty"`
	Preparation    productiveVerificationPreparation `json:"preparation"`
}

func resolveProductiveVerificationIntent(
	ctx context.Context,
	store productiveAdvanceStore,
	factory *ProductionOwnerCoordinatorFactory,
	sourceRevision string,
	candidate reviewtransaction.Snapshot,
	policyHash string,
) (
	productiveVerificationPreparation,
	workrun.WorkAdvanceDiagnosticCode,
	error,
) {
	preparation, storedCandidate, ok, err := store.verificationIntent(
		ctx,
		sourceRevision,
		candidate.Identity,
	)
	if err != nil || ok {
		if ok {
			if !reviewtransaction.SnapshotsEqualExact(
				storedCandidate,
				candidate,
			) {
				return productiveVerificationPreparation{}, "",
					errors.New(
						"productive verification intent binds another candidate",
					)
			}
			err = validateProductiveVerificationIntentForCandidate(
				ctx,
				factory,
				candidate,
				policyHash,
				preparation,
			)
		}
		return preparation, "", err
	}
	preparation, blocker, err := prepareProductiveAdvanceVerification(
		ctx,
		factory,
		candidate,
		policyHash,
	)
	if err != nil || blocker != "" {
		return productiveVerificationPreparation{}, blocker, err
	}
	if err := validateProductiveVerificationIntentForCandidate(
		ctx,
		factory,
		candidate,
		policyHash,
		preparation,
	); err != nil {
		return productiveVerificationPreparation{}, "", err
	}
	if err := store.publishVerificationIntent(
		ctx,
		sourceRevision,
		candidate,
		preparation,
	); err != nil {
		return productiveVerificationPreparation{}, "", err
	}
	return preparation, "", nil
}

func (store productiveAdvanceStore) publishVerificationIntent(
	ctx context.Context,
	sourceRevision string,
	candidate reviewtransaction.Snapshot,
	preparation productiveVerificationPreparation,
) error {
	record := productiveVerificationIntentRecord{
		Schema:         productiveVerificationIntentSchema,
		RepositoryRef:  store.lease.Identity().RepositoryRef,
		WorkRunID:      store.workRunID,
		SourceRevision: sourceRevision,
		CandidateRef:   candidate.Identity,
		Candidate:      candidate,
		DominantLens:   preparation.Assessment.DominantLens,
		Preparation:    preparation,
	}
	if err := store.validateVerificationIntent(record); err != nil {
		return err
	}
	return store.publishJSON(
		ctx,
		productiveVerificationIntentName(sourceRevision),
		record,
	)
}

func (store productiveAdvanceStore) verificationIntent(
	ctx context.Context,
	sourceRevision string,
	candidateRef string,
) (
	productiveVerificationPreparation,
	reviewtransaction.Snapshot,
	bool,
	error,
) {
	var record productiveVerificationIntentRecord
	if err := store.readJSON(
		ctx,
		productiveVerificationIntentName(sourceRevision),
		&record,
	); err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return productiveVerificationPreparation{},
				reviewtransaction.Snapshot{}, false, nil
		}
		return productiveVerificationPreparation{},
			reviewtransaction.Snapshot{}, false, err
	}
	if record.SourceRevision != sourceRevision ||
		record.CandidateRef != candidateRef ||
		func() bool {
			record.Preparation.Assessment.DominantLens =
				record.DominantLens
			return store.validateVerificationIntent(record) != nil
		}() {
		return productiveVerificationPreparation{},
			reviewtransaction.Snapshot{}, false, errors.New(
				"productive verification intent is corrupt",
			)
	}
	return record.Preparation, record.Candidate, true, nil
}

func validateProductiveVerificationIntentForCandidate(
	ctx context.Context,
	factory *ProductionOwnerCoordinatorFactory,
	candidate reviewtransaction.Snapshot,
	policyHash string,
	preparation productiveVerificationPreparation,
) error {
	if preparation.Plan.PolicyHash != policyHash ||
		preparation.Registry.PolicyHash != policyHash ||
		preparation.Applicability.PolicyHash != policyHash {
		return errors.New(
			"productive verification intent does not bind admitted policy",
		)
	}
	builder := reviewtransaction.SnapshotBuilder{
		Repo: factory.repositoryRoot(),
	}
	if err := builder.ValidateEvidence(ctx, candidate); err != nil {
		return errors.New(
			"productive verification intent does not bind Git candidate evidence",
		)
	}
	assessment, err := builder.AssessSnapshotRisk(ctx, candidate)
	if err != nil || !reflect.DeepEqual(assessment, preparation.Assessment) {
		return errors.New(
			"productive verification intent does not bind native risk",
		)
	}
	lenses, err := reviewtransaction.SelectReviewLenses(
		assessment,
		"reliability",
	)
	if err != nil ||
		!equalProductiveStrings(lenses, preparation.Lenses) {
		return errors.New(
			"productive verification intent does not bind native review lenses",
		)
	}
	applicability, err :=
		builder.ClassifyVerificationApplicability(
			ctx,
			candidate,
			preparation.Registry,
			[]string{},
		)
	if err != nil ||
		!reflect.DeepEqual(
			applicability,
			preparation.Applicability,
		) {
		return errors.New(
			"productive verification intent does not bind native applicability",
		)
	}
	return nil
}

func (store productiveAdvanceStore) validateVerificationIntent(
	record productiveVerificationIntentRecord,
) error {
	preparation := record.Preparation
	if record.Schema != productiveVerificationIntentSchema ||
		record.RepositoryRef != store.lease.Identity().RepositoryRef ||
		record.WorkRunID != store.workRunID ||
		!revisionPattern.MatchString(record.SourceRevision) ||
		!validPADImmutableRef(record.CandidateRef) ||
		record.Candidate.Identity != record.CandidateRef ||
		record.DominantLens !=
			preparation.Assessment.DominantLens ||
		preparation.Plan.Subject.SnapshotIdentity != record.CandidateRef ||
		reviewtransaction.ValidateVerificationPlan(
			preparation.Applicability,
			preparation.Registry,
			preparation.Plan,
		) != nil ||
		preparation.Actions == nil ||
		preparation.Lenses == nil {
		return errors.New("productive verification intent is invalid")
	}
	automatic := preparation.Applicability.Decision ==
		reviewtransaction.VerificationApplicable
	if automatic &&
		len(preparation.Actions) != len(preparation.Plan.Obligations) {
		return errors.New(
			"productive verification intent actions differ from its plan",
		)
	}
	for index, action := range preparation.Actions {
		if action.Validate() != nil ||
			index > 0 &&
				preparation.Actions[index-1].ID >= action.ID {
			return errors.New("productive verification intent action is invalid")
		}
		if action.Cost != reviewtransaction.VerificationCostQuick ||
			action.RequiresConsent {
			automatic = false
		}
		observation, err := evidence.ObserveSemanticRequirement(
			evidence.SemanticRequirementRequest{
				Program: action.Program,
				Args:    action.Args,
				CWD:     action.CWD,
				Environment: cloneProductiveEnvironment(
					action.Environment,
				),
				Capability: action.Capability,
			},
		)
		if err != nil ||
			index >= len(preparation.Plan.Obligations) {
			return errors.New(
				"productive verification intent action has no obligation",
			)
		}
		obligation := preparation.Plan.Obligations[index]
		if obligation.ID != action.ID ||
			obligation.RequirementRef != observation.RequirementRef ||
			obligation.ArgvRef != observation.ArgvRef ||
			obligation.CapabilityRef != action.Capability ||
			obligation.Cost != action.Cost {
			return errors.New(
				"productive verification intent action differs from its plan",
			)
		}
	}
	switch preparation.Applicability.Decision {
	case reviewtransaction.VerificationApplicable:
		if len(preparation.Actions) == 0 ||
			preparation.Automatic != automatic {
			return errors.New(
				"productive verification intent automatic decision is invalid",
			)
		}
	case reviewtransaction.VerificationNotApplicable:
		if len(preparation.Actions) != 0 || preparation.Automatic {
			return errors.New(
				"passive productive verification intent contains actions",
			)
		}
	default:
		return errors.New(
			"productive verification intent applicability is not executable",
		)
	}
	return nil
}

func productiveVerificationIntentName(sourceRevision string) string {
	return "verification-intent-" +
		productiveAdvanceRevisionKey(sourceRevision) +
		".json"
}
