package workprovider

import (
	"context"
	"errors"
	"fmt"

	"github.com/gentleman-programming/gentle-ai/internal/agents/capabilitymanifest"
	"github.com/gentleman-programming/gentle-ai/internal/evidence"
	"github.com/gentleman-programming/gentle-ai/internal/hostruntime"
	"github.com/gentleman-programming/gentle-ai/internal/model"
	"github.com/gentleman-programming/gentle-ai/internal/mutationintegrity"
	"github.com/gentleman-programming/gentle-ai/internal/reviewtransaction"
	"github.com/gentleman-programming/gentle-ai/internal/workrun"
)

var ErrAgentWorkRoutingUnavailable = errors.New(
	"agent does not advertise the work-routing capability",
)

// ProductionOwnerCoordinatorFactory retains one read-only repository identity
// lease and opens every mutable owner against that exact lease only after the
// live activation guard permits a general mutation.
//
// Constructing the factory is read-only. In particular, an unset environment
// cannot create provider storage merely because a future caller prepares the
// outcome service.
type ProductionOwnerCoordinatorFactory struct {
	lease      *reviewtransaction.RepositoryIdentityLease
	agent      model.AgentID
	activation ActivationResolver
}

// NewProductionOwnerCoordinatorFactory resolves the exact Git identity without
// opening any owner repository. The agent and activation resolver are fixed by
// provider composition; callers of the outcome service select neither.
func NewProductionOwnerCoordinatorFactory(
	ctx context.Context,
	repo string,
	agent model.AgentID,
	activation ActivationResolver,
) (*ProductionOwnerCoordinatorFactory, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if activation == nil {
		return nil, errors.New(
			"production owner coordinator factory requires activation authority",
		)
	}
	lease, err := reviewtransaction.OpenRepositoryIdentityLease(ctx, repo)
	if err != nil {
		return nil, fmt.Errorf("resolve production owner repository identity: %w", err)
	}
	if err := lease.Validate(ctx); err != nil {
		return nil, fmt.Errorf("validate production owner repository identity: %w", err)
	}
	return &ProductionOwnerCoordinatorFactory{
		lease:      lease,
		agent:      agent,
		activation: activation,
	}, nil
}

// NewDefaultOwnerCoordinatorFactory is the shipped environment-controlled
// composition. Unset remains read-only; this constructor never advertises or
// activates the canonical ACI manifest.
func NewDefaultOwnerCoordinatorFactory(
	ctx context.Context,
	repo string,
	agent model.AgentID,
) (*ProductionOwnerCoordinatorFactory, error) {
	return NewProductionOwnerCoordinatorFactory(
		ctx,
		repo,
		agent,
		EnvironmentActivationResolver{},
	)
}

// RepositoryRef is the path-free identity supplied only to the injected owner
// intake authority. It is never accepted back from an outcome request.
func (factory *ProductionOwnerCoordinatorFactory) RepositoryRef() string {
	if factory == nil || factory.lease == nil {
		return ""
	}
	return factory.lease.Identity().RepositoryRef
}

func (factory *ProductionOwnerCoordinatorFactory) repositoryRoot() string {
	if factory == nil || factory.lease == nil {
		return ""
	}
	return factory.lease.Identity().RepositoryRoot
}

// repositoryIdentityLease is the package-local composition seam for owners
// which must join this exact repository authority later in the work lifecycle
// (including mutation-integrity completion). It deliberately does not open
// their stores: activation preflight remains responsible for preceding every
// potentially mutating owner construction.
func (factory *ProductionOwnerCoordinatorFactory) repositoryIdentityLease(
	ctx context.Context,
) (*reviewtransaction.RepositoryIdentityLease, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if factory == nil || factory.lease == nil {
		return nil, errors.New(
			"production owner coordinator factory is not initialized",
		)
	}
	if err := factory.lease.Validate(ctx); err != nil {
		return nil, fmt.Errorf(
			"validate production owner repository identity: %w",
			err,
		)
	}
	return factory.lease, nil
}

