package deliveryadmission

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/gentleman-programming/gentle-ai/internal/reviewtransaction"
)

// deliveryGitIdentity is the PAD projection of the shared, immutable Git
// identity lease. The lease remains authoritative; commonInfo exists only to
// bind the platform-specific stable directory opener to the same common-dir
// object before any provider storage is created.
type deliveryGitIdentity struct {
	lease          *reviewtransaction.RepositoryIdentityLease
	repositoryRoot string
	commonDir      string
	gitDir         string
	repositoryRef  string
	storageKey     string
	commonInfo     os.FileInfo
}

func resolveDeliveryGitIdentity(
	ctx context.Context,
	authorityRoot string,
) (deliveryGitIdentity, error) {
	if err := ctx.Err(); err != nil {
		return deliveryGitIdentity{}, err
	}
	if !filepath.IsAbs(authorityRoot) ||
		filepath.Clean(authorityRoot) != authorityRoot {
		return deliveryGitIdentity{}, fmt.Errorf(
			"%w: repository authority must return an absolute clean root",
			ErrInvalid,
		)
	}
	lease, err := reviewtransaction.OpenRepositoryIdentityLease(
		ctx,
		authorityRoot,
	)
	if err != nil {
		return deliveryGitIdentity{}, err
	}
	identity := lease.Identity()
	if identity.RepositoryRoot != authorityRoot {
		return deliveryGitIdentity{}, fmt.Errorf(
			"%w: repository authority does not resolve the exact Git root",
			ErrBindingMismatch,
		)
	}
	if !validDeliveryIdentityPath(identity.RepositoryRoot) ||
		!validDeliveryIdentityPath(identity.GitCommonDir) ||
		!validDeliveryIdentityPath(identity.GitDir) ||
		!validDeliveryIdentityRef(identity.RepositoryRef) ||
		!validDeliveryStorageKey(lease.StorageKey()) ||
		lease.StorageKey() != strings.TrimPrefix(identity.RepositoryRef, "sha256:") {
		return deliveryGitIdentity{}, fmt.Errorf(
			"%w: shared Git identity lease returned a non-canonical identity",
			ErrUntrustedPreimage,
		)
	}
	commonInfo, err := os.Lstat(identity.GitCommonDir)
	if err != nil {
		return deliveryGitIdentity{}, fmt.Errorf(
			"inspect leased Git common directory: %w",
			err,
		)
	}
	if !commonInfo.IsDir() || commonInfo.Mode()&os.ModeSymlink != 0 {
		return deliveryGitIdentity{}, fmt.Errorf(
			"%w: leased Git common directory is not a directory",
			ErrUntrustedPreimage,
		)
	}
	if err := lease.Validate(ctx); err != nil {
		return deliveryGitIdentity{}, err
	}
	return deliveryGitIdentity{
		lease:          lease,
		repositoryRoot: identity.RepositoryRoot,
		commonDir:      identity.GitCommonDir,
		gitDir:         identity.GitDir,
		repositoryRef:  identity.RepositoryRef,
		storageKey:     lease.StorageKey(),
		commonInfo:     commonInfo,
	}, nil
}

func validDeliveryIdentityPath(value string) bool {
	return filepath.IsAbs(value) && filepath.Clean(value) == value
}

func validDeliveryIdentityRef(value string) bool {
	return strings.HasPrefix(value, "sha256:") &&
		validDeliveryStorageKey(strings.TrimPrefix(value, "sha256:"))
}

func validDeliveryStorageKey(value string) bool {
	if len(value) != 64 {
		return false
	}
	for _, character := range value {
		if !strings.ContainsRune("0123456789abcdef", character) {
			return false
		}
	}
	return true
}
