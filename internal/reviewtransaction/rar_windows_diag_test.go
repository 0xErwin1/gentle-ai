//go:build windows

package reviewtransaction

// Temporary #3231 diagnostics: prints the per-level verdict of every RAR
// predicate for the effect-marker and repository-context chains on a real
// windows-latest runner. Removed once the failing predicate is identified.

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/sys/windows"
)

func diagDescribe(t *testing.T, label, path string) {
	t.Helper()
	info, err := os.Lstat(path)
	if err != nil {
		t.Logf("DIAG %s %q: lstat err=%v", label, path, err)
		return
	}
	descriptor, err := windows.GetNamedSecurityInfo(path, windows.SE_FILE_OBJECT,
		windows.OWNER_SECURITY_INFORMATION|windows.DACL_SECURITY_INFORMATION)
	if err != nil {
		t.Logf("DIAG %s %q: mode=%v secinfo err=%v", label, path, info.Mode(), err)
		return
	}
	control, _, controlErr := descriptor.Control()
	dacl, defaulted, daclErr := descriptor.DACL()
	aceCount := -1
	if daclErr == nil && dacl != nil {
		aceCount = int(dacl.AceCount)
	}
	ownedByUser := rarSecurityDescriptorOwnedByCurrentUser(descriptor)
	t.Logf("DIAG %s %q: dir=%t mode=%v ownedByCurrentUser=%t control=%#x controlErr=%v daclPresent=%t daclProtected=%t daclDefaulted=%t aceCount=%d daclErr=%v privateSafe=%t sharedSafe=%t unsafeAttr=%t",
		label, path, info.IsDir(), info.Mode(), ownedByUser, control, controlErr,
		control&windows.SE_DACL_PRESENT != 0, control&windows.SE_DACL_PROTECTED != 0,
		defaulted, aceCount, daclErr,
		privateRARPathSafe(path, info), rarRepositoryDirectorySafe(path, info), rarPathUnsafe(path, info))
}

func diagWalk(t *testing.T, label, leaf, stopAt string) {
	t.Helper()
	var chain []string
	for p := leaf; ; p = filepath.Dir(p) {
		chain = append([]string{p}, chain...)
		if strings.EqualFold(p, stopAt) || filepath.Dir(p) == p {
			break
		}
	}
	for _, p := range chain {
		diagDescribe(t, label, p)
	}
}

func TestDiag3231EffectMarkerChain(t *testing.T) {
	repository, marker := newCompactEffectMarkerFixture(t, "diag-marker")
	path, err := repository.path(marker.LineageID, marker.AuthorityRevision, marker.EventID, true)
	if err != nil {
		t.Fatalf("DIAG path create: %v", err)
	}
	stop := filepath.Dir(filepath.Dir(filepath.Dir(filepath.Dir(filepath.Dir(repository.root)))))
	diagWalk(t, "marker-pre", filepath.Dir(path), stop)

	publication, err := repository.write(context.Background(), marker)
	t.Logf("DIAG write publication=%#v err=%v", publication, err)
	diagDescribe(t, "marker-file", path)
	got, readErr := repository.read(marker.LineageID, marker.AuthorityRevision, marker.EventID)
	t.Logf("DIAG read marker=%#v err=%v", got, readErr)
	if readErr != nil {
		if vErr := validatePrivateRARPath(path, false); vErr != nil {
			t.Logf("DIAG validatePrivateRARPath(file): %v", vErr)
		}
		if dErr := validatePrivateRARDirectory(filepath.Dir(path)); dErr != nil {
			t.Logf("DIAG validatePrivateRARDirectory(parent): %v", dErr)
		}
		t.Fatalf("DIAG marker read failed: %v", readErr)
	}
}

func TestDiag3231RepositoryContextChain(t *testing.T) {
	repo := initSnapshotRepo(t)
	state := newCompactTestState(t, repo, "diag-context")
	intent, err := compactRepositoryContextIntent(context.Background(), repo, state)
	if err != nil {
		t.Fatalf("DIAG intent: %v", err)
	}
	record, payload, err := makeCompactRecordWithIntents(state, []CompactEffectIntent{intent})
	if err != nil {
		t.Fatal(err)
	}
	store, _ := CompactAuthoritativeStore(context.Background(), repo, state.LineageID)
	if err := writeAtomic(store.StatePath(), payload, 0o644); err != nil {
		t.Fatal(err)
	}
	home, homeErr := reviewRepositoryContextHome()
	t.Logf("DIAG contextHome=%q err=%v HOME=%q USERPROFILE=%q", home, homeErr, os.Getenv("HOME"), os.Getenv("USERPROFILE"))

	result, err := ReconcileCompactRepositoryContext(context.Background(), store, record)
	t.Logf("DIAG reconcile result=%#v err=%v", result, err)

	path, pathErr := reviewRepositoryContextPath(intent.Destination)
	t.Logf("DIAG contextPath=%q err=%v", path, pathErr)
	if pathErr == nil {
		diagWalk(t, "context", path, filepath.Dir(filepath.Dir(filepath.Dir(filepath.Dir(path)))))
		published, readErr := readPrivateRARFile(path)
		t.Logf("DIAG readPrivateRARFile len=%d err=%v", len(published), readErr)
		if readErr != nil {
			t.Fatalf("DIAG context read failed: %v", readErr)
		}
	}
}
