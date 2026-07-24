//go:build !windows

package deliveryadmission

import (
	"errors"
	"io/fs"
	"os"
)

func secureRecordNotExist(err error) bool {
	return errors.Is(err, fs.ErrNotExist) || errors.Is(err, os.ErrNotExist)
}
