package workprovider

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/gentleman-programming/gentle-ai/internal/reviewtransaction"
)

var (
	errUnsafeCoordinationPath = errors.New(
		"coordination authority path is unavailable or unsafe",
	)
	errCoordinationPathReplaced = errors.New(
		"coordination authority path identity changed",
	)
)

func ensureCoordinationRepositoryRoot(
	commonDir string,
	root string,
	create bool,
) error {
	commonDir = filepath.Clean(commonDir)
	root = filepath.Clean(root)
	relative, err := filepath.Rel(commonDir, root)
	if err != nil ||
		relative == "." ||
		relative == ".." ||
		filepath.IsAbs(relative) ||
		strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return errors.New(
			"coordination authority root escapes Git common-dir",
		)
	}
	want := filepath.Join(
		"gentle-ai",
		"work-provider",
		"coordination-authority",
		coordinationRootVersion,
	)
	if relative != want {
		return errors.New(
			"coordination authority root is not the canonical Git-common-dir path",
		)
	}
	if err := validateSharedCoordinationDirectory(commonDir); err != nil {
		return fmt.Errorf("validate Git common-dir: %w", err)
	}
	current := commonDir
	for index, part := range strings.Split(
		relative,
		string(filepath.Separator),
	) {
		if part == "" || part == "." || part == ".." {
			return errUnsafeCoordinationPath
		}
		parent := current
		current = filepath.Join(current, part)
		info, statErr := os.Lstat(current)
		if errors.Is(statErr, fs.ErrNotExist) && create {
			if index == 0 {
				statErr = os.Mkdir(current, 0o755)
				if errors.Is(statErr, fs.ErrExist) {
					statErr = nil
				}
			} else {
				_, statErr = createPrivateCoordinationDirectory(current)
			}
			if statErr == nil {
				if syncErr := reviewtransaction.SyncReviewDirectory(parent); syncErr != nil {
					return syncErr
				}
				info, statErr = os.Lstat(current)
			}
		}
		if statErr != nil {
			return statErr
		}
		if index == 0 {
			if err := validateSharedCoordinationDirectory(current); err != nil {
				return fmt.Errorf(
					"validate shared gentle-ai directory: %w",
					err,
				)
			}
			continue
		}
		if info == nil || !info.IsDir() {
			return errUnsafeCoordinationPath
		}
		if err := validatePrivateCoordinationDirectory(current); err != nil {
			return fmt.Errorf(
				"validate private coordination directory: %w",
				err,
			)
		}
	}
	return nil
}

func ensurePrivateCoordinationDirectoryTree(
	base string,
	target string,
	create bool,
) error {
	base = filepath.Clean(base)
	target = filepath.Clean(target)
	relative, err := filepath.Rel(base, target)
	if err != nil ||
		relative == ".." ||
		filepath.IsAbs(relative) ||
		strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return errors.New(
			"private coordination directory escapes authority root",
		)
	}
	if err := validatePrivateCoordinationDirectory(base); err != nil {
		return err
	}
	if relative == "." {
		return nil
	}
	current := base
	for _, part := range strings.Split(relative, string(filepath.Separator)) {
		if part == "" || part == "." || part == ".." {
			return errUnsafeCoordinationPath
		}
		parent := current
		current = filepath.Join(current, part)
		_, statErr := os.Lstat(current)
		if errors.Is(statErr, fs.ErrNotExist) && create {
			created, createErr := createPrivateCoordinationDirectory(current)
			if createErr != nil {
				return createErr
			}
			if created {
				if err := reviewtransaction.SyncReviewDirectory(parent); err != nil {
					return err
				}
			}
			_, statErr = os.Lstat(current)
		}
		if statErr != nil {
			return statErr
		}
		if err := validatePrivateCoordinationDirectory(current); err != nil {
			return err
		}
	}
	return nil
}

func validateSharedCoordinationDirectory(path string) error {
	before, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if coordinationPathUnsafe(path, before) ||
		!before.IsDir() ||
		!coordinationSharedDirectorySafe(path, before) {
		return errUnsafeCoordinationPath
	}
	file, err := openCoordinationPathNoFollow(path, true)
	if err != nil {
		return err
	}
	defer file.Close()
	opened, err := file.Stat()
	if err != nil {
		return err
	}
	current, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if !os.SameFile(before, opened) ||
		!os.SameFile(opened, current) ||
		!coordinationSharedOpenDirectorySafe(file, opened) {
		return errCoordinationPathReplaced
	}
	return nil
}

func validatePrivateCoordinationDirectory(path string) error {
	return validatePrivateCoordinationPath(path, true)
}

