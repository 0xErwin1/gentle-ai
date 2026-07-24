package workprovider

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strconv"
	"sync"
	"testing"

	"github.com/gentleman-programming/gentle-ai/internal/deliveryadmission"
	"github.com/gentleman-programming/gentle-ai/internal/model"
)

type runtimePolicyTestFixture struct {
	root        string
	authority   *PADRepositoryAuthority
	provisioner *ProductivePolicyProvisioner
}

type runtimePolicyTestLiveState struct {
	policy   deliveryadmission.RoutePolicy
	revision string
}

func TestProductivePolicySnapshotRequiresCompleteCanonicalPADRoutes(
	t *testing.T,
) {
	repositoryRef := runtimeConnectorTestRef("policy-snapshot-repository")
	policies := runtimePolicyTestPolicies(t, 11, false)
	reversed := append([]deliveryadmission.RoutePolicy(nil), policies...)
	for left, right := 0, len(reversed)-1; left < right; left, right = left+1, right-1 {
		reversed[left], reversed[right] = reversed[right], reversed[left]
	}
	snapshot, err := NewProductivePolicySnapshot(
		repositoryRef,
		model.AgentCodex,
		runtimeConnectorTestSession,
		11,
		reversed,
	)
	if err != nil {
		t.Fatal(err)
	}
	for index, route := range productivePolicyRoutesV1 {
		if snapshot.Policies[index].Route != route ||
			snapshot.Policies[index].Revision != "snapshot:11" {
			t.Fatalf("canonical policy[%d] = %#v", index, snapshot.Policies[index])
		}
	}

	tests := []struct {
		name     string
		revision uint64
		policies []deliveryadmission.RoutePolicy
	}{
		{
			name:     "missing route",
			revision: 11,
			policies: policies[:3],
		},
		{
			name:     "duplicate route",
			revision: 11,
			policies: []deliveryadmission.RoutePolicy{
				policies[0],
				policies[1],
				policies[2],
				policies[2],
			},
		},
		{
			name:     "policy revision differs from snapshot",
			revision: 12,
			policies: policies,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if _, err := NewProductivePolicySnapshot(
				repositoryRef,
				model.AgentCodex,
				runtimeConnectorTestSession,
				test.revision,
				test.policies,
			); !errors.Is(err, ErrProductiveRuntimeBindingMismatch) {
				t.Fatalf("invalid snapshot error = %v", err)
			}
		})
	}
}

func TestProductivePolicyProvisionerInitialUpgradeAndReplay(t *testing.T) {
	fixture := newRuntimePolicyTestFixture(t)
	first := runtimePolicyTestSnapshot(
		t,
		fixture.authority.RepositoryRef(),
		1,
		false,
	)
	if fixture.provisioner.authority != fixture.authority {
		t.Fatal("policy provisioner did not retain the exact PAD authority pointer")
	}
	if err := fixture.provisioner.Provision(
		context.Background(),
		first,
	); err != nil {
		t.Fatal(err)
	}
	firstState := runtimePolicyTestReadLiveState(t, fixture.authority)
	runtimePolicyTestRequireSnapshot(t, firstState, first)
	if _, err := os.Lstat(fixture.provisioner.lockPath); err != nil {
		t.Fatalf("durable owner lock path: %v", err)
	}

	if err := fixture.provisioner.Provision(
		context.Background(),
		first,
	); err != nil {
		t.Fatalf("exact replay: %v", err)
	}
	replayed := runtimePolicyTestReadLiveState(t, fixture.authority)
	if !reflect.DeepEqual(firstState, replayed) {
		t.Fatalf("exact replay rotated live indexes: %#v != %#v", replayed, firstState)
	}
	restarted, err := NewProductivePolicySnapshot(
		fixture.authority.RepositoryRef(),
		model.AgentCodex,
		"session:restarted-runtime",
		first.Revision,
		first.Policies,
	)
	if err != nil {
		t.Fatal(err)
	}
	if restarted.SnapshotRef == first.SnapshotRef {
		t.Fatal("connector session was not bound into the transport snapshot")
	}
	if err := fixture.provisioner.Provision(
		context.Background(),
		restarted,
	); err != nil {
		t.Fatalf("cross-process policy replay: %v", err)
	}
	restartedState := runtimePolicyTestReadLiveState(t, fixture.authority)
	if !reflect.DeepEqual(firstState, restartedState) {
		t.Fatal("cross-process policy replay rotated live indexes")
	}

	second := runtimePolicyTestSnapshot(
		t,
		fixture.authority.RepositoryRef(),
		2,
		false,
	)
	if err := fixture.provisioner.Provision(
		context.Background(),
		second,
	); err != nil {
		t.Fatalf("upgrade: %v", err)
	}
	secondState := runtimePolicyTestReadLiveState(t, fixture.authority)
	runtimePolicyTestRequireSnapshot(t, secondState, second)
	for _, route := range productivePolicyRoutesV1 {
		if secondState[route].revision == firstState[route].revision {
			t.Fatalf("upgrade did not rotate %s live index", route)
		}
	}
}

