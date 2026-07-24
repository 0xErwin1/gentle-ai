package workprovider

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/gentleman-programming/gentle-ai/internal/deliveryadmission"
	"github.com/gentleman-programming/gentle-ai/internal/workrun"
)

type fakePADTrustedRepository struct {
	intent      deliveryadmission.DeliveryIntent
	intentErr   error
	live        deliveryadmission.LiveAuthorization
	liveErr     error
	intentCalls int
	liveCalls   int
	closeCalls  int
	closeErr    error
}

func (repository *fakePADTrustedRepository) ResolveAdmittedIntent(
	_ context.Context,
	_ string,
) (deliveryadmission.DeliveryIntent, error) {
	repository.intentCalls++
	return repository.intent, repository.intentErr
}

func (repository *fakePADTrustedRepository) ResolveLiveAuthorization(
	_ context.Context,
	_ string,
) (deliveryadmission.LiveAuthorization, error) {
	repository.liveCalls++
	return repository.live, repository.liveErr
}

func (repository *fakePADTrustedRepository) Close() error {
	repository.closeCalls++
	return repository.closeErr
}

func TestPADWorkRunAdapterStatusWithoutPADPublishesNothing(t *testing.T) {
	repo := initPADAdapterGitRepository(t)
	authority, err := NewPADRepositoryAuthority(context.Background(), repo)
	if err != nil {
		t.Fatal(err)
	}
	adapter, err := NewPADWorkRunAdapter(authority)
	if err != nil {
		t.Fatal(err)
	}
	deliveryRoot := filepath.Join(
		authority.identity.gitCommonDir,
		"gentle-ai",
		"delivery-admission",
	)
	assertPADPathMissing(t, deliveryRoot)

	store, err := workrun.OpenWorkRunStore(
		context.Background(),
		repo,
		"status-without-pad",
	)
	if err != nil {
		t.Fatal(err)
	}
	store = store.WithAuthorityPorts(workrun.AuthorityPorts{PAD: adapter})
	if _, err := store.PublicStatus(
		context.Background(),
	); !errors.Is(err, workrun.ErrWorkRunNotStarted) {
		t.Fatalf("status without WorkRun = %v", err)
	}
	assertPADPathMissing(t, deliveryRoot)
}

func TestPADWorkRunAdapterResolvesOnlyAdmittedIntent(t *testing.T) {
	authority, adapter := newPADWorkRunTestAdapter(t)
	intent, intentRef := testPADIntent(t, authority.RepositoryRef())

	unadmitted := &fakePADTrustedRepository{
		intentErr: errors.New("intent has no admitted owner decision"),
	}
	var opens int
	adapter.open = func(
		context.Context,
		*PADRepositoryAuthority,
	) (padTrustedRepository, error) {
		opens++
		return unadmitted, nil
	}
	if _, err := adapter.ResolveDeliveryIntent(
		context.Background(),
		intentRef,
	); err == nil {
		t.Fatal("unadmitted intent unexpectedly resolved")
	}
	if opens != 1 || unadmitted.intentCalls != 1 ||
		unadmitted.closeCalls != 1 {
		t.Fatalf(
			"unadmitted lifecycle = opens %d, resolves %d, closes %d",
			opens,
			unadmitted.intentCalls,
			unadmitted.closeCalls,
		)
	}

	admitted := &fakePADTrustedRepository{intent: intent}
	adapter.open = func(
		context.Context,
		*PADRepositoryAuthority,
	) (padTrustedRepository, error) {
		opens++
		return admitted, nil
	}
	resolved, err := adapter.ResolveDeliveryIntent(
		context.Background(),
		intentRef,
	)
	if err != nil {
		t.Fatal(err)
	}
	if resolved.IntentRef != intentRef {
		t.Fatalf("resolved intent = %q, want %q", resolved.IntentRef, intentRef)
	}
	if admitted.intentCalls != 1 || admitted.closeCalls != 1 {
		t.Fatalf(
			"admitted lifecycle = resolves %d, closes %d",
			admitted.intentCalls,
			admitted.closeCalls,
		)
	}

	opensBeforeInvalid := opens
	if _, err := adapter.ResolveDeliveryIntent(
		context.Background(),
		"sha256:not-canonical",
	); err == nil {
		t.Fatal("invalid intent reference unexpectedly resolved")
	}
	if opens != opensBeforeInvalid {
		t.Fatal("invalid intent reference opened PAD storage")
	}
}

