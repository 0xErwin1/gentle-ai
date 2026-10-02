//go:build linux

package shellinstaller

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"syscall"
	"time"
)

// Node ELF pin independently derived from the fully archive-pinned v24.18.0
// Linux-x64 regular member, not from a mutable installed bootstrap inventory.
const privateNativeNodeSize int64 = 123655872
const privateNativeNodeSHA = "41a74efb34cbde5c7632cdac0cf8bd1a14d0b8d73dc1e82755014d9a9ce70f5c"
const privateNativeInstallerSHA = "0d57ec9926847b7363362e5a07d999b464b9cf93a6cffd62b0acc093259df7fb"
const privateNativeResolverSHA = "cbdf5deac8b7a85ab1253dbd049953aeb192a7d1f7987f9206916ab449c10a92"
const privateNativeBinarySHA = "002d09fd2b9628a29986a660c1f51f8a5042ff7fd54c8ab15b7b27de96c6cccc"
const privateNativeManifest = `{"version":"3.7.0","asset":"gentle-ai_3.7.0_linux_amd64.tar.gz","assetSha256":"a730a61a43758f04cc9a4ac644945cc0e8652a1e33d6997a0a3d3f0044d2fff5","binarySha256":"002d09fd2b9628a29986a660c1f51f8a5042ff7fd54c8ab15b7b27de96c6cccc"}` + "\n"
const privateNativeInvoke = `import {installGentleAi} from './scripts/gentle-ai-installer.mjs'; const packageRoot=process.cwd(); await installGentleAi({packageRoot});`

// A component result, not Gentle Shell Ready, signature custody or loaded bytes.
type PrivateNativeResult struct {
	State, StateRoot, Version, BinarySHA256, Manifest string
}

