//go:build !windows

package shellinstaller

import (
	"context"
	"errors"
	"io"
)

// Linux's Separate implementation remains in its independent PR #5242.
// This Windows-only change does not claim to ship or replace that backend.
func UserKernelCheck() error {
	return errors.New("this Separate installer requires Windows 11 x64")
}
func InspectUserInstall(UserInstallRequest) (string, error) { return "", UserKernelCheck() }
func RunUserEntry(context.Context, string, []string, io.Reader, io.Writer, io.Writer) error {
	return UserKernelCheck()
}