func TestProductivePolicyProvisionerRejectsDowngradeAndEqualRevisionFork(
	t *testing.T,
) {
	fixture := newRuntimePolicyTestFixture(t)
	second := runtimePolicyTestSnapshot(
		t,
		fixture.authority.RepositoryRef(),
		2,
		false,
	)
	if err := fixture.provisioner.Provision(
		context.Background(),
		second,
	); err != nil {
		t.Fatal(err)
	}
	before := runtimePolicyTestReadLiveState(t, fixture.authority)

	first := runtimePolicyTestSnapshot(
		t,
		fixture.authority.RepositoryRef(),
		1,
		false,
	)
	if err := fixture.provisioner.Provision(
		context.Background(),
		first,
	); !errors.Is(err, ErrProductivePolicySnapshotDowngrade) {
		t.Fatalf("downgrade error = %v", err)
	}
	afterDowngrade := runtimePolicyTestReadLiveState(t, fixture.authority)
	if !reflect.DeepEqual(before, afterDowngrade) {
		t.Fatal("rejected downgrade changed live PAD authority")
	}

	fork := runtimePolicyTestSnapshot(
		t,
		fixture.authority.RepositoryRef(),
		2,
		true,
	)
	if err := fixture.provisioner.Provision(
		context.Background(),
		fork,
	); !errors.Is(err, ErrProductivePolicySnapshotFork) {
		t.Fatalf("equal-revision fork error = %v", err)
	}
	afterFork := runtimePolicyTestReadLiveState(t, fixture.authority)
	if !reflect.DeepEqual(before, afterFork) {
		t.Fatal("rejected equal-revision fork changed live PAD authority")
	}
}

func TestProductivePolicyProvisionerComparesAllSlotsBeforeRotating(
	t *testing.T,
) {
	fixture := newRuntimePolicyTestFixture(t)
	second := runtimePolicyTestSnapshot(
		t,
		fixture.authority.RepositoryRef(),
		2,
		false,
	)
	if err := fixture.provisioner.Provision(
		context.Background(),
		second,
	); err != nil {
		t.Fatal(err)
	}
	before := runtimePolicyTestReadLiveState(t, fixture.authority)
	desiredThird := runtimePolicyTestSnapshot(
		t,
		fixture.authority.RepositoryRef(),
		3,
		false,
	)
	forkedThird := runtimePolicyTestSnapshot(
		t,
		fixture.authority.RepositoryRef(),
		3,
		true,
	)
	forkIndex := productivePolicyRouteIndex(deliveryadmission.RouteDirectMain)
	repository := runtimePolicyTestOpenRepository(t, fixture.authority)
	forkPolicy := forkedThird.Policies[forkIndex]
	forkRef, err := repository.PublishRoutePolicy(
		context.Background(),
		forkPolicy,
	)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := repository.PutLiveRoutePolicy(
		context.Background(),
		deliveryadmission.LiveRoutePolicyUpdate{
			Route:            forkPolicy.Route,
			PolicyRef:        forkRef,
			ExpectedRevision: before[forkPolicy.Route].revision,
		},
	); err != nil {
		t.Fatal(err)
	}
	if err := repository.Close(); err != nil {
		t.Fatal(err)
	}

	if err := fixture.provisioner.Provision(
		context.Background(),
		desiredThird,
	); !errors.Is(err, ErrProductivePolicySnapshotFork) {
		t.Fatalf("partial fork error = %v", err)
	}
	repository = runtimePolicyTestOpenRepository(t, fixture.authority)
	unpublishedRef, err := desiredThird.Policies[0].Ref()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := repository.ResolveRoutePolicy(
		context.Background(),
		unpublishedRef,
	); !errors.Is(err, deliveryadmission.ErrTrustedRepositoryUnavailable) {
		t.Fatalf("comparison published immutable policy before fork: %v", err)
	}
	if err := repository.Close(); err != nil {
		t.Fatal(err)
	}
	after := runtimePolicyTestReadLiveState(t, fixture.authority)
	for _, route := range productivePolicyRoutesV1 {
		if route == forkPolicy.Route {
			if after[route].policy != forkPolicy {
				t.Fatalf("fork route %s unexpectedly changed", route)
			}
			continue
		}
		if after[route] != before[route] {
			t.Fatalf("route %s rotated before later fork was detected", route)
		}
	}
}

