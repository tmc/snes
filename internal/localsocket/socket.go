//go:build darwin || dragonfly || freebsd || linux || netbsd || openbsd

// Package localsocket creates private Unix control sockets without removing
// unrelated filesystem entries. Parent directories must be trusted by the caller.
package localsocket

import (
	"errors"
	"fmt"
	"net"
	"os"
	"sync"
	"syscall"
	"time"
)

// Listen listens on path, replacing only a stale socket. The returned listener
// removes its own socket on Close, leaving a replacement path untouched.
// Cooperating listeners serialize ownership changes using the persistent regular
// file path+".lock". Do not remove that lock file while the socket is in use.
func Listen(path string) (net.Listener, error) {
	lock, err := lockPath(path)
	if err != nil {
		return nil, err
	}
	defer unlockPath(lock)
	old, err := os.Lstat(path)
	if err == nil {
		if old.Mode()&os.ModeSocket == 0 {
			return nil, fmt.Errorf("socket path %q is not a socket", path)
		}
		conn, dialErr := net.DialTimeout("unix", path, 200*time.Millisecond)
		if dialErr == nil {
			conn.Close()
			return nil, fmt.Errorf("socket %q is active", path)
		}
		if !errors.Is(dialErr, syscall.ECONNREFUSED) {
			return nil, fmt.Errorf("check socket: %w", dialErr)
		}
		current, err := os.Lstat(path)
		if err != nil {
			return nil, err
		}
		if !os.SameFile(old, current) {
			return nil, fmt.Errorf("socket path changed during admission")
		}
		if err := os.Remove(path); err != nil {
			return nil, err
		}
	} else if !os.IsNotExist(err) {
		return nil, err
	}
	l, err := net.ListenUnix("unix", &net.UnixAddr{Name: path, Net: "unix"})
	if err != nil {
		return nil, err
	}
	l.SetUnlinkOnClose(false)
	info, err := os.Lstat(path)
	if err != nil {
		l.Close()
		return nil, err
	}
	out := &listener{UnixListener: l, path: path, info: info}
	if err := os.Chmod(path, 0600); err != nil {
		out.closeLocked()
		return nil, err
	}
	return out, nil
}

type listener struct {
	*net.UnixListener
	path string
	info os.FileInfo
	once sync.Once
	err  error
}

func (l *listener) Close() error {
	l.once.Do(func() {
		lock, err := lockPath(l.path)
		if err != nil {
			_ = l.UnixListener.Close()
			l.err = err
			return
		}
		defer unlockPath(lock)
		l.err = l.closeLocked()
	})
	return l.err
}

// closeLocked requires the companion lock, including during Listen error cleanup.
func (l *listener) closeLocked() error {
	closeErr := l.UnixListener.Close()
	info, err := os.Lstat(l.path)
	if err == nil && os.SameFile(l.info, info) {
		if err := os.Remove(l.path); closeErr == nil {
			closeErr = err
		}
	}
	return closeErr
}
