//go:build !windows

package deliveryadmission

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func initDeliveryUseRepository(t *testing.T) string {
	t.Helper()
	root := filepath.Join(t.TempDir(), "repository")
	command := exec.Command("git", "init", "--quiet", root)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("git init: %v\n%s", err, output)
	}
	absolute, err := filepath.Abs(root)
	if err != nil {
		t.Fatal(err)
	}
	return filepath.Clean(absolute)
}

func TestDirectoryUseStoreRejectsCallerRootAndBindsRepositoryAuthority(t *testing.T) {
	current, authorizationRef, authorization := normalAuthorization(t)
	otherRef := "github:other"
	current.repository.repositories[otherRef] = initDeliveryUseRepository(t)
	store, err := OpenDirectoryUseStore(context.Background(), current.repository, otherRef)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })

	current.repository.now++
	_, err = ConsumeAuthorization(
		context.Background(), current.repository, current.repository,
		authorizationRef, useRequest(authorization), store,
	)
	if !errors.Is(err, ErrBindingMismatch) {
		t.Fatalf("repository-bound use error = %v, want ErrBindingMismatch", err)
	}
	entries, readErr := os.ReadDir(store.directory)
	if readErr != nil {
		t.Fatal(readErr)
	}
	if len(entries) != 0 {
		t.Fatalf("repository mismatch created %d durable records", len(entries))
	}
}

func TestDirectoryUseStoreDoesNotRewriteSharedGitAuthorityModes(t *testing.T) {
	repository := newTrustedRepository()
	repositoryRef := "github:shared-modes"
	root := initDeliveryUseRepository(t)
	common := filepath.Join(root, ".git")
	shared := filepath.Join(common, "gentle-ai")
	if err := os.Chmod(common, 0o775); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(shared, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(shared, 0o755); err != nil {
		t.Fatal(err)
	}
	repository.repositories[repositoryRef] = root
	store, err := OpenDirectoryUseStore(context.Background(), repository, repositoryRef)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })

	for path, want := range map[string]os.FileMode{common: 0o775, shared: 0o755} {
		info, statErr := os.Stat(path)
		if statErr != nil {
			t.Fatal(statErr)
		}
		if got := info.Mode().Perm(); got != want {
			t.Fatalf("shared authority mode %s = %o, want unchanged %o", path, got, want)
		}
	}
	for _, path := range []string{
		filepath.Join(shared, "delivery-authorization-uses"),
		store.directory,
	} {
		info, statErr := os.Stat(path)
		if statErr != nil {
			t.Fatal(statErr)
		}
		if got := info.Mode().Perm(); got != 0o700 {
			t.Fatalf("private PAD mode %s = %o, want 700", path, got)
		}
	}
}

func TestDirectoryUseStoreRejectsRepositoryAuthorityRemap(t *testing.T) {
	current, authorizationRef, authorization := normalAuthorization(t)
	repositoryRef := authorization.Binding.Destination.RepositoryRef
	store := openScenarioUseStore(t, current.repository, repositoryRef)
	current.repository.repositories[repositoryRef] = initDeliveryUseRepository(t)

	current.repository.now++
	_, err := ConsumeAuthorization(
		context.Background(), current.repository, current.repository,
		authorizationRef, useRequest(authorization), store,
	)
	if !errors.Is(err, ErrBindingMismatch) {
		t.Fatalf("remapped repository authority error = %v, want ErrBindingMismatch", err)
	}
	entries, readErr := os.ReadDir(store.directory)
	if readErr != nil {
		t.Fatal(readErr)
	}
	if len(entries) != 0 {
		t.Fatalf("remapped repository authority created %d durable records", len(entries))
	}
}

func TestDirectoryUseStoreRejectsSymlinkedAuthorityComponents(t *testing.T) {
	repository := newTrustedRepository()
	repositoryRef := "github:symlink"
	root := initDeliveryUseRepository(t)
	repository.repositories[repositoryRef] = root
	outside := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(root, ".git", "gentle-ai")); err != nil {
		t.Skipf("symlinks are unavailable: %v", err)
	}

	if store, err := OpenDirectoryUseStore(
		context.Background(), repository, repositoryRef,
	); err == nil {
		_ = store.Close()
		t.Fatal("symlinked authorization-use root was accepted")
	}
	entries, err := os.ReadDir(outside)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("symlink target received %d mutations", len(entries))
	}
}

func TestDirectoryUseStoreRejectsRepositoryRootSymlink(t *testing.T) {
	repository := newTrustedRepository()
	repositoryRef := "github:root-symlink"
	root := initDeliveryUseRepository(t)
	link := filepath.Join(t.TempDir(), "repository-link")
	if err := os.Symlink(root, link); err != nil {
		t.Skipf("symlinks are unavailable: %v", err)
	}
	repository.repositories[repositoryRef] = link

	if store, err := OpenDirectoryUseStore(
		context.Background(), repository, repositoryRef,
	); err == nil {
		_ = store.Close()
		t.Fatal("symlinked repository authority root was accepted")
	}
	if _, err := os.Lstat(filepath.Join(root, ".git", "gentle-ai")); !os.IsNotExist(err) {
		t.Fatalf("rejected root symlink mutated the repository: %v", err)
	}
}

