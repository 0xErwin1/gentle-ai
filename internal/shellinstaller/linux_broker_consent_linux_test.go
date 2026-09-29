//go:build linux

package shellinstaller

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"syscall"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

func brokerTestPTY(t *testing.T) (*os.File, *os.File) {
	t.Helper()
	fd, err := unix.Open("/dev/ptmx", unix.O_RDWR|unix.O_NOCTTY|unix.O_CLOEXEC|unix.O_NONBLOCK, 0)
	if err != nil {
		t.Fatal(err)
	}
	master := os.NewFile(uintptr(fd), "automation-pty-master")
	t.Cleanup(func() { master.Close() })
	if err := unix.IoctlSetPointerInt(fd, unix.TIOCSPTLCK, 0); err != nil {
		t.Fatal(err)
	}
	number, err := unix.IoctlGetInt(fd, unix.TIOCGPTN)
	if err != nil {
		t.Fatal(err)
	}
	slaveFD, err := unix.Open(fmt.Sprintf("/dev/pts/%d", number), unix.O_RDWR|unix.O_NOCTTY|unix.O_CLOEXEC, 0)
	if err != nil {
		t.Fatal(err)
	}
	slave := os.NewFile(uintptr(slaveFD), "automation-pty-slave")
	t.Cleanup(func() { slave.Close() })
	if err := unix.Fchmod(slaveFD, 0o600); err != nil {
		t.Fatal(err)
	}
	term, err := unix.IoctlGetTermios(slaveFD, unix.TCGETS)
	if err != nil {
		t.Fatal(err)
	}
	term.Lflag = (term.Lflag | unix.ICANON) &^ (unix.ECHO | unix.ECHONL)
	if err := unix.IoctlSetTermios(slaveFD, unix.TCSETS, term); err != nil {
		t.Fatal(err)
	}
	return master, slave
}

func brokerTestPreview() linuxBrokerPreview {
	return linuxBrokerPreview{"transaction", "claimed-source\x1b[31m", "claimed-physical-id", "/claimed/destination"}
}

func TestLinuxBrokerTerminalPreviewRefusal(t *testing.T) {
	brokerGuestOnly(t)
	for _, wait := range []time.Duration{-time.Second, 0, 2*time.Minute + time.Nanosecond} {
		observation, err := linuxBrokerObserveTerminal(brokerTestPreview(), wait)
		if observation != nil || err == nil || err.Error() != "invalid terminal wait bound" {
			t.Fatal("invalid wait did not refuse before terminal acquisition")
		}
	}
	for _, value := range []string{"", strings.Repeat("x", 513), "spoof\u202e"} {
		p := brokerTestPreview()
		p.Source = value
		if canonical, err := linuxBrokerCanonical(p); err == nil || canonical != "" {
			t.Fatal("invalid preview accepted")
		}
	}
}

func TestLinuxBrokerTerminalHelper(t *testing.T) {
	brokerGuestOnly(t)
	mode := os.Getenv("BROKER_TERMINAL_CASE")
	if mode == "" {
		return
	}
	if mode == "positive" {
		// Direct checks reject a pipe and a foreign, non-controlling private PTY.
		r, w, err := os.Pipe()
		if err != nil {
			t.Fatal(err)
		}
		defer r.Close()
		defer w.Close()
		if linuxBrokerCheckTerminal(int(r.Fd()), 0) == nil {
			t.Fatal("pipe accepted")
		}
		_, foreign := brokerTestPTY(t)
		dev, err := unix.IoctlGetInt(int(foreign.Fd()), unix.TIOCGDEV)
		if err != nil {
			t.Fatal(err)
		}
		if linuxBrokerCheckTerminal(int(foreign.Fd()), uint64(uint32(dev))) == nil {
			t.Fatal("foreign controlling terminal accepted")
		}
	}
	wait := 2 * time.Second
	if mode == "timeout" {
		wait = 200 * time.Millisecond
	}
	observation, err := linuxBrokerObserveTerminal(brokerTestPreview(), wait)
	if mode != "positive" {
		if err == nil || observation != nil {
			t.Fatal("refusal returned an observation")
		}
		return
	}
	if err != nil || !observation.matches(brokerTestPreview()) {
		t.Fatalf("scripted interaction failed: %v", err)
	}
	changed := brokerTestPreview()
	changed.PhysicalID += "-changed"
	if observation.matches(changed) {
		t.Fatal("changed canonical binding accepted")
	}
}

