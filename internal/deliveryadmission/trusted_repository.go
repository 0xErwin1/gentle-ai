package deliveryadmission

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/gentleman-programming/gentle-ai/internal/reviewtransaction"
)

const (
	trustedRepositoryBindingSchema = "gentle-ai.delivery-trusted-repository/v1"
	liveRoutePolicyIndexSchema     = "gentle-ai.delivery-live-route-policy/v1"
	admittedIntentIndexSchema      = "gentle-ai.delivery-admitted-intent-index/v1"
	authorizationKindIndexSchema   = "gentle-ai.delivery-authorization-kind-index/v1"
)

var (
	ErrTrustedRepositoryConflict    = errors.New("PAD trusted repository publication conflicts")
	ErrTrustedRepositoryCorrupt     = errors.New("PAD trusted repository is corrupt")
	ErrTrustedRepositoryUnavailable = errors.New("PAD trusted repository is unavailable")
	ErrLiveAuthorizationInactive    = errors.New("PAD delivery authorization is inactive")
	ErrLiveAuthorizationExpired     = errors.New("PAD delivery authorization expired")
	ErrLiveAuthorizationConsumed    = errors.New("PAD delivery authorization consumed")
	ErrAdmittedDecisionNotFound     = errors.New("PAD admitted decision was not found")
)

type LiveAuthorizationInactiveReason string

const (
	LiveAuthorizationNotYetValid LiveAuthorizationInactiveReason = "not_yet_valid"
	LiveAuthorizationExpired     LiveAuthorizationInactiveReason = "expired"
	LiveAuthorizationConsumed    LiveAuthorizationInactiveReason = "consumed"
)

// LiveAuthorizationInactiveError is reserved for expected loss of liveness.
// Invalid/corrupt immutable facts and provider outages never use this type.
type LiveAuthorizationInactiveError struct {
	AuthorizationRef string
	Reason           LiveAuthorizationInactiveReason
}

func (err *LiveAuthorizationInactiveError) Error() string {
	if err == nil {
		return ErrLiveAuthorizationInactive.Error()
	}
	return fmt.Sprintf(
		"%v: %s: %s",
		ErrLiveAuthorizationInactive,
		err.AuthorizationRef,
		err.Reason,
	)
}

func (err *LiveAuthorizationInactiveError) Is(target error) bool {
	if target == ErrLiveAuthorizationInactive {
		return true
	}
	switch err.Reason {
	case LiveAuthorizationExpired:
		return target == ErrLiveAuthorizationExpired || target == ErrExpired
	case LiveAuthorizationConsumed:
		return target == ErrLiveAuthorizationConsumed || target == ErrAlreadyConsumed
	default:
		return false
	}
}

type OwnerClock interface {
	Now() time.Time
}

type systemOwnerClock struct{}

func (systemOwnerClock) Now() time.Time { return time.Now() }

type TrustedRepositoryOption func(*trustedRepositoryOptions) error

type trustedRepositoryOptions struct {
	clock OwnerClock
}

// WithTrustedRepositoryClock is intended for deterministic owner-side tests.
// Production callers omit it and use the provider process clock.
func WithTrustedRepositoryClock(clock OwnerClock) TrustedRepositoryOption {
	return func(options *trustedRepositoryOptions) error {
		if clock == nil {
			return fmt.Errorf("%w: nil owner clock", ErrInvalid)
		}
		options.clock = clock
		return nil
	}
}

// DeliveryRepositoryAuthority is the narrow owner control-plane dependency
// used to bind a repositoryRef to one exact Git repository root.
type DeliveryRepositoryAuthority interface {
	ResolveDeliveryRepository(context.Context, string) (string, error)
}

// TrustedRepository stores PAD preimages and live indexes below one exact Git
// common-dir. It implements TrustedResolver and never accepts a storage root on
// publication or resolution methods.
type TrustedRepository struct {
	repositoryRef  string
	repositoryRoot string
	gitCommonDir   string
	gitDir         string
	storageKey     string
	identityLease  *reviewtransaction.RepositoryIdentityLease
	owner          DeliveryRepositoryAuthority
	clock          OwnerClock
	rar            RARAuthorityPort
	directory      *secureUseDirectory
	closeMu        sync.RWMutex
	closed         bool
}

type trustedRepositoryBinding struct {
	Schema         string `json:"schema"`
	RepositoryRef  string `json:"repositoryRef"`
	RepositoryRoot string `json:"repositoryRoot"`
	GitCommonDir   string `json:"gitCommonDir"`
	GitDir         string `json:"gitDir"`
	StorageKey     string `json:"storageKey"`
}

