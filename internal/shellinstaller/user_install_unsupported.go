//go:build !linux && !windows

package shellinstaller

import (
	"context"
	"errors"
	"io"
)

// Native backends exist only for Linux (#5242) and Windows (#5279). Every
// common entry point refuses here; this file grants no installation support.
func UserKernelCheck() error {
	return errors.New("Gentle Shell user installation requires Linux amd64 or Windows 11 x64; this platform is unsupported")
}
func ValidateUserInstall(UserInstallRequest) error          { return UserKernelCheck() }
func InspectUserInstall(UserInstallRequest) (string, error) { return "", UserKernelCheck() }
func RunUserInstall(context.Context, UserInstallRequest) (UserInstallResult, error) {
	return UserInstallResult{}, UserKernelCheck()
}
func RunUserEntry(context.Context, string, []string, io.Reader, io.Writer, io.Writer) error {
	return UserKernelCheck()
}
