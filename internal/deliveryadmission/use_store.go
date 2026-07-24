package deliveryadmission

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

const maximumAuthorizationUseBytes = 1 << 20

var (
	errSecureUseTargetExists = errors.New("secure authorization-use target exists")
	syncSecureUseDirectory   = func(directory *secureUseDirectory) error {
		return directory.sync()
	}
)

type UseRequest struct {
	IntentRef   string
	Route       Route
	Candidate   CandidateBinding
	Destination DestinationBinding
	PolicyRef   string
	Policy      PolicyBinding
}

type AuthorizationUse struct {
	Schema                 string             `json:"schema"`
	Kind                   AuthorizationKind  `json:"kind"`
	AuthorizationRef       string             `json:"authorizationRef"`
	DecisionRef            string             `json:"decisionRef"`
	IntentRef              string             `json:"intentRef"`
	Route                  Route              `json:"route"`
	Candidate              CandidateBinding   `json:"candidate"`
	Destination            DestinationBinding `json:"destination"`
	PolicyRef              string             `json:"policyRef"`
	Policy                 PolicyBinding      `json:"policy"`
	ConsumedAt             int64              `json:"consumedAt"`
	AuthorizationExpiresAt int64              `json:"authorizationExpiresAt"`
}

func (use AuthorizationUse) Validate() error {
	if use.Schema != AuthorizationUseContractV1 ||
		(use.Kind != AuthorizationNormal && use.Kind != AuthorizationException) ||
		!use.Route.valid() || use.ConsumedAt <= 0 ||
		use.AuthorizationExpiresAt <= use.ConsumedAt {
		return fmt.Errorf("%w: authorization use header/window", ErrInvalid)
	}
	for name, ref := range map[string]string{
		"use.authorizationRef": use.AuthorizationRef,
		"use.decisionRef":      use.DecisionRef,
		"use.intentRef":        use.IntentRef,
		"use.policyRef":        use.PolicyRef,
	} {
		if err := validateDigest(name, ref); err != nil {
			return err
		}
	}
	if err := use.Candidate.validate(); err != nil {
		return err
	}
	if err := use.Destination.validate(); err != nil {
		return err
	}
	return use.Policy.validate()
}

type DirectoryUseStore struct {
	directory       string
	repositoryRoot  string
	repositoryInfo  os.FileInfo
	repositoryRef   string
	gitCommonDir    string
	resolver        TrustedResolver
	secureDirectory *secureUseDirectory
	closeMu         sync.RWMutex
	closed          bool

	// Tests use this hook to prove that publication stays relative to the
	// stable directory handle if the visible path is exchanged mid-operation.
	beforePublication func()
}

// OpenDirectoryUseStore resolves repositoryRef through the same owner control
// plane as the rest of PAD, verifies its exact Git root/common-dir identity,
// and opens the owner-only one-shot store through stable directory handles.
// There is intentionally no path/root parameter.
func OpenDirectoryUseStore(
	ctx context.Context,
	resolver TrustedResolver,
	repositoryRef string,
) (*DirectoryUseStore, error) {
	if resolver == nil {
		return nil, fmt.Errorf("%w: missing trusted repository resolver", ErrInvalid)
	}
	if err := validateToken("delivery use repositoryRef", repositoryRef); err != nil {
		return nil, err
	}
	repositoryRoot, err := resolver.ResolveDeliveryRepository(ctx, repositoryRef)
	if err != nil {
		return nil, fmt.Errorf("resolve delivery repository authority: %w", err)
	}
	identity, err := resolveDeliveryGitIdentity(ctx, repositoryRoot)
	if err != nil {
		return nil, err
	}
	directory := filepath.Join(
		identity.commonDir, "gentle-ai", "delivery-authorization-uses", "v1",
	)
	secureDirectory, err := openSecureUseDirectory(
		identity.commonDir, directory, identity.commonInfo,
	)
	if err != nil {
		return nil, fmt.Errorf("open secure delivery use store: %w", err)
	}
	store := &DirectoryUseStore{
		directory: directory, repositoryRoot: identity.repositoryRoot,
		repositoryInfo: identity.repositoryInfo,
		repositoryRef:  repositoryRef, gitCommonDir: identity.commonDir,
		resolver: resolver, secureDirectory: secureDirectory,
	}
	if err := store.validateAuthority(ctx); err != nil {
		_ = secureDirectory.close()
		return nil, err
	}
	return store, nil
}