func (binding trustedRepositoryBinding) validate() error {
	if binding.Schema != trustedRepositoryBindingSchema {
		return fmt.Errorf("%w: trusted repository binding schema", ErrInvalid)
	}
	if !validDeliveryIdentityRef(binding.RepositoryRef) {
		return fmt.Errorf("%w: trusted repository identity ref", ErrInvalid)
	}
	if !validDeliveryStorageKey(binding.StorageKey) ||
		binding.StorageKey != strings.TrimPrefix(binding.RepositoryRef, "sha256:") {
		return fmt.Errorf("%w: trusted repository storage key", ErrInvalid)
	}
	if !validDeliveryIdentityPath(binding.RepositoryRoot) ||
		!validDeliveryIdentityPath(binding.GitCommonDir) ||
		!validDeliveryIdentityPath(binding.GitDir) {
		return fmt.Errorf("%w: trusted repository binding paths", ErrInvalid)
	}
	if err := validateToken("trusted repository ref", binding.RepositoryRef); err != nil {
		return err
	}
	return nil
}

// OpenTrustedRepository resolves repositoryRef through owner authority and
// derives storage from the hardened Git common-dir identity. No caller-selected
// storage path is accepted.
func OpenTrustedRepository(
	ctx context.Context,
	owner DeliveryRepositoryAuthority,
	repositoryRef string,
	options ...TrustedRepositoryOption,
) (*TrustedRepository, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if owner == nil {
		return nil, fmt.Errorf("%w: missing delivery repository authority", ErrInvalid)
	}
	if err := validateToken("trusted repository ref", repositoryRef); err != nil {
		return nil, err
	}
	settings := trustedRepositoryOptions{clock: systemOwnerClock{}}
	for _, option := range options {
		if option == nil {
			return nil, fmt.Errorf("%w: nil trusted repository option", ErrInvalid)
		}
		if err := option(&settings); err != nil {
			return nil, err
		}
	}
	root, err := owner.ResolveDeliveryRepository(ctx, repositoryRef)
	if err != nil {
		return nil, fmt.Errorf("%w: resolve repository authority: %v", ErrTrustedRepositoryUnavailable, err)
	}
	identity, err := resolveDeliveryGitIdentity(ctx, root)
	if err != nil {
		return nil, fmt.Errorf("%w: resolve trusted Git identity: %v", ErrTrustedRepositoryUnavailable, err)
	}
	if identity.repositoryRef != repositoryRef {
		return nil, fmt.Errorf(
			"%w: owner repository ref does not match the exact Git identity",
			ErrTrustedRepositoryCorrupt,
		)
	}
	storePath := filepath.Join(
		identity.commonDir,
		"gentle-ai",
		"delivery-admission",
		"v1",
		"repositories",
		identity.storageKey,
	)
	directory, err := openSecureUseDirectory(
		identity.commonDir,
		storePath,
		identity.commonInfo,
	)
	if err != nil {
		return nil, fmt.Errorf("%w: open PAD provider store: %v", ErrTrustedRepositoryUnavailable, err)
	}
	rar, err := reviewtransaction.OpenRARAuthorityRepository(ctx, identity.repositoryRoot)
	if err != nil {
		_ = directory.close()
		return nil, fmt.Errorf("%w: open native RAR authority: %v", ErrTrustedRepositoryUnavailable, err)
	}
	repository := &TrustedRepository{
		repositoryRef: repositoryRef, repositoryRoot: identity.repositoryRoot,
		gitCommonDir: identity.commonDir, gitDir: identity.gitDir,
		storageKey: identity.storageKey, identityLease: identity.lease,
		owner: owner, clock: settings.clock, rar: rar, directory: directory,
	}
	binding := trustedRepositoryBinding{
		Schema: trustedRepositoryBindingSchema, RepositoryRef: repositoryRef,
		RepositoryRoot: identity.repositoryRoot, GitCommonDir: identity.commonDir,
		GitDir: identity.gitDir, StorageKey: identity.storageKey,
	}
	if err := repository.bind(ctx, binding); err != nil {
		_ = repository.Close()
		return nil, err
	}
	if err := repository.validateAuthority(ctx); err != nil {
		_ = repository.Close()
		return nil, err
	}
	return repository, nil
}

func (repository *TrustedRepository) Close() error {
	if repository == nil {
		return nil
	}
	repository.closeMu.Lock()
	defer repository.closeMu.Unlock()
	if repository.closed {
		return nil
	}
	repository.closed = true
	if repository.directory == nil {
		return nil
	}
	return repository.directory.close()
}

func (repository *TrustedRepository) validateAuthority(ctx context.Context) error {
	if repository == nil {
		return fmt.Errorf("%w: unopened PAD repository", ErrTrustedRepositoryUnavailable)
	}
	repository.closeMu.RLock()
	defer repository.closeMu.RUnlock()
	return repository.validateAuthorityLocked(ctx)
}

