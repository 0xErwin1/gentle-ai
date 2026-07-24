package workprovider

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"

	"github.com/gentleman-programming/gentle-ai/internal/deliveryadmission"
	"github.com/gentleman-programming/gentle-ai/internal/reviewtransaction"
	"github.com/gentleman-programming/gentle-ai/internal/workrun"
)

var ErrPADRepositoryAuthorityMismatch = errors.New(
	"PAD repository authority does not match the exact Git repository",
)

// PADRepositoryAuthority derives the PAD repositoryRef from the same exact Git
// identity used by the provider coordination authority. It never accepts a
// caller-selected repositoryRef-to-path mapping.
type PADRepositoryAuthority struct {
	identity coordinationRepositoryIdentity
}

// NewPADRepositoryAuthority is read-only. It resolves and retains the exact
// repository, common-dir, and git-dir identities without opening PAD storage.
func NewPADRepositoryAuthority(
	ctx context.Context,
	repo string,
) (*PADRepositoryAuthority, error) {
	lease, err := reviewtransaction.OpenRepositoryIdentityLease(ctx, repo)
	if err != nil {
		return nil, fmt.Errorf("resolve PAD repository authority: %w", err)
	}
	return newPADRepositoryAuthorityWithLease(ctx, lease)
}

func newPADRepositoryAuthorityWithLease(
	ctx context.Context,
	lease *reviewtransaction.RepositoryIdentityLease,
) (*PADRepositoryAuthority, error) {
	identity, err := coordinationRepositoryIdentityFromLease(ctx, lease)
	if err != nil {
		return nil, fmt.Errorf("resolve PAD repository authority: %w", err)
	}
	return &PADRepositoryAuthority{identity: identity}, nil
}

// RepositoryRef is the content-addressed exact Git identity shared by PAD,
// WorkRun, the default composition, and OwnerCoordinator.
func (authority *PADRepositoryAuthority) RepositoryRef() string {
	if authority == nil {
		return ""
	}
	return authority.identity.repositoryIdentity
}

// ResolveDeliveryRepository implements deliveryadmission's owner seam. The
// only resolvable repositoryRef is the one derived from the retained Git
// identity, and every resolution revalidates both paths and filesystem objects.
func (authority *PADRepositoryAuthority) ResolveDeliveryRepository(
	ctx context.Context,
	repositoryRef string,
) (string, error) {
	if authority == nil ||
		!validPADImmutableRef(authority.RepositoryRef()) ||
		repositoryRef != authority.RepositoryRef() {
		return "", ErrPADRepositoryAuthorityMismatch
	}
	if authority.identity.lease == nil {
		return "", ErrPADRepositoryAuthorityMismatch
	}
	if err := authority.identity.lease.Validate(ctx); err != nil {
		return "", fmt.Errorf(
			"%w: revalidate Git identity: %w",
			ErrPADRepositoryAuthorityMismatch,
			err,
		)
	}
	live := authority.identity.lease.Identity()
	if live.RepositoryRef != authority.identity.repositoryIdentity ||
		live.RepositoryRoot != authority.identity.repositoryRoot ||
		live.GitCommonDir != authority.identity.gitCommonDir ||
		live.GitDir != authority.identity.gitDir {
		return "", fmt.Errorf(
			"%w: Git filesystem identity changed",
			ErrPADRepositoryAuthorityMismatch,
		)
	}
	return authority.identity.repositoryRoot, nil
}

type padTrustedRepository interface {
	ResolveAdmittedIntent(
		context.Context,
		string,
	) (deliveryadmission.DeliveryIntent, error)
	ResolveLiveAuthorization(
		context.Context,
		string,
	) (deliveryadmission.LiveAuthorization, error)
	Close() error
}

type padRouteReevaluationRepository interface {
	padTrustedRepository
	deliveryadmission.TrustedResolver
	deliveryadmission.RARAuthorityPort
	deliveryadmission.RouteReevaluationResolver
}

type padTrustedRepositoryOpener func(
	context.Context,
	*PADRepositoryAuthority,
) (padTrustedRepository, error)

// PADWorkRunAdapter is the production PADAuthorityPort. It deliberately holds
// no open TrustedRepository. Each actual PAD resolution opens the provider
// store lazily and closes it before returning, so status reads without a bound
// PAD fact neither publish storage nor retain handles.
type PADWorkRunAdapter struct {
	authority *PADRepositoryAuthority
	open      padTrustedRepositoryOpener
}

