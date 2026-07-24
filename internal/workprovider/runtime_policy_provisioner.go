package workprovider

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/gentleman-programming/gentle-ai/internal/deliveryadmission"
	"github.com/gentleman-programming/gentle-ai/internal/reviewtransaction"
)

const (
	productivePolicySnapshotIntentSchema   = "gentle-ai.productive-policy-snapshot-intent/v1"
	productivePolicySetDigestSchema        = "gentle-ai.productive-policy-set-digest/v1"
	productivePolicyProvisionerVersion     = "v1"
	productivePolicyProvisionerLockTimeout = 2 * time.Second
	productivePolicyProvisionerLockPoll    = 10 * time.Millisecond
)

var (
	ErrProductivePolicyProvisionerUnavailable = errors.New(
		"productive policy provisioner is unavailable",
	)
	ErrProductivePolicySnapshotDowngrade = errors.New(
		"productive policy snapshot would downgrade live authority",
	)
	ErrProductivePolicySnapshotFork = errors.New(
		"productive policy snapshot revision conflicts with live authority",
	)
	ErrProductivePolicyProvisionerCorrupt = errors.New(
		"productive policy provisioner observed corrupt live authority",
	)
)

// ProductivePolicyProvisioner installs a complete connector snapshot into
// PAD's immutable policy objects and four mutable live route slots. It retains
// the exact PADRepositoryAuthority pointer used by productive composition and
// accepts no alternate repository mapping or storage root.
type ProductivePolicyProvisioner struct {
	authority *PADRepositoryAuthority
	root      string
	lockPath  string
	open      func(
		context.Context,
		*PADRepositoryAuthority,
		string,
	) (*deliveryadmission.TrustedRepository, error)
}

type productivePolicyLiveSlot struct {
	route            deliveryadmission.Route
	desired          deliveryadmission.RoutePolicy
	desiredRef       string
	current          deliveryadmission.RoutePolicy
	currentRef       string
	currentRevision  uint64
	expectedRevision string
	exists           bool
}

type productivePolicySnapshotIntent struct {
	Schema        string                          `json:"schema"`
	RepositoryRef string                          `json:"repositoryRef"`
	Revision      uint64                          `json:"revision"`
	Policies      []deliveryadmission.RoutePolicy `json:"policies"`
	PolicySetRef  string                          `json:"policySetRef"`
}

func (intent productivePolicySnapshotIntent) Validate(
	repositoryRef string,
) error {
	if intent.Schema != productivePolicySnapshotIntentSchema ||
		intent.RepositoryRef != repositoryRef {
		return ErrProductivePolicyProvisionerCorrupt
	}
	if err := validateProductivePolicySet(
		intent.Revision,
		intent.Policies,
	); err != nil {
		return fmt.Errorf(
			"%w: invalid durable snapshot intent: %v",
			ErrProductivePolicyProvisionerCorrupt,
			err,
		)
	}
	want, err := productivePolicySetRef(
		intent.RepositoryRef,
		intent.Revision,
		intent.Policies,
	)
	if err != nil || intent.PolicySetRef != want {
		return fmt.Errorf(
			"%w: durable snapshot intent policy-set binding",
			ErrProductivePolicyProvisionerCorrupt,
		)
	}
	return nil
}

type productivePolicySetProjection struct {
	Schema        string                          `json:"schema"`
	RepositoryRef string                          `json:"repositoryRef"`
	Revision      uint64                          `json:"revision"`
	Policies      []deliveryadmission.RoutePolicy `json:"policies"`
}

