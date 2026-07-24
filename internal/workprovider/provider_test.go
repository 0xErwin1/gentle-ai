package workprovider

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/gentleman-programming/gentle-ai/internal/workrun"
)

type countingActivationResolver struct {
	mode  ActivationMode
	err   error
	calls int
}

func (resolver *countingActivationResolver) ResolveActivation(
	context.Context,
	string,
) (ActivationMode, error) {
	resolver.calls++
	return resolver.mode, resolver.err
}

type countingRepositoryOpener struct {
	repository Repository
	err        error
	calls      int
}

func (opener *countingRepositoryOpener) OpenRepository(
	context.Context,
	string,
	string,
) (Repository, error) {
	opener.calls++
	return opener.repository, opener.err
}

type fakeRepository struct {
	status        workrun.WorkStatusV1
	statusErr     error
	authorization ResolvedAuthorization
	resolveErr    error
	transition    workrun.WorkTransitionV1
	applyErr      error

	statusCalls  int
	resolveCalls int
	applyCalls   int
}

func (repository *fakeRepository) Status(
	context.Context,
) (workrun.WorkStatusV1, error) {
	repository.statusCalls++
	return repository.status, repository.statusErr
}

func (repository *fakeRepository) ResolveAuthorization(
	_ context.Context,
	_ string,
) (ResolvedAuthorization, error) {
	repository.resolveCalls++
	return repository.authorization, repository.resolveErr
}

func (repository *fakeRepository) ApplyAuthorized(
	_ context.Context,
	_ string,
	_ string,
) (workrun.WorkTransitionV1, error) {
	repository.applyCalls++
	return repository.transition, repository.applyErr
}

func TestContractPreflightRunsBeforeActivationOrRepositoryOpen(t *testing.T) {
	t.Parallel()

	for _, contract := range []string{"", "gentle-ai.work-status/v2", " " + workrun.WorkStatusContractV1} {
		contract := contract
		t.Run("status_"+contract, func(t *testing.T) {
			t.Parallel()
			activation := &countingActivationResolver{mode: ActivationEnabled}
			opener := &countingRepositoryOpener{}
			controller := NewController(opener, activation)

			result, err := controller.Status(context.Background(), StatusRequest{Contract: contract})
			if err != nil {
				t.Fatalf("Status() error = %v", err)
			}
			if result.Diagnostic == nil ||
				result.Diagnostic.Code != "unsupported_contract" ||
				result.Diagnostic.MutationOutcome != "not_started" {
				t.Fatalf("Status() result = %#v", result)
			}
			if activation.calls != 0 || opener.calls != 0 {
				t.Fatalf(
					"unsupported status called activation/open = %d/%d",
					activation.calls,
					opener.calls,
				)
			}
		})
	}

	for _, contract := range []string{"", "gentle-ai.work-transition/v2", workrun.WorkTransitionContractV1 + " "} {
		contract := contract
		t.Run("apply_"+contract, func(t *testing.T) {
			t.Parallel()
			activation := &countingActivationResolver{mode: ActivationEnabled}
			opener := &countingRepositoryOpener{}
			controller := NewController(opener, activation)

			result, err := controller.Apply(context.Background(), ApplyRequest{Contract: contract})
			if err != nil {
				t.Fatalf("Apply() error = %v", err)
			}
			if result.Diagnostic == nil ||
				result.Diagnostic.Code != "unsupported_contract" ||
				result.Diagnostic.MutationOutcome != "not_started" {
				t.Fatalf("Apply() result = %#v", result)
			}
			if activation.calls != 0 || opener.calls != 0 {
				t.Fatalf(
					"unsupported apply called activation/open = %d/%d",
					activation.calls,
					opener.calls,
				)
			}
		})
	}
}