func (repository *TrustedRepository) validateAuthorityLocked(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if repository.closed || repository.owner == nil || repository.clock == nil ||
		repository.directory == nil || repository.identityLease == nil ||
		repository.repositoryRef == "" || repository.repositoryRoot == "" ||
		repository.gitCommonDir == "" || repository.gitDir == "" ||
		!validDeliveryStorageKey(repository.storageKey) {
		return fmt.Errorf("%w: closed or incomplete PAD repository", ErrTrustedRepositoryUnavailable)
	}
	root, err := repository.owner.ResolveDeliveryRepository(ctx, repository.repositoryRef)
	if err != nil {
		return fmt.Errorf("%w: re-resolve repository authority: %v", ErrTrustedRepositoryUnavailable, err)
	}
	if root != repository.repositoryRoot {
		return fmt.Errorf("%w: repository authority mapping changed", ErrTrustedRepositoryCorrupt)
	}
	if err := repository.identityLease.Validate(ctx); err != nil {
		return fmt.Errorf(
			"%w: revalidate trusted Git identity: %w",
			ErrTrustedRepositoryCorrupt,
			err,
		)
	}
	identity := repository.identityLease.Identity()
	if identity.RepositoryRoot != repository.repositoryRoot ||
		identity.GitCommonDir != repository.gitCommonDir ||
		identity.GitDir != repository.gitDir ||
		identity.RepositoryRef != repository.repositoryRef ||
		repository.identityLease.StorageKey() != repository.storageKey {
		return fmt.Errorf(
			"%w: retained Git identity lease changed",
			ErrTrustedRepositoryCorrupt,
		)
	}
	if err := repository.directory.validate(); err != nil {
		return fmt.Errorf("%w: validate PAD provider store: %v", ErrTrustedRepositoryCorrupt, err)
	}
	if err := repository.identityLease.Validate(ctx); err != nil {
		return fmt.Errorf(
			"%w: revalidate trusted Git identity after store access: %w",
			ErrTrustedRepositoryCorrupt,
			err,
		)
	}
	return nil
}

func (repository *TrustedRepository) bind(
	ctx context.Context,
	binding trustedRepositoryBinding,
) error {
	if err := binding.validate(); err != nil {
		return err
	}
	payload, err := json.Marshal(binding)
	if err != nil {
		return err
	}
	return repository.withStoreExclusiveLock(ctx, func() error {
		existing, exists, err := repository.readRawOptionalLocked("repository-binding.json")
		if err != nil {
			return err
		}
		if exists {
			if !bytes.Equal(existing, payload) {
				return fmt.Errorf("%w: repository binding", ErrTrustedRepositoryConflict)
			}
			var prior trustedRepositoryBinding
			if err := decodeCanonicalTrusted(
				existing,
				&prior,
				func() error { return prior.validate() },
			); err != nil {
				return err
			}
			return nil
		}
		if err := repository.directory.publishNoReplace("repository-binding.json", payload); err != nil {
			return fmt.Errorf("%w: publish repository binding: %v", ErrTrustedRepositoryUnavailable, err)
		}
		return nil
	})
}

func (repository *TrustedRepository) CurrentUnix(ctx context.Context) (int64, error) {
	if err := repository.validateAuthority(ctx); err != nil {
		return 0, err
	}
	now := repository.clock.Now().Unix()
	if now <= 0 {
		return 0, fmt.Errorf("%w: owner clock returned non-positive time", ErrTrustedRepositoryUnavailable)
	}
	return now, nil
}

func (repository *TrustedRepository) ResolveDeliveryRepository(
	ctx context.Context,
	repositoryRef string,
) (string, error) {
	if repository == nil || repositoryRef != repository.repositoryRef {
		return "", fmt.Errorf("%w: unknown repository authority", ErrTrustedRepositoryUnavailable)
	}
	if err := repository.validateAuthority(ctx); err != nil {
		return "", err
	}
	return repository.repositoryRoot, nil
}

// ResolveTerminalReview delegates only after revalidating this repository's
// owner/Git identity. Production instances delegate to the native
// reviewtransaction repository opened from the same exact root.
func (repository *TrustedRepository) ResolveTerminalReview(
	ctx context.Context,
	receiptRef string,
	resultRef string,
) (reviewtransaction.RARVerificationAuthority, error) {
	if err := repository.validateAuthority(ctx); err != nil {
		return reviewtransaction.RARVerificationAuthority{}, err
	}
	if repository.rar == nil {
		return reviewtransaction.RARVerificationAuthority{}, fmt.Errorf(
			"%w: missing native RAR authority",
			ErrTrustedRepositoryUnavailable,
		)
	}
	authority, err := repository.rar.ResolveTerminalReview(ctx, receiptRef, resultRef)
	if err != nil {
		classification := ErrTrustedRepositoryUnavailable
		if errors.Is(err, reviewtransaction.ErrRARAuthorityCorrupt) ||
			errors.Is(err, reviewtransaction.ErrRARAuthorityStale) ||
			errors.Is(err, reviewtransaction.ErrRARAuthorityConflict) {
			classification = ErrTrustedRepositoryCorrupt
		}
		return reviewtransaction.RARVerificationAuthority{}, fmt.Errorf(
			"%w: resolve native RAR authority: %v",
			classification,
			err,
		)
	}
	if err := authority.Validate(); err != nil ||
		authority.Receipt.ReceiptRef != receiptRef ||
		authority.Result.ResultRef != resultRef {
		if err != nil {
			return reviewtransaction.RARVerificationAuthority{}, fmt.Errorf(
				"%w: invalid native RAR authority: %v",
				ErrTrustedRepositoryCorrupt,
				err,
			)
		}
		return reviewtransaction.RARVerificationAuthority{}, fmt.Errorf(
			"%w: native RAR receipt/result binding",
			ErrTrustedRepositoryCorrupt,
		)
	}
	if err := repository.validateAuthority(ctx); err != nil {
		return reviewtransaction.RARVerificationAuthority{}, err
	}
	return authority, nil
}

