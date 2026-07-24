package deliveryadmission

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"sync"

	"github.com/gentleman-programming/gentle-ai/internal/reviewtransaction"
)

const (
	maximumAuthorizationUseBytes      = 1 << 20
	executionResultAnchorContractV1   = "gentle-ai.delivery-execution-result-anchor/v1"
	executionResultAnchorTargetSuffix = "-terminal.json"
)

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
	ExecutionCommandRef    string             `json:"executionCommandRef,omitempty"`
	LiveGateRef            string             `json:"liveGateRef,omitempty"`
	ExecutionExpiresAt     int64              `json:"executionExpiresAt,omitempty"`
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

// executionResultAnchor is an independent, single-assignment terminal root in
// the authorization-use ledger. The productive PAD store must resolve to the
// same exact result before replay or reconciliation may trust either copy.
type executionResultAnchor struct {
	Schema           string          `json:"schema"`
	AuthorizationRef string          `json:"authorizationRef"`
	CommandRef       string          `json:"commandRef"`
	RepositoryRef    string          `json:"repositoryRef"`
	ResultRef        string          `json:"resultRef"`
	Result           ExecutionResult `json:"result"`
}

func newExecutionResultAnchor(
	use AuthorizationUse,
	command ExecutionCommand,
	result ExecutionResult,
) (executionResultAnchor, error) {
	commandRef, err := command.Ref()
	if err != nil {
		return executionResultAnchor{}, err
	}
	resultRef, err := result.Ref(command)
	if err != nil {
		return executionResultAnchor{}, err
	}
	anchor := executionResultAnchor{
		Schema:           executionResultAnchorContractV1,
		AuthorizationRef: use.AuthorizationRef,
		CommandRef:       commandRef,
		RepositoryRef:    command.Destination.RepositoryRef,
		ResultRef:        resultRef,
		Result:           result,
	}
	return anchor, anchor.Validate(use, command)
}