// Every affirmative byte below is scripted AUTOMATION, never human consent.
func TestLinuxBrokerTerminalAutomation(t *testing.T) {
	brokerGuestOnly(t)
	if testing.Short() {
		t.Skip("PTY subprocess integration")
	}
	if unix.Geteuid() != 0 {
		t.Fatal("isolated root guest prerequisite missing")
	}
	previous := ""
	for _, mode := range []string{"positive", "stale", "altered-binding", "reject", "malformed", "overflow", "timeout", "unprotected", "nonroot", "missing"} {
		t.Run(mode, func(t *testing.T) {
			master, slave := brokerTestPTY(t)
			if mode == "unprotected" {
				if err := unix.Fchmod(int(slave.Fd()), 0o620); err != nil {
					t.Fatal(err)
				}
			}
			cmd := exec.Command(os.Args[0], "-test.run=^TestLinuxBrokerTerminalHelper$", "-test.timeout=4s")
			cmd.Env = []string{"PATH=/usr/bin:/bin", "HOME=/tmp", "TMPDIR=/tmp", "GENTLE_BROKER_GUEST=1", "BROKER_TERMINAL_CASE=" + mode}
			cmd.Stdin = slave
			cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true, Setctty: mode != "missing", Ctty: 0}
			if mode == "nonroot" {
				cmd.SysProcAttr.Credential = &syscall.Credential{Uid: 65532, Gid: 65532}
			}
			var output bytes.Buffer
			cmd.Stdout, cmd.Stderr = &output, &output
			if err := cmd.Start(); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				if cmd.ProcessState == nil {
					cmd.Process.Kill()
					cmd.Wait()
				}
			})
			if mode != "unprotected" && mode != "nonroot" && mode != "missing" {
				preview := ""
				deadline := time.Now().Add(3 * time.Second)
				for !strings.Contains(preview, "\nReply exactly: accept ") || !strings.HasSuffix(preview, "\n") {
					var buf [4096]byte
					n, err := linuxBrokerTerminalIO(int(master.Fd()), buf[:], false, deadline)
					if err != nil || n == 0 || len(preview)+n > 8192 {
						t.Fatalf("bounded automation preview failed: %v", err)
					}
					preview += string(buf[:n])
				}
				if strings.Contains(preview, "\x1b") || !strings.Contains(preview, `claimed-source\u001b[31m`) {
					t.Fatal("unsafe or incomplete canonical preview")
				}
				canonical, err := linuxBrokerCanonical(brokerTestPreview())
				if err != nil || !strings.Contains(preview, canonical) {
					t.Fatal("exact canonical data missing from preview")
				}
				line := strings.Split(preview, "Reply exactly: ")[1]
				line = strings.ReplaceAll(line, "\r", "")
				response := line
				switch mode {
				case "positive":
					previous = line
				case "stale":
					response = previous
				case "altered-binding":
					response = line[:len(line)-3] + "zz\n"
				case "reject":
					response = strings.Replace(line, "accept", "reject", 1)
				case "malformed":
					response = "yes\n"
				case "overflow":
					response = strings.Repeat("x", 200) + "\n"
				case "timeout":
					response = ""
				}
				if response != "" {
					if n, err := unix.Write(int(master.Fd()), []byte(response)); err != nil || n != len(response) {
						t.Fatalf("automation response failed: %v", err)
					}
				}
			}
			if err := cmd.Wait(); err != nil {
				t.Fatalf("guest helper failed: %v: %s", err, output.String())
			}
		})
	}
}