func NewPADWorkRunAdapter(
	authority *PADRepositoryAuthority,
) (*PADWorkRunAdapter, error) {
	if authority == nil || !validPADImmutableRef(authority.RepositoryRef()) ||
		authority.identity.repositoryRoot == "" {
		return nil, errors.New("PAD WorkRun adapter requires repository authority")
	}
	return &PADWorkRunAdapter{
		authority: authority,
		open: func(
			ctx context.Context,
			authority *PADRepositoryAuthority,
		) (padTrustedRepository, error) {
			return deliveryadmission.OpenTrustedRepository(
				ctx,
				authority,
				authority.RepositoryRef(),
			)
		},
	}, nil
}

func (adapter *PADWorkRunAdapter) ResolveDeliveryIntent(
	ctx context.Context,
	intentRef string,
) (
	result workrun.DeliveryIntentAuthority,
	err error,
) {
	if !validPADImmutableRef(intentRef) {
		return workrun.DeliveryIntentAuthority{}, errors.New(
			"PAD delivery intent ref must be a canonical SHA-256 reference",
		)
	}
	repository, err := adapter.openRepository(ctx)
	if err != nil {
		return workrun.DeliveryIntentAuthority{}, err
	}
	defer func() {
		if closeErr := repository.Close(); closeErr != nil {
			result = workrun.DeliveryIntentAuthority{}
			err = errors.Join(
				err,
				fmt.Errorf("close PAD trusted repository: %w", closeErr),
			)
		}
	}()

	intent, err := repository.ResolveAdmittedIntent(ctx, intentRef)
	if err != nil {
		return workrun.DeliveryIntentAuthority{}, fmt.Errorf(
			"resolve admitted PAD delivery intent: %w",
			err,
		)
	}
	resolvedRef, err := intent.Ref()
	if err != nil {
		return workrun.DeliveryIntentAuthority{}, fmt.Errorf(
			"%w: validate admitted delivery intent: %v",
			deliveryadmission.ErrTrustedRepositoryCorrupt,
			err,
		)
	}
	if resolvedRef != intentRef ||
		intent.Destination.RepositoryRef != adapter.authority.RepositoryRef() {
		return workrun.DeliveryIntentAuthority{}, fmt.Errorf(
			"%w: admitted delivery intent binding",
			deliveryadmission.ErrTrustedRepositoryCorrupt,
		)
	}
	return workrun.DeliveryIntentAuthority{IntentRef: resolvedRef}, nil
}

func (adapter *PADWorkRunAdapter) ResolveLiveDeliveryAuthorization(
	ctx context.Context,
	authorizationRef string,
) (
	result workrun.DeliveryAuthorizationAuthority,
	err error,
) {
	if !validPADImmutableRef(authorizationRef) {
		return workrun.DeliveryAuthorizationAuthority{}, errors.New(
			"PAD delivery authorization ref must be a canonical SHA-256 reference",
		)
	}
	repository, err := adapter.openRepository(ctx)
	if err != nil {
		return workrun.DeliveryAuthorizationAuthority{}, err
	}
	defer func() {
		if closeErr := repository.Close(); closeErr != nil {
			result = workrun.DeliveryAuthorizationAuthority{}
			err = errors.Join(
				err,
				fmt.Errorf("close PAD trusted repository: %w", closeErr),
			)
		}
	}()

	live, err := repository.ResolveLiveAuthorization(ctx, authorizationRef)
	if err != nil {
		if errors.Is(err, deliveryadmission.ErrLiveAuthorizationInactive) {
			reason := ""
			var inactive *deliveryadmission.LiveAuthorizationInactiveError
			if errors.As(err, &inactive) && inactive != nil {
				reason = string(inactive.Reason)
			}
			return workrun.DeliveryAuthorizationAuthority{},
				&workrun.DeliveryAuthorizationInactiveError{
					AuthorizationRef: authorizationRef,
					Reason:           reason,
				}
		}
		return workrun.DeliveryAuthorizationAuthority{}, fmt.Errorf(
			"resolve live PAD delivery authorization: %w",
			err,
		)
	}
	if live.AuthorizationRef != authorizationRef ||
		live.Binding.Destination.RepositoryRef !=
			adapter.authority.RepositoryRef() {
		return workrun.DeliveryAuthorizationAuthority{}, fmt.Errorf(
			"%w: live delivery authorization binding",
			deliveryadmission.ErrTrustedRepositoryCorrupt,
		)
	}
	if err := live.Binding.Validate(); err != nil {
		return workrun.DeliveryAuthorizationAuthority{}, fmt.Errorf(
			"%w: validate live delivery authorization binding: %v",
			deliveryadmission.ErrTrustedRepositoryCorrupt,
			err,
		)
	}
	if live.ObservedAt < live.Binding.IssuedAt ||
		live.ObservedAt >= live.Binding.ExpiresAt {
		return workrun.DeliveryAuthorizationAuthority{}, fmt.Errorf(
			"%w: live delivery authorization observation window",
			deliveryadmission.ErrTrustedRepositoryCorrupt,
		)
	}

	kind, err := padWorkRunAuthorizationKind(live.Kind)
	if err != nil {
		return workrun.DeliveryAuthorizationAuthority{}, err
	}
	return workrun.DeliveryAuthorizationAuthority{
		AuthorizationRef:      live.AuthorizationRef,
		DecisionRef:           live.Binding.DecisionRef,
		Kind:                  kind,
		DeliveryIntentRef:     live.Binding.IntentRef,
		CandidateRef:          live.Binding.Candidate.Digest,
		ReviewReceiptRef:      live.Binding.ReviewReceiptRef,
		VerificationResultRef: live.Binding.VerificationResultRef,
		ValidatedAt:           live.Binding.IssuedAt,
		ObservedAt:            live.ObservedAt,
		ExpiresAt:             live.Binding.ExpiresAt,
	}, nil
}

