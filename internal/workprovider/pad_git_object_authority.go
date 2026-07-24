package workprovider

import (
	"bytes"
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

const (
	PADGitAncestryEvidenceSchema = "gentle-ai.pad-git-ancestry-evidence/v1"

	padGitAncestryEvidenceDigestDomain = "gentle-ai.pad-git-ancestry-evidence-digest/v1"
	fixedPADGitOperationTimeout        = 15 * time.Second
	fixedPADGitOutputLimit             = 4 << 10
)

var (
	ErrPADGitObjectUnavailable = errors.New("PAD fixed Git object authority is unavailable")
	ErrPADGitObjectMismatch    = errors.New("PAD Git object does not match its owner binding")
)

type fixedPADGitOperation uint8

const (
	fixedPADGitObjectFormat fixedPADGitOperation = iota + 1
	fixedPADGitHeadCommit
	fixedPADGitObjectType
	fixedPADGitCommitTree
	fixedPADGitAncestry
)

// FixedPADGitObjectAuthority proves exact local Git objects through a closed
// set of read-only operations. Its executable, argv shape, repository root,
// environment, and timeout are owner-controlled; callers provide only already
// typed repository and full object identities.
type FixedPADGitObjectAuthority struct {
	authority      *PADRepositoryAuthority
	executable     string
	timeout        time.Duration
	objectFormat   string
	objectIDLength int
}

// NewFixedPADGitObjectAuthority retains the exact RepositoryIdentityLease
// already held by PADRepositoryAuthority. It never resolves a repository,
// executable, remote, ref, or argv from model-authored input.
func NewFixedPADGitObjectAuthority(
	ctx context.Context,
	authority *PADRepositoryAuthority,
) (*FixedPADGitObjectAuthority, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if authority == nil || authority.identity.lease == nil {
		return nil, fmt.Errorf(
			"%w: missing PAD repository authority",
			ErrPADGitObjectUnavailable,
		)
	}
	root, err := authority.ResolveDeliveryRepository(
		ctx,
		authority.RepositoryRef(),
	)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrPADGitObjectUnavailable, err)
	}
	if root != authority.identity.repositoryRoot {
		return nil, ErrPADRepositoryAuthorityMismatch
	}
	executable, err := exec.LookPath("git")
	if err != nil {
		return nil, fmt.Errorf(
			"%w: resolve fixed git executable: %w",
			ErrPADGitObjectUnavailable,
			err,
		)
	}
	executable, err = filepath.Abs(executable)
	if err != nil {
		return nil, fmt.Errorf(
			"%w: canonicalize fixed git executable: %w",
			ErrPADGitObjectUnavailable,
			err,
		)
	}
	objects := &FixedPADGitObjectAuthority{
		authority:  authority,
		executable: filepath.Clean(executable),
		timeout:    fixedPADGitOperationTimeout,
	}
	result, err := objects.run(
		ctx,
		root,
		fixedPADGitObjectFormat,
		"",
		"",
	)
	if err != nil {
		return nil, err
	}
	if result.exitCode != 0 || len(result.stderr) != 0 {
		return nil, fmt.Errorf(
			"%w: inspect repository object format",
			ErrPADGitObjectUnavailable,
		)
	}
	objects.objectFormat = strings.TrimSpace(string(result.stdout))
	switch objects.objectFormat {
	case "sha1":
		objects.objectIDLength = 40
	case "sha256":
		objects.objectIDLength = 64
	default:
		return nil, fmt.Errorf(
			"%w: unsupported repository object format %q",
			ErrPADGitObjectUnavailable,
			objects.objectFormat,
		)
	}
	if err := objects.revalidateRepository(
		ctx,
		authority.RepositoryRef(),
		root,
	); err != nil {
		return nil, err
	}
	return objects, nil
}

// RequireCommit proves that revision names one exact commit object in the
// retained repository. Only full lowercase SHA-1 and SHA-256 identities are
// accepted; symbolic, abbreviated, ambient, and replacement refs are rejected.
func (authority *FixedPADGitObjectAuthority) RequireCommit(
	ctx context.Context,
	repositoryRef string,
	revision string,
) error {
	_, err := authority.requireCommit(ctx, repositoryRef, revision, "")
	return err
}