type liveRoutePolicyIndex struct {
	Schema              string `json:"schema"`
	RepositoryRef       string `json:"repositoryRef"`
	Route               Route  `json:"route"`
	PolicyRef           string `json:"policyRef"`
	Sequence            uint64 `json:"sequence"`
	PreviousRevisionRef string `json:"previousRevisionRef,omitempty"`
}

func (index liveRoutePolicyIndex) validate() error {
	if index.Schema != liveRoutePolicyIndexSchema || !index.Route.valid() ||
		index.Sequence == 0 {
		return fmt.Errorf("%w: live route-policy index header", ErrInvalid)
	}
	if err := validateToken("live policy repositoryRef", index.RepositoryRef); err != nil {
		return err
	}
	if err := validateDigest("live policy policyRef", index.PolicyRef); err != nil {
		return err
	}
	if index.Sequence == 1 {
		if index.PreviousRevisionRef != "" {
			return fmt.Errorf("%w: initial live policy has a predecessor", ErrInvalid)
		}
		return nil
	}
	return validateDigest("live policy previousRevisionRef", index.PreviousRevisionRef)
}

func (index liveRoutePolicyIndex) ref() (string, error) {
	return contentRef(index, index.validate())
}

type LiveRoutePolicyUpdate struct {
	Route            Route
	PolicyRef        string
	ExpectedRevision string
}

// PutLiveRoutePolicy atomically rotates one repository+route live index.
// ExpectedRevision is empty only for first publication; subsequent updates use
// the exact revision returned by this method, preventing ABA.
func (repository *TrustedRepository) PutLiveRoutePolicy(
	ctx context.Context,
	update LiveRoutePolicyUpdate,
) (string, error) {
	if err := repository.validateAuthority(ctx); err != nil {
		return "", err
	}
	if !update.Route.valid() {
		return "", fmt.Errorf("%w: live policy route", ErrInvalid)
	}
	if err := validateDigest("live policy ref", update.PolicyRef); err != nil {
		return "", err
	}
	if update.ExpectedRevision != "" {
		if err := validateDigest("live policy expected revision", update.ExpectedRevision); err != nil {
			return "", err
		}
	}
	policy, err := repository.ResolveRoutePolicy(ctx, update.PolicyRef)
	if err != nil {
		return "", err
	}
	if policy.Route != update.Route {
		return "", fmt.Errorf("%w: live policy route binding", ErrBindingMismatch)
	}
	name := liveRoutePolicyRecordName(repository.repositoryRef, update.Route)
	var revision string
	err = repository.withStoreExclusiveLock(ctx, func() error {
		payload, exists, err := repository.readRawOptionalLocked(name)
		if err != nil {
			return err
		}
		next := liveRoutePolicyIndex{
			Schema:        liveRoutePolicyIndexSchema,
			RepositoryRef: repository.repositoryRef,
			Route:         update.Route, PolicyRef: update.PolicyRef, Sequence: 1,
		}
		if exists {
			var current liveRoutePolicyIndex
			if err := decodeCanonicalTrusted(
				payload,
				&current,
				func() error { return current.validate() },
			); err != nil {
				return err
			}
			currentRef, _ := current.ref()
			if current.RepositoryRef != repository.repositoryRef ||
				current.Route != update.Route {
				return fmt.Errorf(
					"%w: live route-policy slot binding",
					ErrTrustedRepositoryCorrupt,
				)
			}
			exactReplay := current.PolicyRef == update.PolicyRef &&
				(update.ExpectedRevision == currentRef ||
					(current.Sequence == 1 && update.ExpectedRevision == "") ||
					(current.Sequence > 1 &&
						update.ExpectedRevision == current.PreviousRevisionRef))
			if exactReplay {
				revision = currentRef
				return repository.validateAuthorityLocked(ctx)
			}
			if update.ExpectedRevision != currentRef {
				return fmt.Errorf("%w: live route-policy CAS", ErrTrustedRepositoryConflict)
			}
			next.Sequence = current.Sequence + 1
			if next.Sequence <= current.Sequence {
				return fmt.Errorf("%w: live route-policy sequence overflow", ErrTrustedRepositoryCorrupt)
			}
			next.PreviousRevisionRef = currentRef
		} else if update.ExpectedRevision != "" {
			return fmt.Errorf("%w: missing live route-policy CAS base", ErrTrustedRepositoryConflict)
		}
		if err := next.validate(); err != nil {
			return err
		}
		nextPayload, err := json.Marshal(next)
		if err != nil {
			return err
		}
		revision, _ = next.ref()
		if exists {
			if err := repository.directory.replace(name, nextPayload); err != nil {
				return fmt.Errorf("%w: rotate live route policy: %v", ErrTrustedRepositoryUnavailable, err)
			}
		} else if err := repository.directory.publishNoReplace(name, nextPayload); err != nil {
			if errors.Is(err, errSecureUseTargetExists) {
				return fmt.Errorf("%w: concurrent initial live route policy", ErrTrustedRepositoryConflict)
			}
			return fmt.Errorf("%w: publish live route policy: %v", ErrTrustedRepositoryUnavailable, err)
		}
		return repository.validateAuthorityLocked(ctx)
	})
	return revision, err
}