func (anchor executionResultAnchor) Validate(
	use AuthorizationUse,
	command ExecutionCommand,
) error {
	if err := use.Validate(); err != nil {
		return err
	}
	commandRef, err := command.Ref()
	if err != nil {
		return err
	}
	resultRef, err := anchor.Result.Ref(command)
	if err != nil {
		return err
	}
	if anchor.Schema != executionResultAnchorContractV1 ||
		anchor.AuthorizationRef != use.AuthorizationRef ||
		anchor.AuthorizationRef != command.AuthorizationRef ||
		anchor.CommandRef != commandRef ||
		anchor.CommandRef != use.ExecutionCommandRef ||
		anchor.RepositoryRef != use.Destination.RepositoryRef ||
		anchor.RepositoryRef != command.Destination.RepositoryRef ||
		anchor.ResultRef != resultRef ||
		anchor.Result.CompletedAt < use.ConsumedAt {
		return fmt.Errorf(
			"%w: execution-result anchor binding",
			ErrExecutionResultCorrupt,
		)
	}
	return nil
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
	hasExecutionCommand := use.ExecutionCommandRef != ""
	hasLiveGate := use.LiveGateRef != ""
	hasExecutionExpiry := use.ExecutionExpiresAt != 0
	if hasExecutionCommand != hasLiveGate ||
		hasExecutionCommand != hasExecutionExpiry {
		return fmt.Errorf("%w: partial delivery execution reservation", ErrInvalid)
	}
	if hasExecutionCommand {
		if err := validateDigest("use.executionCommandRef", use.ExecutionCommandRef); err != nil {
			return err
		}
		if err := validateDigest("use.liveGateRef", use.LiveGateRef); err != nil {
			return err
		}
		if use.ExecutionExpiresAt <= use.ConsumedAt ||
			use.ExecutionExpiresAt > use.AuthorizationExpiresAt {
			return fmt.Errorf("%w: invalid delivery execution deadline", ErrInvalid)
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
	repositoryRef   string
	identityRef     string
	gitCommonDir    string
	gitDir          string
	storageKey      string
	identityLease   *reviewtransaction.RepositoryIdentityLease
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
	if identity.repositoryRef != repositoryRef {
		return nil, fmt.Errorf(
			"%w: delivery use repository ref does not match the exact Git identity",
			ErrBindingMismatch,
		)
	}
	directory := filepath.Join(
		identity.commonDir,
		"gentle-ai",
		"delivery-authorization-uses",
		"v1",
		"repositories",
		identity.storageKey,
	)
	secureDirectory, err := openSecureUseDirectory(
		identity.commonDir, directory, identity.commonInfo,
	)
	if err != nil {
		return nil, fmt.Errorf("open secure delivery use store: %w", err)
	}
	store := &DirectoryUseStore{
		directory: directory, repositoryRoot: identity.repositoryRoot,
		repositoryRef: repositoryRef, identityRef: identity.repositoryRef,
		gitCommonDir: identity.commonDir,
		gitDir:       identity.gitDir, storageKey: identity.storageKey,
		identityLease: identity.lease,
		resolver:      resolver, secureDirectory: secureDirectory,
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
	if err := ctx.Err(); err != nil {
		return err
	}
	if store.repositoryRef == "" || !validDeliveryIdentityRef(store.identityRef) ||
		store.repositoryRoot == "" ||
		store.gitCommonDir == "" || store.gitDir == "" ||
		!validDeliveryStorageKey(store.storageKey) ||
		store.identityLease == nil || store.resolver == nil ||
		store.secureDirectory == nil {
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
	if err := store.identityLease.Validate(ctx); err != nil {
		return fmt.Errorf(
			"%w: delivery repository Git identity changed: %w",
			ErrBindingMismatch,
			err,
		)
	}
	identity := store.identityLease.Identity()
	if identity.RepositoryRoot != store.repositoryRoot ||
		identity.GitCommonDir != store.gitCommonDir ||
		identity.GitDir != store.gitDir ||
		identity.RepositoryRef != store.identityRef ||
		store.identityLease.StorageKey() != store.storageKey {
		return fmt.Errorf(
			"%w: retained delivery repository identity lease changed",
			ErrBindingMismatch,
		)
	}
	if err := store.secureDirectory.validate(); err != nil {
		return fmt.Errorf("validate secure delivery use store: %w", err)
	}
	if err := store.identityLease.Validate(ctx); err != nil {
		return fmt.Errorf(
			"%w: delivery repository Git identity changed after store access: %w",
			ErrBindingMismatch,
			err,
		)
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

// authorizationUse reads one reservation through the same stable handle used
// for publication. Absence is an expected "not consumed" result; malformed,
// non-canonical, or mismatched records are hard authority failures.
func (store *DirectoryUseStore) authorizationUse(
	ctx context.Context,
	authorizationRef string,
) (AuthorizationUse, bool, error) {
	if store == nil {
		return AuthorizationUse{}, false, fmt.Errorf("%w: unopened use store", ErrInvalid)
	}
	if err := validateDigest("authorization use lookup ref", authorizationRef); err != nil {
		return AuthorizationUse{}, false, err
	}
	store.closeMu.RLock()
	defer store.closeMu.RUnlock()
	if store.closed {
		return AuthorizationUse{}, false, fmt.Errorf("%w: closed use store", ErrInvalid)
	}
	if err := store.validateAuthorityLocked(ctx); err != nil {
		return AuthorizationUse{}, false, err
	}
	target := strings.TrimPrefix(authorizationRef, "sha256:") + ".json"
	payload, err := store.secureDirectory.readBounded(target, maximumAuthorizationUseBytes)
	if err != nil {
		if secureRecordNotExist(err) {
			return AuthorizationUse{}, false, nil
		}
		return AuthorizationUse{}, false, err
	}
	var use AuthorizationUse
	if err := json.Unmarshal(payload, &use); err != nil {
		return AuthorizationUse{}, false, fmt.Errorf("decode authorization use: %w", err)
	}
	canonical, err := json.Marshal(use)
	if err != nil {
		return AuthorizationUse{}, false, fmt.Errorf("re-encode authorization use: %w", err)
	}
	if err := use.Validate(); err != nil || !bytes.Equal(payload, canonical) ||
		use.AuthorizationRef != authorizationRef {
		if err != nil {
			return AuthorizationUse{}, false, fmt.Errorf("validate authorization use: %w", err)
		}
		return AuthorizationUse{}, false, fmt.Errorf(
			"%w: authorization-use record is non-canonical or misbound",
			ErrUntrustedPreimage,
		)
	}
	if err := store.validateAuthorityLocked(ctx); err != nil {
		return AuthorizationUse{}, false, err
	}
	return use, true, nil
}

func (store *DirectoryUseStore) publishExecutionResultAnchor(
	ctx context.Context,
	use AuthorizationUse,
	command ExecutionCommand,
	result ExecutionResult,
) error {
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
	anchor, err := newExecutionResultAnchor(use, command, result)
	if err != nil {
		return err
	}
	payload, err := json.Marshal(anchor)
	if err != nil {
		return fmt.Errorf("encode execution-result anchor: %w", err)
	}
	if len(payload) == 0 || len(payload) > maximumAuthorizationUseBytes {
		return fmt.Errorf(
			"%w: execution-result anchor exceeds its byte bound",
			ErrInvalid,
		)
	}
	target := executionResultAnchorTarget(use.AuthorizationRef)
	if err := store.secureDirectory.publishNoReplace(target, payload); err != nil {
		if !errors.Is(err, errSecureUseTargetExists) {
			return fmt.Errorf("commit execution-result anchor: %w", err)
		}
		existing, readErr := store.secureDirectory.readBounded(
			target,
			maximumAuthorizationUseBytes,
		)
		if readErr != nil {
			return fmt.Errorf(
				"%w: read existing execution-result anchor: %v",
				ErrExecutionResultCorrupt,
				readErr,
			)
		}
		var prior executionResultAnchor
		decodeErr := json.Unmarshal(existing, &prior)
		canonicalPrior, canonicalErr := json.Marshal(prior)
		if decodeErr != nil || canonicalErr != nil ||
			!bytes.Equal(existing, canonicalPrior) ||
			prior.Validate(use, command) != nil ||
			prior != anchor {
			return fmt.Errorf(
				"%w: conflicting execution-result anchor",
				ErrExecutionResultCorrupt,
			)
		}
	}
	if err := store.validateAuthorityLocked(ctx); err != nil {
		return err
	}
	return nil
}

func (store *DirectoryUseStore) executionResultAnchor(
	ctx context.Context,
	use AuthorizationUse,
	command ExecutionCommand,
) (executionResultAnchor, bool, error) {
	if store == nil {
		return executionResultAnchor{}, false,
			fmt.Errorf("%w: unopened use store", ErrInvalid)
	}
	store.closeMu.RLock()
	defer store.closeMu.RUnlock()
	if store.closed {
		return executionResultAnchor{}, false,
			fmt.Errorf("%w: closed use store", ErrInvalid)
	}
	if err := store.validateAuthorityLocked(ctx); err != nil {
		return executionResultAnchor{}, false, err
	}
	payload, err := store.secureDirectory.readBounded(
		executionResultAnchorTarget(use.AuthorizationRef),
		maximumAuthorizationUseBytes,
	)
	if err != nil {
		if secureRecordNotExist(err) {
			return executionResultAnchor{}, false, nil
		}
		return executionResultAnchor{}, false, fmt.Errorf(
			"%w: read execution-result anchor: %v",
			ErrExecutionResultCorrupt,
			err,
		)
	}
	var anchor executionResultAnchor
	if err := json.Unmarshal(payload, &anchor); err != nil {
		return executionResultAnchor{}, false, fmt.Errorf(
			"%w: decode execution-result anchor: %v",
			ErrExecutionResultCorrupt,
			err,
		)
	}
	canonical, err := json.Marshal(anchor)
	if err != nil || !bytes.Equal(payload, canonical) {
		return executionResultAnchor{}, false, fmt.Errorf(
			"%w: non-canonical execution-result anchor",
			ErrExecutionResultCorrupt,
		)
	}
	if err := anchor.Validate(use, command); err != nil {
		return executionResultAnchor{}, false, err
	}
	if err := store.validateAuthorityLocked(ctx); err != nil {
		return executionResultAnchor{}, false, err
	}
	return anchor, true, nil
}

func executionResultAnchorTarget(authorizationRef string) string {
	return strings.TrimPrefix(authorizationRef, "sha256:") +
		executionResultAnchorTargetSuffix
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