// NewProductivePolicyProvisioner is read-only. Durable directories and the
// owner lock are created only when Provision is asked to mutate PAD.
func NewProductivePolicyProvisioner(
	ctx context.Context,
	authority *PADRepositoryAuthority,
) (*ProductivePolicyProvisioner, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if authority == nil ||
		authority.identity.lease == nil ||
		!validPADImmutableRef(authority.RepositoryRef()) {
		return nil, ErrProductivePolicyProvisionerUnavailable
	}
	root, err := authority.ResolveDeliveryRepository(
		ctx,
		authority.RepositoryRef(),
	)
	if err != nil {
		return nil, fmt.Errorf(
			"%w: resolve PAD repository authority: %v",
			ErrProductivePolicyProvisionerUnavailable,
			err,
		)
	}
	if root != authority.identity.repositoryRoot {
		return nil, ErrPADRepositoryAuthorityMismatch
	}
	provisionerRoot := filepath.Join(
		authority.identity.gitCommonDir,
		"gentle-ai",
		"work-provider",
		"runtime-policy-provisioner",
		productivePolicyProvisionerVersion,
		"repositories",
		authority.identity.lease.StorageKey(),
	)
	provisioner := &ProductivePolicyProvisioner{
		authority: authority,
		root:      provisionerRoot,
		lockPath: filepath.Join(
			provisionerRoot,
			"locks",
			"OWNER.lock",
		),
		open: func(
			ctx context.Context,
			owner *PADRepositoryAuthority,
			repositoryRef string,
		) (*deliveryadmission.TrustedRepository, error) {
			return deliveryadmission.OpenTrustedRepository(
				ctx,
				owner,
				repositoryRef,
			)
		},
	}
	if err := provisioner.validateAuthority(ctx); err != nil {
		return nil, err
	}
	return provisioner, nil
}

// Provision installs one complete snapshot. All four live slots are observed
// and classified before any immutable object or mutable index is written.
// Immutable objects are published first; each live slot then rotates with the
// exact revision observed under the cross-process owner lock. A crash may leave
// a prefix at the new snapshot, but replay of the same snapshot completes it
// idempotently without deleting or inferring any route.
func (provisioner *ProductivePolicyProvisioner) Provision(
	ctx context.Context,
	snapshot ProductivePolicySnapshot,
) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if provisioner == nil {
		return ErrProductivePolicyProvisionerUnavailable
	}
	snapshot.Policies = append(
		[]deliveryadmission.RoutePolicy(nil),
		snapshot.Policies...,
	)
	if err := snapshot.Validate(
		snapshot.RepositoryRef,
		snapshot.AgentID,
		snapshot.ConnectorSessionRef,
	); err != nil {
		return err
	}
	if snapshot.RepositoryRef != provisioner.authority.RepositoryRef() {
		return ErrPADRepositoryAuthorityMismatch
	}
	if err := provisioner.ensureDirectories(ctx); err != nil {
		return err
	}
	return provisioner.withOwnerLock(ctx, func() (resultErr error) {
		if err := provisioner.validate(ctx); err != nil {
			return err
		}
		repository, err := provisioner.open(
			ctx,
			provisioner.authority,
			provisioner.authority.RepositoryRef(),
		)
		if err != nil {
			return fmt.Errorf(
				"%w: open PAD trusted repository: %v",
				ErrProductivePolicyProvisionerUnavailable,
				err,
			)
		}
		defer func() {
			if closeErr := repository.Close(); closeErr != nil {
				resultErr = errors.Join(
					resultErr,
					fmt.Errorf(
						"%w: close PAD trusted repository: %v",
						ErrProductivePolicyProvisionerUnavailable,
						closeErr,
					),
				)
			}
		}()
		intentExists, err := provisioner.compareSnapshotIntents(
			ctx,
			snapshot,
		)
		if err != nil {
			return err
		}
		slots, err := provisioner.compareAllLiveSlots(
			ctx,
			repository,
			snapshot,
		)
		if err != nil {
			return err
		}
		if !intentExists {
			for _, slot := range slots {
				if slot.exists &&
					slot.currentRevision == snapshot.Revision {
					return fmt.Errorf(
						"%w: live route %s has snapshot:%d without its durable full-snapshot intent",
						ErrProductivePolicyProvisionerCorrupt,
						slot.route,
						snapshot.Revision,
					)
				}
			}
			if err := provisioner.publishSnapshotIntent(
				ctx,
				snapshot,
			); err != nil {
				return err
			}
		}
		if err := provisioner.publishAllImmutablePolicies(
			ctx,
			repository,
			slots,
		); err != nil {
			return err
		}
		if err := provisioner.rotateLiveSlots(
			ctx,
			repository,
			slots,
		); err != nil {
			return err
		}
		return provisioner.verifyCompleteSnapshot(
			ctx,
			repository,
			slots,
		)
	})
}

