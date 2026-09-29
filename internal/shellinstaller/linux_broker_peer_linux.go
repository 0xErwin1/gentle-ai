//go:build linux

package shellinstaller

import (
	"errors"
	"net"
	"os"
	"time"

	"golang.org/x/sys/unix"
)

// Authentication observes a held socket, not caller claims or consent.
func validateBrokerPeer(peer *unix.Ucred, local, operator uint32, server bool) error {
	if peer == nil || peer.Pid <= 0 {
		return errors.New("missing kernel peer credentials")
	}
	if server {
		if local != 0 || operator == 0 || peer.Uid != operator {
			return errors.New("broker requires the configured nonroot operator")
		}
	} else if local == 0 || peer.Uid != 0 {
		return errors.New("nonroot client requires a root broker")
	}
	return nil
}

// CheckLinuxBrokerPeer authenticates a root broker to a nonroot client.
// Success conveys no consent, transaction authority, Apply or Ready state.
func CheckLinuxBrokerPeer(conn *net.UnixConn) error {
	return checkLinuxPeer(conn, 0, false)
}

// CheckLinuxOperatorPeer requires root execution and a nonzero operator UID
// supplied by trusted broker configuration, never by the connecting client.
func CheckLinuxOperatorPeer(conn *net.UnixConn, operator uint32) error {
	return checkLinuxPeer(conn, operator, true)
}

func checkLinuxPeer(conn *net.UnixConn, operator uint32, server bool) error {
	if conn == nil {
		return errors.New("missing Unix connection")
	}
	if err := conn.SetDeadline(time.Now().Add(3 * time.Second)); err != nil {
		return err
	}
	raw, err := conn.SyscallConn()
	if err != nil {
		return err
	}
	var peer *unix.Ucred
	var observation error
	err = raw.Control(func(fd uintptr) {
		peer, observation = unix.GetsockoptUcred(int(fd), unix.SOL_SOCKET, unix.SO_PEERCRED)
		if observation != nil {
			return
		}
		// Detect observable closure even with queued bytes, without consuming them.
		// This observation cannot promise future connection liveness.
		poll := []unix.PollFd{{Fd: int32(fd), Events: unix.POLLIN | unix.POLLRDHUP}}
		if _, observation = unix.Poll(poll, 0); observation != nil {
			return
		}
		if poll[0].Revents&(unix.POLLHUP|unix.POLLRDHUP|unix.POLLERR|unix.POLLNVAL) != 0 {
			observation = errors.New("disconnected or failed Unix peer")
		}
	})
	if err != nil {
		return err
	}
	if observation != nil {
		return observation
	}
	return validateBrokerPeer(peer, uint32(os.Geteuid()), operator, server)
}