func (repository *TrustedRepository) ResolveLiveRoutePolicy(
	ctx context.Context,
	repositoryRef string,
	route Route,
) (RoutePolicy, error) {
	policy, _, err := repository.ResolveLiveRoutePolicyRevision(
		ctx,
		repositoryRef,
		route,
	)
	return policy, err
}

// ResolveLiveRoutePolicyRevision returns the current immutable policy and the
// exact mutable-index revision required by a subsequent CAS rotation.
func (repository *TrustedRepository) ResolveLiveRoutePolicyRevision(
	ctx context.Context,
	repositoryRef string,
	route Route,
) (RoutePolicy, string, error) {
	if repository == nil || repositoryRef != repository.repositoryRef || !route.valid() {
		return RoutePolicy{}, "", fmt.Errorf(
			"%w: unknown live route-policy slot",
			ErrTrustedRepositoryUnavailable,
		)
	}
	index, err := readTrustedValue(
		ctx,
		repository,
		liveRoutePolicyRecordName(repositoryRef, route),
		func(value liveRoutePolicyIndex) error { return value.validate() },
	)
	if err != nil {
		return RoutePolicy{}, "", err
	}
	if index.RepositoryRef != repositoryRef || index.Route != route {
		return RoutePolicy{}, "", fmt.Errorf(
			"%w: live route-policy key mismatch",
			ErrTrustedRepositoryCorrupt,
		)
	}
	policy, err := repository.ResolveRoutePolicy(ctx, index.PolicyRef)
	if err != nil {
		return RoutePolicy{}, "", err
	}
	if policy.Route != route {
		return RoutePolicy{}, "", fmt.Errorf(
			"%w: live route-policy object mismatch",
			ErrTrustedRepositoryCorrupt,
		)
	}
	revision, err := index.ref()
	if err != nil {
		return RoutePolicy{}, "", fmt.Errorf(
			"%w: live route-policy revision: %v",
			ErrTrustedRepositoryCorrupt,
			err,
		)
	}
	return policy, revision, nil
}

type admittedIntentIndex struct {
	Schema       string `json:"schema"`
	IntentRef    string `json:"intentRef"`
	AdmissionRef string `json:"admissionRef"`
}

func (index admittedIntentIndex) validate() error {
	if index.Schema != admittedIntentIndexSchema {
		return fmt.Errorf("%w: admitted-intent index schema", ErrInvalid)
	}
	if err := validateDigest("admitted intent ref", index.IntentRef); err != nil {
		return err
	}
	return validateDigest("admitted admission ref", index.AdmissionRef)
}

type authorizationKindIndex struct {
	Schema           string            `json:"schema"`
	AuthorizationRef string            `json:"authorizationRef"`
	Kind             AuthorizationKind `json:"kind"`
}

func (index authorizationKindIndex) validate() error {
	if index.Schema != authorizationKindIndexSchema ||
		(index.Kind != AuthorizationNormal && index.Kind != AuthorizationException) {
		return fmt.Errorf("%w: authorization-kind index header", ErrInvalid)
	}
	return validateDigest("authorization-kind ref", index.AuthorizationRef)
}

type LiveAuthorization struct {
	AuthorizationRef string
	Kind             AuthorizationKind
	Binding          AuthorizationBinding
	ObservedAt       int64
}

// AdmittedDecision is the exact PAD-owned winner for one delivery intent.
// Callers use it to distinguish byte-identical replay from an attempt to reuse
// an admitted intent with different governance or maintainer authority.
type AdmittedDecision struct {
	AdmissionDecisionRef string
	Decision             AdmissionDecision
}

