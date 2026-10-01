//go:build linux

package shellinstaller

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	assets "github.com/gentleman-programming/gentle-ai/v3/scripts"
	"golang.org/x/sys/unix"
)

func privateMust(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

func privateGuest(t *testing.T) {
	t.Helper()
	if testing.Short() || os.Getenv("GENTLE_PRIVATE_GUEST") != "approved" {
		t.Skip("requires separately approved bounded Linux Guest")
	}
	privateMust(t, privateKernel())
	status, err := os.ReadFile("/proc/self/status")
	privateMust(t, err)
	if os.Getuid() != 65532 || !strings.Contains(string(status), "CapEff:\t0000000000000000") || !strings.Contains(string(status), "NoNewPrivs:\t1") {
		t.Fatal("Guest privilege guard failed")
	}
	mounts, err := os.ReadFile("/proc/self/mountinfo")
	privateMust(t, err)
	if !privateMount(string(mounts), "/", "ro") || !privateMount(string(mounts), "/tmp", "rw,nosuid,nodev") {
		t.Fatal("Guest mount guard failed")
	}
	allowed := map[string]string{"PATH": "/usr/bin:/bin", "HOME": "/tmp", "TMPDIR": "/tmp", "GENTLE_PRIVATE_GUEST": "approved"}
	if len(os.Environ()) != len(allowed) {
		t.Fatal("unexpected Guest environment")
	}
	for name, value := range allowed {
		if os.Getenv(name) != value {
			t.Fatal("Guest environment mismatch", name)
		}
	}
}

func TestPrivateKernel(t *testing.T) {
	privateGuest(t)
	mount := "1 0 0:1 / /sys/fs/cgroup ro - cgroup2 cgroup rw\n"
	limits := []string{"3221225472", "0", "100000 100000", "64"}
	cases := []struct {
		name, membership, mount string
		change                  int
	}{
		{"accepted", "0::/\n", mount, -1},
		{"foreign membership", "0::/host\n", mount, -1},
		{"multiple memberships", "0::/\n1:cpu:/\n", mount, -1},
		{"absent mount", "0::/\n", "", -1},
		{"wrong filesystem", "0::/\n", strings.ReplaceAll(mount, "cgroup2", "tmpfs"), -1},
		{"memory", "0::/\n", mount, 0},
		{"swap", "0::/\n", mount, 1},
		{"cpu", "0::/\n", mount, 2},
		{"pids", "0::/\n", mount, 3},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			values := append([]string(nil), limits...)
			if tc.change >= 0 {
				values[tc.change] = "max"
			}
			if (privateKernelData(tc.membership, tc.mount, values) == nil) != (tc.name == "accepted") {
				t.Fatal("kernel qualification mismatch")
			}
		})
	}
}

func privateFixture(t *testing.T, root string) map[string]string {
	t.Helper()
	result := map[string]string{}
	privateMust(t, filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		info, err := os.Lstat(path)
		if err != nil {
			return err
		}
		var data []byte
		if info.Mode().IsRegular() {
			data, err = os.ReadFile(path)
		}
		if info.Mode()&os.ModeSymlink != 0 {
			var target string
			target, err = os.Readlink(path)
			data = []byte(target)
		}
		st := info.Sys().(*syscall.Stat_t)
		result[path] = fmt.Sprintf("%v:%d:%d:%d:%d:%x", info.Mode(), st.Uid, st.Gid, st.Dev, st.Ino, sha256.Sum256(data))
		return err
	}))
	return result
}

func privateNoStage(t *testing.T, parent string) {
	t.Helper()
	entries, err := os.ReadDir(parent)
	privateMust(t, err)
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), ".gentle-go-") || strings.HasPrefix(entry.Name(), ".gentle-shell-stage.") {
			t.Fatal("owned workspace leaked", entry.Name())
		}
	}
}

