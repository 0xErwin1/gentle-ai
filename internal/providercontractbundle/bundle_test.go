package providercontractbundle

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"os"
	"path/filepath"
	"slices"
	"testing"
)

func TestGenerateIsDeterministicAndVerifiable(t *testing.T) {
	first := filepath.Join(t.TempDir(), "first")
	second := filepath.Join(t.TempDir(), "second")
	for _, directory := range []string{first, second} {
		if err := Generate(directory, "1.0.0"); err != nil {
			t.Fatalf("Generate(%s): %v", directory, err)
		}
	}
	firstFiles := readGeneratedFiles(t, first)
	secondFiles := readGeneratedFiles(t, second)
	wantNames := []string{
		"README.md",
		"manifest.json",
		"schemas/lens.schema.json",
		"schemas/refuter.schema.json",
		"schemas/targeted-validator.schema.json",
		"vectors/lens.json",
		"vectors/refuter.json",
		"vectors/targeted-validator.json",
	}
	if got := sortedFileNames(firstFiles); !equalStrings(got, wantNames) {
		t.Fatalf("generated files = %v, want %v", got, wantNames)
	}
	for _, name := range wantNames {
		if !bytes.Equal(firstFiles[name], secondFiles[name]) {
			t.Fatalf("generated %s differs between runs", name)
		}
	}
	if err := VerifyStaging(first); err != nil {
		t.Fatalf("VerifyStaging(%s): %v", first, err)
	}
	archive := filepath.Join(t.TempDir(), "provider-contract.tar.gz")
	writeArchive(t, archive, firstFiles, nil, false)
	if err := VerifyArchive(archive); err != nil {
		t.Fatalf("VerifyArchive(%s): %v", archive, err)
	}
}

func TestVerifyArchiveRejectsUnsafeTarEntries(t *testing.T) {
	files, err := generatedFiles("1.0.0")
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name      string
		duplicate bool
		mutate    func(map[string][]byte, *tar.Header)
	}{
		{name: "parent traversal", mutate: func(_ map[string][]byte, header *tar.Header) { header.Name = "../README.md" }},
		{name: "absolute path", mutate: func(_ map[string][]byte, header *tar.Header) { header.Name = "/README.md" }},
		{name: "dot path", mutate: func(_ map[string][]byte, header *tar.Header) { header.Name = "." }},
		{name: "backslash path", mutate: func(_ map[string][]byte, header *tar.Header) { header.Name = `schemas\lens.schema.json` }},
		{name: "noncanonical path", mutate: func(_ map[string][]byte, header *tar.Header) { header.Name = "schemas//lens.schema.json" }},
		{name: "duplicate path", duplicate: true, mutate: func(_ map[string][]byte, _ *tar.Header) {}},
		{name: "symlink", mutate: func(_ map[string][]byte, header *tar.Header) {
			header.Typeflag = tar.TypeSymlink
			header.Linkname = "README.md"
		}},
		{name: "hard link", mutate: func(_ map[string][]byte, header *tar.Header) {
			header.Typeflag = tar.TypeLink
			header.Linkname = "README.md"
		}},
		{name: "directory", mutate: func(_ map[string][]byte, header *tar.Header) { header.Typeflag = tar.TypeDir }},
		{name: "fifo", mutate: func(_ map[string][]byte, header *tar.Header) { header.Typeflag = tar.TypeFifo }},
		{name: "character device", mutate: func(_ map[string][]byte, header *tar.Header) { header.Typeflag = tar.TypeChar }},
		{name: "block device", mutate: func(_ map[string][]byte, header *tar.Header) { header.Typeflag = tar.TypeBlock }},
		{name: "unknown type", mutate: func(_ map[string][]byte, header *tar.Header) { header.Typeflag = 'Z' }},
	} {
		t.Run(test.name, func(t *testing.T) {
			archive := filepath.Join(t.TempDir(), "unsafe.tar.gz")
			copy := cloneFiles(files)
			header := &tar.Header{Name: "README.md"}
			test.mutate(copy, header)
			writeArchive(t, archive, copy, header, test.duplicate)
			if err := VerifyArchive(archive); err == nil {
				t.Fatal("VerifyArchive accepted an unsafe archive")
			}
		})
	}
}

func TestVerifyArchiveAcceptsTypeRegA(t *testing.T) {
	files, err := generatedFiles("1.0.0")
	if err != nil {
		t.Fatal(err)
	}
	archive := filepath.Join(t.TempDir(), "regular-a.tar.gz")
	writeArchive(t, archive, files, &tar.Header{Name: "README.md", Typeflag: tar.TypeRegA}, false)
	if err := VerifyArchive(archive); err != nil {
		t.Fatalf("VerifyArchive rejected TypeRegA: %v", err)
	}
}

func TestReadContractSemverRejectsNonCanonicalInput(t *testing.T) {
	filename := filepath.Join(t.TempDir(), "CONTRACT_SEMVER")
	for _, value := range []string{"v1.0.0\n", "1.0\n", "01.0.0\n", "1.0.0"} {
		if err := os.WriteFile(filename, []byte(value), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := ReadContractSemver(filename); err == nil {
			t.Fatalf("ReadContractSemver accepted %q", value)
		}
	}
}

func readGeneratedFiles(t *testing.T, directory string) map[string][]byte {
	t.Helper()
	files := make(map[string][]byte)
	err := filepath.WalkDir(directory, func(filename string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			return nil
		}
		payload, err := os.ReadFile(filename)
		if err != nil {
			return err
		}
		relative, err := filepath.Rel(directory, filename)
		if err != nil {
			return err
		}
		files[filepath.ToSlash(relative)] = payload
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return files
}

func writeArchive(t *testing.T, filename string, files map[string][]byte, override *tar.Header, duplicate bool) {
	t.Helper()
	file, err := os.Create(filename)
	if err != nil {
		t.Fatal(err)
	}
	gzipWriter := gzip.NewWriter(file)
	gzipWriter.Name = ""
	gzipWriter.Comment = ""
	gzipWriter.OS = 255
	tarWriter := tar.NewWriter(gzipWriter)
	names := sortedFileNames(files)
	for _, name := range names {
		header := &tar.Header{Name: name, Mode: 0o644, Size: int64(len(files[name])), Typeflag: tar.TypeReg}
		if override != nil && name == "README.md" {
			header.Typeflag = override.Typeflag
			header.Name = override.Name
			header.Linkname = override.Linkname
			if header.Typeflag != tar.TypeReg && header.Typeflag != tar.TypeRegA {
				header.Size = 0
			}
		}
		if err := tarWriter.WriteHeader(header); err != nil {
			t.Fatal(err)
		}
		if header.Size > 0 {
			if _, err := tarWriter.Write(files[name]); err != nil {
				t.Fatal(err)
			}
		}
	}
	if duplicate {
		payload := files["README.md"]
		if err := tarWriter.WriteHeader(&tar.Header{Name: "README.md", Mode: 0o644, Size: int64(len(payload)), Typeflag: tar.TypeReg}); err != nil {
			t.Fatal(err)
		}
		if _, err := tarWriter.Write(payload); err != nil {
			t.Fatal(err)
		}
	}
	if err := tarWriter.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gzipWriter.Close(); err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
}

func cloneFiles(files map[string][]byte) map[string][]byte {
	result := make(map[string][]byte, len(files))
	for name, payload := range files {
		result[name] = append([]byte(nil), payload...)
	}
	return result
}

func equalStrings(left, right []string) bool {
	return slices.Equal(left, right)
}
