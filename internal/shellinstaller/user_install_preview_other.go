//go:build !linux

package shellinstaller

func userPreviewReadSettings(UserInstallRequest, string) ([]byte, bool, error) {
	return nil, false, UserKernelCheck()
}