func TestKillSwitchModesFailClosedAndNeverRestoreAuthority(t *testing.T) {
	t.Parallel()

	t.Run("disabled opens nothing", func(t *testing.T) {
		t.Parallel()
		opener := &countingRepositoryOpener{}
		controller := NewController(opener, StaticActivationResolver{Mode: ActivationDisabled})

		_, statusErr := controller.Status(context.Background(), validStatusRequest())
		_, applyErr := controller.Apply(context.Background(), validApplyRequest())
		if !errors.Is(statusErr, ErrCapabilityDisabled) ||
			!errors.Is(applyErr, ErrCapabilityDisabled) {
			t.Fatalf("disabled errors = %v / %v", statusErr, applyErr)
		}
		if opener.calls != 0 {
			t.Fatalf("disabled mode opened repository %d times", opener.calls)
		}
	})

	t.Run("read only strips transition and blocks apply before open", func(t *testing.T) {
		t.Parallel()
		repository := validFakeRepository(AuthorizationGeneral)
		statusOpener := &countingRepositoryOpener{repository: repository}
		controller := NewController(
			statusOpener,
			StaticActivationResolver{Mode: ActivationReadOnly},
		)

		status, err := controller.Status(context.Background(), validStatusRequest())
		if err != nil {
			t.Fatalf("Status() error = %v", err)
		}
		if status.Status == nil || status.Status.AuthorizedTransition != nil {
			t.Fatalf("read-only status = %#v", status)
		}
		if repository.resolveCalls != 0 {
			t.Fatal("read-only status resolved an authorization")
		}

		applyOpener := &countingRepositoryOpener{repository: repository}
		controller = NewController(
			applyOpener,
			StaticActivationResolver{Mode: ActivationReadOnly},
		)
		_, err = controller.Apply(context.Background(), validApplyRequest())
		if !errors.Is(err, ErrCapabilityReadOnly) {
			t.Fatalf("Apply() error = %v", err)
		}
		if applyOpener.calls != 0 || repository.applyCalls != 0 {
			t.Fatalf(
				"read-only apply opened/mutated = %d/%d",
				applyOpener.calls,
				repository.applyCalls,
			)
		}
	})

	t.Run("recovery only rejects general before mutation", func(t *testing.T) {
		t.Parallel()
		repository := validFakeRepository(AuthorizationGeneral)
		opener := &countingRepositoryOpener{repository: repository}
		controller := NewController(
			opener,
			StaticActivationResolver{Mode: ActivationRecoveryOnly},
		)

		_, err := controller.Apply(context.Background(), validApplyRequest())
		if !errors.Is(err, ErrRecoveryOnly) {
			t.Fatalf("Apply() error = %v", err)
		}
		if repository.applyCalls != 0 {
			t.Fatalf("general transition mutated in recovery-only mode")
		}
	})

	t.Run("recovery only permits owner classified recovery", func(t *testing.T) {
		t.Parallel()
		repository := validFakeRepository(AuthorizationRecovery)
		opener := &countingRepositoryOpener{repository: repository}
		controller := NewController(
			opener,
			StaticActivationResolver{Mode: ActivationRecoveryOnly},
		)

		result, err := controller.Apply(context.Background(), validApplyRequest())
		if err != nil {
			t.Fatalf("Apply() error = %v", err)
		}
		if result.Transition == nil || repository.applyCalls != 1 {
			t.Fatalf("recovery result/calls = %#v / %d", result, repository.applyCalls)
		}
	})

	t.Run("enabled applies only resolved owner record", func(t *testing.T) {
		t.Parallel()
		repository := validFakeRepository(AuthorizationGeneral)
		controller := NewController(
			&countingRepositoryOpener{repository: repository},
			StaticActivationResolver{Mode: ActivationEnabled},
		)

		result, err := controller.Apply(context.Background(), validApplyRequest())
		if err != nil {
			t.Fatalf("Apply() error = %v", err)
		}
		if result.Transition == nil ||
			repository.resolveCalls != 1 ||
			repository.applyCalls != 1 {
			t.Fatalf(
				"enabled result/resolve/apply = %#v / %d / %d",
				result,
				repository.resolveCalls,
				repository.applyCalls,
			)
		}
	})
}

func TestMissingStaleOrMismatchedAuthorizationNeverMutates(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		resolve ResolvedAuthorization
		err     error
		want    error
	}{
		{name: "missing", err: ErrAuthorizationNotFound, want: ErrAuthorizationNotFound},
		{name: "stale", err: ErrAuthorizationStale, want: ErrAuthorizationStale},
		{
			name: "mismatched revision",
			resolve: func() ResolvedAuthorization {
				value := validAuthorization(AuthorizationGeneral)
				value.ExpectedRevision = testRevision("f")
				return value
			}(),
			want: ErrAuthorizationMismatch,
		},
		{
			name: "mismatched work run",
			resolve: func() ResolvedAuthorization {
				value := validAuthorization(AuthorizationGeneral)
				value.WorkRunID = "foreign-work-run"
				return value
			}(),
			want: ErrAuthorizationMismatch,
		},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			repository := validFakeRepository(AuthorizationGeneral)
			if test.resolve.AuthorizationRef != "" {
				repository.authorization = test.resolve
			}
			repository.resolveErr = test.err
			controller := NewController(
				&countingRepositoryOpener{repository: repository},
				StaticActivationResolver{Mode: ActivationEnabled},
			)

			_, err := controller.Apply(context.Background(), validApplyRequest())
			if !errors.Is(err, test.want) {
				t.Fatalf("Apply() error = %v, want %v", err, test.want)
			}
			if repository.applyCalls != 0 {
				t.Fatalf("authorization failure called ApplyAuthorized %d times", repository.applyCalls)
			}
		})
	}
}

