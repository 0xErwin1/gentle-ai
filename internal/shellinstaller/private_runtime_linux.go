//go:build linux

package shellinstaller

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"syscall"
	"time"
	"unicode/utf8"

	assets "github.com/gentleman-programming/gentle-ai/v3/scripts"
	"golang.org/x/sys/unix"
)

type PrivateRuntimeError struct {
	Kind, Workspace, Destination string
	Cause                        error
}

func (e *PrivateRuntimeError) Error() string { return "private installation: " + e.Kind }
func (e *PrivateRuntimeError) Unwrap() error { return e.Cause }

type PrivateInstallResult struct {
	State, Destination, LockSHA256 string
}

func privateError(kind string, cause error) *PrivateRuntimeError {
	return &PrivateRuntimeError{Kind: kind, Cause: cause}
}

func privateMount(data, target, required string) bool {
	for _, line := range strings.Split(data, "\n") {
		fields := strings.Fields(line)
		if len(fields) < 7 || fields[4] != target {
			continue
		}
		options := "," + fields[5] + ","
		for _, option := range strings.Split(required, ",") {
			if !strings.Contains(options, ","+option+",") {
				return false
			}
		}
		return target != "/tmp" || !strings.Contains(options, ",noexec,")
	}
	return false
}

func privateKernelData(membership, mounts string, limits []string) error {
	mounted := false
	for _, line := range strings.Split(mounts, "\n") {
		fields := strings.Fields(line)
		if len(fields) >= 10 && fields[3] == "/" && fields[4] == "/sys/fs/cgroup" && strings.Contains(line, " - cgroup2 cgroup ") {
			mounted = true
		}
	}
	if !mounted || strings.TrimSpace(membership) != "0::/" || strings.Join(limits, "\n") != "3221225472\n0\n100000 100000\n64" {
		return privateError("unavailable", nil)
	}
	return nil
}

func privateKernel() error {
	membership, e1 := os.ReadFile("/proc/self/cgroup")
	mounts, e2 := os.ReadFile("/proc/self/mountinfo")
	var fs unix.Statfs_t
	root, e3 := filepath.EvalSymlinks("/sys/fs/cgroup")
	if e1 != nil || e2 != nil || e3 != nil || root != "/sys/fs/cgroup" || unix.Statfs(root, &fs) != nil || fs.Type != unix.CGROUP2_SUPER_MAGIC {
		return privateError("unavailable", errors.Join(e1, e2, e3))
	}
	var limits []string
	for _, name := range []string{"memory.max", "memory.swap.max", "cpu.max", "pids.max"} {
		path := filepath.Join(root, name)
		data, err := os.ReadFile(path)
		if err != nil || len(data) > 64 || unix.Statfs(path, &fs) != nil || fs.Type != unix.CGROUP2_SUPER_MAGIC {
			return privateError("unavailable", err)
		}
		limits = append(limits, strings.TrimSpace(string(data)))
	}
	return privateKernelData(string(membership), string(mounts), limits)
}

func privateDirectory(path string) (os.FileInfo, error) {
	info, err := os.Lstat(path)
	canonical, canonicalErr := filepath.EvalSymlinks(path)
	if err != nil || canonicalErr != nil || canonical != path || !info.IsDir() || info.Mode() != os.ModeDir|0700 || info.Sys().(*syscall.Stat_t).Uid != uint32(os.Getuid()) {
		return nil, privateError("filesystem", errors.Join(err, canonicalErr))
	}
	return info, nil
}

func privateDestination(dest string) error {
	if !filepath.IsAbs(dest) || filepath.Clean(dest) != dest || !regexp.MustCompile(`^[A-Za-z0-9_.-]+$`).MatchString(filepath.Base(dest)) {
		return privateError("refused", nil)
	}
	if _, err := privateDirectory(filepath.Dir(dest)); err != nil {
		return privateError("refused", err)
	}
	if _, err := os.Lstat(dest); !os.IsNotExist(err) {
		return privateError("refused", err)
	}
	return nil
}

func privateStage(parent string) (string, os.FileInfo, error) {
	workspace, err := os.MkdirTemp(parent, ".gentle-go-")
	if err != nil {
		return "", nil, err
	}
	identity, err := privateDirectory(workspace)
	for _, name := range assets.PrivateHelperNames() {
		if err != nil {
			break
		}
		var data []byte
		data, err = assets.ReadPrivateHelper(name)
		if err == nil {
			path := filepath.Join(workspace, name)
			err = errors.Join(os.WriteFile(path, data, 0444), os.Chmod(path, 0444))
		}
	}
	return workspace, identity, err
}