func (repository *TrustedRepository) ResolveAdmittedDecision(
	ctx context.Context,
	intentRef string,
) (AdmittedDecision, error) {
	if err := validateDigest("admitted intent ref", intentRef); err != nil {
		return AdmittedDecision{}, err
	}
	if err := repository.validateAuthority(ctx); err != nil {
		return AdmittedDecision{}, err
	}
	name := indexRecordName(intentRef, "admitted-intent")
	payload, exists, err := repository.readRawOptional(name)
	if err != nil {
		return AdmittedDecision{}, err
	}
	if !exists {
		return AdmittedDecision{}, fmt.Errorf(
			"%w: %s",
			ErrAdmittedDecisionNotFound,
			intentRef,
		)
	}
	var index admittedIntentIndex
	if err := decodeCanonicalTrusted(
		payload,
		&index,
		func() error { return index.validate() },
	); err != nil {
		return AdmittedDecision{}, err
	}
	if err := repository.validateAuthority(ctx); err != nil {
		return AdmittedDecision{}, err
	}
	if index.IntentRef != intentRef {
		return AdmittedDecision{}, fmt.Errorf(
			"%w: admitted-intent key mismatch",
			ErrTrustedRepositoryCorrupt,
		)
	}
	decision, err := ValidateAdmissionDecision(
		ctx,
		repository,
		index.AdmissionRef,
	)
	if err != nil {
		return AdmittedDecision{}, fmt.Errorf(
			"%w: validate admitted intent authority: %v",
			ErrTrustedRepositoryCorrupt,
			err,
		)
	}
	if decision.Disposition != AdmissionAdmitted ||
		decision.IntentRef != intentRef {
		return AdmittedDecision{}, fmt.Errorf(
			"%w: intent has no admitted decision",
			ErrTrustedRepositoryCorrupt,
		)
	}
	return AdmittedDecision{
		AdmissionDecisionRef: index.AdmissionRef,
		Decision:             decision,
	}, nil
}

func (repository *TrustedRepository) ResolveAdmittedIntent(
	ctx context.Context,
	intentRef string,
) (DeliveryIntent, error) {
	admitted, err := repository.ResolveAdmittedDecision(ctx, intentRef)
	if err != nil {
		return DeliveryIntent{}, err
	}
	return admitted.Decision.Intent, nil
}

