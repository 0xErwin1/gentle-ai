package workrun

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestPrivateWorkRunPathLifecycle(t *testing.T) {
	root := t.TempDir()
	privateDir := filepath.Join(root, "private")
	created, err := createPrivateWorkRunDirectory(privateDir)
	if err != nil {
		t.Fatal(err)
	}
	if !created {
		t.Fatal("new private directory was reported as existing")
	}
	if err := validatePrivateWorkRunDirectory(privateDir); err != nil {
		t.Fatalf("created directory is not private: %v", err)
	}
	created, err = createPrivateWorkRunDirectory(privateDir)
	if err != nil || created {
		t.Fatalf("existing private directory = (%v, %v), want (false, nil)", created, err)
	}

	privateFile := filepath.Join(privateDir, "record")
	file, err := createPrivateWorkRunFile(privateFile)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := file.WriteString("record\n"); err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	if err := validatePrivateWorkRunFile(privateFile); err != nil {
		t.Fatalf("created file is not private: %v", err)
	}
	payload, err := readBoundedPrivateWorkRunFile(privateFile, 32)
	if err != nil {
		t.Fatal(err)
	}
	if string(payload) != "record\n" {
		t.Fatalf("payload = %q, want record", payload)
	}
}

func TestPrivateWorkRunPathRejectsLinksAndUnboundedFiles(t *testing.T) {
	root := t.TempDir()
	privateDir := filepath.Join(root, "private")
	if _, err := createPrivateWorkRunDirectory(privateDir); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(privateDir, "target")
	file, err := createPrivateWorkRunFile(target)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := file.WriteString("too-large"); err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := readBoundedPrivateWorkRunFile(target, 3); err == nil {
		t.Fatal("oversized file was accepted")
	}

	empty := filepath.Join(privateDir, "empty")
	emptyFile, err := createPrivateWorkRunFile(empty)
	if err != nil {
		t.Fatal(err)
	}
	if err := emptyFile.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := readBoundedPrivateWorkRunFile(empty, 32); err == nil {
		t.Fatal("empty authority artifact was accepted")
	}

	link := filepath.Join(privateDir, "link")
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("symlinks are unavailable: %v", err)
	}
	if err := validatePrivateWorkRunFile(link); err == nil {
		t.Fatal("symlink was accepted as a private file")
	}
	if _, err := readBoundedPrivateWorkRunFile(link, 32); err == nil {
		t.Fatal("symlink was opened as a bounded private file")
	}
}

func TestWorkRunDirectoryIdentityDetectsReplacement(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "run")
	if _, err := createPrivateWorkRunDirectory(path); err != nil {
		t.Fatal(err)
	}
	identity, err := openWorkRunDirectoryIdentity(path)
	if err != nil {
		t.Fatal(err)
	}
	defer identity.Close()
	if err := identity.Validate(); err != nil {
		t.Fatalf("unchanged directory identity failed: %v", err)
	}

	original := filepath.Join(root, "original")
	if err := os.Rename(path, original); err != nil {
		t.Skipf("platform does not permit replacing an open directory: %v", err)
	}
	if _, err := createPrivateWorkRunDirectory(path); err != nil {
		t.Fatal(err)
	}
	if err := identity.Validate(); !errors.Is(err, errWorkRunPathReplaced) {
		t.Fatalf("replacement error = %v, want errWorkRunPathReplaced", err)
	}
}

func TestBoundedWorkRunFileDetectsReplacementAfterOpen(t *testing.T) {
	root := t.TempDir()
	privateDir := filepath.Join(root, "private")
	if _, err := createPrivateWorkRunDirectory(privateDir); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(privateDir, "HEAD")
	file, err := createPrivateWorkRunFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := file.WriteString("one\n"); err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	opened, err := openBoundedPrivateWorkRunFile(path, 32)
	if err != nil {
		t.Fatal(err)
	}
	defer opened.Close()

	original := filepath.Join(privateDir, "HEAD.old")
	if err := os.Rename(path, original); err != nil {
		t.Skipf("platform does not permit replacing an open file: %v", err)
	}
	replacement, err := createPrivateWorkRunFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := replacement.WriteString("two\n"); err != nil {
		t.Fatal(err)
	}
	if err := replacement.Close(); err != nil {
		t.Fatal(err)
	}
	if err := opened.Validate(); !errors.Is(err, errWorkRunPathReplaced) {
		t.Fatalf("replacement error = %v, want errWorkRunPathReplaced", err)
	}
	if _, err := opened.ReadAll(); !errors.Is(err, errWorkRunPathReplaced) {
		t.Fatalf("read replacement error = %v, want errWorkRunPathReplaced", err)
	}
}
