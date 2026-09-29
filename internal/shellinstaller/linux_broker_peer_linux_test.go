//go:build linux

package shellinstaller

import (
	"bytes"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

func brokerGuestOnly(t *testing.T) {
	t.Helper()
	if os.Getenv("GENTLE_BROKER_GUEST") != "1" {
		t.Skip("candidate execution is guest-only")
	}
}

func TestLinuxBrokerCredentialValidation(t *testing.T) {
	brokerGuestOnly(t)
	for _, tt := range []struct {
		name            string
		peer            *unix.Ucred
		local, operator uint32
		server, valid   bool
	}{
		{"root broker", &unix.Ucred{Pid: 1, Uid: 0}, 65532, 0, false, true},
		{"same UID broker", &unix.Ucred{Pid: 1, Uid: 65532}, 65532, 0, false, false},
		{"root client", &unix.Ucred{Pid: 1, Uid: 0}, 0, 0, false, false},
		{"operator", &unix.Ucred{Pid: 1, Uid: 65532}, 0, 65532, true, true},
		{"wrong operator", &unix.Ucred{Pid: 1, Uid: 65531}, 0, 65532, true, false},
		{"root operator", &unix.Ucred{Pid: 1, Uid: 0}, 0, 0, true, false},
		{"untrusted server", &unix.Ucred{Pid: 1, Uid: 65532}, 65532, 65532, true, false},
		{"nil", nil, 0, 65532, true, false},
		{"missing PID", &unix.Ucred{Uid: 0}, 65532, 0, false, false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if valid := validateBrokerPeer(tt.peer, tt.local, tt.operator, tt.server) == nil; valid != tt.valid {
				t.Fatalf("valid=%v, want %v", valid, tt.valid)
			}
		})
	}
}

func TestLinuxBrokerSameUIDRefusal(t *testing.T) {
	brokerGuestOnly(t)
	listener, err := net.ListenUnix("unix", &net.UnixAddr{Name: filepath.Join(t.TempDir(), "socket"), Net: "unix"})
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	conn, err := net.DialUnix("unix", nil, listener.Addr().(*net.UnixAddr))
	if err != nil {
		t.Fatal(err)
	}
	if CheckLinuxBrokerPeer(conn) == nil {
		t.Fatal("same-UID broker accepted")
	}
	conn.Close()
	if CheckLinuxBrokerPeer(conn) == nil || CheckLinuxBrokerPeer(nil) == nil {
		t.Fatal("closed or nil connection accepted")
	}
}

func TestLinuxBrokerBufferedClosure(t *testing.T) {
	brokerGuestOnly(t)
	for _, mode := range []string{"close", "shutdown"} {
		t.Run(mode, func(t *testing.T) {
			fds, err := unix.Socketpair(unix.AF_UNIX, unix.SOCK_STREAM|unix.SOCK_CLOEXEC, 0)
			if err != nil {
				t.Fatal(err)
			}
			local := os.NewFile(uintptr(fds[0]), "local")
			remote := os.NewFile(uintptr(fds[1]), "remote")
			defer remote.Close()
			conn, err := net.FileConn(local)
			local.Close()
			if err != nil {
				t.Fatal(err)
			}
			defer conn.Close()
			if _, err := remote.Write([]byte("queued")); err != nil {
				t.Fatal(err)
			}
			if mode == "close" {
				err = remote.Close()
			} else {
				err = unix.Shutdown(fds[1], unix.SHUT_WR)
			}
			if err != nil {
				t.Fatal(err)
			}
			if err := CheckLinuxBrokerPeer(conn.(*net.UnixConn)); err == nil || err.Error() != "disconnected or failed Unix peer" {
				t.Fatalf("buffered closure must fail before credential validation: %v", err)
			}
		})
	}
}

