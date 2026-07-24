package workprovider

import (
	"context"
	"errors"

	"github.com/gentleman-programming/gentle-ai/internal/evidence"
	"github.com/gentleman-programming/gentle-ai/internal/hostruntime"
	"github.com/gentleman-programming/gentle-ai/internal/mutationintegrity"
	"github.com/gentleman-programming/gentle-ai/internal/reviewtransaction"
	"github.com/gentleman-programming/gentle-ai/internal/workrun"
)

// NewDefaultController exposes the production common-work composition. Runtime
// activation can narrow or disable it, but the enabled path is never backed by
// a read-only placeholder.
func NewDefaultController() Controller {
	return NewController(
		EnvironmentProductionRepositoryOpener{},
		EnvironmentActivationResolver{},
	)
}

// ProductionRepositoryOpener binds every provider authority to the same exact
// repository and WorkRun. Opening is read-only: provider storage is published
// only by an owner operation or by an already-authorized transition.
type ProductionRepositoryOpener struct {
	// SemanticEvaluator is supplied by the owner runtime that understands the
	// registered RAR requirements. Nil is deliberately safe: clean process
	// evidence remains incomplete.
	SemanticEvaluator SemanticEvaluatorPort
}

func (opener ProductionRepositoryOpener) OpenRepository(
	ctx context.Context,
	repo string,
	workRunID string,
) (Repository, error) {
	lease, err := reviewtransaction.OpenRepositoryIdentityLease(ctx, repo)
	if err != nil {
		return nil, err
	}
	padAuthority, err := newPADRepositoryAuthorityWithLease(ctx, lease)
	if err != nil {
		return nil, err
	}
	repositoryRoot := lease.Identity().RepositoryRoot
	pad, err := NewPADWorkRunAdapter(padAuthority)
	if err != nil {
		return nil, err
	}
	rar, err := reviewtransaction.
		OpenRARAuthorityRepositoryWithRepositoryIdentityLease(ctx, lease)
	if err != nil {
		return nil, err
	}
	coordination, err := openCoordinationAuthorityStoreWithLease(
		ctx,
		lease,
		workRunID,
		rar,
	)
	if err != nil {
		return nil, err
	}
	transitions, err := openTransitionAuthorityStoreWithLease(
		ctx,
		lease,
		workRunID,
	)
	if err != nil {
		return nil, err
	}
	store, err := workrun.OpenWorkRunStoreWithRepositoryIdentityLease(
		ctx,
		lease,
		workRunID,
	)
	if err != nil {
		return nil, err
	}
	executor := hostruntime.NewExecutor()
	mutationAuthority := MutationCompletionWorkRunAuthority{
		lease:        lease,
		existingOnly: true,
	}
	if mutationStore, openErr := mutationAuthority.resolveStore(ctx); openErr == nil {
		mutationAuthority.Store = mutationStore
	} else if !errors.Is(openErr, mutationintegrity.ErrCompletionNotFound) {
		return nil, openErr
	}
	store = store.WithAuthorityPorts(workrun.AuthorityPorts{
		PAD:                pad,
		ExplicitSDDRequest: coordination,
		MutationCompletion: mutationAuthority,
		Route:              coordination,
		SDD:                SDDWorkRunAuthority{Repo: repositoryRoot},
		Verification:       RARWorkRunAuthority{Coordination: coordination, RAR: rar},
		Launch:             executor,
	})
	if _, err := padAuthority.ResolveDeliveryRepository(
		ctx,
		padAuthority.RepositoryRef(),
	); err != nil {
		return nil, err
	}
	return &ProductionRepository{
		WorkRun:     store,
		Transitions: transitions,
		Plans:       rar,
		Executor:    executor,
		OpenEvidence: func(ctx context.Context) (evidence.Store, error) {
			return evidence.OpenStoreWithRepositoryIdentityLease(ctx, lease)
		},
		SemanticEvaluator: opener.SemanticEvaluator,
	}, nil
}

// ReadOnlyWorkRunRepositoryOpener remains available as an explicit rollback
// composition and for compatibility tests; it is not the shipped default.
type ReadOnlyWorkRunRepositoryOpener struct{}

func (ReadOnlyWorkRunRepositoryOpener) OpenRepository(
	ctx context.Context,
	repo string,
	workRunID string,
) (Repository, error) {
	store, err := workrun.OpenWorkRunStore(ctx, repo, workRunID)
	if err != nil {
		return nil, err
	}
	return readOnlyWorkRunRepository{store: store}, nil
}

type readOnlyWorkRunRepository struct {
	store workrun.WorkRunStore
}

func (repository readOnlyWorkRunRepository) Status(
	ctx context.Context,
) (workrun.WorkStatusV1, error) {
	return repository.store.PublicStatus(ctx)
}

func (readOnlyWorkRunRepository) ResolveAuthorization(
	context.Context,
	string,
) (ResolvedAuthorization, error) {
	return ResolvedAuthorization{}, ErrAuthorizationNotFound
}

func (readOnlyWorkRunRepository) ApplyAuthorized(
	context.Context,
	string,
	string,
) (workrun.WorkTransitionV1, error) {
	return workrun.WorkTransitionV1{}, ErrCapabilityReadOnly
}

var _ RepositoryOpener = ProductionRepositoryOpener{}
var _ RepositoryOpener = ReadOnlyWorkRunRepositoryOpener{}
var _ Repository = readOnlyWorkRunRepository{}