// ResolveHeadRevision resolves the exact current HEAD commit through the same
// closed, environment-scrubbed Git boundary used for PAD object proofs. The
// symbolic name never escapes this owner method; callers receive one full
// object identity in PAD's git:<oid> form.
func (authority *FixedPADGitObjectAuthority) ResolveHeadRevision(
	ctx context.Context,
	repositoryRef string,
) (string, error) {
	root, err := authority.repositoryRoot(ctx, repositoryRef)
	if err != nil {
		return "", err
	}
	result, err := authority.run(
		ctx,
		root,
		fixedPADGitHeadCommit,
		"",
		"",
	)
	if err != nil {
		return "", err
	}
	if result.exitCode != 0 || len(result.stderr) != 0 {
		return "", fmt.Errorf(
			"%w: resolve exact HEAD commit",
			ErrPADGitObjectUnavailable,
		)
	}
	objectID := strings.TrimSpace(string(result.stdout))
	if !authority.validObjectID(objectID) {
		return "", fmt.Errorf(
			"%w: HEAD did not resolve to one full commit identity",
			ErrPADGitObjectMismatch,
		)
	}
	revision := "git:" + objectID
	if err := authority.RequireCommit(ctx, repositoryRef, revision); err != nil {
		return "", err
	}
	return revision, nil
}

// RequireCommitTree additionally proves that the exact commit resolves to the
// exact reviewed candidate tree. candidateTree is the raw full Git object ID
// used by native review receipts, while revision uses PAD's git:<oid> form.
func (authority *FixedPADGitObjectAuthority) RequireCommitTree(
	ctx context.Context,
	repositoryRef string,
	revision string,
	candidateTree string,
) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if !authority.validObjectID(candidateTree) {
		return fmt.Errorf(
			"%w: invalid candidate tree identity",
			ErrPADGitObjectMismatch,
		)
	}
	_, err := authority.requireCommit(
		ctx,
		repositoryRef,
		revision,
		candidateTree,
	)
	return err
}

// ObserveAncestry returns deterministic, content-addressed ancestry evidence
// after proving both endpoints are exact commits in the retained repository.
func (authority *FixedPADGitObjectAuthority) ObserveAncestry(
	ctx context.Context,
	repositoryRef string,
	baseRevision string,
	headRevision string,
) (PADGitAncestryObservation, error) {
	root, err := authority.repositoryRoot(ctx, repositoryRef)
	if err != nil {
		return PADGitAncestryObservation{}, err
	}
	baseObject, ok := authority.revisionObjectID(baseRevision)
	if !ok {
		return PADGitAncestryObservation{}, fmt.Errorf(
			"%w: invalid base commit identity",
			ErrPADGitObjectMismatch,
		)
	}
	headObject, ok := authority.revisionObjectID(headRevision)
	if !ok {
		return PADGitAncestryObservation{}, fmt.Errorf(
			"%w: invalid head commit identity",
			ErrPADGitObjectMismatch,
		)
	}
	if err := authority.requireCommitAtRoot(
		ctx,
		root,
		baseRevision,
		baseObject,
		"",
	); err != nil {
		return PADGitAncestryObservation{}, err
	}
	if err := authority.requireCommitAtRoot(
		ctx,
		root,
		headRevision,
		headObject,
		"",
	); err != nil {
		return PADGitAncestryObservation{}, err
	}
	result, err := authority.run(
		ctx,
		root,
		fixedPADGitAncestry,
		baseObject,
		headObject,
	)
	if err != nil {
		return PADGitAncestryObservation{}, err
	}
	if len(result.stdout) != 0 || len(result.stderr) != 0 {
		return PADGitAncestryObservation{}, fmt.Errorf(
			"%w: ancestry operation emitted unexpected output",
			ErrPADGitObjectUnavailable,
		)
	}
	var state PADGitAncestryState
	switch result.exitCode {
	case 0:
		state = PADGitAncestor
	case 1:
		state = PADGitNotAncestor
	default:
		return PADGitAncestryObservation{}, fmt.Errorf(
			"%w: ancestry operation failed with exit code %d",
			ErrPADGitObjectUnavailable,
			result.exitCode,
		)
	}
	if err := authority.revalidateRepository(ctx, repositoryRef, root); err != nil {
		return PADGitAncestryObservation{}, err
	}
	observation := PADGitAncestryObservation{
		RepositoryRef: repositoryRef,
		BaseRevision:  baseRevision,
		HeadRevision:  headRevision,
		State:         state,
	}
	observation.EvidenceRef = padGitAncestryEvidenceRef(observation)
	if err := observation.Validate(
		repositoryRef,
		baseRevision,
		headRevision,
	); err != nil {
		return PADGitAncestryObservation{}, fmt.Errorf(
			"%w: %w",
			ErrPADGitObjectUnavailable,
			err,
		)
	}
	return observation, nil
}