// Explicit opt-in is mandatory and never converts a missing prerequisite to skip.
func TestLinuxBrokerGuest(t *testing.T) {
	if os.Getenv("GENTLE_BROKER_GUEST") != "1" {
		t.Skip("requires isolated root guest opt-in")
	}
	if os.Geteuid() != 0 || os.Getegid() != 0 {
		t.Fatal("opt-in requires root guest")
	}
	root := t.TempDir()
	// testing nests the fixture in its own private temporary parent directory.
	// Permit socket traversal, but keep broker state in its separate 0700 root.
	for _, dir := range []string{filepath.Dir(root), root} {
		if err := os.Chmod(dir, 0755); err != nil {
			t.Fatal(err)
		}
	}
	private := filepath.Join(root, "private")
	if err := os.Mkdir(private, 0700); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"key", "journal"} {
		if err := os.WriteFile(filepath.Join(private, name), []byte("broker-only"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	path := filepath.Join(root, "socket")
	listener, err := net.ListenUnix("unix", &net.UnixAddr{Name: path, Net: "unix"})
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	if err := os.Chmod(path, 0666); err != nil {
		t.Fatal(err)
	}
	listener.SetDeadline(time.Now().Add(5 * time.Second))
	binary, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(binary, "-test.v", "-test.run=^TestLinuxBrokerGuestClient$", "-test.timeout=10s")
	cmd.Env = []string{"GENTLE_BROKER_CHILD=" + root, "HOME=/tmp", "TMPDIR=/tmp"}
	cmd.SysProcAttr = &syscall.SysProcAttr{Credential: &syscall.Credential{Uid: 65532, Gid: 65532, Groups: []uint32{}}}
	var output bytes.Buffer
	cmd.Stdout, cmd.Stderr = &output, &output
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	waited := false
	defer func() {
		if !waited {
			_ = cmd.Process.Kill()
			_ = cmd.Wait()
		}
	}()
	conn, err := listener.AcceptUnix()
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if err := CheckLinuxOperatorPeer(conn, 65532); err != nil {
		t.Fatal(err)
	}
	if CheckLinuxOperatorPeer(conn, 65531) == nil || CheckLinuxOperatorPeer(conn, 0) == nil {
		t.Fatal("wrong or root operator accepted")
	}
	claim := make([]byte, len(`{"uid":0,"pid":1,"approved":true}`))
	if _, err := io.ReadFull(conn, claim); err != nil {
		t.Fatal(err)
	}
	// Caller JSON is never parsed: the only response is authentication, not consent.
	if _, err := conn.Write([]byte("authenticated-only")); err != nil {
		t.Fatal(err)
	}
	conn.Close()
	listener.Close()
	err = cmd.Wait()
	waited = true
	if err != nil {
		t.Fatalf("client failed: %v\n%s", err, &output)
	}
	t.Logf("client command: same test binary -test.v -test.run=^TestLinuxBrokerGuestClient$ -test.timeout=10s; outcome: PASS\n%s", &output)
	for _, name := range []string{"key", "journal"} {
		data, err := os.ReadFile(filepath.Join(private, name))
		if err != nil || string(data) != "broker-only" {
			t.Fatal("private state changed")
		}
	}
	t.Logf("PASS: root broker authenticated kernel operator UID 65532; private key/journal bytes unchanged; no consent or Apply granted")
}

func TestLinuxBrokerGuestClient(t *testing.T) {
	root := os.Getenv("GENTLE_BROKER_CHILD")
	if root == "" {
		t.Skip("subprocess only")
	}
	if os.Geteuid() != 65532 || os.Getegid() != 65532 {
		t.Fatal("client credential drop failed")
	}
	for _, name := range []string{"key", "journal"} {
		path := filepath.Join(root, "private", name)
		if _, err := os.ReadFile(path); !os.IsPermission(err) {
			t.Fatal("private read not denied")
		}
		if err := os.WriteFile(path, []byte("forged"), 0600); !os.IsPermission(err) {
			t.Fatal("private overwrite not denied")
		}
	}
	if err := os.WriteFile(filepath.Join(root, "private", "forged"), nil, 0600); !os.IsPermission(err) {
		t.Fatal("private root mutation not denied")
	}
	path := filepath.Join(root, "socket")
	conn, err := net.DialTimeout("unix", path, 3*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	peer := conn.(*net.UnixConn)
	if err := CheckLinuxBrokerPeer(peer); err != nil {
		t.Fatal(err)
	}
	if _, err := peer.Write([]byte(`{"uid":0,"pid":1,"approved":true}`)); err != nil {
		t.Fatal(err)
	}
	response, err := os.ReadFile(filepath.Join(root, "private", "journal"))
	if err == nil || response != nil {
		t.Fatal("caller claim granted private access")
	}
	reply, err := io.ReadAll(peer)
	if err != nil || string(reply) != "authenticated-only" {
		t.Fatal("unexpected authority response")
	}
	if CheckLinuxBrokerPeer(peer) == nil {
		t.Fatal("disconnected broker accepted")
	}
	peer.Close()
	if CheckLinuxBrokerPeer(peer) == nil {
		t.Fatal("closed connection accepted")
	}
	if unavailable, err := net.DialTimeout("unix", filepath.Join(root, "absent"), time.Second); err == nil {
		unavailable.Close()
		t.Fatal("unavailable broker accepted")
	}
	t.Logf("PASS: UID/GID 65532 client authenticated kernel broker UID 0; private reads, overwrites and root mutation denied; forged approval conferred no access; closed/unavailable broker refused")
}