func TestPADWorkRunAdapterProjectsLiveNormalAndExceptionAuthority(t *testing.T) {
	for _, test := range []struct {
		name string
		pad  deliveryadmission.AuthorizationKind
		work workrun.DeliveryAuthorizationKind
	}{
		{
			name: "normal",
			pad:  deliveryadmission.AuthorizationNormal,
			work: workrun.DeliveryAuthorizationNormal,
		},
		{
			name: "exception",
			pad:  deliveryadmission.AuthorizationException,
			work: workrun.DeliveryAuthorizationException,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			authority, adapter := newPADWorkRunTestAdapter(t)
			live := testPADLiveAuthorization(
				t,
				authority.RepositoryRef(),
				test.pad,
			)
			repository := &fakePADTrustedRepository{live: live}
			adapter.open = func(
				context.Context,
				*PADRepositoryAuthority,
			) (padTrustedRepository, error) {
				return repository, nil
			}

			resolved, err := adapter.ResolveLiveDeliveryAuthorization(
				context.Background(),
				live.AuthorizationRef,
			)
			if err != nil {
				t.Fatal(err)
			}
			if resolved.Kind != test.work ||
				resolved.AuthorizationRef != live.AuthorizationRef ||
				resolved.DeliveryIntentRef != live.Binding.IntentRef ||
				resolved.CandidateRef != live.Binding.Candidate.Digest ||
				resolved.ReviewReceiptRef != live.Binding.ReviewReceiptRef ||
				resolved.VerificationResultRef !=
					live.Binding.VerificationResultRef ||
				resolved.ValidatedAt != live.Binding.IssuedAt ||
				resolved.ObservedAt != live.ObservedAt ||
				resolved.ExpiresAt != live.Binding.ExpiresAt {
				t.Fatalf("WorkRun PAD projection = %#v", resolved)
			}
			if resolved.CandidateRef == live.Binding.Candidate.Ref {
				t.Fatal("WorkRun projection used candidate label instead of digest")
			}
			if repository.liveCalls != 1 || repository.closeCalls != 1 {
				t.Fatalf(
					"live lifecycle = resolves %d, closes %d",
					repository.liveCalls,
					repository.closeCalls,
				)
			}
		})
	}
}

func TestPADWorkRunAdapterMapsOnlyInactiveAuthorization(t *testing.T) {
	for _, reason := range []deliveryadmission.LiveAuthorizationInactiveReason{
		deliveryadmission.LiveAuthorizationExpired,
		deliveryadmission.LiveAuthorizationConsumed,
	} {
		t.Run(string(reason), func(t *testing.T) {
			_, adapter := newPADWorkRunTestAdapter(t)
			ref := testPADRef("inactive-" + string(reason))
			repository := &fakePADTrustedRepository{
				liveErr: &deliveryadmission.LiveAuthorizationInactiveError{
					AuthorizationRef: ref,
					Reason:           reason,
				},
			}
			adapter.open = func(
				context.Context,
				*PADRepositoryAuthority,
			) (padTrustedRepository, error) {
				return repository, nil
			}
			_, err := adapter.ResolveLiveDeliveryAuthorization(
				context.Background(),
				ref,
			)
			if !errors.Is(err, workrun.ErrDeliveryAuthorizationInactive) {
				t.Fatalf("inactive mapping = %v", err)
			}
			var inactive *workrun.DeliveryAuthorizationInactiveError
			if !errors.As(err, &inactive) ||
				inactive.AuthorizationRef != ref ||
				inactive.Reason != string(reason) {
				t.Fatalf("inactive detail = %#v, %v", inactive, err)
			}
			if repository.closeCalls != 1 {
				t.Fatalf("inactive close calls = %d", repository.closeCalls)
			}
		})
	}

	for _, test := range []struct {
		name string
		err  error
	}{
		{
			name: "corrupt",
			err:  deliveryadmission.ErrTrustedRepositoryCorrupt,
		},
		{
			name: "unavailable",
			err:  deliveryadmission.ErrTrustedRepositoryUnavailable,
		},
		{
			name: "expired-sentinel-without-inactive-authority",
			err:  deliveryadmission.ErrLiveAuthorizationExpired,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			_, adapter := newPADWorkRunTestAdapter(t)
			ref := testPADRef("visible-" + test.name)
			repository := &fakePADTrustedRepository{liveErr: test.err}
			adapter.open = func(
				context.Context,
				*PADRepositoryAuthority,
			) (padTrustedRepository, error) {
				return repository, nil
			}
			_, err := adapter.ResolveLiveDeliveryAuthorization(
				context.Background(),
				ref,
			)
			if !errors.Is(err, test.err) {
				t.Fatalf("visible PAD error = %v, want %v", err, test.err)
			}
			if errors.Is(err, workrun.ErrDeliveryAuthorizationInactive) {
				t.Fatalf("PAD error was hidden as inactive: %v", err)
			}
		})
	}
}

