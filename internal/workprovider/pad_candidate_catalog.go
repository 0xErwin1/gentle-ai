package workprovider

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/gentleman-programming/gentle-ai/internal/deliveryadmission"
	"github.com/gentleman-programming/gentle-ai/internal/reviewtransaction"
)

const (
	padCandidateCatalogRecordSchema = "gentle-ai.pad-candidate-catalog-record/v1"
	padCandidateCatalogLookupSchema = "gentle-ai.pad-candidate-catalog-lookup/v1"
	padCandidateCatalogIndexSchema  = "gentle-ai.pad-candidate-catalog-index/v1"
	padCandidateCatalogVersion      = "v1"

	padCandidateCatalogRecordDigestDomain = "gentle-ai.pad-candidate-catalog-record-digest/v1"
	padCandidateCatalogLookupDigestDomain = "gentle-ai.pad-candidate-catalog-lookup-digest/v1"
	padCandidateCatalogLockTimeout        = 2 * time.Second
	padCandidateCatalogLockPoll           = 10 * time.Millisecond
)

var (
	ErrPADCandidateCatalogUnavailable = errors.New("PAD candidate catalog is unavailable")
	ErrPADCandidateCatalogCorrupt     = errors.New("PAD candidate catalog is corrupt")
	ErrPADCandidateCatalogConflict    = errors.New("PAD candidate catalog binding conflicts")
)

// PADCandidateCatalog is an append-only owner mapping from PAD's opaque
// candidate identity and reviewed Git tree to one exact commit and delivery
// destination. It implements PADGitBindingAuthority without observing HEAD,
// origin, branches, remotes, pull requests, or caller argv.
type PADCandidateCatalog struct {
	authority *PADRepositoryAuthority
	objects   *FixedPADGitObjectAuthority
	root      string
}

type padCandidateCatalogLookup struct {
	Schema        string                               `json:"schema"`
	RepositoryRef string                               `json:"repository_ref"`
	Candidate     deliveryadmission.CandidateBinding   `json:"candidate"`
	Destination   deliveryadmission.DestinationBinding `json:"destination"`
	Mechanism     deliveryadmission.Mechanism          `json:"mechanism"`
}

type padCandidateCatalogRecord struct {
	Schema        string        `json:"schema"`
	RepositoryRef string        `json:"repository_ref"`
	LookupRef     string        `json:"lookup_ref"`
	CandidateTree string        `json:"candidate_tree"`
	Binding       PADGitBinding `json:"binding"`
	RecordRef     string        `json:"record_ref"`
}

type padCandidateCatalogIndex struct {
	Schema        string `json:"schema"`
	RepositoryRef string `json:"repository_ref"`
	LookupRef     string `json:"lookup_ref"`
	RecordRef     string `json:"record_ref"`
}

// NewPADCandidateCatalog retains the same exact repository authority and
// RepositoryIdentityLease as its fixed Git object authority.
func NewPADCandidateCatalog(
	ctx context.Context,
	authority *PADRepositoryAuthority,
	objects *FixedPADGitObjectAuthority,
) (*PADCandidateCatalog, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if authority == nil ||
		authority.identity.lease == nil ||
		objects == nil ||
		objects.authority != authority {
		return nil, fmt.Errorf(
			"%w: catalog and Git proof must share one repository lease",
			ErrPADCandidateCatalogUnavailable,
		)
	}
	root, err := authority.ResolveDeliveryRepository(
		ctx,
		authority.RepositoryRef(),
	)
	if err != nil {
		return nil, fmt.Errorf(
			"%w: resolve exact repository: %w",
			ErrPADCandidateCatalogUnavailable,
			err,
		)
	}
	if root != authority.identity.repositoryRoot {
		return nil, ErrPADRepositoryAuthorityMismatch
	}
	catalogRoot := filepath.Join(
		authority.identity.gitCommonDir,
		"gentle-ai",
		"work-provider",
		"pad-candidate-catalog",
		padCandidateCatalogVersion,
		"repositories",
		authority.identity.lease.StorageKey(),
	)
	if err := ensurePADCandidateCatalogDirectories(
		authority.identity.gitCommonDir,
		catalogRoot,
	); err != nil {
		return nil, fmt.Errorf(
			"%w: open durable catalog: %w",
			ErrPADCandidateCatalogUnavailable,
			err,
		)
	}
	catalog := &PADCandidateCatalog{
		authority: authority,
		objects:   objects,
		root:      catalogRoot,
	}
	if err := catalog.validate(ctx); err != nil {
		return nil, err
	}
	return catalog, nil
}