func (provisioner *ProductivePolicyProvisioner) compareSnapshotIntents(
	ctx context.Context,
	snapshot ProductivePolicySnapshot,
) (bool, error) {
	entries, err := os.ReadDir(provisioner.snapshotDirectory())
	if err != nil {
		return false, fmt.Errorf(
			"%w: read durable snapshot intents: %v",
			ErrProductivePolicyProvisionerUnavailable,
			err,
		)
	}
	var exact bool
	var highest uint64
	for _, entry := range entries {
		if err := ctx.Err(); err != nil {
			return false, err
		}
		if productivePolicyCoordinationTempFilename(entry.Name()) {
			tempPath := filepath.Join(
				provisioner.snapshotDirectory(),
				entry.Name(),
			)
			info, statErr := os.Lstat(tempPath)
			if statErr != nil ||
				!info.Mode().IsRegular() ||
				validatePrivateCoordinationFile(tempPath) != nil {
				return false, fmt.Errorf(
					"%w: unsafe durable snapshot-intent temporary entry %q",
					ErrProductivePolicyProvisionerCorrupt,
					entry.Name(),
				)
			}
			// publishPrivateCoordinationImmutable removes this file during
			// ordinary returns. A SIGKILL can strand it at any write stage,
			// including empty or partially written, so only its exact safe
			// private-file identity is meaningful during recovery.
			continue
		}
		revision, err := parseProductivePolicySnapshotFilename(entry.Name())
		if err != nil || entry.IsDir() {
			return false, fmt.Errorf(
				"%w: unexpected durable snapshot-intent entry %q",
				ErrProductivePolicyProvisionerCorrupt,
				entry.Name(),
			)
		}
		intent, err := provisioner.readSnapshotIntent(revision)
		if err != nil {
			return false, err
		}
		if revision > highest {
			highest = revision
		}
		if revision == snapshot.Revision {
			incomingRef, err := productivePolicySetRef(
				snapshot.RepositoryRef,
				snapshot.Revision,
				snapshot.Policies,
			)
			if err != nil {
				return false, err
			}
			if intent.PolicySetRef != incomingRef {
				return false, fmt.Errorf(
					"%w: durable snapshot:%d already binds another full snapshot",
					ErrProductivePolicySnapshotFork,
					revision,
				)
			}
			exact = true
		}
	}
	if highest > snapshot.Revision {
		return false, fmt.Errorf(
			"%w: durable snapshot intent is at snapshot:%d, requested snapshot:%d",
			ErrProductivePolicySnapshotDowngrade,
			highest,
			snapshot.Revision,
		)
	}
	return exact, nil
}

func (provisioner *ProductivePolicyProvisioner) publishSnapshotIntent(
	ctx context.Context,
	snapshot ProductivePolicySnapshot,
) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	policySetRef, err := productivePolicySetRef(
		snapshot.RepositoryRef,
		snapshot.Revision,
		snapshot.Policies,
	)
	if err != nil {
		return err
	}
	intent := productivePolicySnapshotIntent{
		Schema:        productivePolicySnapshotIntentSchema,
		RepositoryRef: snapshot.RepositoryRef,
		Revision:      snapshot.Revision,
		Policies: append(
			[]deliveryadmission.RoutePolicy(nil),
			snapshot.Policies...,
		),
		PolicySetRef: policySetRef,
	}
	if err := intent.Validate(
		provisioner.authority.RepositoryRef(),
	); err != nil {
		return err
	}
	payload, err := canonicalCoordinationPayload(intent)
	if err != nil {
		return fmt.Errorf(
			"%w: encode durable snapshot intent: %v",
			ErrProductivePolicyProvisionerCorrupt,
			err,
		)
	}
	path := provisioner.snapshotPath(snapshot.Revision)
	if err := publishPrivateCoordinationImmutable(path, payload); err != nil {
		if !errors.Is(err, ErrCoordinationAuthorityConflict) {
			return fmt.Errorf(
				"%w: publish durable snapshot intent: %v",
				ErrProductivePolicyProvisionerUnavailable,
				err,
			)
		}
		existing, readErr := readPrivateCoordinationFile(path)
		if readErr != nil || !bytes.Equal(existing, payload) {
			return fmt.Errorf(
				"%w: durable snapshot:%d intent slot is occupied",
				ErrProductivePolicySnapshotFork,
				snapshot.Revision,
			)
		}
	}
	reloaded, err := provisioner.readSnapshotIntent(snapshot.Revision)
	if err != nil {
		return err
	}
	if reloaded.PolicySetRef != policySetRef {
		return fmt.Errorf(
			"%w: durable snapshot:%d intent changed after publication",
			ErrProductivePolicySnapshotFork,
			snapshot.Revision,
		)
	}
	return nil
}