func TestPADRepositoryAuthorityFailsClosedAcrossRepositories(t *testing.T) {
	firstRepo := initPADAdapterGitRepository(t)
	secondRepo := initPADAdapterGitRepository(t)
	first, err := NewPADRepositoryAuthority(context.Background(), firstRepo)
	if err != nil {
		t.Fatal(err)
	}
	second, err := NewPADRepositoryAuthority(context.Background(), secondRepo)
	if err != nil {
		t.Fatal(err)
	}
	if first.RepositoryRef() == second.RepositoryRef() {
		t.Fatal("different exact Git repositories received the same repositoryRef")
	}
	resolvedRoot, err := first.ResolveDeliveryRepository(
		context.Background(),
		first.RepositoryRef(),
	)
	if err != nil || resolvedRoot != firstRepo {
		t.Fatalf(
			"exact repository resolution = %q, %v; want %q",
			resolvedRoot,
			err,
			firstRepo,
		)
	}
	if _, err := first.ResolveDeliveryRepository(
		context.Background(),
		second.RepositoryRef(),
	); !errors.Is(err, ErrPADRepositoryAuthorityMismatch) {
		t.Fatalf("cross-repository resolution = %v", err)
	}

	adapter, err := NewPADWorkRunAdapter(first)
	if err != nil {
		t.Fatal(err)
	}
	live := testPADLiveAuthorization(
		t,
		second.RepositoryRef(),
		deliveryadmission.AuthorizationNormal,
	)
	repository := &fakePADTrustedRepository{live: live}
	adapter.open = func(
		context.Context,
		*PADRepositoryAuthority,
	) (padTrustedRepository, error) {
		return repository, nil
	}
	if _, err := adapter.ResolveLiveDeliveryAuthorization(
		context.Background(),
		live.AuthorizationRef,
	); !errors.Is(err, deliveryadmission.ErrTrustedRepositoryCorrupt) {
		t.Fatalf("cross-repository live authorization = %v", err)
	}
}

func newPADWorkRunTestAdapter(
	t *testing.T,
) (*PADRepositoryAuthority, *PADWorkRunAdapter) {
	t.Helper()
	authority, err := NewPADRepositoryAuthority(
		context.Background(),
		initPADAdapterGitRepository(t),
	)
	if err != nil {
		t.Fatal(err)
	}
	adapter, err := NewPADWorkRunAdapter(authority)
	if err != nil {
		t.Fatal(err)
	}
	return authority, adapter
}

func initPADAdapterGitRepository(t *testing.T) string {
	t.Helper()
	repo := filepath.Join(t.TempDir(), "repository")
	if output, err := exec.Command(
		"git",
		"init",
		"--quiet",
		repo,
	).CombinedOutput(); err != nil {
		t.Fatalf("git init: %v\n%s", err, output)
	}
	absolute, err := filepath.Abs(repo)
	if err != nil {
		t.Fatal(err)
	}
	return filepath.Clean(absolute)
}