func (repository *TrustedRepository) ResolveLiveAuthorization(
	ctx context.Context,
	authorizationRef string,
) (LiveAuthorization, error) {
	if err := validateDigest("live authorization ref", authorizationRef); err != nil {
		return LiveAuthorization{}, err
	}
	index, err := readTrustedValue(
		ctx,
		repository,
		indexRecordName(authorizationRef, "authorization-kind"),
		func(value authorizationKindIndex) error { return value.validate() },
	)
	if err != nil {
		return LiveAuthorization{}, err
	}
	if index.AuthorizationRef != authorizationRef {
		return LiveAuthorization{}, fmt.Errorf("%w: authorization-kind key mismatch", ErrTrustedRepositoryCorrupt)
	}
	var binding AuthorizationBinding
	switch index.Kind {
	case AuthorizationNormal:
		stored, err := repository.ResolveAuthorization(ctx, authorizationRef)
		if err != nil {
			return LiveAuthorization{}, err
		}
		if _, err := repository.ResolveTerminalReview(
			ctx,
			stored.Binding.ReviewReceiptRef,
			stored.Binding.VerificationResultRef,
		); err != nil {
			return LiveAuthorization{}, err
		}
		authorization, err := ValidateAuthorization(ctx, repository, repository, authorizationRef)
		if err != nil {
			return LiveAuthorization{}, fmt.Errorf("%w: validate normal authorization authority: %v", ErrTrustedRepositoryCorrupt, err)
		}
		binding = authorization.Binding
	case AuthorizationException:
		stored, err := repository.ResolveExceptionAuthorization(ctx, authorizationRef)
		if err != nil {
			return LiveAuthorization{}, err
		}
		if _, err := repository.ResolveTerminalReview(
			ctx,
			stored.Binding.ReviewReceiptRef,
			stored.Binding.VerificationResultRef,
		); err != nil {
			return LiveAuthorization{}, err
		}
		authorization, err := ValidateExceptionAuthorization(
			ctx,
			repository,
			repository,
			authorizationRef,
		)
		if err != nil {
			return LiveAuthorization{}, fmt.Errorf("%w: validate exception authorization authority: %v", ErrTrustedRepositoryCorrupt, err)
		}
		binding = authorization.Binding
	default:
		return LiveAuthorization{}, fmt.Errorf("%w: unknown authorization kind", ErrTrustedRepositoryCorrupt)
	}
	if binding.Destination.RepositoryRef != repository.repositoryRef {
		return LiveAuthorization{}, fmt.Errorf("%w: authorization repository binding", ErrTrustedRepositoryCorrupt)
	}
	if err := requireLivePolicy(
		ctx,
		repository,
		repository.repositoryRef,
		binding.Route,
		binding.PolicyRef,
		binding.Policy,
	); err != nil {
		return LiveAuthorization{}, fmt.Errorf("%w: live authorization policy: %v", ErrTrustedRepositoryCorrupt, err)
	}
	now, err := repository.CurrentUnix(ctx)
	if err != nil {
		return LiveAuthorization{}, err
	}
	if now < binding.IssuedAt {
		return LiveAuthorization{}, &LiveAuthorizationInactiveError{
			AuthorizationRef: authorizationRef,
			Reason:           LiveAuthorizationNotYetValid,
		}
	}
	if now >= binding.ExpiresAt {
		return LiveAuthorization{}, &LiveAuthorizationInactiveError{
			AuthorizationRef: authorizationRef,
			Reason:           LiveAuthorizationExpired,
		}
	}
	useStore, err := OpenDirectoryUseStore(ctx, repository, repository.repositoryRef)
	if err != nil {
		return LiveAuthorization{}, fmt.Errorf("%w: open authorization-use authority: %v", ErrTrustedRepositoryUnavailable, err)
	}
	defer useStore.Close()
	use, consumed, err := useStore.authorizationUse(ctx, authorizationRef)
	if err != nil {
		return LiveAuthorization{}, fmt.Errorf("%w: read authorization-use authority: %v", ErrTrustedRepositoryCorrupt, err)
	}
	if consumed {
		if use.Kind != index.Kind || use.DecisionRef != binding.DecisionRef ||
			use.IntentRef != binding.IntentRef || use.Route != binding.Route ||
			use.Candidate != binding.Candidate || use.Destination != binding.Destination ||
			use.PolicyRef != binding.PolicyRef || use.Policy != binding.Policy.Binding() ||
			use.AuthorizationExpiresAt != binding.ExpiresAt ||
			use.ConsumedAt < binding.IssuedAt {
			return LiveAuthorization{}, fmt.Errorf("%w: authorization-use binding mismatch", ErrTrustedRepositoryCorrupt)
		}
		return LiveAuthorization{}, &LiveAuthorizationInactiveError{
			AuthorizationRef: authorizationRef,
			Reason:           LiveAuthorizationConsumed,
		}
	}
	return LiveAuthorization{
		AuthorizationRef: authorizationRef,
		Kind:             index.Kind, Binding: binding, ObservedAt: now,
	}, nil
}

func liveRoutePolicyRecordName(repositoryRef string, route Route) string {
	key := hashValue(struct {
		Schema        string `json:"schema"`
		RepositoryRef string `json:"repositoryRef"`
		Route         Route  `json:"route"`
	}{
		Schema:        "gentle-ai.delivery-live-route-policy-key/v1",
		RepositoryRef: repositoryRef,
		Route:         route,
	})
	return strings.TrimPrefix(key, "sha256:") + ".live-route-policy.json"
}

func indexRecordName(ref, kind string) string {
	return strings.TrimPrefix(ref, "sha256:") + "." + kind + ".json"
}

func objectRecordName(ref, kind string) string {
	return strings.TrimPrefix(ref, "sha256:") + "." + kind + ".json"
}

func (repository *TrustedRepository) withStoreExclusiveLock(
	ctx context.Context,
	action func() error,
) error {
	if repository == nil || action == nil {
		return fmt.Errorf("%w: invalid PAD store lock", ErrTrustedRepositoryUnavailable)
	}
	repository.closeMu.RLock()
	defer repository.closeMu.RUnlock()
	if repository.closed || repository.directory == nil {
		return fmt.Errorf("%w: closed PAD store", ErrTrustedRepositoryUnavailable)
	}
	return repository.directory.withExclusiveLock(ctx, action)
}

func (repository *TrustedRepository) readRawOptional(
	name string,
) ([]byte, bool, error) {
	if repository == nil {
		return nil, false, fmt.Errorf("%w: unopened PAD store", ErrTrustedRepositoryUnavailable)
	}
	repository.closeMu.RLock()
	defer repository.closeMu.RUnlock()
	if repository.closed || repository.directory == nil {
		return nil, false, fmt.Errorf("%w: closed PAD store", ErrTrustedRepositoryUnavailable)
	}
	return repository.readRawOptionalLocked(name)
}

func (repository *TrustedRepository) readRawOptionalLocked(
	name string,
) ([]byte, bool, error) {
	payload, err := repository.directory.readBounded(name, maximumSecureRecordBytes)
	if err == nil {
		return payload, true, nil
	}
	if secureRecordNotExist(err) {
		return nil, false, nil
	}
	return nil, false, fmt.Errorf("%w: read %s: %v", ErrTrustedRepositoryCorrupt, name, err)
}