func (provisioner *ProductivePolicyProvisioner) readSnapshotIntent(
	revision uint64,
) (productivePolicySnapshotIntent, error) {
	payload, err := readPrivateCoordinationFile(
		provisioner.snapshotPath(revision),
	)
	if err != nil {
		return productivePolicySnapshotIntent{}, fmt.Errorf(
			"%w: read durable snapshot:%d intent: %v",
			ErrProductivePolicyProvisionerCorrupt,
			revision,
			err,
		)
	}
	var intent productivePolicySnapshotIntent
	if err := decodeStrictCoordinationJSON(payload, &intent); err != nil {
		return productivePolicySnapshotIntent{}, fmt.Errorf(
			"%w: decode durable snapshot:%d intent: %v",
			ErrProductivePolicyProvisionerCorrupt,
			revision,
			err,
		)
	}
	canonical, err := canonicalCoordinationPayload(intent)
	if err != nil ||
		!bytes.Equal(payload, canonical) ||
		intent.Validate(provisioner.authority.RepositoryRef()) != nil ||
		intent.Revision != revision {
		return productivePolicySnapshotIntent{}, fmt.Errorf(
			"%w: durable snapshot:%d intent is not canonical",
			ErrProductivePolicyProvisionerCorrupt,
			revision,
		)
	}
	return intent, nil
}

func (provisioner *ProductivePolicyProvisioner) compareAllLiveSlots(
	ctx context.Context,
	repository *deliveryadmission.TrustedRepository,
	snapshot ProductivePolicySnapshot,
) ([]productivePolicyLiveSlot, error) {
	slots := make(
		[]productivePolicyLiveSlot,
		len(productivePolicyRoutesV1),
	)
	for index, route := range productivePolicyRoutesV1 {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		desired := snapshot.Policies[index]
		desiredRef, err := desired.Ref()
		if err != nil {
			return nil, fmt.Errorf(
				"%w: derive desired policy ref: %v",
				ErrProductivePolicyProvisionerCorrupt,
				err,
			)
		}
		slot := productivePolicyLiveSlot{
			route:      route,
			desired:    desired,
			desiredRef: desiredRef,
		}
		current, currentIndexRevision, err :=
			repository.ResolveLiveRoutePolicyRevision(
				ctx,
				provisioner.authority.RepositoryRef(),
				route,
			)
		if err != nil {
			if !missingProductivePolicyLiveSlot(err) {
				return nil, fmt.Errorf(
					"%w: observe live route %s: %v",
					ErrProductivePolicyProvisionerUnavailable,
					route,
					err,
				)
			}
			slots[index] = slot
			continue
		}
		currentSnapshotRevision, err := parseProductivePolicyRevision(
			current.Revision,
		)
		if err != nil {
			return nil, fmt.Errorf(
				"%w: route %s: %v",
				ErrProductivePolicyProvisionerCorrupt,
				route,
				err,
			)
		}
		currentRef, err := current.Ref()
		if err != nil {
			return nil, fmt.Errorf(
				"%w: derive current policy ref for %s: %v",
				ErrProductivePolicyProvisionerCorrupt,
				route,
				err,
			)
		}
		slot.current = current
		slot.currentRef = currentRef
		slot.currentRevision = currentSnapshotRevision
		slot.expectedRevision = currentIndexRevision
		slot.exists = true
		switch {
		case currentSnapshotRevision > snapshot.Revision:
			return nil, fmt.Errorf(
				"%w: route %s is at snapshot:%d, requested snapshot:%d",
				ErrProductivePolicySnapshotDowngrade,
				route,
				currentSnapshotRevision,
				snapshot.Revision,
			)
		case currentSnapshotRevision == snapshot.Revision &&
			currentRef != desiredRef:
			return nil, fmt.Errorf(
				"%w: route %s already binds another snapshot:%d policy",
				ErrProductivePolicySnapshotFork,
				route,
				snapshot.Revision,
			)
		}
		slots[index] = slot
	}
	return slots, nil
}