func privateNativeFile(ctx context.Context, path string, mode os.FileMode, size int64, pin string) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	info, err := privatePhysical(path)
	if err != nil || info.Sys().(*syscall.Stat_t).Uid != uint32(os.Getuid()) || (mode != 0 && info.Mode() != mode) || (mode == 0 && info.Mode() != 0600 && info.Mode() != 0644 && info.Mode() != 0755) || info.Size() > 268435456 || (size >= 0 && info.Size() != size) {
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

// Inspect DATA without loading Node. Local generated inventory is a preimage,
// never executable authority: Node and both modules have independent fixed pins.
func privateNativeAuthority(ctx context.Context, dest string) (string, error) {
	if !privateHierarchyPath(dest) || !regexp.MustCompile(`^[A-Za-z0-9_.-]+$`).MatchString(filepath.Base(dest)) {
		return "", privateError("refused", nil)
	}
	if _, err := privateDirectory(dest); err != nil {
		return "", err
	}
	if _, err := privateDirectory(filepath.Dir(dest)); err != nil {
		return "", err
	}
	pkg := filepath.Join(dest, "project/node_modules/gentle-pi")
	var witness strings.Builder
	for _, path := range []string{
		filepath.Dir(dest), dest, filepath.Join(dest, "node"), filepath.Join(dest, "node/bin"),
		filepath.Join(dest, "node/lib/node_modules/npm"), filepath.Join(dest, "project"),
		filepath.Join(dest, "project/node_modules"), pkg, filepath.Join(pkg, "scripts"), filepath.Join(pkg, "runtime"),
	} {
		if err := privateNativeDirectory(path); err != nil {
			return "", err
		}
		info, err := os.Lstat(path)
		if err != nil {
			return "", err
		}
		st := info.Sys().(*syscall.Stat_t)
		witness.WriteString(fmt.Sprintf("%s:%d:%d:%d:%v", path, st.Dev, st.Ino, st.Uid, info.Mode()))
	}
	files := []struct {
		path, pin string
		mode      os.FileMode
		size      int64
	}{
		{"node/bin/node", privateNativeNodeSHA, 0700, privateNativeNodeSize},
		{"project/node_modules/gentle-pi/scripts/gentle-ai-installer.mjs", privateNativeInstallerSHA, 0644, 40940},
		{"project/node_modules/gentle-pi/runtime/gentle-ai-binary.mjs", privateNativeResolverSHA, 0644, 15444},
		{"project/package.json", "", 0, -1},
		{"project/package-lock.json", "", 0, -1},
		{"closure.json", "", 0, -1},
		{"node/lib/node_modules/npm/package.json", "", 0, -1},
		{"node/lib/node_modules/npm/bin/npm-cli.js", "", 0, -1},
		{"node/BOOTSTRAP-PROVENANCE", "", 0, -1},
		{"node/BOOTSTRAP-SHA256SUMS", "", 0, -1},
		{"SHA256SUMS", "", 0, -1},
	}
	for _, file := range files {
		stamp, err := privateNativeFile(ctx, filepath.Join(dest, file.path), file.mode, file.size, file.pin)
		if err != nil {
			return "", err
		}
		witness.WriteString(file.path + stamp)
	}
	read := func(relative string, target any) error {
		path := filepath.Join(dest, relative)
		info, err := privatePhysical(path)
		if err != nil || info.Size() > 8388608 {
			return privateError("source", err)
		}
		data, err := os.ReadFile(path)
		if err == nil {
			err = json.Unmarshal(data, target)
		}
		return err
	}
	var project struct{ Dependencies map[string]string }
	var lock struct {
		LockfileVersion int
		Packages        map[string]struct {
			Name, Version, Integrity string
			Dependencies             map[string]string
		}
	}
	var closure []string
	var npm struct{ Version string }
	if err := errors.Join(read("project/package.json", &project), read("project/package-lock.json", &lock), read("closure.json", &closure), read("node/lib/node_modules/npm/package.json", &npm)); err != nil {
		return "", err
	}
	pins := map[string]string{"gentle-pi": "3.7.0", "@earendil-works/pi-coding-agent": "0.85.1", "@earendil-works/pi-tui": "0.85.1", "@heyhuynhgiabuu/pi-pretty": "0.6.27", "typebox": "1.3.7"}
	if lock.LockfileVersion != 3 || len(project.Dependencies) != 5 || len(lock.Packages[""].Dependencies) != 5 || len(closure) != 283 || npm.Version != "11.16.0" {
		return "", privateError("source", nil)
	}
	for name, version := range pins {
		if project.Dependencies[name] != version || lock.Packages[""].Dependencies[name] != version || lock.Packages["node_modules/"+name].Version != version {
			return "", privateError("source", nil)
		}
	}
	if lock.Packages["node_modules/gentle-pi"].Integrity != "sha512-SXBp9jIRnVIcOsLCW/Zw4XDTXxhViXNxrCZS7LyioB6kdRl99Ohojeaj+yIH5+K/ucwLlTlHYBz8zwue9aspNQ==" {
		return "", privateError("source", nil)
	}
	seen := map[string]bool{}
	for _, entry := range closure {
		if !strings.HasPrefix(entry, "node_modules/") || filepath.Clean(entry) != entry || strings.Contains(entry, "..") || seen[entry] || lock.Packages[entry].Version == "" {
			return "", privateError("source", nil)
		}
		seen[entry] = true
		path := "project/" + entry + "/package.json"
		stamp, err := privateNativeFile(ctx, filepath.Join(dest, path), 0, -1, "")
		var metadata struct{ Name, Version string }
		name := lock.Packages[entry].Name
		if name == "" {
			parts := strings.Split(entry, "node_modules/")
			name = parts[len(parts)-1]
		}
		if err != nil || read(path, &metadata) != nil || metadata.Version != lock.Packages[entry].Version || metadata.Name != name {
			return "", privateError("source", err)
		}
		witness.WriteString(path + stamp)
	}
	lockSHA, err := privateLock(filepath.Join(dest, "project/package-lock.json"))
	if err != nil {
		return "", err
	}
	return witness.String(), privateReadback(dest, lockSHA)
}

func privateNativeReadback(ctx context.Context, root string) error {
	for _, directory := range []string{root, filepath.Join(root, "v3.7.0")} {
		if _, err := privateDirectory(directory); err != nil {
			return err
		}
	}
	if _, err := privateNativeFile(ctx, filepath.Join(root, "v3.7.0/gentle-ai"), 0700, -1, privateNativeBinarySHA); err != nil {
		return err
	}
	manifest := filepath.Join(root, "v3.7.0/integrity.json")
	if _, err := privateNativeFile(ctx, manifest, 0600, int64(len(privateNativeManifest)), fmt.Sprintf("%x", sha256.Sum256([]byte(privateNativeManifest)))); err != nil {
		return err
	}
	entries, err := privateNativeEntries(filepath.Join(root, "v3.7.0"))
	if err != nil || len(entries) != 2 || !((entries[0].Name() == "gentle-ai" && entries[1].Name() == "integrity.json") || (entries[1].Name() == "gentle-ai" && entries[0].Name() == "integrity.json")) {
		return privateError("readback", err)
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

func privateNativeState(ctx context.Context, root string) (bool, error) {
	if _, err := os.Lstat(root); os.IsNotExist(err) {
		return false, nil
	}
	if _, err := privateDirectory(root); err != nil {
		return false, err
	}
	entries, err := privateNativeEntries(root)
	if err != nil || len(entries) > 1 || (len(entries) == 1 && entries[0].Name() != "v3.7.0") {
		return false, privateError("refused", err)
	}
	if len(entries) == 0 {
		return false, nil
	}
	err = privateNativeReadback(ctx, root)
	return err == nil, err
}

func privateNativeEnvironment(workspace string) []string {
	return []string{"PATH=/usr/bin:/bin", "HOME=" + workspace + "/home", "TMPDIR=" + workspace + "/tmp", "XDG_CONFIG_HOME=" + workspace + "/config", "XDG_STATE_HOME=" + workspace + "/state", "GENTLE_PI_CONFIG_HOME=" + workspace + "/config"}
}

func privateNativeFinish(ctx context.Context, result PrivateNativeResult, cause error, root, workspace string, identity os.FileInfo) (PrivateNativeResult, error) {
	cleanupErr := privateCleanup(workspace, identity)
	cause = errors.Join(cause, cleanupErr, ctx.Err())
	if cause == nil {
		return result, nil
	}
	inspection, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	_, inspectionErr := privateNativeState(inspection, root)
	cancel()
	cause = errors.Join(cause, inspectionErr)
	kind := "uncertain"
	// Never reclaim supplier paths. Even an empty created runtime is evidence
	// of partial effects; inspection failure or publication requires recovery.
	if _, err := os.Lstat(root); os.IsNotExist(err) && cleanupErr == nil {
		var failure *PrivateRuntimeError
		if errors.As(cause, &failure) {
			kind = failure.Kind
		}
	}
	failure := privateError(kind, cause)
	failure.Workspace, failure.Destination = workspace, root
	return PrivateNativeResult{}, failure
}

// RunPrivateNativeInstall requires separate external caller effect approval.
// Cooperative private single writer only; no hostile same-UID or loaded-byte
// guarantee. It runs the stock supplier and version ONLY, never Pi or an updater.
func RunPrivateNativeInstall(ctx context.Context, destination string) (result PrivateNativeResult, err error) {
	if ctx == nil {
		return result, privateError("canceled", context.Canceled)
	}
	if err = ctx.Err(); err != nil {
		return result, privateError("canceled", err)
	}
	ctx, cancel := context.WithTimeout(ctx, 120*time.Second)
	defer cancel()
	if err = privateKernel(); err != nil {
		return result, err
	}
	status, statusErr := os.ReadFile("/proc/self/status")
	if statusErr != nil || runtime.GOARCH != "amd64" || os.Getuid() != 65532 || !strings.Contains(string(status), "CapEff:\t0000000000000000") || !strings.Contains(string(status), "NoNewPrivs:\t1") {
		return result, privateError("unavailable", statusErr)
	}
	before, err := privateNativeAuthority(ctx, destination)
	if err != nil {
		if ctx.Err() != nil {
			return result, privateError("canceled", ctx.Err())
		}
		return result, privateError("refused", err)
	}
	pkg := filepath.Join(destination, "project/node_modules/gentle-pi")
	root := filepath.Join(pkg, ".gentle-ai")
	present, err := privateNativeState(ctx, root)
	if err != nil {
		return result, privateError("refused", err)
	}
	if err = ctx.Err(); err != nil {
		return result, privateError("canceled", err)
	}
	workspace, err := os.MkdirTemp(filepath.Dir(destination), ".gentle-native-")
	if err != nil {
		return result, privateError("filesystem", err)
	}
	identity, err := privateDirectory(workspace)
	defer func() {
		fresh, sourceErr := privateNativeAuthority(ctx, destination)
		if sourceErr != nil || fresh != before {
			err = errors.Join(err, privateError("preimage", sourceErr))
		}
		result, err = privateNativeFinish(ctx, result, err, root, workspace, identity)
	}()
	if err != nil {
		return result, err
	}
	for _, name := range []string{"home", "tmp", "config", "state"} {
		if err = os.Mkdir(filepath.Join(workspace, name), 0700); err != nil {
			return result, err
		}
	}
	if !present {
		cmd := exec.CommandContext(ctx, filepath.Join(destination, "node/bin/node"), "--max-old-space-size=256", "--input-type=module", "-e", privateNativeInvoke)
		cmd.Dir, cmd.Env = pkg, privateNativeEnvironment(workspace)
		if _, err = privateRun(ctx, cmd, cancel); err != nil {
			return result, err
		}
	}
	if _, err = privateNativeState(ctx, root); err != nil {
		return result, err
	}
	if err = privateNativeReadback(ctx, root); err != nil {
		return result, err
	}
	probeCtx, probeCancel := context.WithTimeout(ctx, 15*time.Second)
	defer probeCancel()
	cmd := exec.CommandContext(probeCtx, filepath.Join(root, "v3.7.0/gentle-ai"), "version")
	cmd.Dir, cmd.Env = workspace, privateNativeEnvironment(workspace)
	output, err := privateRun(probeCtx, cmd, probeCancel)
	if err != nil || output != "gentle-ai 3.7.0\n" {
		return result, privateError("readback", err)
	}
	if err = privateNativeReadback(ctx, root); err != nil {
		return result, err
	}
	return PrivateNativeResult{"NativeInstalled", root, "3.7.0", privateNativeBinarySHA, privateNativeManifest}, nil
}