func (adapter *PADWorkRunAdapter) ResolveDeliveryRouteReevaluation(
	ctx context.Context,
	reevaluationRef string,
) (
	authority workrun.DeliveryRouteReevaluationAuthority,
	err error,
) {
	if !validPADImmutableRef(reevaluationRef) {
		return workrun.DeliveryRouteReevaluationAuthority{}, errors.New(
			"PAD route reevaluation ref must be a canonical SHA-256 reference",
		)
	}
	opened, err := adapter.openRepository(ctx)
	if err != nil {
		return workrun.DeliveryRouteReevaluationAuthority{}, err
	}
	repository, ok := opened.(padRouteReevaluationRepository)
	if !ok {
		_ = opened.Close()
		return workrun.DeliveryRouteReevaluationAuthority{}, errors.New(
			"PAD owner repository does not support route reevaluation",
		)
	}
	defer func() {
		if closeErr := repository.Close(); closeErr != nil {
			authority = workrun.DeliveryRouteReevaluationAuthority{}
			err = errors.Join(
				err,
				fmt.Errorf("close PAD trusted repository: %w", closeErr),
			)
		}
	}()
	reevaluation, source, target, err :=
		resolveValidatedPADRouteReevaluation(
			ctx,
			repository,
			reevaluationRef,
		)
	if err != nil {
		return workrun.DeliveryRouteReevaluationAuthority{}, err
	}
	return workrun.DeliveryRouteReevaluationAuthority{
		ReevaluationRef:            reevaluationRef,
		RepositoryRef:              reevaluation.Destination.RepositoryRef,
		SourceDecisionRef:          reevaluation.SourceDecisionRef,
		TargetAdmissionDecisionRef: reevaluation.TargetAdmissionDecisionRef,
		TargetDecisionRef:          reevaluation.DecisionRef,
		SourceDeliveryIntentRef:    source.AdmissionDecision.IntentRef,
		TargetDeliveryIntentRef:    target.AdmissionDecision.IntentRef,
		SourceRoute:                string(reevaluation.SourceRoute),
		TargetRoute:                string(reevaluation.TargetRoute),
		Mechanism:                  string(reevaluation.Mechanism),
		ScopeDigest:                source.AdmissionDecision.Intent.ScopeDigest,
		CandidateRef:               reevaluation.Candidate.Digest,
		ReviewReceiptRef:           reevaluation.ReviewReceiptRef,
		VerificationResultRef:      reevaluation.VerificationResultRef,
	}, nil
}