// BindCandidate atomically and idempotently occupies the candidate,
// destination, and mechanism slot. The exact candidate commit must exist and
// resolve to candidateTree; the expected remote must also be an exact commit.
// Once occupied, a slot can never be rebound to another commit or host fact.
func (catalog *PADCandidateCatalog) BindCandidate(
	ctx context.Context,
	candidateTree string,
	binding PADGitBinding,
) (PADGitBinding, error) {
	record, err := catalog.newRecord(candidateTree, binding)
	if err != nil {
		return PADGitBinding{}, err
	}
	var resolved PADGitBinding
	err = catalog.withLookupLock(ctx, record.LookupRef, func() error {
		if err := catalog.requireRecordObjects(ctx, record); err != nil {
			return err
		}
		index, exists, err := catalog.readIndex(record.LookupRef)
		if err != nil {
			return err
		}
		if exists {
			existing, err := catalog.readRecord(index, record.LookupRef)
			if err != nil {
				return err
			}
			if existing != record {
				return ErrPADCandidateCatalogConflict
			}
			resolved = existing.Binding
			return nil
		}
		recordPayload, err := canonicalCoordinationPayload(record)
		if err != nil {
			return err
		}
		index = padCandidateCatalogIndex{
			Schema:        padCandidateCatalogIndexSchema,
			RepositoryRef: catalog.authority.RepositoryRef(),
			LookupRef:     record.LookupRef,
			RecordRef:     record.RecordRef,
		}
		indexPayload, err := canonicalCoordinationPayload(index)
		if err != nil {
			return err
		}
		if err := publishPrivateCoordinationImmutable(
			catalog.recordPath(record.RecordRef),
			recordPayload,
		); err != nil {
			return catalog.publishError(err)
		}
		if err := publishPrivateCoordinationImmutable(
			catalog.indexPath(record.LookupRef),
			indexPayload,
		); err != nil {
			return catalog.publishError(err)
		}
		reloadedIndex, exists, err := catalog.readIndex(record.LookupRef)
		if err != nil {
			return err
		}
		if !exists {
			return fmt.Errorf(
				"%w: published index disappeared",
				ErrPADCandidateCatalogCorrupt,
			)
		}
		reloaded, err := catalog.readRecord(
			reloadedIndex,
			record.LookupRef,
		)
		if err != nil {
			return err
		}
		if reloaded != record {
			return ErrPADCandidateCatalogConflict
		}
		resolved = reloaded.Binding
		return nil
	})
	if err != nil {
		return PADGitBinding{}, err
	}
	return resolved, nil
}

// ResolvePADGitBinding resolves only the immutable catalog slot identified by
// the caller's already-authorized PAD bindings. Every resolution re-proves the
// exact commit/tree objects and retained repository identity.
func (catalog *PADCandidateCatalog) ResolvePADGitBinding(
	ctx context.Context,
	candidate deliveryadmission.CandidateBinding,
	destination deliveryadmission.DestinationBinding,
	mechanism deliveryadmission.Mechanism,
) (PADGitBinding, error) {
	lookup, lookupRef, err := catalog.lookup(
		candidate,
		destination,
		mechanism,
	)
	if err != nil {
		return PADGitBinding{}, err
	}
	var binding PADGitBinding
	err = catalog.withLookupLock(ctx, lookupRef, func() error {
		index, exists, err := catalog.readIndex(lookupRef)
		if err != nil {
			return err
		}
		if !exists {
			return fmt.Errorf(
				"%w: candidate binding was not published",
				ErrPADCandidateCatalogUnavailable,
			)
		}
		record, err := catalog.readRecord(index, lookupRef)
		if err != nil {
			return err
		}
		if record.Binding.Candidate != lookup.Candidate ||
			record.Binding.Destination != lookup.Destination ||
			record.Binding.Mechanism != lookup.Mechanism {
			return fmt.Errorf(
				"%w: lookup resolved another candidate binding",
				ErrPADCandidateCatalogCorrupt,
			)
		}
		if err := catalog.requireRecordObjects(ctx, record); err != nil {
			return err
		}
		binding = record.Binding
		return nil
	})
	if err != nil {
		return PADGitBinding{}, err
	}
	return binding, nil
}

