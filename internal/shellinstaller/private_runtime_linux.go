//go:build linux

package shellinstaller

import (
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"syscall"
	"time"
	"unicode"
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

func privateHierarchyPath(path string) bool {
	return utf8.ValidString(path) && filepath.IsAbs(path) && filepath.Clean(path) == path && !strings.ContainsRune(path, '\\') && strings.IndexFunc(path, unicode.IsControl) == -1
}

func privateMountPath(field string) (string, bool) {
	// Only the defined space escape is supported. Refuse all other escapes,
	// including encoded separators and control characters, rather than guess.
	path := strings.ReplaceAll(field, `\040`, " ")
	return path, privateHierarchyPath(path)
}

// privateCgroupPath resolves DATA only; production supplies /proc/self bytes.
// Membership is in hierarchy coordinates, not relative to the mountpoint.
func privateCgroupPath(membership, mounts string) (string, error) {
	member := strings.TrimSuffix(membership, "\n")
	if !strings.HasPrefix(member, "0::") || !privateHierarchyPath(strings.TrimPrefix(member, "0::")) {
		return "", privateError("unavailable", nil)
	}
	member = strings.TrimPrefix(member, "0::")
	const trusted = "/sys/fs/cgroup"
	root := ""
	for _, line := range strings.Split(strings.TrimSuffix(mounts, "\n"), "\n") {
		fields := strings.Fields(line)
		separator := -1
		for i, field := range fields {
			if field == "-" {
				if separator != -1 {
					return "", privateError("unavailable", nil)
				}
				separator = i
			}
		}
		if separator < 6 || len(fields) != separator+4 {
			return "", privateError("unavailable", nil)
		}
		mountRoot, rootOK := privateMountPath(fields[3])
		mountpoint, pointOK := privateMountPath(fields[4])
		if !rootOK || !pointOK || strings.HasPrefix(mountpoint, trusted+"/") {
			return "", privateError("unavailable", nil)
		}
		if mountpoint == trusted {
			if root != "" || fields[separator+1] != "cgroup2" {
				return "", privateError("unavailable", nil)
			}
			root = mountRoot
		}
	}
	if root == "" || (root != "/" && member != root && !strings.HasPrefix(member, root+"/")) {
		return "", privateError("unavailable", nil)
	}
	relative := strings.TrimPrefix(member, root)
	return filepath.Join(trusted, relative), nil
}

func privateKernelData(membership, mounts string, limits []string) error {
	if _, err := privateCgroupPath(membership, mounts); err != nil {
		return err
	}
	if len(limits) != 4 || strings.Join(limits, "\n") != "3221225472\n0\n100000 100000\n64" {
		return privateError("unavailable", nil)
	}
	return nil
}

func privateCgroupPhysical(path string, directory bool) error {
	info, err := os.Lstat(path)
	canonical, canonicalErr := filepath.EvalSymlinks(path)
	var fs unix.Statfs_t
	if err != nil || canonicalErr != nil || canonical != path || (directory && !info.IsDir()) || (!directory && !info.Mode().IsRegular()) || unix.Statfs(path, &fs) != nil || fs.Type != unix.CGROUP2_SUPER_MAGIC {
		return privateError("unavailable", errors.Join(err, canonicalErr))
	}
	return nil
}

func privateKernel() error {
	membership, e1 := os.ReadFile("/proc/self/cgroup")
	mounts, e2 := os.ReadFile("/proc/self/mountinfo")
	if e1 != nil || e2 != nil {
		return privateError("unavailable", errors.Join(e1, e2))
	}
	current, err := privateCgroupPath(string(membership), string(mounts))
	if err != nil {
		return err
	}
	for _, path := range []string{"/sys/fs/cgroup", current} {
		if err := privateCgroupPhysical(path, true); err != nil {
			return err
		}
	}
	// Exact leaf limits enforce upper bounds even with permissive ancestors.
	// Stricter ancestors can reduce availability; this does not promise capacity.
	var limits []string
	for _, name := range []string{"memory.max", "memory.swap.max", "cpu.max", "pids.max"} {
		path := filepath.Join(current, name)
		if err := privateCgroupPhysical(path, false); err != nil {
			return err
		}
		data, err := os.ReadFile(path)
		if err != nil || len(data) > 64 {
			return privateError("unavailable", err)
		}
		if err := privateCgroupPhysical(path, false); err != nil {
			return err
		}
		limits = append(limits, strings.TrimSpace(string(data)))
	}
	freshMembership, e1 := os.ReadFile("/proc/self/cgroup")
	freshMounts, e2 := os.ReadFile("/proc/self/mountinfo")
	if e1 != nil || e2 != nil || !bytes.Equal(membership, freshMembership) || !bytes.Equal(mounts, freshMounts) {
		return privateError("unavailable", errors.Join(e1, e2))
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

const privateColdURL = "https://nodejs.org/dist/v24.18.0/node-v24.18.0-linux-x64.tar.gz"
const privateColdSize int64 = 57224421
const privateColdSHA = "783130984963db7ba9cbd01089eaf2c2efb055c7c1693c943174b967b3050cb8"

func privateColdClient() (*http.Client, error) {
	roots, err := x509.SystemCertPool()
	if err != nil || roots == nil || len(roots.Subjects()) == 0 {
		return nil, privateError("acquisition", errors.Join(err, errors.New("system TLS roots unavailable")))
	}
	transport := &http.Transport{
		Proxy:                  nil,
		DisableCompression:     true,
		MaxResponseHeaderBytes: 16384,
		DialContext:            (&net.Dialer{Timeout: 10 * time.Second}).DialContext,
		TLSHandshakeTimeout:    10 * time.Second,
		ResponseHeaderTimeout:  30 * time.Second,
		TLSClientConfig:        &tls.Config{RootCAs: roots, MinVersion: tls.VersionTLS12, ServerName: "nodejs.org"},
	}
	return &http.Client{
		Transport:     transport,
		Timeout:       120 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}, nil
}

// privateColdReceive owns both resources. Small expected-pin fixtures are DATA
// seams only; the public API always supplies the independent fixed Node pins.
func privateColdReceive(ctx context.Context, response *http.Response, file io.WriteCloser, size int64, digest string) (err error) {
	defer func() {
		var bodyErr error
		if response != nil && response.Body != nil {
			bodyErr = response.Body.Close()
		}
		var fileErr error
		if file != nil {
			fileErr = file.Close()
		}
		var contextErr error
		if ctx != nil {
			contextErr = ctx.Err()
		}
		if joined := errors.Join(err, bodyErr, fileErr, contextErr); joined != nil {
			err = privateError("acquisition", joined)
		}
	}()
	root, _ := url.Parse("https://nodejs.org/")
	if ctx == nil || ctx.Err() != nil || file == nil || response == nil || response.Body == nil {
		return errors.New("missing or canceled acquisition resource")
	}
	if !gentleShellStableResponseMatches(response, root, "/dist/v24.18.0/node-v24.18.0-linux-x64.tar.gz") || response.StatusCode != http.StatusOK || response.ContentLength != size || response.Uncompressed {
		return errors.New("Node response origin, TLS, status or length differs")
	}
	encoding := response.Header.Values("Content-Encoding")
	if len(encoding) > 1 || (len(encoding) == 1 && encoding[0] != "" && !strings.EqualFold(encoding[0], "identity")) {
		return errors.New("Node response content encoding differs")
	}
	hash := sha256.New()
	count, copyErr := io.Copy(io.MultiWriter(file, hash), io.LimitReader(response.Body, size+1))
	if copyErr != nil || count != size || fmt.Sprintf("%x", hash.Sum(nil)) != digest {
		return errors.Join(copyErr, errors.New("Node DATA length or SHA256 differs"))
	}
	return nil
}

func privateColdFetch(ctx context.Context, client *http.Client, archive string, size int64, digest string) error {
	if ctx == nil || client == nil {
		return privateError("acquisition", errors.New("missing acquisition context/client"))
	}
	if err := ctx.Err(); err != nil {
		return privateError("acquisition", err)
	}
	ctx, cancel := context.WithTimeout(ctx, 120*time.Second)
	defer cancel()
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, privateColdURL, nil)
	if err != nil {
		return privateError("acquisition", err)
	}
	bounded := *client
	bounded.Jar = nil
	bounded.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	response, err := bounded.Do(request)
	if err != nil {
		return privateError("acquisition", err)
	}
	file, openErr := os.OpenFile(archive, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if openErr != nil {
		var closeErr error
		if response != nil && response.Body != nil {
			closeErr = response.Body.Close()
		}
		return privateError("acquisition", errors.Join(openErr, closeErr))
	}
	return privateColdReceive(ctx, response, file, size, digest)
}

func privateColdWorkspace(parent string, parentIdentity os.FileInfo, workspace string, identity os.FileInfo, dest string) error {
	freshParent, parentErr := privateDirectory(parent)
	freshStage, stageErr := privateDirectory(workspace)
	destErr := privateDestination(dest)
	if parentErr != nil || stageErr != nil || destErr != nil || parentIdentity == nil || identity == nil || !os.SameFile(parentIdentity, freshParent) || !os.SameFile(identity, freshStage) || filepath.Dir(workspace) != parent || filepath.Dir(dest) != parent || dest == workspace {
		return privateError("preimage", errors.Join(parentErr, stageErr, destErr))
	}
	return nil
}

func privateColdFinish(ctx context.Context, result PrivateInstallResult, err error, workspace string, identity os.FileInfo, dest string) (PrivateInstallResult, error) {
	if cleanupErr := privateCleanup(workspace, identity); cleanupErr != nil {
		err = privateError("uncertain", errors.Join(err, cleanupErr))
	}
	if ctx.Err() != nil {
		err = errors.Join(err, privateError("canceled", ctx.Err()))
	}
	if err != nil {
		failure := privateFailure(dest, err)
		failure.Workspace, failure.Destination = workspace, dest
		return PrivateInstallResult{}, failure
	}
	return result, nil
}

// RunPrivateInstallCold requires separate external caller effect approval and a
// private single writer, just like RunPrivateInstall. It acquires only fixed Node
// DATA before the entire existing installation; it never launches Pi or claims
// Ready, provisions Host isolation, or accepts caller-selected trust authorities.
func RunPrivateInstallCold(ctx context.Context, destination string) (result PrivateInstallResult, err error) {
	if ctx == nil {
		return result, privateError("canceled", context.Canceled)
	}
	if err = ctx.Err(); err != nil {
		return result, privateError("canceled", err)
	}
	ctx, cancel := context.WithTimeout(ctx, 870*time.Second)
	defer cancel()
	if err = privateKernel(); err != nil {
		return result, err
	}
	if err = privateDestination(destination); err != nil {
		return result, err
	}
	parent := filepath.Dir(destination)
	parentIdentity, err := privateDirectory(parent)
	if err != nil {
		return result, err
	}
	if err = ctx.Err(); err != nil {
		return result, privateError("canceled", err)
	}
	workspace, err := os.MkdirTemp(parent, ".gentle-go-cold-")
	if err != nil {
		return result, privateError("filesystem", err)
	}
	identity, err := privateDirectory(workspace)
	defer func() {
		result, err = privateColdFinish(ctx, result, err, workspace, identity, destination)
	}()
	if err != nil {
		return result, err
	}
	client, err := privateColdClient()
	if err != nil {
		return result, err
	}
	defer client.CloseIdleConnections()
	archive := filepath.Join(workspace, "node.tgz")
	if err = privateColdFetch(ctx, client, archive, privateColdSize, privateColdSHA); err != nil {
		return result, err
	}
	client.CloseIdleConnections() // No live HTTP body/file or idle connection reaches Node.
	info, err := privatePhysical(archive)
	if err != nil || info.Mode() != 0600 || info.Size() != privateColdSize || info.Sys().(*syscall.Stat_t).Uid != uint32(os.Getuid()) {
		return result, privateError("preimage", err)
	}
	if err = privateColdWorkspace(parent, parentIdentity, workspace, identity, destination); err != nil {
		return result, err
	}
	if err = ctx.Err(); err != nil {
		return result, privateError("canceled", err)
	}
	result, err = RunPrivateInstall(ctx, destination, archive)
	if err == nil {
		err = privateReadback(destination, result.LockSHA256)
	}
	return result, err
}