func TestPrivateAssetsAndStates(t *testing.T) {
	privateGuest(t)
	parent := t.TempDir()
	privateMust(t, os.Chmod(parent, 0700))
	workspace, identity, err := privateStage(parent)
	privateMust(t, err)
	if identity.Mode() != os.ModeDir|0700 || identity.Sys().(*syscall.Stat_t).Uid != uint32(os.Getuid()) {
		t.Fatal("workspace ownership")
	}
	pins := map[string]string{
		"acquire-gentle-shell-private-bundle.sh":          "584459543e037544b5d5dab181fab55efa412cc52bc96bccf7f3bac180f92cad",
		"bootstrap-gentle-shell-private-node.sh":          "ac220abf4621a56bddc900209925df1716d8859c621c2994299ce3fc1f84e964",
		"complete-generated-lock-sri.mjs":                 "f4930552535513241f35dfeb67d5660b1efca19492bc4092e557a23cd5e612b6",
		"install-gentle-shell-private.sh":                 "54e18072fe243290546d7706245060deb59cfcb78cac3ca3f0a6b2ee295b78fc",
		"normalize-private-optional-platform-closure.mjs": "013529f77c887af5eab97cdd0568210b7a6f61a04121a67018d6fa5101a995f3",
	}
	entries, err := os.ReadDir(workspace)
	privateMust(t, err)
	if len(entries) != 5 || len(assets.PrivateHelperNames()) != 5 {
		t.Fatal("helper inventory differs")
	}
	for _, entry := range entries {
		path := filepath.Join(workspace, entry.Name())
		data, err := os.ReadFile(path)
		privateMust(t, err)
		info, err := os.Lstat(path)
		privateMust(t, err)
		if !info.Mode().IsRegular() || info.Mode() != 0444 || fmt.Sprintf("%x", sha256.Sum256(data)) != pins[entry.Name()] {
			t.Fatal("fixed asset mismatch", entry.Name())
		}
	}
	for _, path := range []string{parent, filepath.Join(workspace, "install-gentle-shell-private.sh")} {
		info, err := os.Lstat(path)
		privateMust(t, err)
		privateMust(t, os.Chmod(path, info.Mode()|os.ModeSticky))
		_, directoryErr := privateDirectory(parent)
		_, sourceErr := privateSources(workspace)
		if (path == parent && directoryErr == nil) || (path != parent && sourceErr == nil) {
			t.Fatal("special mode bit accepted", path)
		}
		privateMust(t, os.Chmod(path, info.Mode()))
	}
	before, err := privateSources(workspace)
	privateMust(t, err)
	path := filepath.Join(workspace, "install-gentle-shell-private.sh")
	privateMust(t, os.Chmod(path, 0664))
	if _, err := privateSources(workspace); err == nil {
		t.Fatal("source mode mutation accepted")
	}
	privateMust(t, os.WriteFile(path, []byte("not executable fixture"), 0600))
	privateMust(t, os.Chmod(path, 0444))
	if _, err := privateSources(workspace); err == nil {
		t.Fatal("source content mutation accepted")
	}
	original, err := assets.ReadPrivateHelper("install-gentle-shell-private.sh")
	privateMust(t, err)
	privateMust(t, os.Rename(path, path+".preimage"))
	privateMust(t, os.WriteFile(path, original, 0600))
	privateMust(t, os.Chmod(path, 0444))
	after, err := privateSources(workspace)
	privateMust(t, err)
	if before == after {
		t.Fatal("source metadata preimage mutation missed")
	}
	// Physical classifier cases simulate ambiguity, not actual transport failure.
	dest := filepath.Join(parent, "appeared")
	privateMust(t, os.Mkdir(dest, 0700))
	if privateReadback(dest, "") == nil || privateFailure(dest, context.Canceled).Kind != "uncertain" {
		t.Fatal("empty or cancelled publication treated as success")
	}
	blocked := filepath.Join(workspace, "blocked")
	privateMust(t, os.Mkdir(blocked, 0700))
	privateMust(t, os.WriteFile(filepath.Join(blocked, "child"), nil, 0600))
	privateMust(t, os.Chmod(blocked, 0000))
	cleanupErr := privateCleanup(workspace, identity)
	privateMust(t, os.Chmod(blocked, 0700))
	if cleanupErr == nil {
		t.Fatal("cleanup fault hidden")
	}
	old := workspace + ".old"
	privateMust(t, os.Rename(workspace, old))
	privateMust(t, os.Mkdir(workspace, 0700))
	if privateCleanup(workspace, identity) == nil {
		t.Fatal("replacement deleted")
	}
	_, err = os.Lstat(workspace)
	privateMust(t, err)
}

