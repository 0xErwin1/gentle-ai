//go:build windows

package deliveryadmission

import (
	"errors"
	"io/fs"
	"os"

	"golang.org/x/sys/windows"
)

func secureRecordNotExist(err error) bool {
	return errors.Is(err, fs.ErrNotExist) ||
		errors.Is(err, os.ErrNotExist) ||
		errors.Is(err, windows.STATUS_OBJECT_NAME_NOT_FOUND) ||
		errors.Is(err, windows.STATUS_OBJECT_PATH_NOT_FOUND) ||
		errors.Is(err, windows.ERROR_FILE_NOT_FOUND) ||
		errors.Is(err, windows.ERROR_PATH_NOT_FOUND)
}
