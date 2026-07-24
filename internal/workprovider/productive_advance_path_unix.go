//go:build !windows

package workprovider

import (
	"io/fs"
	"os"
	"syscall"
)

func productiveAdvanceSingleLink(info fs.FileInfo) bool {
	stat, ok := info.Sys().(*syscall.Stat_t)
	return ok && stat.Nlink == 1
}

func productiveAdvanceOpenedSingleLink(file *os.File) bool {
	if file == nil {
		return false
	}
	info, err := file.Stat()
	return err == nil && productiveAdvanceSingleLink(info)
}