func (provisioner *ProductivePolicyProvisioner) publishAllImmutablePolicies(
	ctx context.Context,
	repository *deliveryadmission.TrustedRepository,
	slots []productivePolicyLiveSlot,
) error {
	if len(slots) != len(productivePolicyRoutesV1) {
		return ErrProductivePolicyProvisionerCorrupt
	}
	for _, slot := range slots {
		publishedRef, err := repository.PublishRoutePolicy(
			ctx,
			slot.desired,
		)
		if err != nil {
			return fmt.Errorf(
				"%w: publish immutable route %s policy: %v",
				ErrProductivePolicyProvisionerUnavailable,
				slot.route,
				err,
			)
		}
		if publishedRef != slot.desiredRef {
			return fmt.Errorf(
				"%w: immutable route %s policy ref changed",
				ErrProductivePolicyProvisionerCorrupt,
				slot.route,
			)
		}
	}
	return nil
}

func (provisioner *ProductivePolicyProvisioner) rotateLiveSlots(
	ctx context.Context,
	repository *deliveryadmission.TrustedRepository,
	slots []productivePolicyLiveSlot,
) error {
	for _, slot := range slots {
		if slot.exists && slot.currentRef == slot.desiredRef {
			continue
		}
		revision, err := repository.PutLiveRoutePolicy(
			ctx,
			deliveryadmission.LiveRoutePolicyUpdate{
				Route:            slot.route,
				PolicyRef:        slot.desiredRef,
				ExpectedRevision: slot.expectedRevision,
			},
		)
		if err != nil {
			return fmt.Errorf(
				"%w: rotate live route %s with exact CAS: %v",
				ErrProductivePolicyProvisionerUnavailable,
				slot.route,
				err,
			)
		}
		if !validPADImmutableRef(revision) {
			return fmt.Errorf(
				"%w: route %s returned an invalid live revision",
				ErrProductivePolicyProvisionerCorrupt,
				slot.route,
			)
		}
	}
	return nil
}

func (provisioner *ProductivePolicyProvisioner) verifyCompleteSnapshot(
	ctx context.Context,
	repository *deliveryadmission.TrustedRepository,
	slots []productivePolicyLiveSlot,
) error {
	for _, slot := range slots {
		policy, revision, err := repository.ResolveLiveRoutePolicyRevision(
			ctx,
			provisioner.authority.RepositoryRef(),
			slot.route,
		)
		if err != nil {
			return fmt.Errorf(
				"%w: verify live route %s: %v",
				ErrProductivePolicyProvisionerUnavailable,
				slot.route,
				err,
			)
		}
		resolvedRef, err := policy.Ref()
		if err != nil ||
			policy != slot.desired ||
			resolvedRef != slot.desiredRef ||
			!validPADImmutableRef(revision) {
			return fmt.Errorf(
				"%w: route %s does not match the complete snapshot",
				ErrProductivePolicyProvisionerCorrupt,
				slot.route,
			)
		}
	}
	return nil
}

func (provisioner *ProductivePolicyProvisioner) withOwnerLock(
	ctx context.Context,
	action func() error,
) (resultErr error) {
	if action == nil {
		return ErrProductivePolicyProvisionerUnavailable
	}
	timer := time.NewTimer(productivePolicyProvisionerLockTimeout)
	defer timer.Stop()
	ticker := time.NewTicker(productivePolicyProvisionerLockPoll)
	defer ticker.Stop()
	for {
		lock, err := reviewtransaction.AcquireAuthorityFileLock(
			provisioner.lockPath,
		)
		if err == nil {
			defer func() {
				if releaseErr := lock.Release(); releaseErr != nil {
					resultErr = errors.Join(
						resultErr,
						fmt.Errorf(
							"%w: release policy owner lock: %v",
							ErrProductivePolicyProvisionerUnavailable,
							releaseErr,
						),
					)
				}
			}()
			if err := action(); err != nil {
				return err
			}
			return provisioner.validate(ctx)
		}
		if !errors.Is(err, reviewtransaction.ErrConcurrentUpdate) {
			return fmt.Errorf(
				"%w: acquire policy owner lock: %v",
				ErrProductivePolicyProvisionerUnavailable,
				err,
			)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-timer.C:
			return fmt.Errorf(
				"%w: policy owner lock timed out",
				ErrProductivePolicyProvisionerUnavailable,
			)
		case <-ticker.C:
		}
	}
}