func testPADIntent(
	t *testing.T,
	repositoryRef string,
) (deliveryadmission.DeliveryIntent, string) {
	t.Helper()
	policy, err := deliveryadmission.NewRoutePolicy(
		"policy:adapter",
		"revision:1",
		deliveryadmission.RouteDirectMain,
		true,
		false,
		false,
		120,
		0,
	)
	if err != nil {
		t.Fatal(err)
	}
	policyRef, err := policy.Ref()
	if err != nil {
		t.Fatal(err)
	}
	intent, err := deliveryadmission.NewIntent(
		"nonce:adapter-intent",
		deliveryadmission.RouteDirectMain,
		testPADRef("scope"),
		deliveryadmission.DestinationBinding{
			RepositoryRef:    repositoryRef,
			TargetRef:        "refs/heads/main",
			ObservedRevision: "git:base/1",
			DefaultBranch:    true,
		},
		policyRef,
		policy.Binding(),
		100,
	)
	if err != nil {
		t.Fatal(err)
	}
	ref, err := intent.Ref()
	if err != nil {
		t.Fatal(err)
	}
	return intent, ref
}

func testPADLiveAuthorization(
	t *testing.T,
	repositoryRef string,
	kind deliveryadmission.AuthorizationKind,
) deliveryadmission.LiveAuthorization {
	t.Helper()
	policy, err := deliveryadmission.NewRoutePolicy(
		"policy:adapter-live",
		"revision:1",
		deliveryadmission.RouteDirectMain,
		true,
		false,
		false,
		120,
		0,
	)
	if err != nil {
		t.Fatal(err)
	}
	policyRef, err := policy.Ref()
	if err != nil {
		t.Fatal(err)
	}
	signal := deliveryadmission.AuthoritySignal{
		Schema:     deliveryadmission.AuthoritySignalContractV1,
		SignalID:   "signal:adapter-live",
		IssuerRef:  "maintainer:adapter",
		Provenance: deliveryadmission.ProvenanceMaintainerControl,
		PolicyRef:  policyRef,
		Policy:     policy.Binding(),
		ExpiresAt:  300,
	}
	signalRef, err := signal.Ref()
	if err != nil {
		t.Fatal(err)
	}
	binding := deliveryadmission.AuthorizationBinding{
		Nonce:       "nonce:adapter-live",
		DecisionRef: testPADRef("decision"),
		IntentRef:   testPADRef("intent"),
		Route:       deliveryadmission.RouteDirectMain,
		Candidate: deliveryadmission.CandidateBinding{
			Ref:    "candidate:human-readable-label",
			Digest: testPADRef("candidate"),
		},
		Destination: deliveryadmission.DestinationBinding{
			RepositoryRef:    repositoryRef,
			TargetRef:        "refs/heads/main",
			ObservedRevision: "git:base/1",
			DefaultBranch:    true,
		},
		PolicyRef:             policyRef,
		Policy:                policy,
		ReviewReceiptRef:      testPADRef("receipt"),
		VerificationResultRef: testPADRef("result"),
		AuthorityRef:          signalRef,
		Authority:             signal,
		IssuedAt:              100,
		ExpiresAt:             200,
		UseLimit:              1,
	}
	if err := binding.Validate(); err != nil {
		t.Fatal(err)
	}
	return deliveryadmission.LiveAuthorization{
		AuthorizationRef: testPADRef("authorization-" + string(kind)),
		Kind:             kind,
		Binding:          binding,
		ObservedAt:       150,
	}
}

func testPADRef(seed string) string {
	sum := sha256.Sum256([]byte(seed))
	return "sha256:" + hex.EncodeToString(sum[:])
}

func assertPADPathMissing(t *testing.T, path string) {
	t.Helper()
	if _, err := os.Lstat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("PAD path %q exists or is unreadable: %v", path, err)
	}
}
