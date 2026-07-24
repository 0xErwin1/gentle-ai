package evidence

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"errors"
	"fmt"
)

const (
	semanticAuthoritySeedRecordDomain = "gentle-ai.semantic-authority-seed/v1"
	semanticAuthoritySeedRecordBytes  = len(semanticAuthoritySeedRecordDomain) +
		1 + SemanticAuthoritySeedBytes + sha256.Size
)

// SemanticAuthority provisions or restores the one owner-held signer for an
// exact semantic requirement. The seed never leaves this provider-private
// store: callers receive only a signer whose private key fields are
// unexported.
func (store Store) SemanticAuthority(
	ctx context.Context,
	requirementRef string,
) (*SemanticAuthority, error) {
	if ctx == nil {
		return nil, errors.New("semantic authority context is nil")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if !validSHA256Ref(requirementRef) {
		return nil, errors.New(
			"semantic authority requirement must be a lowercase SHA-256 reference",
		)
	}
	if err := store.validate(); err != nil {
		return nil, err
	}
	if err := store.lease.Validate(ctx); err != nil {
		return nil, err
	}
	path, err := store.objectPath(
		"semantic-authority-seeds",
		requirementRef,
		".seed",
		true,
	)
	if err != nil {
		return nil, err
	}

	record, readErr := readImmutable(path, semanticAuthoritySeedRecordBytes)
	switch {
	case readErr == nil:
	case errors.Is(readErr, ErrEvidenceNotFound):
		seed := make([]byte, SemanticAuthoritySeedBytes)
		defer clear(seed)
		if _, err := rand.Read(seed); err != nil {
			return nil, errors.New("generate semantic authority owner seed")
		}
		record = semanticAuthoritySeedRecord(
			store.RepositoryRef(),
			requirementRef,
			seed,
		)
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if err := store.validate(); err != nil {
			return nil, err
		}
		if err := store.lease.Validate(ctx); err != nil {
			return nil, err
		}
		if err := publishImmutable(path, record); err != nil {
			var conflict *immutableBytesConflict
			if !errors.As(err, &conflict) {
				return nil, err
			}
			record = append([]byte(nil), conflict.existing...)
		}
	default:
		return nil, readErr
	}

	seed, err := parseSemanticAuthoritySeedRecord(
		store.RepositoryRef(),
		requirementRef,
		record,
	)
	if err != nil {
		return nil, &CorruptionError{Artifact: "semantic_authority_seed"}
	}
	defer clear(seed)
	authority, err := ProvisionSemanticAuthority(
		store.RepositoryRef(),
		requirementRef,
		seed,
	)
	if err != nil {
		return nil, &CorruptionError{Artifact: "semantic_authority_seed"}
	}
	if err := store.validate(); err != nil {
		return nil, err
	}
	if err := store.lease.Validate(ctx); err != nil {
		return nil, err
	}
	if err := authority.Identity().validateFor(
		store.RepositoryRef(),
		requirementRef,
	); err != nil {
		return nil, fmt.Errorf(
			"validate restored semantic authority identity: %w",
			err,
		)
	}
	return authority, nil
}

func semanticAuthoritySeedRecord(
	repositoryRef string,
	requirementRef string,
	seed []byte,
) []byte {
	record := make([]byte, 0, semanticAuthoritySeedRecordBytes)
	record = append(record, semanticAuthoritySeedRecordDomain...)
	record = append(record, 0)
	record = append(record, seed...)
	checksum := semanticAuthoritySeedChecksum(
		repositoryRef,
		requirementRef,
		seed,
	)
	record = append(record, checksum[:]...)
	return record
}

func parseSemanticAuthoritySeedRecord(
	repositoryRef string,
	requirementRef string,
	record []byte,
) ([]byte, error) {
	if len(record) != semanticAuthoritySeedRecordBytes ||
		string(record[:len(semanticAuthoritySeedRecordDomain)]) !=
			semanticAuthoritySeedRecordDomain ||
		record[len(semanticAuthoritySeedRecordDomain)] != 0 {
		return nil, errors.New("semantic authority seed record header is invalid")
	}
	seedStart := len(semanticAuthoritySeedRecordDomain) + 1
	seedEnd := seedStart + SemanticAuthoritySeedBytes
	seed := append([]byte(nil), record[seedStart:seedEnd]...)
	nonzero := false
	for _, value := range seed {
		nonzero = nonzero || value != 0
	}
	if !nonzero {
		clear(seed)
		return nil, errors.New("semantic authority seed record is zero")
	}
	want := semanticAuthoritySeedChecksum(
		repositoryRef,
		requirementRef,
		seed,
	)
	if subtle.ConstantTimeCompare(record[seedEnd:], want[:]) != 1 {
		clear(seed)
		return nil, errors.New("semantic authority seed record checksum is invalid")
	}
	return seed, nil
}

func semanticAuthoritySeedChecksum(
	repositoryRef string,
	requirementRef string,
	seed []byte,
) [sha256.Size]byte {
	hash := sha256.New()
	_, _ = hash.Write([]byte(semanticAuthoritySeedRecordDomain))
	_, _ = hash.Write([]byte{0})
	_, _ = hash.Write([]byte(repositoryRef))
	_, _ = hash.Write([]byte{0})
	_, _ = hash.Write([]byte(requirementRef))
	_, _ = hash.Write([]byte{0})
	_, _ = hash.Write(seed)
	var checksum [sha256.Size]byte
	copy(checksum[:], hash.Sum(nil))
	return checksum
}