func (provisioner *ProductivePolicyProvisioner) validateAuthority(
	ctx context.Context,
) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if provisioner == nil ||
		provisioner.authority == nil ||
		provisioner.authority.identity.lease == nil ||
		provisioner.open == nil {
		return ErrProductivePolicyProvisionerUnavailable
	}
	root, err := provisioner.authority.ResolveDeliveryRepository(
		ctx,
		provisioner.authority.RepositoryRef(),
	)
	if err != nil {
		return fmt.Errorf(
			"%w: revalidate PAD repository authority: %v",
			ErrProductivePolicyProvisionerUnavailable,
			err,
		)
	}
	if root != provisioner.authority.identity.repositoryRoot {
		return ErrPADRepositoryAuthorityMismatch
	}
	expectedRoot := filepath.Join(
		provisioner.authority.identity.gitCommonDir,
		"gentle-ai",
		"work-provider",
		"runtime-policy-provisioner",
		productivePolicyProvisionerVersion,
		"repositories",
		provisioner.authority.identity.lease.StorageKey(),
	)
	if provisioner.root != expectedRoot ||
		provisioner.lockPath != filepath.Join(
			expectedRoot,
			"locks",
			"OWNER.lock",
		) {
		return ErrPADRepositoryAuthorityMismatch
	}
	return validateSharedCoordinationDirectory(
		provisioner.authority.identity.gitCommonDir,
	)
}

func (provisioner *ProductivePolicyProvisioner) validate(
	ctx context.Context,
) error {
	if err := provisioner.validateAuthority(ctx); err != nil {
		return err
	}
	for _, directory := range []string{
		provisioner.root,
		filepath.Join(provisioner.root, "locks"),
		provisioner.snapshotDirectory(),
	} {
		if err := validatePrivateCoordinationDirectory(directory); err != nil {
			return fmt.Errorf(
				"%w: validate policy provisioner directory: %v",
				ErrProductivePolicyProvisionerUnavailable,
				err,
			)
		}
	}
	return nil
}

func (provisioner *ProductivePolicyProvisioner) ensureDirectories(
	ctx context.Context,
) error {
	if err := provisioner.validateAuthority(ctx); err != nil {
		return err
	}
	commonDir := provisioner.authority.identity.gitCommonDir
	gentleRoot := filepath.Join(commonDir, "gentle-ai")
	if _, err := os.Lstat(gentleRoot); errors.Is(err, fs.ErrNotExist) {
		if err := os.Mkdir(gentleRoot, 0o755); err != nil &&
			!errors.Is(err, fs.ErrExist) {
			return fmt.Errorf(
				"%w: create shared gentle-ai directory: %v",
				ErrProductivePolicyProvisionerUnavailable,
				err,
			)
		}
		if err := reviewtransaction.SyncReviewDirectory(commonDir); err != nil {
			return fmt.Errorf(
				"%w: sync shared gentle-ai directory: %v",
				ErrProductivePolicyProvisionerUnavailable,
				err,
			)
		}
	} else if err != nil {
		return fmt.Errorf(
			"%w: inspect shared gentle-ai directory: %v",
			ErrProductivePolicyProvisionerUnavailable,
			err,
		)
	}
	if err := validateSharedCoordinationDirectory(gentleRoot); err != nil {
		return fmt.Errorf(
			"%w: validate shared gentle-ai directory: %v",
			ErrProductivePolicyProvisionerUnavailable,
			err,
		)
	}
	workProviderRoot := filepath.Join(gentleRoot, "work-provider")
	created, err := createPrivateCoordinationDirectory(workProviderRoot)
	if err != nil {
		return fmt.Errorf(
			"%w: create work-provider authority directory: %v",
			ErrProductivePolicyProvisionerUnavailable,
			err,
		)
	}
	if created {
		if err := reviewtransaction.SyncReviewDirectory(gentleRoot); err != nil {
			return fmt.Errorf(
				"%w: sync work-provider authority directory: %v",
				ErrProductivePolicyProvisionerUnavailable,
				err,
			)
		}
	}
	for _, directory := range []string{
		provisioner.root,
		filepath.Join(provisioner.root, "locks"),
		provisioner.snapshotDirectory(),
	} {
		if err := ensurePrivateCoordinationDirectoryTree(
			workProviderRoot,
			directory,
			true,
		); err != nil {
			return fmt.Errorf(
				"%w: create policy provisioner directory: %v",
				ErrProductivePolicyProvisionerUnavailable,
				err,
			)
		}
	}
	return provisioner.validate(ctx)
}