// Close releases the stable repository/store directory handles.
func (store *DirectoryUseStore) Close() error {
	if store == nil {
		return nil
	}
	store.closeMu.Lock()
	defer store.closeMu.Unlock()
	if store.closed {
		return nil
	}
	store.closed = true
	if store.secureDirectory == nil {
		return nil
	}
	return store.secureDirectory.close()
}

func (store *DirectoryUseStore) validateAuthority(ctx context.Context) error {
	if store == nil {
		return fmt.Errorf("%w: unopened use store", ErrInvalid)
	}
	store.closeMu.RLock()
	defer store.closeMu.RUnlock()
	return store.validateAuthorityLocked(ctx)
}

func (store *DirectoryUseStore) validateAuthorityLocked(ctx context.Context) error {
	if store.repositoryRef == "" || store.repositoryRoot == "" || store.repositoryInfo == nil ||
		store.gitCommonDir == "" || store.resolver == nil || store.secureDirectory == nil {
		return fmt.Errorf("%w: unopened use store", ErrInvalid)
	}
	if store.closed {
		return fmt.Errorf("%w: closed use store", ErrInvalid)
	}
	liveRoot, err := store.resolver.ResolveDeliveryRepository(ctx, store.repositoryRef)
	if err != nil {
		return fmt.Errorf("re-resolve delivery repository authority: %w", err)
	}
	if liveRoot != store.repositoryRoot {
		return fmt.Errorf("%w: delivery repository authority root changed", ErrBindingMismatch)
	}
	identity, err := resolveDeliveryGitIdentity(ctx, store.repositoryRoot)
	if err != nil {
		return err
	}
	if identity.repositoryRoot != store.repositoryRoot ||
		identity.commonDir != store.gitCommonDir ||
		identity.repositoryInfo == nil ||
		!os.SameFile(store.repositoryInfo, identity.repositoryInfo) {
		return fmt.Errorf("%w: delivery repository Git identity changed", ErrBindingMismatch)
	}
	if err := store.secureDirectory.validate(); err != nil {
		return fmt.Errorf("validate secure delivery use store: %w", err)
	}
	return nil
}

// consumeOnce publishes canonical evidence relative to a stable directory
// handle with atomic no-replace semantics. Concurrent callers have exactly one
// winner and directory durability is mandatory on every platform.
func (store *DirectoryUseStore) consumeOnce(ctx context.Context, use AuthorizationUse) error {
	if store == nil {
		return fmt.Errorf("%w: unopened use store", ErrInvalid)
	}
	store.closeMu.RLock()
	defer store.closeMu.RUnlock()
	if store.closed {
		return fmt.Errorf("%w: closed use store", ErrInvalid)
	}
	if err := store.validateAuthorityLocked(ctx); err != nil {
		return err
	}
	if err := use.Validate(); err != nil {
		return err
	}
	payload, err := json.Marshal(use)
	if err != nil {
		return fmt.Errorf("encode authorization use: %w", err)
	}
	if len(payload) == 0 || len(payload) > maximumAuthorizationUseBytes {
		return fmt.Errorf("%w: authorization use exceeds its byte bound", ErrInvalid)
	}
	target := strings.TrimPrefix(use.AuthorizationRef, "sha256:") + ".json"
	if store.beforePublication != nil {
		store.beforePublication()
	}
	if err := store.secureDirectory.publishNoReplace(target, payload); err != nil {
		if errors.Is(err, errSecureUseTargetExists) {
			existing, readErr := store.secureDirectory.readBounded(target, maximumAuthorizationUseBytes)
			var prior AuthorizationUse
			decodeErr := json.Unmarshal(existing, &prior)
			canonicalPrior, canonicalErr := json.Marshal(prior)
			if readErr != nil || decodeErr != nil || prior.Validate() != nil ||
				canonicalErr != nil || !bytes.Equal(existing, canonicalPrior) ||
				!sameAuthorizationUse(prior, use) {
				if readErr != nil {
					return fmt.Errorf("validate existing authorization use: %w", readErr)
				}
				if decodeErr != nil {
					return fmt.Errorf("decode existing authorization use: %w", decodeErr)
				}
				if canonicalErr != nil {
					return fmt.Errorf("re-encode existing authorization use: %w", canonicalErr)
				}
				return fmt.Errorf("%w: conflicting authorization use", ErrBindingMismatch)
			}
			return ErrAlreadyConsumed
		}
		return fmt.Errorf("commit authorization use reservation: %w", err)
	}
	if err := store.validateAuthorityLocked(ctx); err != nil {
		return err
	}
	return nil
}

