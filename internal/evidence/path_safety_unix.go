//go:build !windows

package evidence

import (
	"errors"
	"io/fs"
	"os"
)

func evidencePathUnsafe(_ string, info fs.FileInfo) bool {
	return info.Mode()&os.ModeSymlink != 0
}

func createEvidenceDirectory(path string, mode os.FileMode, _ bool) (bool, error) {
	err := os.Mkdir(path, mode)
	switch {
	case err == nil:
		return true, nil
	case errors.Is(err, fs.ErrExist):
		return false, nil
	default:
		return false, err
	}
}

func securePrivateEvidencePath(path string) error {
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if !privateEvidencePathSafe(path, info) {
		return errors.New("evidence path permissions are not owner-only")
	}
	return nil
}

func privateEvidencePathSafe(_ string, info fs.FileInfo) bool {
	return info.Mode().Perm()&0o077 == 0
}