func TestPrivateRunner(t *testing.T) {
	privateGuest(t)
	cases := []struct{ name, path, body, kind string }{
		{"start", "/nonexistent-gentle-private", "", "start"},
		{"nonzero", "/bin/sh", "exit 7", "nonzero"},
		{"combined overflow", "/bin/sh", "printf '%3000s' x; printf '%3000s' y >&2; sleep 30", "output"},
		{"cancel", "/bin/sh", "sleep 30 & echo $!; wait", "canceled"},
		{"deadline", "/bin/sh", "sleep 30 & echo $!; wait", "deadline"},
		{"held pipes", "/bin/sh", "sleep 30 & echo $!; exit 0", "pipes"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			budget := 5 * time.Second
			if tc.kind == "deadline" {
				budget = 200 * time.Millisecond
			}
			ctx, cancel := context.WithTimeout(context.Background(), budget)
			defer cancel()
			if tc.kind == "canceled" {
				timer := time.AfterFunc(200*time.Millisecond, cancel)
				defer timer.Stop()
			}
			cmd := exec.CommandContext(ctx, tc.path, "-c", tc.body)
			cmd.Env = []string{"PATH=/usr/bin:/bin"}
			start := time.Now()
			output, err := privateRun(ctx, cmd, cancel)
			var failure *PrivateRuntimeError
			if !errors.As(err, &failure) || failure.Kind != tc.kind || time.Since(start) > 3*time.Second || len(output) > 4096 {
				t.Fatal("runner outcome", err, len(output))
			}
			if tc.kind != "start" && cmd.ProcessState == nil {
				t.Fatal("direct child not reaped")
			}
			if tc.kind == "output" && output != "" {
				t.Fatal("overflow output disclosed")
			}
			if tc.kind == "canceled" || tc.kind == "deadline" || tc.kind == "pipes" {
				pid, err := strconv.Atoi(strings.TrimSpace(output))
				privateMust(t, err)
				fd, err := unix.PidfdOpen(pid, 0)
				if err == nil {
					defer unix.Close(fd)
					fds := []unix.PollFd{{Fd: int32(fd), Events: unix.POLLIN}}
					n, err := unix.Poll(fds, 1000)
					if err != nil || n != 1 || fds[0].Revents&unix.POLLIN == 0 {
						t.Fatal("descendant still live", err)
					}
				} else if !errors.Is(err, unix.ESRCH) {
					t.Fatal(err)
				}
			}
		})
	}
}

