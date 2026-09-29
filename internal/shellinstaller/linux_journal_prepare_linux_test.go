//go:build linux

package shellinstaller

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

func openTestLinuxJournalRoot(t *testing.T) (string, *LinuxJournalRoot) {
	t.Helper()
	_, path := makePrivateJournalRoot(t)
	root, err := OpenLinuxJournalRoot(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = root.Close() })
	return path, root
}

func TestLinuxJournalPublishRecordWritesExactBytesAndQuarantinesNonemptyRoot(t *testing.T) {
	path, root := openTestLinuxJournalRoot(t)
	want := []byte("synthetic transaction record\n")
	if err := linuxJournalPublishRecord(root, "txn-01.json", want); err != nil {
		t.Fatal(err)
	}

	recordPath := filepath.Join(path, "txn-01.json")
	got, err := os.ReadFile(recordPath)
	if err != nil || !bytes.Equal(got, want) {
		t.Fatalf("published bytes = %q, %v; want %q", got, err, want)
	}
	info, err := os.Stat(recordPath)
	if err != nil || info.Mode().Perm() != 0o400 {
		t.Fatalf("published mode = %v, %v; want 0400", info, err)
	}
	if err := linuxJournalPublishRecord(root, "txn-02.json", []byte("later")); err == nil {
		t.Fatal("publication continued after the root became nonempty")
	}
	entries, err := os.ReadDir(path)
	if err != nil || len(entries) != 1 || entries[0].Name() != "txn-01.json" {
		t.Fatalf("nonempty-root refusal changed entries: %v, %v", entries, err)
	}
	got, err = os.ReadFile(recordPath)
	if err != nil || !bytes.Equal(got, want) {
		t.Fatalf("published bytes changed after refusal: %q, %v", got, err)
	}
}

func TestLinuxJournalPublishRecordRefusesAnyPreexistingEntryWithoutNamedTemporary(t *testing.T) {
	for _, kind := range []string{"record-collision", "symlink", "directory", "partial-entry"} {
		t.Run(kind, func(t *testing.T) {
			parent, rootPath := makePrivateJournalRoot(t)
			entryName := "txn-existing"
			targetName := entryName
			switch kind {
			case "record-collision":
				if err := os.WriteFile(filepath.Join(rootPath, entryName), []byte("preserve"), 0o400); err != nil {
					t.Fatal(err)
				}
			case "symlink":
				outside := filepath.Join(parent, "outside")
				if err := os.WriteFile(outside, []byte("untouched"), 0o600); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(outside, filepath.Join(rootPath, entryName)); err != nil {
					t.Fatal(err)
				}
			case "directory":
				if err := os.Mkdir(filepath.Join(rootPath, entryName), 0o700); err != nil {
					t.Fatal(err)
				}
			case "partial-entry":
				entryName = ".partial-fragment"
				targetName = "txn-new"
				if err := os.WriteFile(filepath.Join(rootPath, entryName), []byte("partial"), 0o600); err != nil {
					t.Fatal(err)
				}
			}

			root, err := OpenLinuxJournalRoot(rootPath)
			if err != nil {
				t.Fatal(err)
			}
			defer root.Close()
			if err := linuxJournalPublishRecord(root, targetName, []byte("record")); err == nil {
				t.Fatal("publication continued with a preexisting root entry")
			}
			entries, err := os.ReadDir(rootPath)
			if err != nil || len(entries) != 1 || entries[0].Name() != entryName {
				t.Fatalf("refusal changed root entries: %v, %v", entries, err)
			}
			if kind == "partial-entry" {
				if _, err := os.Lstat(filepath.Join(rootPath, targetName)); !os.IsNotExist(err) {
					t.Fatalf("refusal left a published destination: %v", err)
				}
			}
			if kind == "record-collision" {
				got, err := os.ReadFile(filepath.Join(rootPath, entryName))
				if err != nil || string(got) != "preserve" {
					t.Fatalf("existing destination changed: %q, %v", got, err)
				}
			}
			if kind == "symlink" {
				got, err := os.ReadFile(filepath.Join(parent, "outside"))
				if err != nil || string(got) != "untouched" {
					t.Fatalf("symlink target changed: %q, %v", got, err)
				}
			}
			if kind == "partial-entry" {
				got, err := os.ReadFile(filepath.Join(rootPath, entryName))
				if err != nil || string(got) != "partial" {
					t.Fatalf("partial entry changed: %q, %v", got, err)
				}
			}
		})
	}
}

func TestLinuxJournalPublishRecordRejectsUnboundedData(t *testing.T) {
	path, root := openTestLinuxJournalRoot(t)
	data := make([]byte, linuxJournalRecordMaxBytes+1)
	if err := linuxJournalPublishRecord(root, "txn-too-large", data); err == nil {
		t.Fatal("oversized record was accepted")
	}
	entries, err := os.ReadDir(path)
	if err != nil || len(entries) != 0 {
		t.Fatalf("invalid input left artifacts: entries=%v err=%v", entries, err)
	}
}