func privatePhysical(path string) (os.FileInfo, error) {
	info, err := os.Lstat(path)
	canonical, canonicalErr := filepath.EvalSymlinks(path)
	if err != nil || canonicalErr != nil || canonical != path || !info.Mode().IsRegular() {
		return nil, privateError("filesystem", errors.Join(err, canonicalErr))
	}
	return info, nil
}

func privateStamp(info os.FileInfo) string {
	st := info.Sys().(*syscall.Stat_t)
	return fmt.Sprintf("%d:%d:%d:%d:%v:%d:%v:%v", st.Dev, st.Ino, st.Uid, st.Gid, info.Mode(), info.Size(), st.Mtim, st.Ctim)
}

func privateSources(workspace string) (string, error) {
	var snapshot strings.Builder
	for _, name := range assets.PrivateHelperNames() {
		path := filepath.Join(workspace, name)
		info, err := privatePhysical(path)
		if err != nil || info.Mode() != 0444 || info.Sys().(*syscall.Stat_t).Uid != uint32(os.Getuid()) {
			return "", privateError("source", err)
		}
		want, err := assets.ReadPrivateHelper(name)
		got, readErr := os.ReadFile(path)
		if err != nil || readErr != nil || !bytes.Equal(got, want) {
			return "", privateError("source", errors.Join(err, readErr))
		}
		snapshot.WriteString(name + privateStamp(info))
	}
	return snapshot.String(), nil
}

func privateCleanup(workspace string, identity os.FileInfo) error {
	current, err := privateDirectory(workspace)
	if err != nil || identity == nil || !os.SameFile(identity, current) {
		return privateError("uncertain", err)
	}
	return os.RemoveAll(workspace)
}

func privateLock(path string) (string, error) {
	info, err := privatePhysical(path)
	if err != nil || info.Size() > 8388608 {
		return "", privateError("readback", err)
	}
	data, err := os.ReadFile(path)
	return fmt.Sprintf("%x", sha256.Sum256(data)), err
}

func privateReadback(dest, lock string) error {
	if _, err := privateDirectory(dest); err != nil {
		return err
	}
	for _, name := range []string{"node/bin/node", "node/lib/node_modules/npm/bin/npm-cli.js", "project/package.json", "closure.json", "project/node_modules/gentle-pi/package.json", "project/node_modules/@earendil-works/pi-coding-agent/package.json", "project/node_modules/@earendil-works/pi-tui/package.json", "project/node_modules/@heyhuynhgiabuu/pi-pretty/package.json", "project/node_modules/typebox/package.json"} {
		info, err := privatePhysical(filepath.Join(dest, name))
		if err != nil || (name == "node/bin/node" && info.Mode().Perm()&0111 == 0) {
			return privateError("readback", err)
		}
	}
	actual, err := privateLock(filepath.Join(dest, "project/package-lock.json"))
	if err != nil || actual != lock || len(lock) != 64 {
		return privateError("readback", err)
	}
	return nil
}

func privateFailure(dest string, cause error) *PrivateRuntimeError {
	if _, err := os.Lstat(dest); cause == nil || !os.IsNotExist(err) {
		return privateError("uncertain", cause)
	}
	var failure *PrivateRuntimeError
	if errors.As(cause, &failure) {
		return privateError(failure.Kind, cause)
	}
	return privateError("filesystem", cause)
}

type privateOutput struct {
	sync.Mutex
	data     []byte
	overflow bool
	cancel   context.CancelFunc
}

func (w *privateOutput) Write(data []byte) (int, error) {
	w.Lock()
	defer w.Unlock()
	if !w.overflow && len(w.data)+len(data) > 4096 {
		w.overflow, w.data = true, nil
		w.cancel()
	}
	if !w.overflow {
		w.data = append(w.data, data...)
	}
	return len(data), nil
}

func privateRun(ctx context.Context, cmd *exec.Cmd, cancel context.CancelFunc) (string, error) {
	output := &privateOutput{cancel: cancel}
	cmd.Stdout, cmd.Stderr = output, output
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.WaitDelay = 750 * time.Millisecond
	kill := func() error {
		if err := syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL); errors.Is(err, syscall.ESRCH) {
			return os.ErrProcessDone
		} else {
			return err
		}
	}
	cmd.Cancel = kill
	if err := cmd.Start(); err != nil {
		return "", privateError("start", err)
	}
	err := cmd.Wait() // WaitDelay bounds inherited pipes and joins stdlib copiers.
	if killErr := kill(); killErr != nil && !errors.Is(killErr, os.ErrProcessDone) {
		err = errors.Join(err, killErr)
	}
	if output.overflow || !utf8.Valid(output.data) {
		return "", privateError("output", err)
	}
	kind := "nonzero"
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		kind = "deadline"
	} else if ctx.Err() != nil {
		kind = "canceled"
	} else if errors.Is(err, exec.ErrWaitDelay) {
		kind = "pipes"
	} else if err == nil {
		return string(output.data), nil
	}
	return string(output.data), privateError(kind, err)
}