// ownerProductivePreflight is an owner-issued intake snapshot. Its private
// fields prevent outcome callers from selecting an agent, activation mode, or
// capability exposure. It authorizes the bounded intake read only: owner-store
// opening rechecks the live activation authority after intake returns.
type ownerProductivePreflight struct {
	factory       *ProductionOwnerCoordinatorFactory
	lease         *reviewtransaction.RepositoryIdentityLease
	repositoryRef string
	agent         model.AgentID
	mode          ActivationMode
	manifest      EffectiveCapabilityManifest
}

// preflightProductiveOwner resolves the live kill switch exactly once, derives
// the effective ACI view for the composition-owned agent, and proves the
// productive contract before intake resolution or owner-store opening.
func (factory *ProductionOwnerCoordinatorFactory) preflightProductiveOwner(
	ctx context.Context,
) (ownerProductivePreflight, error) {
	if err := ctx.Err(); err != nil {
		return ownerProductivePreflight{}, err
	}
	if factory == nil || factory.lease == nil || factory.activation == nil {
		return ownerProductivePreflight{}, errors.New(
			"production owner coordinator factory is not initialized",
		)
	}
	lease, err := factory.repositoryIdentityLease(ctx)
	if err != nil {
		return ownerProductivePreflight{}, err
	}
	mode, err := factory.activation.ResolveActivation(ctx, factory.repositoryRoot())
	if err != nil {
		return ownerProductivePreflight{}, err
	}
	if err := mode.Validate(); err != nil {
		return ownerProductivePreflight{}, err
	}
	manifest, err := effectiveAgentCapabilityManifest(factory.agent, mode)
	if err != nil {
		return ownerProductivePreflight{}, fmt.Errorf(
			"resolve productive owner capability manifest: %w",
			err,
		)
	}
	if err := manifest.Validate(); err != nil {
		return ownerProductivePreflight{}, fmt.Errorf(
			"validate productive owner capability manifest: %w",
			err,
		)
	}
	if !manifest.Advertises(capabilitymanifest.ContractWorkRoutingV1) {
		if err := activationMutationError(mode, false); err != nil {
			return ownerProductivePreflight{}, err
		}
		return ownerProductivePreflight{}, ErrAgentWorkRoutingUnavailable
	}
	if err := activationMutationError(mode, false); err != nil {
		return ownerProductivePreflight{}, err
	}
	return ownerProductivePreflight{
		factory:       factory,
		lease:         lease,
		repositoryRef: factory.RepositoryRef(),
		agent:         factory.agent,
		mode:          mode,
		manifest:      manifest,
	}, nil
}

func (preflight ownerProductivePreflight) validate(
	ctx context.Context,
	factory *ProductionOwnerCoordinatorFactory,
) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if factory == nil ||
		preflight.factory != factory ||
		preflight.lease == nil ||
		preflight.lease != factory.lease ||
		preflight.repositoryRef == "" ||
		preflight.repositoryRef != factory.RepositoryRef() ||
		preflight.agent != factory.agent ||
		preflight.mode != ActivationEnabled ||
		preflight.manifest.mode != preflight.mode ||
		preflight.manifest.manifest.AgentID != preflight.agent {
		return errors.New("productive owner preflight does not match factory")
	}
	if err := preflight.lease.Validate(ctx); err != nil {
		return fmt.Errorf(
			"validate productive owner preflight repository identity: %w",
			err,
		)
	}
	if err := preflight.manifest.Validate(); err != nil {
		return fmt.Errorf(
			"validate productive owner preflight manifest: %w",
			err,
		)
	}
	if !preflight.manifest.Advertises(
		capabilitymanifest.ContractWorkRoutingV1,
	) {
		return ErrAgentWorkRoutingUnavailable
	}
	return nil
}