func (catalog *PADCandidateCatalog) newRecord(
	candidateTree string,
	binding PADGitBinding,
) (padCandidateCatalogRecord, error) {
	if catalog == nil || catalog.authority == nil {
		return padCandidateCatalogRecord{}, ErrPADCandidateCatalogUnavailable
	}
	if err := binding.Validate(
		binding.Candidate,
		binding.Destination,
		binding.Mechanism,
	); err != nil {
		return padCandidateCatalogRecord{}, err
	}
	if binding.Destination.RepositoryRef != catalog.authority.RepositoryRef() {
		return padCandidateCatalogRecord{}, ErrPADRepositoryAuthorityMismatch
	}
	if binding.ExpectedRemoteRevision !=
		binding.Destination.ObservedRevision {
		return padCandidateCatalogRecord{}, fmt.Errorf(
			"%w: expected remote differs from destination observation",
			ErrPADCandidateCatalogCorrupt,
		)
	}
	if !validPADGitObjectID(candidateTree) {
		return padCandidateCatalogRecord{}, fmt.Errorf(
			"%w: invalid reviewed candidate tree",
			ErrPADCandidateCatalogCorrupt,
		)
	}
	_, lookupRef, err := catalog.lookup(
		binding.Candidate,
		binding.Destination,
		binding.Mechanism,
	)
	if err != nil {
		return padCandidateCatalogRecord{}, err
	}
	record := padCandidateCatalogRecord{
		Schema:        padCandidateCatalogRecordSchema,
		RepositoryRef: catalog.authority.RepositoryRef(),
		LookupRef:     lookupRef,
		CandidateTree: candidateTree,
		Binding:       binding,
	}
	record.RecordRef = padCandidateCatalogRecordRef(record)
	if err := record.Validate(catalog.authority.RepositoryRef()); err != nil {
		return padCandidateCatalogRecord{}, err
	}
	return record, nil
}

func (catalog *PADCandidateCatalog) lookup(
	candidate deliveryadmission.CandidateBinding,
	destination deliveryadmission.DestinationBinding,
	mechanism deliveryadmission.Mechanism,
) (padCandidateCatalogLookup, string, error) {
	if catalog == nil || catalog.authority == nil {
		return padCandidateCatalogLookup{}, "",
			ErrPADCandidateCatalogUnavailable
	}
	lookup := padCandidateCatalogLookup{
		Schema:        padCandidateCatalogLookupSchema,
		RepositoryRef: catalog.authority.RepositoryRef(),
		Candidate:     candidate,
		Destination:   destination,
		Mechanism:     mechanism,
	}
	if err := lookup.Validate(catalog.authority.RepositoryRef()); err != nil {
		return padCandidateCatalogLookup{}, "", err
	}
	return lookup, padCandidateCatalogLookupRef(lookup), nil
}

func (lookup padCandidateCatalogLookup) Validate(
	repositoryRef string,
) error {
	if lookup.Schema != padCandidateCatalogLookupSchema ||
		lookup.RepositoryRef != repositoryRef ||
		lookup.Destination.RepositoryRef != repositoryRef ||
		!validPADImmutableRef(repositoryRef) ||
		!validPADCandidateBinding(lookup.Candidate) ||
		!validPADDestinationBinding(lookup.Destination) {
		return fmt.Errorf(
			"%w: invalid catalog lookup binding",
			ErrPADCandidateCatalogCorrupt,
		)
	}
	switch lookup.Mechanism {
	case deliveryadmission.MechanismFastForwardOnly,
		deliveryadmission.MechanismPullRequest:
		return nil
	default:
		return fmt.Errorf(
			"%w: invalid catalog mechanism",
			ErrPADCandidateCatalogCorrupt,
		)
	}
}

