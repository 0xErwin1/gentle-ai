//go:build linux

package shellinstaller

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"syscall"
)

// Node ELF pin independently derived from the fully archive-pinned v24.18.0
// Linux-x64 regular member, not from a mutable installed bootstrap inventory.
const privateNativeNodeSize int64 = 123655872
const privateNativeNodeSHA = "41a74efb34cbde5c7632cdac0cf8bd1a14d0b8d73dc1e82755014d9a9ce70f5c"
const privateNativeResolverSHA = "cbdf5deac8b7a85ab1253dbd049953aeb192a7d1f7987f9206916ab449c10a92"
const privateNativeInvoke = `import {installGentleAi} from './scripts/gentle-ai-installer.mjs'; const packageRoot=process.cwd(); await installGentleAi({packageRoot});`

func privateNativeFile(ctx context.Context, path string, mode os.FileMode, size int64, pin string) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	info, err := privatePhysical(path)
	if err != nil || info.Sys().(*syscall.Stat_t).Uid != uint32(os.Getuid()) || (mode != 0 && info.Mode() != mode) || (mode == 0 && info.Mode() != 0600 && info.Mode() != 0644 && info.Mode() != 0700 && info.Mode() != 0755) || info.Size() > 268435456 || (size >= 0 && info.Size() != size) {
		return "", privateError("source", errors.Join(err, fmt.Errorf("native file refused: %s metadata=%v", path, info)))
	}
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	hash := sha256.New()
	buffer := make([]byte, 65536)
	var count int64
	for err == nil {
		var n int
		n, err = file.Read(buffer)
		count += int64(n)
		_, _ = hash.Write(buffer[:n])
		if contextErr := ctx.Err(); contextErr != nil {
			err = contextErr
		}
		if count > 268435456 {
			err = privateError("source", nil)
		}
	}
	if errors.Is(err, io.EOF) {
		err = nil
	}
	err = errors.Join(err, file.Close())
	fresh, freshErr := privatePhysical(path)
	digest := fmt.Sprintf("%x", hash.Sum(nil))
	if err != nil || freshErr != nil || privateStamp(info) != privateStamp(fresh) || count != info.Size() || (pin != "" && digest != pin) {
		return "", privateError("source", errors.Join(err, freshErr))
	}
	return privateStamp(info) + ":" + digest, nil
}

func privateNativeDirectory(path string) error {
	info, err := os.Lstat(path)
	canonical, canonicalErr := filepath.EvalSymlinks(path)
	if err != nil || canonicalErr != nil || canonical != path || (info.Mode() != os.ModeDir|0700 && info.Mode() != os.ModeDir|0755) || info.Sys().(*syscall.Stat_t).Uid != uint32(os.Getuid()) {
		return privateError("source", errors.Join(err, canonicalErr))
	}
	return nil
}

func privateNativeEntries(path string) ([]os.DirEntry, error) {
	directory, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	entries, err := directory.ReadDir(17)
	if errors.Is(err, io.EOF) {
		err = nil
	}
	return entries, errors.Join(err, directory.Close())
}