// validateLiveProductivePreflight narrows the intake/open TOCTOU window. A
// potentially slow owner intake may outlive the enabled snapshot that admitted
// it, so no owner repository is opened until the factory has resolved the live
// kill switch and effective ACI view again.
func (factory *ProductionOwnerCoordinatorFactory) validateLiveProductivePreflight(
	ctx context.Context,
	preflight ownerProductivePreflight,
) error {
	if err := preflight.validate(ctx, factory); err != nil {
		return err
	}
	mode, err := factory.activation.ResolveActivation(
		ctx,
		factory.repositoryRoot(),
	)
	if err != nil {
		return err
	}
	if err := mode.Validate(); err != nil {
		return err
	}
	if err := activationMutationError(mode, false); err != nil {
		return err
	}
	manifest, err := effectiveAgentCapabilityManifest(factory.agent, mode)
	if err != nil {
		return fmt.Errorf(
			"revalidate productive owner capability manifest: %w",
			err,
		)
	}
	if err := manifest.Validate(); err != nil {
		return fmt.Errorf(
			"validate live productive owner capability manifest: %w",
			err,
		)
	}
	if !manifest.Advertises(capabilitymanifest.ContractWorkRoutingV1) {
		return ErrAgentWorkRoutingUnavailable
	}
	if mode != preflight.mode ||
		manifest.manifest != preflight.manifest.manifest {
		return errors.New(
			"productive owner activation changed after intake",
		)
	}
	return nil
}

// Open composes PAD, RAR, EPD, WorkRun, coordination, transition, SDD, and HCR
// ports from one retained lease. It does not create a second planner, ledger, or
// owner repository.
func (factory *ProductionOwnerCoordinatorFactory) Open(
	ctx context.Context,
	workRunID string,
) (*OwnerCoordinator, error) {
	preflight, err := factory.preflightProductiveOwner(ctx)
	if err != nil {
		return nil, err
	}
	return factory.openWithProductivePreflight(ctx, workRunID, preflight)
}

func (factory *ProductionOwnerCoordinatorFactory) openWithProductivePreflight(
	ctx context.Context,
	workRunID string,
	preflight ownerProductivePreflight,
) (*OwnerCoordinator, error) {
	if err := factory.validateLiveProductivePreflight(
		ctx,
		preflight,
	); err != nil {
		return nil, err
	}
	if !workRunIDPattern.MatchString(workRunID) {
		return nil, errors.New(
			"production owner coordinator requires a canonical work-run identifier",
		)
	}

	padRepositoryAuthority, err := newPADRepositoryAuthorityWithLease(
		ctx,
		factory.lease,
	)
	if err != nil {
		return nil, err
	}
	pad, err := NewPADWorkRunAdapter(padRepositoryAuthority)
	if err != nil {
		return nil, err
	}
	rar, err := reviewtransaction.
		OpenRARAuthorityRepositoryWithRepositoryIdentityLease(ctx, factory.lease)
	if err != nil {
		return nil, err
	}
	coordination, err := openCoordinationAuthorityStoreWithLease(
		ctx,
		factory.lease,
		workRunID,
		rar,
	)
	if err != nil {
		return nil, err
	}
	transitions, err := openTransitionAuthorityStoreWithLease(
		ctx,
		factory.lease,
		workRunID,
	)
	if err != nil {
		return nil, err
	}
	work, err := workrun.OpenWorkRunStoreWithRepositoryIdentityLease(
		ctx,
		factory.lease,
		workRunID,
	)
	if err != nil {
		return nil, err
	}
	evidenceStore, err := evidence.OpenStoreWithRepositoryIdentityLease(
		ctx,
		factory.lease,
	)
	if err != nil {
		return nil, err
	}
	mutationStore, err := mutationintegrity.OpenStore(ctx, factory.lease)
	if err != nil {
		return nil, err
	}
	executor := hostruntime.NewExecutor()
	return NewOwnerCoordinator(ctx, OwnerCoordinatorDependencies{
		WorkRun:      work,
		Coordination: coordination,
		RAR:          rar,
		Transitions:  transitions,
		Evidence:     evidenceStore,
		Mutations:    mutationStore,
		PADAuthority: pad,
		SDDAuthority: SDDWorkRunAuthority{
			Repo: factory.repositoryRoot(),
		},
		LaunchAuthority: executor,
		// The preflight gates store opening, but the reusable coordinator must
		// still resolve the live kill switch before every later mutation.
		Activation: factory.activation,
	})
}
