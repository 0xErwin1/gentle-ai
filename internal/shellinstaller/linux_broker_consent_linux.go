//go:build linux

package shellinstaller

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"golang.org/x/sys/unix"
)

// All fields remain claimed DATA, including physical identity and source.
// Only the separate-instance operation is supported by this prerequisite.
type linuxBrokerPreview struct {
	Transaction string
	Source      string
	PhysicalID  string
	Destination string
}

type linuxBrokerTerminalObservation struct {
	canonical string
	challenge string
	binding   string
}

func linuxBrokerCanonical(p linuxBrokerPreview) (string, error) {
	for _, value := range []string{p.Transaction, p.Source, p.PhysicalID, p.Destination} {
		if value == "" || len(value) > 512 {
			return "", fmt.Errorf("invalid bounded preview data")
		}
		for _, b := range []byte(value) {
			if b >= 127 {
				return "", fmt.Errorf("preview requires ASCII data to prevent display spoofing")
			}
		}
	}
	data, err := json.Marshal(struct {
		Domain    string
		Operation string
		Data      linuxBrokerPreview
	}{"gentle-linux-terminal/v1", "install-separate-gentle-shell", p})
	return string(data), err
}

// /dev/tty is a magic alias: its inode metadata is NOT the slave metadata.
// Resolve only supported Linux devpts slaves using the kernel TIOCGDEV result,
// then bind the real held descriptor to the controlling session and foreground.
func linuxBrokerOpenTerminal() (int, error) {
	if unix.Geteuid() != 0 || unix.Getuid() != 0 {
		return -1, fmt.Errorf("root broker required")
	}
	alias, err := unix.Open("/dev/tty", unix.O_RDWR|unix.O_NOCTTY|unix.O_NONBLOCK|unix.O_CLOEXEC, 0)
	if err != nil {
		return -1, fmt.Errorf("controlling terminal unavailable: %w", err)
	}
	defer unix.Close(alias)
	dev, err := unix.IoctlGetInt(alias, unix.TIOCGDEV)
	if err != nil {
		return -1, err
	}
	major, minor := unix.Major(uint64(uint32(dev))), unix.Minor(uint64(uint32(dev)))
	if major < 136 || major > 143 || minor > 255 {
		return -1, fmt.Errorf("unsupported controlling terminal")
	}
	fd, err := unix.Open(fmt.Sprintf("/dev/pts/%d", (major-136)*256+minor),
		unix.O_RDWR|unix.O_NOCTTY|unix.O_NOFOLLOW|unix.O_NONBLOCK|unix.O_CLOEXEC, 0)
	if err != nil {
		return -1, err
	}
	if err := linuxBrokerCheckTerminal(fd, uint64(uint32(dev))); err != nil {
		unix.Close(fd)
		return -1, err
	}
	return fd, nil
}

func linuxBrokerCheckTerminal(fd int, device uint64) error {
	var st unix.Stat_t
	if err := unix.Fstat(fd, &st); err != nil {
		return err
	}
	sid, err := unix.IoctlGetInt(fd, unix.TIOCGSID)
	if err != nil {
		return err
	}
	foreground, err := unix.IoctlGetInt(fd, unix.TIOCGPGRP)
	if err != nil {
		return err
	}
	session, err := unix.Getsid(0)
	if err != nil {
		return err
	}
	term, err := unix.IoctlGetTermios(fd, unix.TCGETS)
	if err != nil {
		return err
	}
	if st.Mode&unix.S_IFMT != unix.S_IFCHR || st.Rdev != device || st.Uid != 0 ||
		st.Mode&0o7777 != 0o600 || sid != session || session != unix.Getpid() ||
		foreground != unix.Getpgrp() || term.Lflag&unix.ICANON == 0 ||
		term.Lflag&(unix.ECHO|unix.ECHONL) != 0 {
		return fmt.Errorf("terminal is not private, canonical, no-echo and broker-controlled")
	}
	return nil
}

func linuxBrokerTerminalIO(fd int, data []byte, write bool, deadline time.Time) (int, error) {
	event := int16(unix.POLLIN)
	if write {
		event = unix.POLLOUT
	}
	remaining := time.Until(deadline).Milliseconds()
	if remaining <= 0 {
		return 0, fmt.Errorf("terminal interaction expired")
	}
	poll := []unix.PollFd{{Fd: int32(fd), Events: event}}
	n, err := unix.Poll(poll, int(remaining))
	if err != nil || n != 1 || poll[0].Revents != event {
		return 0, fmt.Errorf("terminal readiness failed: %v", err)
	}
	if write {
		return unix.Write(fd, data)
	}
	return unix.Read(fd, data)
}

// This is an internal PREPARED INTERACTION OBSERVATION, not human approval.
// A trusted launcher must separately establish pre-launch custody: these checks
// cannot exclude an attacker already holding this terminal, nor provision an
// operator. No effects, persistence, source authorization or Ready exist here.
func linuxBrokerObserveTerminal(p linuxBrokerPreview, wait time.Duration) (*linuxBrokerTerminalObservation, error) {
	if wait <= 0 || wait > 2*time.Minute {
		return nil, fmt.Errorf("invalid terminal wait bound")
	}
	canonical, err := linuxBrokerCanonical(p)
	if err != nil {
		return nil, err
	}
	fd, err := linuxBrokerOpenTerminal()
	if err != nil {
		return nil, err
	}
	defer unix.Close(fd)
	dev, err := unix.IoctlGetInt(fd, unix.TIOCGDEV)
	if err != nil {
		return nil, err
	}
	var nonce [32]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		return nil, err
	}
	challenge := hex.EncodeToString(nonce[:])
	digest := sha256.Sum256([]byte("gentle-linux-terminal-decision/v1\x00" + canonical + "\x00" + challenge))
	binding := hex.EncodeToString(digest[:])
	// JSON escapes caller control characters; no caller bytes become ANSI commands.
	preview := "CLAIMED DATA ONLY; scripted input is not human consent.\n" + canonical +
		"\nReply exactly: accept " + challenge + " " + binding + "\n"
	deadline := time.Now().Add(wait)
	for len(preview) > 0 {
		n, err := linuxBrokerTerminalIO(fd, []byte(preview), true, deadline)
		if err != nil || n == 0 {
			return nil, fmt.Errorf("terminal preview failed: %v", err)
		}
		preview = preview[n:]
	}
	var input [160]byte
	n, err := linuxBrokerTerminalIO(fd, input[:], false, deadline)
	if err != nil || string(input[:n]) != "accept "+challenge+" "+binding+"\n" {
		return nil, fmt.Errorf("terminal decision refused or malformed: %v", err)
	}
	if err := linuxBrokerCheckTerminal(fd, uint64(uint32(dev))); err != nil {
		return nil, err
	}
	return &linuxBrokerTerminalObservation{canonical: canonical, challenge: challenge, binding: binding}, nil
}

func (o *linuxBrokerTerminalObservation) matches(p linuxBrokerPreview) bool {
	canonical, err := linuxBrokerCanonical(p)
	return o != nil && err == nil && canonical == o.canonical &&
		len(o.challenge) == 64 && len(o.binding) == 64 && !strings.Contains(canonical, "\x1b")
}