func resolveValidatedPADRouteReevaluation(
	ctx context.Context,
	repository padRouteReevaluationRepository,
	reevaluationRef string,
) (
	deliveryadmission.RouteReevaluation,
	deliveryadmission.DeliveryDecision,
	deliveryadmission.DeliveryDecision,
	error,
) {
	reevaluation, err := repository.ResolveRouteReevaluation(
		ctx,
		reevaluationRef,
	)
	if err != nil {
		return deliveryadmission.RouteReevaluation{},
			deliveryadmission.DeliveryDecision{},
			deliveryadmission.DeliveryDecision{},
			fmt.Errorf("resolve PAD route reevaluation: %w", err)
	}
	resolvedRef, err := reevaluation.Ref()
	if err != nil || resolvedRef != reevaluationRef {
		return deliveryadmission.RouteReevaluation{},
			deliveryadmission.DeliveryDecision{},
			deliveryadmission.DeliveryDecision{},
			fmt.Errorf(
				"%w: route reevaluation immutable reference",
				deliveryadmission.ErrTrustedRepositoryCorrupt,
			)
	}
	source, err := deliveryadmission.ValidateDeliveryDecision(
		ctx,
		repository,
		repository,
		reevaluation.SourceDecisionRef,
	)
	if err != nil {
		return deliveryadmission.RouteReevaluation{},
			deliveryadmission.DeliveryDecision{},
			deliveryadmission.DeliveryDecision{},
			fmt.Errorf("validate PAD source delivery decision: %w", err)
	}
	target, err := deliveryadmission.ValidateDeliveryDecision(
		ctx,
		repository,
		repository,
		reevaluation.DecisionRef,
	)
	if err != nil {
		return deliveryadmission.RouteReevaluation{},
			deliveryadmission.DeliveryDecision{},
			deliveryadmission.DeliveryDecision{},
			fmt.Errorf("validate PAD target delivery decision: %w", err)
	}
	if source.AdmissionDecision.Route != reevaluation.SourceRoute ||
		target.AdmissionDecision.Route != reevaluation.TargetRoute ||
		target.AdmissionDecisionRef !=
			reevaluation.TargetAdmissionDecisionRef ||
		source.AdmissionDecision.Intent.ScopeDigest !=
			target.AdmissionDecision.Intent.ScopeDigest ||
		source.Gates.Destination != target.Gates.Destination ||
		target.Gates.Destination != reevaluation.Destination ||
		target.Gates.Mechanism != reevaluation.Mechanism ||
		target.GateRef != reevaluation.GateRef ||
		target.DecidedAt != reevaluation.EvaluatedAt ||
		source.Candidate != reevaluation.Candidate ||
		target.Candidate != reevaluation.Candidate ||
		source.ReviewReceiptRef != reevaluation.ReviewReceiptRef ||
		target.ReviewReceiptRef != reevaluation.ReviewReceiptRef ||
		source.VerificationResultRef !=
			reevaluation.VerificationResultRef ||
		target.VerificationResultRef !=
			reevaluation.VerificationResultRef {
		return deliveryadmission.RouteReevaluation{},
			deliveryadmission.DeliveryDecision{},
			deliveryadmission.DeliveryDecision{},
			fmt.Errorf(
				"%w: route reevaluation content lineage",
				deliveryadmission.ErrTrustedRepositoryCorrupt,
			)
	}
	return reevaluation, source, target, nil
}

func (adapter *PADWorkRunAdapter) openRepository(
	ctx context.Context,
) (padTrustedRepository, error) {
	if adapter == nil || adapter.authority == nil || adapter.open == nil {
		return nil, errors.New("PAD WorkRun adapter is not initialized")
	}
	if _, err := adapter.authority.ResolveDeliveryRepository(
		ctx,
		adapter.authority.RepositoryRef(),
	); err != nil {
		return nil, err
	}
	repository, err := adapter.open(ctx, adapter.authority)
	if err != nil {
		return nil, fmt.Errorf("open PAD trusted repository: %w", err)
	}
	if repository == nil {
		return nil, errors.New("open PAD trusted repository returned nil")
	}
	return repository, nil
}

func padWorkRunAuthorizationKind(
	kind deliveryadmission.AuthorizationKind,
) (workrun.DeliveryAuthorizationKind, error) {
	switch kind {
	case deliveryadmission.AuthorizationNormal:
		return workrun.DeliveryAuthorizationNormal, nil
	case deliveryadmission.AuthorizationException:
		return workrun.DeliveryAuthorizationException, nil
	default:
		return "", fmt.Errorf(
			"%w: unsupported live delivery authorization kind %q",
			deliveryadmission.ErrTrustedRepositoryCorrupt,
			kind,
		)
	}
}

func validPADImmutableRef(ref string) bool {
	if len(ref) != 71 ||
		!strings.HasPrefix(ref, "sha256:") ||
		strings.ToLower(ref) != ref {
		return false
	}
	_, err := hex.DecodeString(ref[7:])
	return err == nil
}

var _ deliveryadmission.DeliveryRepositoryAuthority = (*PADRepositoryAuthority)(nil)
var _ workrun.PADAuthorityPort = (*PADWorkRunAdapter)(nil)
var _ workrun.DeliveryRouteReevaluationAuthorityPort = (*PADWorkRunAdapter)(nil)