func (record padCandidateCatalogRecord) Validate(
	repositoryRef string,
) error {
	if record.Schema != padCandidateCatalogRecordSchema ||
		record.RepositoryRef != repositoryRef ||
		!validPADImmutableRef(record.LookupRef) ||
		!validPADImmutableRef(record.RecordRef) ||
		!validPADGitObjectID(record.CandidateTree) {
		return fmt.Errorf(
			"%w: invalid candidate record header",
			ErrPADCandidateCatalogCorrupt,
		)
	}
	if err := record.Binding.Validate(
		record.Binding.Candidate,
		record.Binding.Destination,
		record.Binding.Mechanism,
	); err != nil {
		return fmt.Errorf("%w: %w", ErrPADCandidateCatalogCorrupt, err)
	}
	if record.Binding.Destination.RepositoryRef != repositoryRef ||
		record.Binding.ExpectedRemoteRevision !=
			record.Binding.Destination.ObservedRevision {
		return fmt.Errorf(
			"%w: record repository or expected remote mismatch",
			ErrPADCandidateCatalogCorrupt,
		)
	}
	lookup := padCandidateCatalogLookup{
		Schema:        padCandidateCatalogLookupSchema,
		RepositoryRef: repositoryRef,
		Candidate:     record.Binding.Candidate,
		Destination:   record.Binding.Destination,
		Mechanism:     record.Binding.Mechanism,
	}
	if err := lookup.Validate(repositoryRef); err != nil ||
		record.LookupRef != padCandidateCatalogLookupRef(lookup) ||
		record.RecordRef != padCandidateCatalogRecordRef(record) {
		return fmt.Errorf(
			"%w: candidate record content address mismatch",
			ErrPADCandidateCatalogCorrupt,
		)
	}
	return nil
}

func (index padCandidateCatalogIndex) Validate(
	repositoryRef string,
	lookupRef string,
) error {
	if index.Schema != padCandidateCatalogIndexSchema ||
		index.RepositoryRef != repositoryRef ||
		index.LookupRef != lookupRef ||
		!validPADImmutableRef(index.LookupRef) ||
		!validPADImmutableRef(index.RecordRef) {
		return fmt.Errorf(
			"%w: invalid candidate catalog index",
			ErrPADCandidateCatalogCorrupt,
		)
	}
	return nil
}

func (catalog *PADCandidateCatalog) requireRecordObjects(
	ctx context.Context,
	record padCandidateCatalogRecord,
) error {
	if err := catalog.objects.RequireCommitTree(
		ctx,
		record.RepositoryRef,
		record.Binding.CandidateRevision,
		record.CandidateTree,
	); err != nil {
		return fmt.Errorf(
			"%w: candidate commit/tree proof: %w",
			ErrPADCandidateCatalogCorrupt,
			err,
		)
	}
	if err := catalog.objects.RequireCommit(
		ctx,
		record.RepositoryRef,
		record.Binding.ExpectedRemoteRevision,
	); err != nil {
		return fmt.Errorf(
			"%w: expected remote commit proof: %w",
			ErrPADCandidateCatalogCorrupt,
			err,
		)
	}
	return nil
}