func (provisioner *ProductivePolicyProvisioner) snapshotDirectory() string {
	if provisioner == nil {
		return ""
	}
	return filepath.Join(provisioner.root, "snapshots")
}

func (provisioner *ProductivePolicyProvisioner) snapshotPath(
	revision uint64,
) string {
	return filepath.Join(
		provisioner.snapshotDirectory(),
		strconv.FormatUint(revision, 10)+".json",
	)
}

func productivePolicySetRef(
	repositoryRef string,
	revision uint64,
	policies []deliveryadmission.RoutePolicy,
) (string, error) {
	if err := validateProductivePolicySet(revision, policies); err != nil {
		return "", err
	}
	return productiveRuntimeDigest(productivePolicySetProjection{
		Schema:        productivePolicySetDigestSchema,
		RepositoryRef: repositoryRef,
		Revision:      revision,
		Policies: append(
			[]deliveryadmission.RoutePolicy(nil),
			policies...,
		),
	})
}

func parseProductivePolicySnapshotFilename(name string) (uint64, error) {
	if filepath.Base(name) != name ||
		!strings.HasSuffix(name, ".json") {
		return 0, errors.New("invalid snapshot-intent filename")
	}
	value := strings.TrimSuffix(name, ".json")
	revision, err := strconv.ParseUint(value, 10, 64)
	if err != nil ||
		revision == 0 ||
		strconv.FormatUint(revision, 10) != value {
		return 0, errors.New("invalid snapshot-intent revision")
	}
	return revision, nil
}

func productivePolicyCoordinationTempFilename(name string) bool {
	const prefix = ".coordination-"
	if filepath.Base(name) != name ||
		!strings.HasPrefix(name, prefix) ||
		len(name) != len(prefix)+32 {
		return false
	}
	for _, character := range strings.TrimPrefix(name, prefix) {
		if (character < '0' || character > '9') &&
			(character < 'a' || character > 'f') {
			return false
		}
	}
	return true
}

func parseProductivePolicyRevision(revision string) (uint64, error) {
	const prefix = "snapshot:"
	if !strings.HasPrefix(revision, prefix) {
		return 0, errors.New(
			"live route policy revision is not a snapshot revision",
		)
	}
	value := strings.TrimPrefix(revision, prefix)
	parsed, err := strconv.ParseUint(value, 10, 64)
	if err != nil ||
		parsed == 0 ||
		strconv.FormatUint(parsed, 10) != value {
		return 0, errors.New(
			"live route policy snapshot revision is not canonical",
		)
	}
	return parsed, nil
}

func missingProductivePolicyLiveSlot(err error) bool {
	// TrustedRepository intentionally exposes one unavailable sentinel for a
	// missing live record and provider I/O failures. Only its exact missing
	// record diagnostic is safe to treat as an empty CAS base; every other
	// unavailable/corrupt condition fails closed.
	return errors.Is(err, deliveryadmission.ErrTrustedRepositoryUnavailable) &&
		strings.Contains(err.Error(), ": missing ") &&
		strings.HasSuffix(err.Error(), ".live-route-policy.json")
}