func sameAuthorizationUse(left, right AuthorizationUse) bool {
	left.ConsumedAt = 0
	right.ConsumedAt = 0
	return left == right
}

func randomUseStoreName() (string, error) {
	var value [16]byte
	if _, err := rand.Read(value[:]); err != nil {
		return "", err
	}
	return ".pending-" + hex.EncodeToString(value[:]), nil
}

func ConsumeAuthorization(
	ctx context.Context,
	resolver TrustedResolver,
	rar RARAuthorityPort,
	authorizationRef string,
	request UseRequest,
	store *DirectoryUseStore,
) (AuthorizationUse, error) {
	now, err := trustedNow(ctx, resolver)
	if err != nil {
		return AuthorizationUse{}, err
	}
	authorization, err := ValidateAuthorization(ctx, resolver, rar, authorizationRef)
	if err != nil {
		return AuthorizationUse{}, err
	}
	if err := requireLivePolicy(
		ctx, resolver, authorization.Binding.Destination.RepositoryRef,
		authorization.Binding.Route, authorization.Binding.PolicyRef,
		authorization.Binding.Policy,
	); err != nil {
		return AuthorizationUse{}, err
	}
	return consume(
		ctx, AuthorizationNormal, authorizationRef, authorization.Binding, request, now, store,
	)
}

func ConsumeExceptionAuthorization(
	ctx context.Context,
	resolver TrustedResolver,
	rar RARAuthorityPort,
	authorizationRef string,
	request UseRequest,
	store *DirectoryUseStore,
) (AuthorizationUse, error) {
	now, err := trustedNow(ctx, resolver)
	if err != nil {
		return AuthorizationUse{}, err
	}
	authorization, err := ValidateExceptionAuthorization(ctx, resolver, rar, authorizationRef)
	if err != nil {
		return AuthorizationUse{}, err
	}
	if err := requireLivePolicy(
		ctx, resolver, authorization.Binding.Destination.RepositoryRef,
		authorization.Binding.Route, authorization.Binding.PolicyRef,
		authorization.Binding.Policy,
	); err != nil {
		return AuthorizationUse{}, err
	}
	return consume(
		ctx, AuthorizationException, authorizationRef, authorization.Binding, request, now, store,
	)
}

func consume(
	ctx context.Context,
	kind AuthorizationKind,
	authorizationRef string,
	binding AuthorizationBinding,
	request UseRequest,
	at int64,
	store *DirectoryUseStore,
) (AuthorizationUse, error) {
	if at < binding.IssuedAt || at >= binding.ExpiresAt {
		return AuthorizationUse{}, ErrExpired
	}
	if request.IntentRef != binding.IntentRef || request.Route != binding.Route ||
		request.Candidate != binding.Candidate || request.Destination != binding.Destination ||
		request.PolicyRef != binding.PolicyRef || request.Policy != binding.Policy.Binding() {
		return AuthorizationUse{}, ErrBindingMismatch
	}
	if store == nil {
		return AuthorizationUse{}, fmt.Errorf("%w: missing atomic durable use store", ErrInvalid)
	}
	if store.repositoryRef != binding.Destination.RepositoryRef {
		return AuthorizationUse{}, fmt.Errorf("%w: authorization use repository", ErrBindingMismatch)
	}
	use := AuthorizationUse{
		Schema: AuthorizationUseContractV1, Kind: kind,
		AuthorizationRef: authorizationRef, DecisionRef: binding.DecisionRef,
		IntentRef: binding.IntentRef, Route: binding.Route, Candidate: binding.Candidate,
		Destination: binding.Destination, PolicyRef: binding.PolicyRef,
		Policy: binding.Policy.Binding(), ConsumedAt: at,
		AuthorizationExpiresAt: binding.ExpiresAt,
	}
	if err := store.consumeOnce(ctx, use); err != nil {
		return AuthorizationUse{}, err
	}
	return use, nil
}
