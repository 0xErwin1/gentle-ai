//go:build linux

package shellinstaller

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func privateNativeRejected(t *testing.T, result PrivateNativeResult, err error, kind string) {
	t.Helper()
	var failure *PrivateRuntimeError
	if result != (PrivateNativeResult{}) || !errors.As(err, &failure) || failure.Kind != kind {
		t.Fatalf("native rejection: want %s, got %v", kind, err)
	}
}

func TestPrivateNativeControls(t *testing.T) {
	privateGuest(t)
	// These literals are independent of production constants and local inventories.
	if privateNativeNodeSHA != "41a74efb34cbde5c7632cdac0cf8bd1a14d0b8d73dc1e82755014d9a9ce70f5c" || privateNativeNodeSize != 123655872 || privateNativeInstallerSHA != "0d57ec9926847b7363362e5a07d999b464b9cf93a6cffd62b0acc093259df7fb" || privateNativeResolverSHA != "cbdf5deac8b7a85ab1253dbd049953aeb192a7d1f7987f9206916ab449c10a92" || privateNativeBinarySHA != "002d09fd2b9628a29986a660c1f51f8a5042ff7fd54c8ab15b7b27de96c6cccc" {
		t.Fatal("independent native/Node/source pins differ")
	}
	manifest := "{\"version\":\"3.7.0\",\"asset\":\"gentle-ai_3.7.0_linux_amd64.tar.gz\",\"assetSha256\":\"a730a61a43758f04cc9a4ac644945cc0e8652a1e33d6997a0a3d3f0044d2fff5\",\"binarySha256\":\"002d09fd2b9628a29986a660c1f51f8a5042ff7fd54c8ab15b7b27de96c6cccc\"}\n"
	if privateNativeManifest != manifest {
		t.Fatal("canonical manifest differs")
	}
	workspace := t.TempDir()
	env := privateNativeEnvironment(workspace)
	want := []string{"PATH=/usr/bin:/bin", "HOME=" + workspace + "/home", "TMPDIR=" + workspace + "/tmp", "XDG_CONFIG_HOME=" + workspace + "/config", "XDG_STATE_HOME=" + workspace + "/state", "GENTLE_PI_CONFIG_HOME=" + workspace + "/config"}
	if !reflect.DeepEqual(env, want) {
		t.Fatal("private environment differs")
	}
	for _, selector := range []string{"NODE_OPTIONS", "NODE_PATH", "GENTLE_PI_GENTLE_AI_DEV_BINARY", "GITHUB_TOKEN", "HTTP_PROXY", "HTTPS_PROXY", "NPM_CONFIG_USERCONFIG"} {
		for _, entry := range env {
			if strings.HasPrefix(entry, selector+"=") {
				t.Fatal("ambient selector retained", selector)
			}
		}
	}
	if !strings.Contains(privateNativeInvoke, "installGentleAi({packageRoot})") || strings.Contains(privateNativeInvoke, "process.env") || strings.Contains(privateNativeInvoke, "install-gentle-ai.mjs") {
		t.Fatal("supplier invocation changed")
	}
	// Owned-filesystem hash controls only: no fixture executable is launched.
	path := filepath.Join(workspace, "data")
	data := []byte("owned bounded file DATA")
	pin := fmt.Sprintf("%x", sha256.Sum256(data))
	privateMust(t, os.WriteFile(path, data, 0600))
	before, err := privateNativeFile(context.Background(), path, 0600, int64(len(data)), pin)
	privateMust(t, err)
	cases := []struct {
		name string
		mode os.FileMode
		size int64
		pin  string
	}{
		{"mode", 0644, int64(len(data)), pin},
		{"size", 0600, int64(len(data)) + 1, pin},
		{"hash", 0600, int64(len(data)), strings.Repeat("0", 64)},
	}
	for _, tc := range cases {
		if _, err := privateNativeFile(context.Background(), path, tc.mode, tc.size, tc.pin); err == nil {
			t.Fatal("file DATA accepted", tc.name)
		}
	}
	privateMust(t, os.Chmod(path, 0600|os.ModeSticky))
	if _, err := privateNativeFile(context.Background(), path, 0600, int64(len(data)), pin); err == nil {
		t.Fatal("special mode accepted")
	}
	privateMust(t, os.Chmod(path, 0600))
	privateMust(t, os.Rename(path, path+".old"))
	privateMust(t, os.WriteFile(path, data, 0600))
	after, err := privateNativeFile(context.Background(), path, 0600, int64(len(data)), pin)
	privateMust(t, err)
	if before == after {
		t.Fatal("same-content replacement identity missed")
	}
	link := filepath.Join(workspace, "link")
	privateMust(t, os.Symlink(path, link))
	for _, candidate := range []string{link, workspace, filepath.Join(workspace, "missing"), "/cold-root-owned", "/node.tgz"} {
		if _, err := privateNativeFile(context.Background(), candidate, 0600, -1, ""); err == nil {
			t.Fatal("nonregular/foreign physical file accepted")
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := privateNativeFile(ctx, path, 0600, -1, ""); !errors.Is(err, context.Canceled) {
		t.Fatal("file read ignored cancellation", err)
	}
}

func TestPrivateNativeStates(t *testing.T) {
	privateGuest(t)
	// Never supplier input: these classify actual owned filesystem states only.
	cases := []string{"absent", "empty", "file", "symlink", "mode", "lock", "tombstone", "backup", "staging", "unknown", "bad live", "too many"}
	for _, name := range cases {
		base := t.TempDir()
		root := filepath.Join(base, ".gentle-ai")
		if name == "file" {
			privateMust(t, os.WriteFile(root, []byte("preserve"), 0600))
		} else if name == "symlink" {
			privateMust(t, os.Symlink(base, root))
		} else if name != "absent" {
			privateMust(t, os.Mkdir(root, 0700))
			switch name {
			case "mode":
				privateMust(t, os.Chmod(root, 0755))
			case "lock", "tombstone", "backup", "staging", "unknown", "bad live":
				entry := map[string]string{"lock": ".v3.7.0.install.lock", "tombstone": ".v3.7.0.install.tombstone-nonce", "backup": ".v3.7.0.backup-1", "staging": ".v3.7.0.staging-1", "unknown": "foreign", "bad live": "v3.7.0"}[name]
				privateMust(t, os.Mkdir(filepath.Join(root, entry), 0700))
				if name == "bad live" {
					privateMust(t, os.WriteFile(filepath.Join(root, entry, "gentle-ai"), []byte("wrong binary DATA"), 0700))
					privateMust(t, os.WriteFile(filepath.Join(root, entry, "integrity.json"), []byte(privateNativeManifest), 0600))
				}
			case "too many":
				for i := 0; i < 18; i++ {
					privateMust(t, os.Mkdir(filepath.Join(root, fmt.Sprint(i)), 0700))
				}
			}
		}
		preimage := privateFixture(t, base)
		valid, err := privateNativeState(context.Background(), root)
		if valid || (err == nil) != (name == "absent" || name == "empty") || !reflect.DeepEqual(preimage, privateFixture(t, base)) {
			t.Fatal("native state classifier changed preimage", name, err)
		}
	}
	_, err := privateDirectory("/cold-root-owned")
	if err == nil {
		t.Fatal("actual foreign private directory accepted")
	}
}

func TestPrivateNativeFinish(t *testing.T) {
	privateGuest(t)
	// Finalizer faults are real filesystem effects, not public transport injection.
	for _, name := range []string{"clean absent", "empty runtime", "partial", "published", "permission", "replacement", "nil identity", "cancel"} {
		parent := t.TempDir()
		root := filepath.Join(parent, ".gentle-ai")
		workspace, err := os.MkdirTemp(parent, ".gentle-native-")
		privateMust(t, err)
		identity, err := privateDirectory(workspace)
		privateMust(t, err)
		if name != "clean absent" {
			privateMust(t, os.Mkdir(root, 0700))
		}
		if name == "partial" || name == "published" {
			entry := ".v3.7.0.install.lock"
			if name == "published" {
				entry = "v3.7.0"
			}
			privateMust(t, os.Mkdir(filepath.Join(root, entry), 0700))
			privateMust(t, os.WriteFile(filepath.Join(root, entry, "sentinel"), []byte("never delete"), 0600))
		}
		blocked := filepath.Join(workspace, "blocked")
		if name == "permission" {
			privateMust(t, os.Mkdir(blocked, 0700))
			privateMust(t, os.WriteFile(filepath.Join(blocked, "child"), []byte("owned"), 0600))
			privateMust(t, os.Chmod(blocked, 0000))
		}
		if name == "replacement" {
			privateMust(t, os.Rename(workspace, workspace+".old"))
			privateMust(t, os.Mkdir(workspace, 0700))
			privateMust(t, os.WriteFile(filepath.Join(workspace, "foreign"), []byte("preserve"), 0600))
		}
		if name == "nil identity" {
			identity = nil
		}
		var runtimeBefore map[string]string
		if name != "clean absent" {
			runtimeBefore = privateFixture(t, root)
		}
		var workspaceBefore map[string]string
		if name == "replacement" || name == "nil identity" {
			workspaceBefore = privateFixture(t, workspace)
		}
		ctx, cancel := context.WithCancel(context.Background())
		if name == "cancel" {
			cancel()
		}
		result, finishErr := privateNativeFinish(ctx, PrivateNativeResult{}, privateError("nonzero", nil), root, workspace, identity)
		cancel()
		kind := "uncertain"
		if name == "clean absent" {
			kind = "nonzero"
		}
		privateNativeRejected(t, result, finishErr, kind)
		var failure *PrivateRuntimeError
		if !errors.As(finishErr, &failure) || failure.Destination != root || failure.Workspace != workspace {
			t.Fatal("recovery paths missing")
		}
		if name == "permission" {
			privateMust(t, os.Chmod(blocked, 0700))
		}
		if runtimeBefore != nil && !reflect.DeepEqual(runtimeBefore, privateFixture(t, root)) {
			t.Fatal("native state deleted by wrapper finalizer")
		}
		if workspaceBefore != nil && !reflect.DeepEqual(workspaceBefore, privateFixture(t, workspace)) {
			t.Fatal("foreign workspace deleted")
		}
	}
}

func TestPrivateNativeInstall(t *testing.T) {
	privateGuest(t)
	parent := t.TempDir()
	privateMust(t, os.Chmod(parent, 0700))
	pi := filepath.Join(parent, "pi")
	privateMust(t, os.Mkdir(pi, 0700))
	privateMust(t, os.WriteFile(filepath.Join(pi, "settings.json"), []byte("unrelated Pi preimage"), 0600))
	privateMust(t, os.Symlink("settings.json", filepath.Join(pi, "link")))
	piBefore := privateFixture(t, pi)
	dest := filepath.Join(parent, "installed")
	for _, name := range []string{"nil", "cancel", "expired", "relative", "missing", "file", "symlink", "empty", "foreign owner"} {
		ctx := context.Background()
		candidate := dest
		kind := "refused"
		var cancel context.CancelFunc
		switch name {
		case "nil":
			ctx, kind = nil, "canceled"
		case "cancel":
			ctx, cancel = context.WithCancel(ctx)
			cancel()
			kind = "canceled"
		case "expired":
			ctx, cancel = context.WithDeadline(ctx, time.Now().Add(-time.Second))
			defer cancel()
			kind = "canceled"
		case "relative":
			candidate = "relative"
		case "file":
			candidate = filepath.Join(parent, "file")
			privateMust(t, os.WriteFile(candidate, []byte("preserve"), 0600))
		case "symlink":
			candidate = filepath.Join(parent, "symlink")
			privateMust(t, os.Symlink(pi, candidate))
		case "empty":
			candidate = filepath.Join(parent, "empty")
			privateMust(t, os.Mkdir(candidate, 0700))
		case "foreign owner":
			candidate = "/cold-root-owned"
		}
		before := privateFixture(t, parent)
		result, err := RunPrivateNativeInstall(ctx, candidate)
		privateNativeRejected(t, result, err, kind)
		if !reflect.DeepEqual(before, privateFixture(t, parent)) {
			t.Fatal("early public refusal changed preimage", name)
		}
	}
	// Positive authority comes from the actual public Go installation, not fixtures.
	installed, err := RunPrivateInstall(context.Background(), dest, "/node.tgz")
	privateMust(t, err)
	privateMust(t, privateReadback(dest, installed.LockSHA256))
	pkg := filepath.Join(dest, "project/node_modules/gentle-pi")
	root := filepath.Join(pkg, ".gentle-ai")
	// No nested success logs: every named case executes under the unchanged raw cap.
	mutations := []struct {
		name, path string
		mode       os.FileMode
		content    bool
	}{
		{"Node content", "node/bin/node", 0700, true},
		{"Node mode", "node/bin/node", 0644, false},
		{"supplier content", "project/node_modules/gentle-pi/scripts/gentle-ai-installer.mjs", 0600, true},
		{"supplier mode", "project/node_modules/gentle-pi/scripts/gentle-ai-installer.mjs", 0664, false},
		{"resolver content", "project/node_modules/gentle-pi/runtime/gentle-ai-binary.mjs", 0600, true},
		{"metadata content", "project/node_modules/gentle-pi/package.json", 0644, true},
		{"lock content", "project/package-lock.json", 0600, true},
		{"closure content", "closure.json", 0600, true},
	}
	for _, tc := range mutations {
		path := filepath.Join(dest, tc.path)
		info, err := os.Lstat(path)
		privateMust(t, err)
		// Rename preserves the original 124MB Node; never load fixture code.
		privateMust(t, os.Rename(path, path+".native-preimage"))
		if tc.content {
			privateMust(t, os.WriteFile(path, []byte("incorrect DATA"), tc.mode))
			if tc.name == "Node content" {
				file, err := os.OpenFile(path, os.O_WRONLY, 0)
				privateMust(t, err)
				privateMust(t, file.Truncate(123655872))
				privateMust(t, file.Close())
			}
		} else {
			data, err := os.ReadFile(path + ".native-preimage")
			privateMust(t, err)
			privateMust(t, os.WriteFile(path, data, tc.mode))
			privateMust(t, os.Chmod(path, tc.mode))
		}
		before := privateFixture(t, dest)
		result, err := RunPrivateNativeInstall(context.Background(), dest)
		privateNativeRejected(t, result, err, "refused")
		if !reflect.DeepEqual(before, privateFixture(t, dest)) {
			t.Fatal("source rejection changed installed root", tc.name)
		}
		privateMust(t, os.Remove(path))
		privateMust(t, os.Rename(path+".native-preimage", path))
		privateMust(t, os.Chmod(path, info.Mode()))
	}
	for _, relative := range []string{"node/bin/node", "project/node_modules/gentle-pi/scripts/gentle-ai-installer.mjs", "project/node_modules/gentle-pi/runtime/gentle-ai-binary.mjs", "project/package-lock.json"} {
		path := filepath.Join(dest, relative)
		privateMust(t, os.Rename(path, path+".native-preimage"))
		privateMust(t, os.Symlink(path+".native-preimage", path))
		before := privateFixture(t, dest)
		result, err := RunPrivateNativeInstall(context.Background(), dest)
		privateNativeRejected(t, result, err, "refused")
		if !reflect.DeepEqual(before, privateFixture(t, dest)) {
			t.Fatal("symlink refusal changed preimage", relative)
		}
		privateMust(t, os.Remove(path))
		privateMust(t, os.Rename(path+".native-preimage", path))
	}
	for _, relative := range []string{".", "project/node_modules/gentle-pi/scripts", "project/node_modules/gentle-pi/runtime"} {
		path := filepath.Join(dest, relative)
		info, err := os.Lstat(path)
		privateMust(t, err)
		privateMust(t, os.Chmod(path, 0777))
		before := privateFixture(t, dest)
		result, err := RunPrivateNativeInstall(context.Background(), dest)
		privateNativeRejected(t, result, err, "refused")
		if !reflect.DeepEqual(before, privateFixture(t, dest)) {
			t.Fatal("writable source directory accepted", relative)
		}
		privateMust(t, os.Chmod(path, info.Mode()))
	}
	privateMust(t, os.Mkdir(root, 0700))
	for _, entry := range []string{".v3.7.0.install.lock", ".v3.7.0.install.tombstone-nonce", ".v3.7.0.backup-1", ".v3.7.0.staging-1", "foreign", "v3.7.0"} {
		path := filepath.Join(root, entry)
		privateMust(t, os.Mkdir(path, 0700))
		before := privateFixture(t, dest)
		result, err := RunPrivateNativeInstall(context.Background(), dest)
		privateNativeRejected(t, result, err, "refused")
		if !reflect.DeepEqual(before, privateFixture(t, dest)) {
			t.Fatal("public preflight allowed supplier recovery", entry)
		}
		privateMust(t, os.Remove(path))
	}
	// Generated local provenance is not Node executable authority, even if replaced.
	node := filepath.Join(dest, "node/bin/node")
	inventory := filepath.Join(dest, "node/BOOTSTRAP-SHA256SUMS")
	privateMust(t, os.Rename(node, node+".native-preimage"))
	privateMust(t, os.Rename(inventory, inventory+".native-preimage"))
	privateMust(t, os.WriteFile(node, []byte("fake Node DATA"), 0755))
	privateMust(t, os.WriteFile(inventory, []byte("caller-generated matching inventory DATA"), 0600))
	before := privateFixture(t, dest)
	result, err := RunPrivateNativeInstall(context.Background(), dest)
	privateNativeRejected(t, result, err, "refused")
	if !reflect.DeepEqual(before, privateFixture(t, dest)) {
		t.Fatal("mutable inventory authorized Node")
	}
	privateMust(t, os.Remove(node))
	privateMust(t, os.Remove(inventory))
	privateMust(t, os.Rename(node+".native-preimage", node))
	privateMust(t, os.Rename(inventory+".native-preimage", inventory))
	before = privateFixture(t, dest)
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	result, err = RunPrivateNativeInstall(ctx, dest)
	if err != nil {
		t.Fatalf("native positive: %v; detail=%v", err, errors.Unwrap(errors.Unwrap(errors.Unwrap(err))))
	}
	if result.State != "NativeInstalled" || result.StateRoot != root || result.Version != "3.7.0" || result.BinarySHA256 != privateNativeBinarySHA || result.Manifest != privateNativeManifest {
		t.Fatal("public native component result differs")
	}
	privateMust(t, privateNativeReadback(context.Background(), root))
	// Supplier resolver executes only after independent Go physical/pin readback.
	workspace := t.TempDir()
	for _, name := range []string{"home", "tmp", "config", "state"} {
		privateMust(t, os.Mkdir(filepath.Join(workspace, name), 0700))
	}
	probeCtx, probeCancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer probeCancel()
	cmd := exec.CommandContext(probeCtx, node, "--input-type=module", "-e", `import {resolveGentleAiBinary} from './runtime/gentle-ai-binary.mjs'; process.stdout.write(resolveGentleAiBinary(process.cwd()));`)
	cmd.Dir, cmd.Env = pkg, privateNativeEnvironment(workspace)
	output, err := privateRun(probeCtx, cmd, probeCancel)
	privateMust(t, err)
	if output != filepath.Join(root, "v3.7.0/gentle-ai") {
		t.Fatal("stock resolver did not select package pin")
	}
	// The wrapper ran the installed fixed binary's version ONLY; repeat no effects.
	published := privateFixture(t, dest)
	retry, err := RunPrivateNativeInstall(context.Background(), dest)
	privateMust(t, err)
	if retry != result || !reflect.DeepEqual(published, privateFixture(t, dest)) {
		t.Fatal("authenticated native retry was not idempotent")
	}
	for path, stamp := range before {
		if path != pkg && path != root && published[path] != stamp {
			t.Fatal("Go installation preimage changed", path)
		}
	}
	if !reflect.DeepEqual(piBefore, privateFixture(t, pi)) {
		t.Fatal("Pi preimage changed")
	}
	entries, err := os.ReadDir(root)
	privateMust(t, err)
	if len(entries) != 1 || entries[0].Name() != "v3.7.0" {
		t.Fatal("successful native install left lock/stage/recovery state")
	}
	// Actual published root plus actual owned cleanup denial through the shared
	// finalizer. This is not an injected public supplier/network failure.
	cleanupStage, err := os.MkdirTemp(parent, ".gentle-native-")
	privateMust(t, err)
	identity, err := privateDirectory(cleanupStage)
	privateMust(t, err)
	blocked := filepath.Join(cleanupStage, "blocked")
	privateMust(t, os.Mkdir(blocked, 0700))
	privateMust(t, os.WriteFile(filepath.Join(blocked, "child"), []byte("cleanup witness"), 0600))
	privateMust(t, os.Chmod(blocked, 0000))
	uncertain, cleanupErr := privateNativeFinish(context.Background(), result, nil, root, cleanupStage, identity)
	privateMust(t, os.Chmod(blocked, 0700))
	privateNativeRejected(t, uncertain, cleanupErr, "uncertain")
	if !reflect.DeepEqual(published, privateFixture(t, dest)) || !reflect.DeepEqual(piBefore, privateFixture(t, pi)) {
		t.Fatal("cleanup denial changed published native or Pi state")
	}
	privateMust(t, privateCleanup(cleanupStage, identity))
	entries, err = os.ReadDir(parent)
	privateMust(t, err)
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), ".gentle-native-") {
			t.Fatal("wrapper workspace leaked")
		}
	}
	privateNoStage(t, parent)
}