func TestLinuxJournalPublishRecordRejectsRootModeDrift(t *testing.T) {
	path, root := openTestLinuxJournalRoot(t)
	if err := os.Chmod(path, 0o750); err != nil {
		t.Fatal(err)
	}
	if err := linuxJournalPublishRecord(root, "txn-drift", []byte("record")); err == nil {
		t.Fatal("root mode drift was accepted")
	}
	entries, err := os.ReadDir(path)
	if err != nil || len(entries) != 0 {
		t.Fatalf("mode drift left entries: entries=%v err=%v", entries, err)
	}
}

func TestLinuxJournalPrepareRecordReadsBackAndPinsObservation(t *testing.T) {
	path, root := openTestLinuxJournalRoot(t)
	want := []byte("synthetic prepared record\n")
	record, err := linuxJournalPrepareRecord(root, "txn-prepared", want)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = record.Close() })
	observation := record.Observation()
	if observation.name != "txn-prepared" || observation.root != root.Identity() ||
		observation.record.size != uint64(len(want)) || observation.record.mode != 0o400 || observation.record.links != 1 ||
		observation.record.identity.Device != observation.root.Device || observation.record.identity.MountID != observation.root.MountID {
		t.Fatalf("unexpected readback observation: %+v", observation)
	}
	if err := root.Close(); err != nil {
		t.Fatal(err)
	}
	if err := record.Verify(); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(filepath.Join(path, observation.name))
	if err != nil || !bytes.Equal(got, want) {
		t.Fatalf("prepared bytes = %q, %v; want %q", got, err, want)
	}
}

func corruptPublishedLinuxJournalRecordForTest(t *testing.T, root *LinuxJournalRoot, rootPath, name string, expected, corrupted []byte) error {
	t.Helper()
	var evidence linuxJournalRecordMetadata
	if err := linuxJournalPublishRecordWithEvidence(root, name, expected, &evidence); err != nil {
		return err
	}
	recordPath := filepath.Join(rootPath, name)
	if err := os.Chmod(recordPath, 0o600); err != nil {
		return err
	}
	if err := os.WriteFile(recordPath, corrupted, 0o600); err != nil {
		return err
	}
	if err := os.Chmod(recordPath, 0o400); err != nil {
		return err
	}
	rootFact, err := linuxJournalRootDescriptorFact(root.fd)
	if err != nil {
		return err
	}
	fd, err := linuxJournalOpenVerifiedReadback(root.fd, root.identity, rootFact.mode, name, expected, evidence)
	if fd >= 0 {
		_ = os.NewFile(uintptr(fd), "test journal record").Close()
	}
	return err
}

func TestLinuxJournalPrepareRecordRejectsCorruptionAfterPublication(t *testing.T) {
	path, root := openTestLinuxJournalRoot(t)
	if err := corruptPublishedLinuxJournalRecordForTest(t, root, path, "txn-corrupt", []byte("before"), []byte("broken")); err == nil {
		t.Fatal("readback accepted corrupted bytes published before verification")
	}
}

func TestLinuxJournalPreparedRecordRejectsSubstitutionAndMissingEntry(t *testing.T) {
	for _, scenario := range []string{"substituted", "missing", "extra"} {
		t.Run(scenario, func(t *testing.T) {
			path, root := openTestLinuxJournalRoot(t)
			record, err := linuxJournalPrepareRecord(root, "txn-entry", []byte("record"))
			if err != nil {
				t.Fatal(err)
			}
			defer record.Close()
			recordPath := filepath.Join(path, "txn-entry")
			if scenario == "extra" {
				if err := os.WriteFile(filepath.Join(path, "unexpected"), []byte("extra"), 0o400); err != nil {
					t.Fatal(err)
				}
			} else {
				if err := os.Remove(recordPath); err != nil {
					t.Fatal(err)
				}
				if scenario == "substituted" {
					if err := os.WriteFile(recordPath, []byte("record"), 0o400); err != nil {
						t.Fatal(err)
					}
				}
			}
			if err := record.Verify(); err == nil {
				t.Fatalf("verification accepted %s entry", scenario)
			}
		})
	}
}

func TestLinuxJournalPreparedRecordRejectsRootModeDriftAndClosedUse(t *testing.T) {
	path, root := openTestLinuxJournalRoot(t)
	record, err := linuxJournalPrepareRecord(root, "txn-state", []byte("record"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, 0o750); err != nil {
		t.Fatal(err)
	}
	if err := record.Verify(); err == nil {
		t.Fatal("verification accepted root mode drift")
	}
	if err := record.Close(); err != nil {
		t.Fatal(err)
	}
	if err := record.Verify(); err == nil {
		t.Fatal("verification succeeded after close")
	}
	if err := record.Close(); err != nil {
		t.Fatalf("repeated close failed: %v", err)
	}
}
