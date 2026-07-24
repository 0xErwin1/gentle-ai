//go:build windows

package workprovider

import (
	"io/fs"
	"os"

	"golang.org/x/sys/windows"
)

func productiveAdvanceSingleLink(info fs.FileInfo) bool {
	// The authoritative link-count check uses the opened handle below. This
	// path-only predicate still rejects reparse/symlink FileInfo values.
	return info != nil && info.Mode()&os.ModeSymlink == 0
}

func productiveAdvanceOpenedSingleLink(file *os.File) bool {
	if file == nil {
		return false
	}
	var info windows.ByHandleFileInformation
	if err := windows.GetFileInformationByHandle(
		windows.Handle(file.Fd()),
		&info,
	); err != nil {
		return false
	}
	return info.NumberOfLinks == 1
}