func (catalog *PADCandidateCatalog) readIndex(
	lookupRef string,
) (padCandidateCatalogIndex, bool, error) {
	payload, err := readPrivateCoordinationFile(
		catalog.indexPath(lookupRef),
	)
	if errors.Is(err, fs.ErrNotExist) {
		return padCandidateCatalogIndex{}, false, nil
	}
	if err != nil {
		return padCandidateCatalogIndex{}, false, fmt.Errorf(
			"%w: read candidate index: %w",
			ErrPADCandidateCatalogCorrupt,
			err,
		)
	}
	var index padCandidateCatalogIndex
	if err := decodeStrictCoordinationJSON(payload, &index); err != nil {
		return padCandidateCatalogIndex{}, false, fmt.Errorf(
			"%w: decode candidate index: %w",
			ErrPADCandidateCatalogCorrupt,
			err,
		)
	}
	canonical, err := canonicalCoordinationPayload(index)
	if err != nil ||
		!bytes.Equal(payload, canonical) ||
		index.Validate(
			catalog.authority.RepositoryRef(),
			lookupRef,
		) != nil {
		return padCandidateCatalogIndex{}, false, fmt.Errorf(
			"%w: candidate index is not canonical",
			ErrPADCandidateCatalogCorrupt,
		)
	}
	return index, true, nil
}

func (catalog *PADCandidateCatalog) readRecord(
	index padCandidateCatalogIndex,
	lookupRef string,
) (padCandidateCatalogRecord, error) {
	if err := index.Validate(
		catalog.authority.RepositoryRef(),
		lookupRef,
	); err != nil {
		return padCandidateCatalogRecord{}, err
	}
	payload, err := readPrivateCoordinationFile(
		catalog.recordPath(index.RecordRef),
	)
	if err != nil {
		return padCandidateCatalogRecord{}, fmt.Errorf(
			"%w: read candidate record: %w",
			ErrPADCandidateCatalogCorrupt,
			err,
		)
	}
	var record padCandidateCatalogRecord
	if err := decodeStrictCoordinationJSON(payload, &record); err != nil {
		return padCandidateCatalogRecord{}, fmt.Errorf(
			"%w: decode candidate record: %w",
			ErrPADCandidateCatalogCorrupt,
			err,
		)
	}
	canonical, err := canonicalCoordinationPayload(record)
	if err != nil ||
		!bytes.Equal(payload, canonical) ||
		record.RecordRef != index.RecordRef ||
		record.LookupRef != lookupRef ||
		record.Validate(catalog.authority.RepositoryRef()) != nil {
		return padCandidateCatalogRecord{}, fmt.Errorf(
			"%w: candidate record is not canonical",
			ErrPADCandidateCatalogCorrupt,
		)
	}
	return record, nil
}

func (catalog *PADCandidateCatalog) withLookupLock(
	ctx context.Context,
	lookupRef string,
	action func() error,
) error {
	if catalog == nil ||
		action == nil ||
		!validPADImmutableRef(lookupRef) {
		return ErrPADCandidateCatalogUnavailable
	}
	if err := catalog.validate(ctx); err != nil {
		return err
	}
	timer := time.NewTimer(padCandidateCatalogLockTimeout)
	defer timer.Stop()
	ticker := time.NewTicker(padCandidateCatalogLockPoll)
	defer ticker.Stop()
	for {
		lock, err := reviewtransaction.AcquireAuthorityFileLock(
			catalog.lockPath(lookupRef),
		)
		if err == nil {
			defer lock.Release()
			if err := catalog.validate(ctx); err != nil {
				return err
			}
			if err := action(); err != nil {
				return err
			}
			return catalog.validate(ctx)
		}
		if !errors.Is(err, reviewtransaction.ErrConcurrentUpdate) {
			return fmt.Errorf(
				"%w: acquire candidate binding lock: %w",
				ErrPADCandidateCatalogUnavailable,
				err,
			)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-timer.C:
			return fmt.Errorf(
				"%w: candidate binding lock timed out",
				ErrPADCandidateCatalogUnavailable,
			)
		case <-ticker.C:
		}
	}
}