func TestProductivePolicyProvisionerCompletesPartialReplayConcurrently(
	t *testing.T,
) {
	fixture := newRuntimePolicyTestFixture(t)
	snapshot := runtimePolicyTestSnapshot(
		t,
		fixture.authority.RepositoryRef(),
		7,
		false,
	)
	if err := fixture.provisioner.ensureDirectories(
		context.Background(),
	); err != nil {
		t.Fatal(err)
	}
	if err := fixture.provisioner.withOwnerLock(
		context.Background(),
		func() (resultErr error) {
			if err := fixture.provisioner.publishSnapshotIntent(
				context.Background(),
				snapshot,
			); err != nil {
				return err
			}
			repository := runtimePolicyTestOpenRepository(
				t,
				fixture.authority,
			)
			defer func() {
				resultErr = errors.Join(resultErr, repository.Close())
			}()
			for index := 0; index < 2; index++ {
				policy := snapshot.Policies[index]
				policyRef, err := repository.PublishRoutePolicy(
					context.Background(),
					policy,
				)
				if err != nil {
					return err
				}
				if _, err := repository.PutLiveRoutePolicy(
					context.Background(),
					deliveryadmission.LiveRoutePolicyUpdate{
						Route:     policy.Route,
						PolicyRef: policyRef,
					},
				); err != nil {
					return err
				}
			}
			return nil
		},
	); err != nil {
		t.Fatal(err)
	}

	fork := runtimePolicyTestSnapshot(
		t,
		fixture.authority.RepositoryRef(),
		7,
		true,
	)
	if err := fixture.provisioner.Provision(
		context.Background(),
		fork,
	); !errors.Is(err, ErrProductivePolicySnapshotFork) {
		t.Fatalf("partial same-revision fork error = %v", err)
	}

	const workers = 4
	var wait sync.WaitGroup
	failures := make(chan error, workers)
	for range workers {
		wait.Add(1)
		go func() {
			defer wait.Done()
			if err := fixture.provisioner.Provision(
				context.Background(),
				snapshot,
			); err != nil {
				failures <- err
			}
		}()
	}
	wait.Wait()
	close(failures)
	for err := range failures {
		t.Errorf("concurrent partial replay: %v", err)
	}
	if t.Failed() {
		t.FailNow()
	}
	state := runtimePolicyTestReadLiveState(t, fixture.authority)
	runtimePolicyTestRequireSnapshot(t, state, snapshot)
}

