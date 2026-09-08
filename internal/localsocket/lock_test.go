//go:build darwin || dragonfly || freebsd || linux || netbsd || openbsd

package localsocket

import (
	"net"
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

func holdSocketLock(t *testing.T, path string) *os.File {
	t.Helper()
	f, err := os.OpenFile(path+".lock", os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		t.Fatal(err)
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX); err != nil {
		f.Close()
		t.Fatal(err)
	}
	t.Cleanup(func() { syscall.Flock(int(f.Fd()), syscall.LOCK_UN); f.Close() })
	return f
}

func TestConcurrentSocketStart(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sock")
	stale, err := net.ListenUnix("unix", &net.UnixAddr{Name: path, Net: "unix"})
	if err != nil {
		t.Fatal(err)
	}
	stale.SetUnlinkOnClose(false)
	stale.Close()
	lock := holdSocketLock(t, path)
	before, err := lock.Stat()
	if err != nil {
		t.Fatal(err)
	}
	type result struct {
		l   net.Listener
		err error
	}
	results := make(chan result, 2)
	started := make(chan struct{}, 2)
	for i := 0; i < 2; i++ {
		go func() { started <- struct{}{}; l, err := Listen(path); results <- result{l, err} }()
	}
	<-started
	<-started
	select {
	case r := <-results:
		if r.l != nil {
			r.l.(*listener).UnixListener.Close()
		}
		t.Fatalf("starter bypassed ownership lock: %v", r.err)
	case <-time.After(50 * time.Millisecond):
	}
	if err := syscall.Flock(int(lock.Fd()), syscall.LOCK_UN); err != nil {
		t.Fatal(err)
	}
	var winner net.Listener
	successes := 0
	for i := 0; i < 2; i++ {
		select {
		case r := <-results:
			if r.err == nil {
				successes++
				winner = r.l
				t.Cleanup(func() { r.l.Close() })
			}
		case <-time.After(5 * time.Second):
			t.Fatal("starter did not finish after unlock")
		}
	}
	if successes != 1 {
		t.Fatalf("concurrent starts succeeded %d times", successes)
	}
	conn, err := net.Dial("unix", path)
	if err != nil {
		t.Fatalf("winning socket was unlinked: %v", err)
	}
	conn.Close()
	if err := winner.Close(); err != nil {
		t.Fatal(err)
	}
	after, err := os.Lstat(path + ".lock")
	if err != nil {
		t.Fatal(err)
	}
	if !os.SameFile(before, after) {
		t.Fatal("ownership lock inode was replaced")
	}
}

func TestCloseWaitsForReplacement(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sock")
	old, err := Listen(path)
	if err != nil {
		t.Fatal(err)
	}
	lock := holdSocketLock(t, path)
	finished := make(chan error, 1)
	started := make(chan struct{})
	go func() { close(started); finished <- old.Close() }()
	<-started
	select {
	case err := <-finished:
		t.Fatalf("cleanup bypassed ownership lock: %v", err)
	case <-time.After(50 * time.Millisecond):
	}
	// Perform a cooperating replacement while holding the stable lock. Close
	// must inspect the replacement inode only after this transition completes.
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	replacement, err := net.ListenUnix("unix", &net.UnixAddr{Name: path, Net: "unix"})
	if err != nil {
		t.Fatal(err)
	}
	defer replacement.Close()
	if err := syscall.Flock(int(lock.Fd()), syscall.LOCK_UN); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-finished:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("cleanup did not finish after unlock")
	}
	conn, err := net.Dial("unix", path)
	if err != nil {
		t.Fatalf("cleanup removed replacement: %v", err)
	}
	conn.Close()
}

func TestSocketLockPathAdmission(t *testing.T) {
	for _, kind := range []string{"symlink", "directory", "fifo", "regular"} {
		t.Run(kind, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "sock")
			name := path + ".lock"
			switch kind {
			case "symlink":
				if err := os.Symlink("missing", name); err != nil {
					t.Fatal(err)
				}
			case "directory":
				if err := os.Mkdir(name, 0700); err != nil {
					t.Fatal(err)
				}
			case "fifo":
				if err := syscall.Mkfifo(name, 0600); err != nil {
					t.Fatal(err)
				}
			case "regular":
				if err := os.WriteFile(name, []byte("preserve lock contents"), 0600); err != nil {
					t.Fatal(err)
				}
			}
			before, err := os.Lstat(name)
			if err != nil {
				t.Fatal(err)
			}
			l, err := Listen(path)
			if kind == "regular" {
				if err != nil {
					t.Fatal(err)
				}
				l.Close()
				data, err := os.ReadFile(name)
				if err != nil || string(data) != "preserve lock contents" {
					t.Fatalf("lock contents changed: %q, %v", data, err)
				}
			} else if err == nil {
				l.Close()
				t.Fatal("nonregular lock path accepted")
			}
			after, err := os.Lstat(name)
			if err != nil {
				t.Fatal(err)
			}
			if !os.SameFile(before, after) {
				t.Fatal("lock path was replaced")
			}
		})
	}
}