func privatePhase(ctx context.Context, workspace, helper string, budget time.Duration, args ...string) error {
	ctx, cancel := context.WithTimeout(ctx, budget)
	defer cancel()
	cmd := exec.CommandContext(ctx, "/bin/sh", append([]string{filepath.Join(workspace, helper)}, args...)...)
	cmd.Dir = workspace
	cmd.Env = []string{"PATH=/usr/bin:/bin", "HOME=" + workspace, "TMPDIR=" + workspace}
	_, err := privateRun(ctx, cmd, cancel)
	return err
}

// RunPrivateInstall requires separate caller effect approval and a private single
// writer. Kernel limits are freshly checked; this never launches Pi or claims Ready.
func RunPrivateInstall(ctx context.Context, destination, nodeArchive string) (result PrivateInstallResult, err error) {
	if ctx == nil || ctx.Err() != nil {
		return result, privateError("canceled", context.Canceled)
	}
	ctx, cancel := context.WithTimeout(ctx, 750*time.Second)
	defer cancel()
	if err = privateKernel(); err != nil {
		return result, err
	}
	if err = privateDestination(destination); err != nil {
		return result, err
	}
	archive, err := privatePhysical(nodeArchive)
	if err != nil || !filepath.IsAbs(nodeArchive) {
		return result, privateError("refused", err)
	}
	parent := filepath.Dir(destination)
	parentBefore, err := privateDirectory(parent)
	if err != nil {
		return result, err
	}
	if err = ctx.Err(); err != nil {
		return result, privateError("canceled", err)
	}
	workspace, identity, err := privateStage(parent)
	if workspace == "" {
		return result, privateError("filesystem", err)
	}
	defer func() {
		if cleanupErr := privateCleanup(workspace, identity); cleanupErr != nil {
			err = privateError("uncertain", errors.Join(err, cleanupErr))
		}
		if ctx.Err() != nil {
			err = errors.Join(err, privateError("canceled", ctx.Err()))
		}
		if err != nil {
			failure := privateFailure(destination, err)
			failure.Workspace, failure.Destination = workspace, destination
			result, err = PrivateInstallResult{}, failure
		}
	}()
	if err != nil {
		return result, err
	}
	sources, err := privateSources(workspace)
	if err != nil {
		return result, err
	}
	acquired := filepath.Join(workspace, "acquired")
	if err = privatePhase(ctx, workspace, "acquire-gentle-shell-private-bundle.sh", 450*time.Second, "--destination", acquired, "--node-archive", nodeArchive); err != nil {
		return result, err
	}
	bundle := filepath.Join(acquired, "bundle")
	lock, err := privateLock(filepath.Join(bundle, "project/package-lock.json")) // PRE-install witness.
	if err != nil {
		return result, err
	}
	receiptPath := filepath.Join(acquired, "bundle-receipt")
	info, err := privatePhysical(receiptPath)
	if err != nil || info.Size() != 65 {
		return result, privateError("readback", err)
	}
	receipt, err := os.ReadFile(receiptPath)
	digest := strings.TrimSpace(string(receipt))
	decoded, decodeErr := hex.DecodeString(digest)
	if err != nil || decodeErr != nil || len(decoded) != 32 {
		return result, privateError("readback", errors.Join(err, decodeErr))
	}
	installed := filepath.Join(workspace, "installed")
	if err = privatePhase(ctx, workspace, "install-gentle-shell-private.sh", 300*time.Second, "--destination", installed, "--bundle", bundle, "--bundle-sha256", digest); err != nil {
		return result, err
	}
	if err = privateReadback(installed, lock); err != nil {
		return result, err
	}
	freshSources, sourceErr := privateSources(workspace)
	freshArchive, archiveErr := privatePhysical(nodeArchive)
	freshParent, parentErr := privateDirectory(parent)
	if sourceErr != nil || archiveErr != nil || parentErr != nil || freshSources != sources || privateStamp(freshArchive) != privateStamp(archive) || !os.SameFile(parentBefore, freshParent) {
		return result, privateError("preimage", errors.Join(sourceErr, archiveErr, parentErr))
	}
	if err = privateDestination(destination); err != nil {
		return result, err
	}
	if err = ctx.Err(); err != nil {
		return result, privateError("canceled", err)
	}
	if err = unix.Renameat2(unix.AT_FDCWD, installed, unix.AT_FDCWD, destination, unix.RENAME_NOREPLACE); err != nil {
		return result, privateError("publication", err)
	}
	if err = privateReadback(destination, lock); err != nil {
		return result, err
	}
	return PrivateInstallResult{State: "ComponentInstalled", Destination: destination, LockSHA256: lock}, nil
}