func TestProductivePolicyProvisionerRecoversFromOrphanedPublishTemps(
	t *testing.T,
) {
	const orphanName = ".coordination-0123456789abcdef0123456789abcdef"
	t.Run("before first intent publish", func(t *testing.T) {
		fixture := newRuntimePolicyTestFixture(t)
		if err := fixture.provisioner.ensureDirectories(
			context.Background(),
		); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(
			filepath.Join(
				fixture.provisioner.snapshotDirectory(),
				orphanName,
			),
			nil,
			0o600,
		); err != nil {
			t.Fatal(err)
		}
		snapshot := runtimePolicyTestSnapshot(
			t,
			fixture.authority.RepositoryRef(),
			1,
			false,
		)
		if err := fixture.provisioner.Provision(
			context.Background(),
			snapshot,
		); err != nil {
			t.Fatalf("recover before intent publish: %v", err)
		}
		runtimePolicyTestRequireSnapshot(
			t,
			runtimePolicyTestReadLiveState(t, fixture.authority),
			snapshot,
		)
	})

	t.Run("alongside published intent", func(t *testing.T) {
		fixture := newRuntimePolicyTestFixture(t)
		snapshot := runtimePolicyTestSnapshot(
			t,
			fixture.authority.RepositoryRef(),
			1,
			false,
		)
		if err := fixture.provisioner.Provision(
			context.Background(),
			snapshot,
		); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(
			filepath.Join(
				fixture.provisioner.snapshotDirectory(),
				orphanName,
			),
			[]byte("partially-written-intent"),
			0o600,
		); err != nil {
			t.Fatal(err)
		}
		if err := fixture.provisioner.Provision(
			context.Background(),
			snapshot,
		); err != nil {
			t.Fatalf("recover alongside intent: %v", err)
		}
	})
}

func TestProductivePolicyProvisionerRejectsUnsafeSnapshotDirectoryEntries(
	t *testing.T,
) {
	for _, test := range []struct {
		name   string
		entry  string
		create func(string) error
	}{
		{
			name:  "malformed temporary name",
			entry: ".coordination-not-an-internal-nonce",
			create: func(path string) error {
				return os.WriteFile(path, nil, 0o600)
			},
		},
		{
			name:  "temporary directory",
			entry: ".coordination-0123456789abcdef0123456789abcdef",
			create: func(path string) error {
				return os.Mkdir(path, 0o700)
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			fixture := newRuntimePolicyTestFixture(t)
			if err := fixture.provisioner.ensureDirectories(
				context.Background(),
			); err != nil {
				t.Fatal(err)
			}
			if err := test.create(filepath.Join(
				fixture.provisioner.snapshotDirectory(),
				test.entry,
			)); err != nil {
				t.Fatal(err)
			}
			snapshot := runtimePolicyTestSnapshot(
				t,
				fixture.authority.RepositoryRef(),
				1,
				false,
			)
			if err := fixture.provisioner.Provision(
				context.Background(),
				snapshot,
			); !errors.Is(err, ErrProductivePolicyProvisionerCorrupt) {
				t.Fatalf("unsafe snapshot entry error = %v", err)
			}
		})
	}
}

func TestProductivePolicyProvisionerRejectsCrossRepositorySnapshot(
	t *testing.T,
) {
	fixture := newRuntimePolicyTestFixture(t)
	foreign := newRuntimePolicyTestFixture(t)
	snapshot := runtimePolicyTestSnapshot(
		t,
		foreign.authority.RepositoryRef(),
		1,
		false,
	)
	if err := fixture.provisioner.Provision(
		context.Background(),
		snapshot,
	); !errors.Is(err, ErrPADRepositoryAuthorityMismatch) {
		t.Fatalf("cross-repository snapshot error = %v", err)
	}
	repository := runtimePolicyTestOpenRepository(t, fixture.authority)
	for _, route := range productivePolicyRoutesV1 {
		if _, _, err := repository.ResolveLiveRoutePolicyRevision(
			context.Background(),
			fixture.authority.RepositoryRef(),
			route,
		); !missingProductivePolicyLiveSlot(err) {
			t.Fatalf("cross-repository snapshot populated %s: %v", route, err)
		}
	}
	if err := repository.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := NewProductivePolicyProvisioner(
		context.Background(),
		nil,
	); !errors.Is(err, ErrProductivePolicyProvisionerUnavailable) {
		t.Fatalf("nil authority error = %v", err)
	}
}