func (repository *TrustedRepository) publishNoReplace(
	name string,
	payload []byte,
) error {
	if repository == nil {
		return fmt.Errorf("%w: unopened PAD store", ErrTrustedRepositoryUnavailable)
	}
	repository.closeMu.RLock()
	defer repository.closeMu.RUnlock()
	if repository.closed || repository.directory == nil {
		return fmt.Errorf("%w: closed PAD store", ErrTrustedRepositoryUnavailable)
	}
	return repository.directory.publishNoReplace(name, payload)
}

func readTrustedValue[T any](
	ctx context.Context,
	repository *TrustedRepository,
	name string,
	validate func(T) error,
) (T, error) {
	var zero T
	if err := repository.validateAuthority(ctx); err != nil {
		return zero, err
	}
	payload, exists, err := repository.readRawOptional(name)
	if err != nil {
		return zero, err
	}
	if !exists {
		return zero, fmt.Errorf("%w: missing %s", ErrTrustedRepositoryUnavailable, name)
	}
	var value T
	if err := decodeCanonicalTrusted(payload, &value, func() error { return validate(value) }); err != nil {
		return zero, err
	}
	if err := repository.validateAuthority(ctx); err != nil {
		return zero, err
	}
	return value, nil
}

func decodeCanonicalTrusted[T any](
	payload []byte,
	target *T,
	validate func() error,
) error {
	if len(payload) == 0 || len(payload) > maximumSecureRecordBytes {
		return fmt.Errorf("%w: trusted record byte bound", ErrTrustedRepositoryCorrupt)
	}
	if err := json.Unmarshal(payload, target); err != nil {
		return fmt.Errorf("%w: decode trusted record: %v", ErrTrustedRepositoryCorrupt, err)
	}
	if err := validate(); err != nil {
		return fmt.Errorf("%w: validate trusted record: %v", ErrTrustedRepositoryCorrupt, err)
	}
	canonical, err := json.Marshal(*target)
	if err != nil || !bytes.Equal(payload, canonical) {
		if err != nil {
			return fmt.Errorf("%w: canonicalize trusted record: %v", ErrTrustedRepositoryCorrupt, err)
		}
		return fmt.Errorf("%w: non-canonical trusted record", ErrTrustedRepositoryCorrupt)
	}
	return nil
}

func publishTrustedValue[T any](
	ctx context.Context,
	repository *TrustedRepository,
	kind string,
	value T,
	validate func(T) error,
	ref func(T) (string, error),
) (string, error) {
	if err := validate(value); err != nil {
		return "", err
	}
	contentRef, err := ref(value)
	if err != nil {
		return "", err
	}
	if err := repository.validateAuthority(ctx); err != nil {
		return "", err
	}
	payload, err := json.Marshal(value)
	if err != nil {
		return "", err
	}
	name := objectRecordName(contentRef, kind)
	if err := repository.publishNoReplace(name, payload); err != nil {
		if !errors.Is(err, errSecureUseTargetExists) {
			return "", fmt.Errorf("%w: publish %s: %v", ErrTrustedRepositoryUnavailable, kind, err)
		}
		existing, exists, readErr := repository.readRawOptional(name)
		if readErr != nil {
			return "", readErr
		}
		if !exists || !bytes.Equal(existing, payload) {
			return "", fmt.Errorf("%w: immutable %s slot", ErrTrustedRepositoryConflict, kind)
		}
		var prior T
		if err := decodeCanonicalTrusted(existing, &prior, func() error { return validate(prior) }); err != nil {
			return "", err
		}
		priorRef, err := ref(prior)
		if err != nil || priorRef != contentRef {
			return "", fmt.Errorf("%w: immutable %s identity", ErrTrustedRepositoryCorrupt, kind)
		}
	}
	if err := repository.validateAuthority(ctx); err != nil {
		return "", err
	}
	return contentRef, nil
}

func (repository *TrustedRepository) publishIndex(
	ctx context.Context,
	name string,
	value any,
	validate func() error,
) error {
	if err := validate(); err != nil {
		return err
	}
	payload, err := json.Marshal(value)
	if err != nil {
		return err
	}
	if err := repository.publishNoReplace(name, payload); err != nil {
		if !errors.Is(err, errSecureUseTargetExists) {
			return fmt.Errorf("%w: publish index %s: %v", ErrTrustedRepositoryUnavailable, name, err)
		}
		existing, exists, readErr := repository.readRawOptional(name)
		if readErr != nil {
			return readErr
		}
		if !exists || !bytes.Equal(existing, payload) {
			return fmt.Errorf("%w: immutable index %s", ErrTrustedRepositoryConflict, name)
		}
	}
	return repository.validateAuthority(ctx)
}
