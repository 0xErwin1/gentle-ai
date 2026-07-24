package evidence

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
)

func TestSemanticAuthorityStoreConcurrentProvisionAndReplay(t *testing.T) {
	t.Parallel()

	repo := initEvidenceRepository(t)
	store, err := OpenStore(context.Background(), repo)
	if err != nil {
		t.Fatal(err)
	}
	requirementRef := testRef("stored-semantic-requirement")

	const contenders = 24
	identities := make(chan SemanticAuthorityIdentity, contenders)
	errs := make(chan error, contenders)
	var wait sync.WaitGroup
	for range contenders {
		wait.Add(1)
		go func() {
			defer wait.Done()
			authority, provisionErr := store.SemanticAuthority(
				context.Background(),
				requirementRef,
			)
			if provisionErr != nil {
				errs <- provisionErr
				return
			}
			identities <- authority.Identity()
		}()
	}
	wait.Wait()
	close(errs)
	close(identities)
	for provisionErr := range errs {
		t.Fatalf("concurrent SemanticAuthority() error = %v", provisionErr)
	}
	var winner SemanticAuthorityIdentity
	count := 0
	for identity := range identities {
		count++
		if winner.IsZero() {
			winner = identity
		} else if identity != winner {
			t.Fatalf(
				"concurrent semantic authorities diverged: %#v / %#v",
				winner,
				identity,
			)
		}
	}
	if count != contenders {
		t.Fatalf("semantic authority results = %d, want %d", count, contenders)
	}

	reopened, err := OpenStore(context.Background(), repo)
	if err != nil {
		t.Fatal(err)
	}
	replayed, err := reopened.SemanticAuthority(
		context.Background(),
		requirementRef,
	)
	if err != nil {
		t.Fatal(err)
	}
	if replayed.Identity() != winner {
		t.Fatalf(
			"reopened semantic authority changed: %#v / %#v",
			winner,
			replayed.Identity(),
		)
	}

	path, err := store.objectPath(
		"semantic-authority-seeds",
		requirementRef,
		".seed",
		false,
	)
	if err != nil {
		t.Fatal(err)
	}
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() ||
		!privateEvidencePathSafe(path, info) ||
		info.Size() != int64(semanticAuthoritySeedRecordBytes) {
		t.Fatalf(
			"semantic authority seed record is unsafe: info=%v err=%v",
			info,
			err,
		)
	}
}

func TestSemanticAuthorityStoreRejectsTamperAndForeignRepositoryReplay(
	t *testing.T,
) {
	t.Parallel()

	first, err := OpenStore(
		context.Background(),
		initEvidenceRepository(t),
	)
	if err != nil {
		t.Fatal(err)
	}
	requirementRef := testRef("seed-tamper-requirement")
	if _, err := first.SemanticAuthority(
		context.Background(),
		requirementRef,
	); err != nil {
		t.Fatal(err)
	}
	firstPath, err := first.objectPath(
		"semantic-authority-seeds",
		requirementRef,
		".seed",
		false,
	)
	if err != nil {
		t.Fatal(err)
	}
	original, err := os.ReadFile(firstPath)
	if err != nil {
		t.Fatal(err)
	}

	t.Run("tamper", func(t *testing.T) {
		tampered := append([]byte(nil), original...)
		tampered[len(semanticAuthoritySeedRecordDomain)+1] ^= 0xff
		if err := os.WriteFile(firstPath, tampered, 0o600); err != nil {
			t.Fatal(err)
		}
		_, err := first.SemanticAuthority(
			context.Background(),
			requirementRef,
		)
		var corruption *CorruptionError
		if !errors.As(err, &corruption) ||
			corruption.Artifact != "semantic_authority_seed" {
			t.Fatalf(
				"tampered SemanticAuthority() error = %v, want corruption",
				err,
			)
		}
	})

	t.Run("foreign repository", func(t *testing.T) {
		second, openErr := OpenStore(
			context.Background(),
			initEvidenceRepository(t),
		)
		if openErr != nil {
			t.Fatal(openErr)
		}
		secondPath, pathErr := second.objectPath(
			"semantic-authority-seeds",
			requirementRef,
			".seed",
			true,
		)
		if pathErr != nil {
			t.Fatal(pathErr)
		}
		if writeErr := os.WriteFile(secondPath, original, 0o600); writeErr != nil {
			t.Fatal(writeErr)
		}
		_, provisionErr := second.SemanticAuthority(
			context.Background(),
			requirementRef,
		)
		var corruption *CorruptionError
		if !errors.As(provisionErr, &corruption) ||
			corruption.Artifact != "semantic_authority_seed" {
			t.Fatalf(
				"foreign SemanticAuthority() error = %v, want corruption",
				provisionErr,
			)
		}
	})
}

func TestSemanticAuthorityStoreRejectsUnsafeRequestWithoutPublication(
	t *testing.T,
) {
	t.Parallel()

	store, err := OpenStore(
		context.Background(),
		initEvidenceRepository(t),
	)
	if err != nil {
		t.Fatal(err)
	}
	before, err := directoryFingerprint(store.Root())
	if err != nil {
		t.Fatal(err)
	}
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := store.SemanticAuthority(
		cancelled,
		testRef("cancelled-semantic-requirement"),
	); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled SemanticAuthority() error = %v", err)
	}
	if _, err := store.SemanticAuthority(
		context.Background(),
		"not-a-digest",
	); err == nil {
		t.Fatal("SemanticAuthority() accepted an invalid requirement")
	}
	after, err := directoryFingerprint(store.Root())
	if err != nil {
		t.Fatal(err)
	}
	if before != after {
		t.Fatalf("rejected semantic authority request mutated store")
	}
}

func directoryFingerprint(root string) (string, error) {
	hash := sha256.New()
	err := filepath.WalkDir(
		root,
		func(path string, entry os.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			relative, err := filepath.Rel(root, path)
			if err != nil {
				return err
			}
			_, _ = hash.Write([]byte(relative))
			if entry.Type().IsRegular() {
				payload, err := os.ReadFile(path)
				if err != nil {
					return err
				}
				_, _ = hash.Write(payload)
			}
			return nil
		},
	)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%x", hash.Sum(nil)), nil
}
