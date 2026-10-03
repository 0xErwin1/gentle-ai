//go:build !linux

package shellinstaller

import (
	"context"
	"errors"
	"io"
)

func UserKernelCheck() error { return errors.New("Gentle Shell user installation requires Linux amd64") }
func ValidateUserInstall(UserInstallRequest) error { return UserKernelCheck() }
func InspectUserInstall(UserInstallRequest) (string, error) { return "", UserKernelCheck() }
func RunUserInstall(context.Context, UserInstallRequest) (UserInstallResult, error) {
	return UserInstallResult{}, UserKernelCheck()
}
func RunUserEntry(context.Context, string, []string, io.Reader, io.Writer, io.Writer) error {
	return UserKernelCheck()
}