func (authority *FixedPADGitObjectAuthority) requireCommit(
	ctx context.Context,
	repositoryRef string,
	revision string,
	expectedTree string,
) (string, error) {
	root, err := authority.repositoryRoot(ctx, repositoryRef)
	if err != nil {
		return "", err
	}
	objectID, ok := authority.revisionObjectID(revision)
	if !ok {
		return "", fmt.Errorf(
			"%w: invalid commit identity",
			ErrPADGitObjectMismatch,
		)
	}
	if err := authority.requireCommitAtRoot(
		ctx,
		root,
		revision,
		objectID,
		expectedTree,
	); err != nil {
		return "", err
	}
	if err := authority.revalidateRepository(ctx, repositoryRef, root); err != nil {
		return "", err
	}
	return objectID, nil
}

func (authority *FixedPADGitObjectAuthority) requireCommitAtRoot(
	ctx context.Context,
	root string,
	revision string,
	objectID string,
	expectedTree string,
) error {
	result, err := authority.run(
		ctx,
		root,
		fixedPADGitObjectType,
		objectID,
		"",
	)
	if err != nil {
		return err
	}
	if result.exitCode != 0 ||
		len(result.stderr) != 0 ||
		strings.TrimSpace(string(result.stdout)) != "commit" {
		return fmt.Errorf(
			"%w: %s is not an exact commit object",
			ErrPADGitObjectMismatch,
			revision,
		)
	}
	if expectedTree == "" {
		return nil
	}
	result, err = authority.run(
		ctx,
		root,
		fixedPADGitCommitTree,
		objectID,
		"",
	)
	if err != nil {
		return err
	}
	tree := strings.TrimSpace(string(result.stdout))
	if result.exitCode != 0 ||
		len(result.stderr) != 0 ||
		!authority.validObjectID(tree) ||
		tree != expectedTree {
		return fmt.Errorf(
			"%w: commit %s does not resolve to candidate tree %s",
			ErrPADGitObjectMismatch,
			revision,
			expectedTree,
		)
	}
	return nil
}

func (authority *FixedPADGitObjectAuthority) repositoryRoot(
	ctx context.Context,
	repositoryRef string,
) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if authority == nil ||
		authority.authority == nil ||
		authority.authority.identity.lease == nil ||
		authority.timeout <= 0 ||
		!((authority.objectFormat == "sha1" &&
			authority.objectIDLength == 40) ||
			(authority.objectFormat == "sha256" &&
				authority.objectIDLength == 64)) ||
		!filepath.IsAbs(authority.executable) ||
		filepath.Clean(authority.executable) != authority.executable {
		return "", ErrPADGitObjectUnavailable
	}
	if !validPADImmutableRef(repositoryRef) ||
		repositoryRef != authority.authority.RepositoryRef() {
		return "", ErrPADRepositoryAuthorityMismatch
	}
	root, err := authority.authority.ResolveDeliveryRepository(
		ctx,
		repositoryRef,
	)
	if err != nil {
		return "", fmt.Errorf(
			"%w: resolve exact repository: %w",
			ErrPADRepositoryAuthorityMismatch,
			err,
		)
	}
	if root != authority.authority.identity.repositoryRoot {
		return "", ErrPADRepositoryAuthorityMismatch
	}
	return root, nil
}