func validatePrivateCoordinationFile(path string) error {
	return validatePrivateCoordinationPath(path, false)
}

func validatePrivateCoordinationPath(path string, directory bool) error {
	before, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if coordinationPathUnsafe(path, before) ||
		before.IsDir() != directory ||
		!privateCoordinationPathSafe(path, before) {
		return errUnsafeCoordinationPath
	}
	file, err := openCoordinationPathNoFollow(path, directory)
	if err != nil {
		return err
	}
	defer file.Close()
	opened, err := file.Stat()
	if err != nil {
		return err
	}
	current, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if !os.SameFile(before, opened) ||
		!os.SameFile(opened, current) ||
		!privateOpenCoordinationPathSafe(file, opened) {
		return errCoordinationPathReplaced
	}
	return nil
}

func readPrivateCoordinationFile(path string) ([]byte, error) {
	before, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if coordinationPathUnsafe(path, before) ||
		!before.Mode().IsRegular() ||
		!privateCoordinationPathSafe(path, before) {
		return nil, errUnsafeCoordinationPath
	}
	file, err := openCoordinationPathNoFollow(path, false)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	opened, err := file.Stat()
	if err != nil ||
		!opened.Mode().IsRegular() ||
		!os.SameFile(before, opened) ||
		!privateOpenCoordinationPathSafe(file, opened) {
		if err != nil {
			return nil, err
		}
		return nil, errCoordinationPathReplaced
	}
	if opened.Size() < 1 || opened.Size() > coordinationMaximumBytes {
		return nil, errors.New(
			"coordination authority artifact has an invalid byte length",
		)
	}
	payload, err := io.ReadAll(
		io.LimitReader(file, coordinationMaximumBytes+1),
	)
	if err != nil {
		return nil, err
	}
	afterOpen, err := file.Stat()
	if err != nil {
		return nil, err
	}
	current, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if len(payload) > coordinationMaximumBytes ||
		afterOpen.Size() != int64(len(payload)) ||
		!os.SameFile(opened, afterOpen) ||
		!os.SameFile(afterOpen, current) ||
		!privateOpenCoordinationPathSafe(file, afterOpen) {
		return nil, errCoordinationPathReplaced
	}
	return payload, nil
}

func publishPrivateCoordinationImmutable(
	path string,
	payload []byte,
) error {
	if len(payload) == 0 || len(payload) > coordinationMaximumBytes {
		return errors.New(
			"coordination immutable payload has an invalid byte length",
		)
	}
	directory := filepath.Dir(path)
	if err := validatePrivateCoordinationDirectory(directory); err != nil {
		return err
	}
	existing, err := readPrivateCoordinationFile(path)
	if err == nil {
		if !bytes.Equal(existing, payload) {
			return ErrCoordinationAuthorityConflict
		}
		return reviewtransaction.SyncReviewDirectory(directory)
	}
	if !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	temp, tempPath, err := createPrivateCoordinationTempFile(directory)
	if err != nil {
		return err
	}
	defer os.Remove(tempPath)
	if _, err := temp.Write(payload); err != nil {
		_ = temp.Close()
		return err
	}
	if err := temp.Sync(); err != nil {
		_ = temp.Close()
		return err
	}
	if err := temp.Close(); err != nil {
		return err
	}
	if err := validatePrivateCoordinationFile(tempPath); err != nil {
		return err
	}
	if err := reviewtransaction.PublishFileNoReplace(tempPath, path); err != nil {
		if !errors.Is(err, fs.ErrExist) {
			return err
		}
		existing, readErr := readPrivateCoordinationFile(path)
		if readErr != nil {
			return readErr
		}
		if !bytes.Equal(existing, payload) {
			return ErrCoordinationAuthorityConflict
		}
	}
	if err := validatePrivateCoordinationFile(path); err != nil {
		return err
	}
	return reviewtransaction.SyncReviewDirectory(directory)
}

func createPrivateCoordinationTempFile(
	directory string,
) (*os.File, string, error) {
	for attempt := 0; attempt < 32; attempt++ {
		var nonce [16]byte
		if _, err := rand.Read(nonce[:]); err != nil {
			return nil, "", err
		}
		path := filepath.Join(
			directory,
			".coordination-"+hex.EncodeToString(nonce[:]),
		)
		file, err := createPrivateCoordinationFile(path)
		if errors.Is(err, fs.ErrExist) {
			continue
		}
		if err != nil {
			return nil, "", err
		}
		return file, path, nil
	}
	return nil, "", errors.New(
		"could not allocate a private coordination temporary file",
	)
}