func newRuntimePolicyTestFixture(t *testing.T) runtimePolicyTestFixture {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is unavailable")
	}
	root := filepath.Join(t.TempDir(), "repository")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	runtimePolicyTestGit(t, root, "init", "--quiet")
	runtimePolicyTestGit(t, root, "config", "user.name", "Runtime Policy Test")
	runtimePolicyTestGit(
		t,
		root,
		"config",
		"user.email",
		"runtime-policy@example.test",
	)
	if err := os.WriteFile(
		filepath.Join(root, "README.md"),
		[]byte("# runtime policy\n"),
		0o644,
	); err != nil {
		t.Fatal(err)
	}
	runtimePolicyTestGit(t, root, "add", "--", "README.md")
	runtimePolicyTestGit(t, root, "commit", "--quiet", "-m", "initial")
	authority, err := NewPADRepositoryAuthority(context.Background(), root)
	if err != nil {
		t.Fatal(err)
	}
	provisioner, err := NewProductivePolicyProvisioner(
		context.Background(),
		authority,
	)
	if err != nil {
		t.Fatal(err)
	}
	return runtimePolicyTestFixture{
		root:        root,
		authority:   authority,
		provisioner: provisioner,
	}
}

func runtimePolicyTestSnapshot(
	t *testing.T,
	repositoryRef string,
	revision uint64,
	fork bool,
) ProductivePolicySnapshot {
	t.Helper()
	snapshot, err := NewProductivePolicySnapshot(
		repositoryRef,
		model.AgentCodex,
		runtimeConnectorTestSession,
		revision,
		runtimePolicyTestPolicies(t, revision, fork),
	)
	if err != nil {
		t.Fatal(err)
	}
	return snapshot
}

func runtimePolicyTestPolicies(
	t *testing.T,
	revision uint64,
	fork bool,
) []deliveryadmission.RoutePolicy {
	t.Helper()
	policies := make(
		[]deliveryadmission.RoutePolicy,
		0,
		len(productivePolicyRoutesV1),
	)
	for _, route := range productivePolicyRoutesV1 {
		ttl := int64(120)
		if fork && route == deliveryadmission.RouteDirectMain {
			ttl++
		}
		policy, err := deliveryadmission.NewRoutePolicy(
			"policy:"+string(route),
			"snapshot:"+strconv.FormatUint(revision, 10),
			route,
			true,
			false,
			false,
			ttl,
			0,
		)
		if err != nil {
			t.Fatal(err)
		}
		policies = append(policies, policy)
	}
	return policies
}

func runtimePolicyTestOpenRepository(
	t *testing.T,
	authority *PADRepositoryAuthority,
) *deliveryadmission.TrustedRepository {
	t.Helper()
	repository, err := deliveryadmission.OpenTrustedRepository(
		context.Background(),
		authority,
		authority.RepositoryRef(),
	)
	if err != nil {
		t.Fatal(err)
	}
	return repository
}

func runtimePolicyTestReadLiveState(
	t *testing.T,
	authority *PADRepositoryAuthority,
) map[deliveryadmission.Route]runtimePolicyTestLiveState {
	t.Helper()
	repository := runtimePolicyTestOpenRepository(t, authority)
	defer func() {
		if err := repository.Close(); err != nil {
			t.Fatal(err)
		}
	}()
	state := make(
		map[deliveryadmission.Route]runtimePolicyTestLiveState,
		len(productivePolicyRoutesV1),
	)
	for _, route := range productivePolicyRoutesV1 {
		policy, revision, err := repository.ResolveLiveRoutePolicyRevision(
			context.Background(),
			authority.RepositoryRef(),
			route,
		)
		if err != nil {
			t.Fatal(err)
		}
		state[route] = runtimePolicyTestLiveState{
			policy:   policy,
			revision: revision,
		}
	}
	return state
}

func runtimePolicyTestRequireSnapshot(
	t *testing.T,
	state map[deliveryadmission.Route]runtimePolicyTestLiveState,
	snapshot ProductivePolicySnapshot,
) {
	t.Helper()
	if len(state) != len(productivePolicyRoutesV1) {
		t.Fatalf("live state has %d routes", len(state))
	}
	for index, route := range productivePolicyRoutesV1 {
		resolved, ok := state[route]
		if !ok || resolved.policy != snapshot.Policies[index] ||
			!validPADImmutableRef(resolved.revision) {
			t.Fatalf("live route %s = %#v", route, resolved)
		}
	}
}

func runtimePolicyTestGit(t *testing.T, root string, args ...string) string {
	t.Helper()
	command := exec.Command("git", append([]string{"-C", root}, args...)...)
	command.Env = append(
		os.Environ(),
		"LC_ALL=C",
		"GIT_CONFIG_NOSYSTEM=1",
	)
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, output)
	}
	return string(output)
}