func (catalog *PADCandidateCatalog) validate(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if catalog == nil ||
		catalog.authority == nil ||
		catalog.authority.identity.lease == nil ||
		catalog.objects == nil ||
		catalog.objects.authority != catalog.authority {
		return ErrPADCandidateCatalogUnavailable
	}
	root, err := catalog.authority.ResolveDeliveryRepository(
		ctx,
		catalog.authority.RepositoryRef(),
	)
	if err != nil {
		return fmt.Errorf(
			"%w: revalidate exact repository: %w",
			ErrPADCandidateCatalogUnavailable,
			err,
		)
	}
	if root != catalog.authority.identity.repositoryRoot {
		return ErrPADRepositoryAuthorityMismatch
	}
	expectedRoot := filepath.Join(
		catalog.authority.identity.gitCommonDir,
		"gentle-ai",
		"work-provider",
		"pad-candidate-catalog",
		padCandidateCatalogVersion,
		"repositories",
		catalog.authority.identity.lease.StorageKey(),
	)
	if catalog.root != expectedRoot {
		return ErrPADRepositoryAuthorityMismatch
	}
	for _, directory := range []string{
		catalog.root,
		filepath.Join(catalog.root, "objects"),
		filepath.Join(catalog.root, "bindings"),
		filepath.Join(catalog.root, "locks"),
	} {
		if err := validatePrivateCoordinationDirectory(directory); err != nil {
			return fmt.Errorf(
				"%w: validate catalog directory: %w",
				ErrPADCandidateCatalogUnavailable,
				err,
			)
		}
	}
	return nil
}

func (catalog *PADCandidateCatalog) publishError(err error) error {
	if errors.Is(err, ErrCoordinationAuthorityConflict) {
		return fmt.Errorf("%w: immutable slot occupied", ErrPADCandidateCatalogConflict)
	}
	return fmt.Errorf(
		"%w: publish immutable catalog artifact: %w",
		ErrPADCandidateCatalogUnavailable,
		err,
	)
}

func (catalog *PADCandidateCatalog) indexPath(ref string) string {
	return filepath.Join(catalog.root, "bindings", padCandidateCatalogFilename(ref))
}

func (catalog *PADCandidateCatalog) recordPath(ref string) string {
	return filepath.Join(catalog.root, "objects", padCandidateCatalogFilename(ref))
}

func (catalog *PADCandidateCatalog) lockPath(ref string) string {
	return filepath.Join(
		catalog.root,
		"locks",
		strings.TrimPrefix(ref, "sha256:")+".lock",
	)
}

func padCandidateCatalogFilename(ref string) string {
	return strings.TrimPrefix(ref, "sha256:") + ".json"
}

func padCandidateCatalogLookupRef(
	lookup padCandidateCatalogLookup,
) string {
	return padDeliveryDigest(
		padCandidateCatalogLookupDigestDomain,
		lookup,
	)
}

func padCandidateCatalogRecordRef(
	record padCandidateCatalogRecord,
) string {
	record.RecordRef = ""
	return padDeliveryDigest(
		padCandidateCatalogRecordDigestDomain,
		record,
	)
}

func ensurePADCandidateCatalogDirectories(
	commonDir string,
	root string,
) error {
	if err := validateSharedCoordinationDirectory(commonDir); err != nil {
		return err
	}
	gentleRoot := filepath.Join(commonDir, "gentle-ai")
	if _, err := os.Lstat(gentleRoot); errors.Is(err, fs.ErrNotExist) {
		if err := os.Mkdir(gentleRoot, 0o755); err != nil &&
			!errors.Is(err, fs.ErrExist) {
			return err
		}
		if err := reviewtransaction.SyncReviewDirectory(commonDir); err != nil {
			return err
		}
	} else if err != nil {
		return err
	}
	if err := validateSharedCoordinationDirectory(gentleRoot); err != nil {
		return err
	}
	workProviderRoot := filepath.Join(gentleRoot, "work-provider")
	created, err := createPrivateCoordinationDirectory(workProviderRoot)
	if err != nil {
		return err
	}
	if created {
		if err := reviewtransaction.SyncReviewDirectory(gentleRoot); err != nil {
			return err
		}
	}
	for _, directory := range []string{
		root,
		filepath.Join(root, "objects"),
		filepath.Join(root, "bindings"),
		filepath.Join(root, "locks"),
	} {
		if err := ensurePrivateCoordinationDirectoryTree(
			workProviderRoot,
			directory,
			true,
		); err != nil {
			return err
		}
	}
	return nil
}

var _ PADGitBindingAuthority = (*PADCandidateCatalog)(nil)