func TestPrivateInstall(t *testing.T) {
	privateGuest(t)
	parent := t.TempDir()
	privateMust(t, os.Chmod(parent, 0700))
	pi := filepath.Join(parent, "pi")
	privateMust(t, os.Mkdir(pi, 0700))
	privateMust(t, os.WriteFile(filepath.Join(pi, "settings.json"), []byte("private Pi fixture"), 0600))
	privateMust(t, os.Symlink("settings.json", filepath.Join(pi, "link")))
	before := privateFixture(t, pi)
	assertPreserved := func() {
		t.Helper()
		privateNoStage(t, parent)
		if !reflect.DeepEqual(before, privateFixture(t, pi)) {
			t.Fatal("Pi preimage changed")
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	cancelledBefore := privateFixture(t, parent)
	_, err := RunPrivateInstall(ctx, filepath.Join(parent, "cancelled"), "/node.tgz")
	var failure *PrivateRuntimeError
	if !errors.As(err, &failure) || failure.Kind != "canceled" || !reflect.DeepEqual(cancelledBefore, privateFixture(t, parent)) {
		t.Fatal("pre-cancelled invocation", err)
	}
	assertPreserved()
	for _, kind := range []string{"file", "directory", "symlink"} {
		dest := filepath.Join(parent, kind)
		switch kind {
		case "file":
			privateMust(t, os.WriteFile(dest, []byte("Pi sentinel"), 0600))
		case "directory":
			privateMust(t, os.Mkdir(dest, 0700))
		case "symlink":
			privateMust(t, os.Symlink(pi, dest))
		}
		preimage := privateFixture(t, parent)
		_, err := RunPrivateInstall(context.Background(), dest, "/node.tgz")
		if !errors.As(err, &failure) || failure.Kind != "refused" || !reflect.DeepEqual(preimage, privateFixture(t, parent)) {
			t.Fatal("real API collision changed preimage", kind, err)
		}
		assertPreserved()
	}
	for _, kind := range []string{"full-size wrong hash", "truncated", "symlink archive"} {
		archive := filepath.Join(parent, strings.ReplaceAll(kind, " ", "-")+".tgz")
		if kind == "symlink archive" {
			privateMust(t, os.Symlink("/node.tgz", archive))
		} else {
			file, err := os.OpenFile(archive, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
			privateMust(t, err)
			if kind == "full-size wrong hash" {
				privateMust(t, file.Truncate(57224421))
			}
			privateMust(t, file.Close())
		}
		preimage := privateFixture(t, parent)
		_, err := RunPrivateInstall(context.Background(), filepath.Join(parent, "rejected"), archive)
		if !errors.As(err, &failure) || !reflect.DeepEqual(preimage, privateFixture(t, parent)) {
			t.Fatal("invalid archive accepted", kind, err)
		}
		if _, err := os.Lstat(filepath.Join(parent, "rejected")); !os.IsNotExist(err) {
			t.Fatal("invalid archive published")
		}
		assertPreserved()
	}
	ctx, cancel = context.WithTimeout(context.Background(), 750*time.Second)
	defer cancel()
	dest := filepath.Join(parent, "installed")
	result, err := RunPrivateInstall(ctx, dest, "/node.tgz")
	if err != nil || result.State != "ComponentInstalled" {
		t.Fatal("cold production pipeline", err)
	}
	lock, err := os.ReadFile(filepath.Join(dest, "project/package-lock.json"))
	privateMust(t, err)
	if fmt.Sprintf("%x", sha256.Sum256(lock)) != result.LockSHA256 {
		t.Fatal("provider lock preimage changed")
	}
	var manifest struct{ Dependencies map[string]string }
	data, err := os.ReadFile(filepath.Join(dest, "project/package.json"))
	privateMust(t, err)
	privateMust(t, json.Unmarshal(data, &manifest))
	pins := map[string]string{"gentle-pi": "3.7.0", "@earendil-works/pi-coding-agent": "0.85.1", "@earendil-works/pi-tui": "0.85.1", "@heyhuynhgiabuu/pi-pretty": "0.6.27", "typebox": "1.3.7"}
	if !reflect.DeepEqual(manifest.Dependencies, pins) {
		t.Fatal("five independent version pins differ")
	}
	var closure []string
	data, err = os.ReadFile(filepath.Join(dest, "closure.json"))
	privateMust(t, err)
	privateMust(t, json.Unmarshal(data, &closure))
	if len(closure) != 283 {
		t.Fatal("closure cardinality differs; identity is checked by the fixed installer")
	}
	installed := privateFixture(t, dest)
	_, err = RunPrivateInstall(context.Background(), dest, "/node.tgz")
	if !errors.As(err, &failure) || failure.Kind != "refused" || !reflect.DeepEqual(installed, privateFixture(t, dest)) {
		t.Fatal("retry changed published installation", err)
	}
	assertPreserved()
	fmt.Printf("Go private pipeline: ComponentInstalled closure=283 lock-sha256=%s launch=not-run Ready=false\n", result.LockSHA256)
}
