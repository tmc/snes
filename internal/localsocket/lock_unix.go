//go:build darwin || dragonfly || freebsd || linux || netbsd || openbsd

package localsocket

import (
	"fmt"
	"os"
	"syscall"
)

func lockPath(path string) (*os.File, error) {
	name := path + ".lock"
	if info, err := os.Lstat(name); err == nil {
		if !info.Mode().IsRegular() {
			return nil, fmt.Errorf("socket lock %q is not a regular file", name)
		}
	} else if !os.IsNotExist(err) {
		return nil, err
	}
	// NOFOLLOW protects the final component; NONBLOCK prevents an unexpected
	// replacement FIFO from blocking open before we can check the descriptor.
	fd, err := syscall.Open(name, syscall.O_CREAT|syscall.O_RDWR|syscall.O_NOFOLLOW|syscall.O_NONBLOCK|syscall.O_CLOEXEC, 0600)
	if err != nil {
		return nil, fmt.Errorf("open socket lock: %w", err)
	}
	f := os.NewFile(uintptr(fd), name)
	info, err := f.Stat()
	if err != nil {
		f.Close()
		return nil, err
	}
	if !info.Mode().IsRegular() {
		f.Close()
		return nil, fmt.Errorf("socket lock %q is not a regular file", name)
	}
	for {
		err = syscall.Flock(fd, syscall.LOCK_EX)
		if err != syscall.EINTR {
			break
		}
	}
	if err != nil {
		f.Close()
		return nil, fmt.Errorf("lock socket: %w", err)
	}
	current, err := os.Lstat(name)
	if err != nil || !os.SameFile(info, current) {
		unlockPath(f)
		if err != nil {
			return nil, err
		}
		return nil, fmt.Errorf("socket lock changed during admission")
	}
	return f, nil
}

func unlockPath(f *os.File) {
	_ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
	_ = f.Close()
}