func TestDirectoryUseStoreRejectsAuthorityThatResolvesOnlyASubdirectory(t *testing.T) {
	repository := newTrustedRepository()
	repositoryRef := "github:subdirectory"
	root := initDeliveryUseRepository(t)
	subdirectory := filepath.Join(root, "nested")
	if err := os.Mkdir(subdirectory, 0o700); err != nil {
		t.Fatal(err)
	}
	repository.repositories[repositoryRef] = subdirectory

	if store, err := OpenDirectoryUseStore(
		context.Background(), repository, repositoryRef,
	); err == nil {
		_ = store.Close()
		t.Fatal("subdirectory was accepted as the exact repository authority root")
	}
	if _, err := os.Lstat(filepath.Join(root, ".git", "gentle-ai")); !os.IsNotExist(err) {
		t.Fatalf("rejected repository-root mismatch mutated Git authority: %v", err)
	}
}

func TestDirectoryUseStoreRejectsModeWeakeningBeforeUse(t *testing.T) {
	current, authorizationRef, authorization := normalAuthorization(t)
	store := openScenarioUseStore(
		t, current.repository, authorization.Binding.Destination.RepositoryRef,
	)
	if err := os.Chmod(store.directory, 0o777); err != nil {
		t.Fatal(err)
	}
	current.repository.now++
	_, err := ConsumeAuthorization(
		context.Background(), current.repository, current.repository,
		authorizationRef, useRequest(authorization), store,
	)
	if err == nil {
		t.Fatal("world-accessible authorization-use directory was accepted")
	}
	entries, readErr := os.ReadDir(store.directory)
	if readErr != nil {
		t.Fatal(readErr)
	}
	if len(entries) != 0 {
		t.Fatalf("unsafe directory received %d durable records", len(entries))
	}
}

func TestDirectoryUseStoreDetectsDirectorySwapWithoutMutatingReplacement(t *testing.T) {
	current, authorizationRef, authorization := normalAuthorization(t)
	store := openScenarioUseStore(
		t, current.repository, authorization.Binding.Destination.RepositoryRef,
	)
	original := store.directory + ".original"
	store.beforePublication = func() {
		store.beforePublication = nil
		if err := os.Rename(store.directory, original); err != nil {
			t.Fatal(err)
		}
		if err := os.Mkdir(store.directory, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	current.repository.now++
	_, err := ConsumeAuthorization(
		context.Background(), current.repository, current.repository,
		authorizationRef, useRequest(authorization), store,
	)
	if err == nil {
		t.Fatal("replaced authorization-use directory was accepted")
	}
	for name, path := range map[string]string{
		"original": original, "replacement": store.directory,
	} {
		entries, readErr := os.ReadDir(path)
		if readErr != nil {
			t.Fatal(readErr)
		}
		if len(entries) != 0 {
			t.Fatalf("%s directory received %d records after rejected swap", name, len(entries))
		}
	}
}

func TestDirectoryUseStoreFailsClosedWhenDirectoryDurabilityCannotBeProved(t *testing.T) {
	current, authorizationRef, authorization := normalAuthorization(t)
	store := openScenarioUseStore(
		t, current.repository, authorization.Binding.Destination.RepositoryRef,
	)
	originalSync := syncSecureUseDirectory
	syncSecureUseDirectory = func(*secureUseDirectory) error {
		return errors.New("injected directory flush failure")
	}
	t.Cleanup(func() { syncSecureUseDirectory = originalSync })
	current.repository.now++
	_, err := ConsumeAuthorization(
		context.Background(), current.repository, current.repository,
		authorizationRef, useRequest(authorization), store,
	)
	if err == nil || !strings.Contains(err.Error(), "flush failure") {
		t.Fatalf("durability failure = %v, want fail-closed error", err)
	}

	syncSecureUseDirectory = originalSync
	current.repository.now++
	_, err = ConsumeAuthorization(
		context.Background(), current.repository, current.repository,
		authorizationRef, useRequest(authorization), store,
	)
	if !errors.Is(err, ErrAlreadyConsumed) {
		t.Fatalf("replay after uncertain durability = %v, want ErrAlreadyConsumed", err)
	}
}

func TestDirectoryUseStoreRejectsConflictingSymlinkRecord(t *testing.T) {
	current, authorizationRef, authorization := normalAuthorization(t)
	store := openScenarioUseStore(
		t, current.repository, authorization.Binding.Destination.RepositoryRef,
	)
	outside := filepath.Join(t.TempDir(), "outside")
	if err := os.WriteFile(outside, []byte("not a use record"), 0o600); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(
		store.directory,
		strings.TrimPrefix(authorizationRef, "sha256:")+".json",
	)
	if err := os.Symlink(outside, target); err != nil {
		t.Skipf("symlinks are unavailable: %v", err)
	}
	current.repository.now++
	_, err := ConsumeAuthorization(
		context.Background(), current.repository, current.repository,
		authorizationRef, useRequest(authorization), store,
	)
	if err == nil || errors.Is(err, ErrAlreadyConsumed) {
		t.Fatalf("conflicting symlink record error = %v, want unsafe failure", err)
	}
	payload, readErr := os.ReadFile(outside)
	if readErr != nil {
		t.Fatal(readErr)
	}
	if string(payload) != "not a use record" {
		t.Fatalf("conflicting symlink target was mutated: %q", payload)
	}
}