func (authority *FixedPADGitObjectAuthority) revalidateRepository(
	ctx context.Context,
	repositoryRef string,
	expectedRoot string,
) error {
	root, err := authority.repositoryRoot(ctx, repositoryRef)
	if err != nil {
		return err
	}
	if root != expectedRoot {
		return ErrPADRepositoryAuthorityMismatch
	}
	return nil
}

type fixedPADGitResult struct {
	stdout   []byte
	stderr   []byte
	exitCode int
}

func (authority *FixedPADGitObjectAuthority) run(
	ctx context.Context,
	root string,
	operation fixedPADGitOperation,
	firstObject string,
	secondObject string,
) (fixedPADGitResult, error) {
	if err := ctx.Err(); err != nil {
		return fixedPADGitResult{}, err
	}
	args, err := fixedPADGitArguments(
		operation,
		firstObject,
		secondObject,
	)
	if err != nil {
		return fixedPADGitResult{}, err
	}
	operationContext, cancel := context.WithTimeout(ctx, authority.timeout)
	defer cancel()
	commandArgs := append(
		[]string{
			"--no-replace-objects",
			"--no-optional-locks",
			"-C",
			root,
		},
		args...,
	)
	command := exec.CommandContext(
		operationContext,
		authority.executable,
		commandArgs...,
	)
	command.WaitDelay = time.Second
	command.Env = fixedPADGitEnvironment(os.Environ())
	var stdout, stderr fixedPADGitOutput
	stdout.limit = fixedPADGitOutputLimit
	stderr.limit = fixedPADGitOutputLimit
	command.Stdout = &stdout
	command.Stderr = &stderr
	runErr := command.Run()
	if operationContext.Err() != nil {
		cause := operationContext.Err()
		if ctx.Err() != nil {
			cause = ctx.Err()
		}
		return fixedPADGitResult{}, fmt.Errorf(
			"%w: fixed Git operation timed out or was cancelled: %w",
			ErrPADGitObjectUnavailable,
			cause,
		)
	}
	if stdout.exceeded || stderr.exceeded {
		return fixedPADGitResult{}, fmt.Errorf(
			"%w: fixed Git operation exceeded its output limit",
			ErrPADGitObjectUnavailable,
		)
	}
	result := fixedPADGitResult{
		stdout: append([]byte(nil), stdout.Bytes()...),
		stderr: append([]byte(nil), stderr.Bytes()...),
	}
	if runErr == nil {
		result.exitCode = 0
		return result, nil
	}
	var exitErr *exec.ExitError
	if errors.As(runErr, &exitErr) {
		result.exitCode = exitErr.ExitCode()
		return result, nil
	}
	return fixedPADGitResult{}, fmt.Errorf(
		"%w: start fixed Git operation: %w",
		ErrPADGitObjectUnavailable,
		runErr,
	)
}