func TestStatusDropsUnresolvableOrNonRecoveryAuthorization(t *testing.T) {
	t.Parallel()

	t.Run("stale enabled transition", func(t *testing.T) {
		t.Parallel()
		repository := validFakeRepository(AuthorizationGeneral)
		repository.resolveErr = ErrAuthorizationStale
		controller := NewController(
			&countingRepositoryOpener{repository: repository},
			StaticActivationResolver{Mode: ActivationEnabled},
		)
		result, err := controller.Status(context.Background(), validStatusRequest())
		if err != nil {
			t.Fatalf("Status() error = %v", err)
		}
		if result.Status == nil || result.Status.AuthorizedTransition != nil {
			t.Fatalf("stale status = %#v", result)
		}
	})

	t.Run("general recovery-only transition", func(t *testing.T) {
		t.Parallel()
		repository := validFakeRepository(AuthorizationGeneral)
		controller := NewController(
			&countingRepositoryOpener{repository: repository},
			StaticActivationResolver{Mode: ActivationRecoveryOnly},
		)
		result, err := controller.Status(context.Background(), validStatusRequest())
		if err != nil {
			t.Fatalf("Status() error = %v", err)
		}
		if result.Status == nil || result.Status.AuthorizedTransition != nil {
			t.Fatalf("recovery-only status = %#v", result)
		}
	})
}

func validStatusRequest() StatusRequest {
	return StatusRequest{
		Repo: "/repo", WorkRunID: "work-provider-test",
		Contract: workrun.WorkStatusContractV1,
	}
}

func validApplyRequest() ApplyRequest {
	return ApplyRequest{
		Repo: "/repo", WorkRunID: "work-provider-test",
		Contract:         workrun.WorkTransitionContractV1,
		AuthorizationRef: "authorization:provider-test",
		ExpectedRevision: testRevision("a"),
	}
}

func validFakeRepository(class AuthorizationClass) *fakeRepository {
	authorization := validAuthorization(class)
	return &fakeRepository{
		status:        validStatus(),
		authorization: authorization,
		transition: workrun.WorkTransitionV1{
			Schema: workrun.WorkTransitionContractV1, Contract: workrun.WorkTransitionContractV1,
			WorkRunID:        "work-provider-test",
			PreviousRevision: testRevision("a"), Revision: testRevision("b"),
			AppliedAuthorizationRef: authorization.AuthorizationRef,
			PublicState:             workrun.PublicStateChecking,
		},
	}
}

func validAuthorization(class AuthorizationClass) ResolvedAuthorization {
	return ResolvedAuthorization{
		WorkRunID:                  "work-provider-test",
		AuthorizationRef:           "authorization:provider-test",
		ExpectedRevision:           testRevision("a"),
		CandidateRef:               "candidate:provider-test",
		ActionTicketRef:            "ticket:provider-test",
		ApplicableAuthorizationRef: "authorization:applicability-test",
		Class:                      class,
	}
}

func validStatus() workrun.WorkStatusV1 {
	authorization := validAuthorization(AuthorizationGeneral)
	return workrun.WorkStatusV1{
		Schema: workrun.WorkStatusContractV1, Contract: workrun.WorkStatusContractV1,
		WorkRunID: "work-provider-test", Revision: testRevision("a"),
		PublicState:         workrun.PublicStateWorking,
		RouteDecision:       workrun.RouteDecisionDirectInline,
		ImplementationRoute: workrun.ImplementationRouteDirectInline,
		Verification: workrun.VerificationSummaryV1{
			Outcome: workrun.VerificationPending, ResultRefs: []string{},
		},
		AuthorizedTransition: &workrun.AuthorizedTransitionV1{
			Contract: workrun.WorkTransitionContractV1, Operation: "apply",
			AuthorizationRef:           authorization.AuthorizationRef,
			ExpectedRevision:           authorization.ExpectedRevision,
			CandidateRef:               authorization.CandidateRef,
			ActionTicketRef:            authorization.ActionTicketRef,
			ApplicableAuthorizationRef: authorization.ApplicableAuthorizationRef,
		},
	}
}

func testRevision(character string) string {
	return "sha256:" + strings.Repeat(character, 64)
}