func fixedPADGitArguments(
	operation fixedPADGitOperation,
	firstObject string,
	secondObject string,
) ([]string, error) {
	if operation == fixedPADGitObjectFormat {
		if firstObject != "" || secondObject != "" {
			return nil, fmt.Errorf(
				"%w: invalid fixed Git object-format operation",
				ErrPADGitObjectMismatch,
			)
		}
		return []string{"rev-parse", "--show-object-format"}, nil
	}
	if operation == fixedPADGitHeadCommit {
		if firstObject != "" || secondObject != "" {
			return nil, fmt.Errorf(
				"%w: invalid fixed Git HEAD operation",
				ErrPADGitObjectMismatch,
			)
		}
		return []string{
			"rev-parse",
			"--verify",
			"HEAD^{commit}",
		}, nil
	}
	if !validPADGitObjectID(firstObject) {
		return nil, fmt.Errorf(
			"%w: invalid fixed Git object identity",
			ErrPADGitObjectMismatch,
		)
	}
	switch operation {
	case fixedPADGitObjectType:
		if secondObject != "" {
			break
		}
		return []string{"cat-file", "-t", firstObject}, nil
	case fixedPADGitCommitTree:
		if secondObject != "" {
			break
		}
		return []string{
			"rev-parse",
			"--verify",
			firstObject + "^{tree}",
		}, nil
	case fixedPADGitAncestry:
		if !validPADGitObjectID(secondObject) {
			break
		}
		return []string{
			"merge-base",
			"--is-ancestor",
			firstObject,
			secondObject,
		}, nil
	}
	return nil, fmt.Errorf(
		"%w: invalid fixed Git operation",
		ErrPADGitObjectMismatch,
	)
}

func fixedPADGitEnvironment(environment []string) []string {
	result := make([]string, 0, len(environment)+9)
	for _, entry := range environment {
		name, _, ok := strings.Cut(entry, "=")
		if !ok {
			continue
		}
		upper := strings.ToUpper(name)
		if strings.HasPrefix(upper, "GIT_") ||
			upper == "LANG" ||
			upper == "LANGUAGE" ||
			strings.HasPrefix(upper, "LC_") {
			continue
		}
		result = append(result, entry)
	}
	return append(
		result,
		"GIT_CONFIG_NOSYSTEM=1",
		"GIT_CONFIG_SYSTEM="+os.DevNull,
		"GIT_CONFIG_GLOBAL="+os.DevNull,
		"GIT_CONFIG_COUNT=0",
		"GIT_ATTR_NOSYSTEM=1",
		"GIT_NO_LAZY_FETCH=1",
		"GIT_TERMINAL_PROMPT=0",
		"LANG=C",
		"LC_ALL=C",
	)
}

type fixedPADGitOutput struct {
	bytes.Buffer
	limit    int
	exceeded bool
}

func (output *fixedPADGitOutput) Write(payload []byte) (int, error) {
	size := len(payload)
	remaining := output.limit - output.Len()
	if remaining > 0 {
		if remaining > size {
			remaining = size
		}
		_, _ = output.Buffer.Write(payload[:remaining])
	}
	if size > remaining {
		output.exceeded = true
	}
	return size, nil
}

func (authority *FixedPADGitObjectAuthority) revisionObjectID(
	revision string,
) (string, bool) {
	if !validPADGitRevision(revision) {
		return "", false
	}
	objectID := strings.TrimPrefix(revision, "git:")
	return objectID, authority.validObjectID(objectID)
}

func (authority *FixedPADGitObjectAuthority) validObjectID(
	objectID string,
) bool {
	return authority != nil &&
		len(objectID) == authority.objectIDLength &&
		validPADGitObjectID(objectID)
}

func validPADGitObjectID(objectID string) bool {
	if len(objectID) != 40 && len(objectID) != 64 {
		return false
	}
	if strings.ToLower(objectID) != objectID {
		return false
	}
	_, err := hex.DecodeString(objectID)
	return err == nil
}

func padGitAncestryEvidenceRef(
	observation PADGitAncestryObservation,
) string {
	preimage := struct {
		Schema         string              `json:"schema"`
		RepositoryRef  string              `json:"repository_ref"`
		BaseRevision   string              `json:"base_revision"`
		HeadRevision   string              `json:"head_revision"`
		AncestryResult PADGitAncestryState `json:"ancestry_result"`
	}{
		Schema:         PADGitAncestryEvidenceSchema,
		RepositoryRef:  observation.RepositoryRef,
		BaseRevision:   observation.BaseRevision,
		HeadRevision:   observation.HeadRevision,
		AncestryResult: observation.State,
	}
	return padDeliveryDigest(
		padGitAncestryEvidenceDigestDomain,
		preimage,
	)
}

var _ PADGitObjectAuthority = (*FixedPADGitObjectAuthority)(nil)
