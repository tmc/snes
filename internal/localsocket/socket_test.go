//go:build darwin || dragonfly || freebsd || linux || netbsd || openbsd

package localsocket

import (
	"fmt"
	"net"
	"os"
	"path/filepath"
	"testing"
)

func TestListenAdmission(t *testing.T) {
	for _, kind := range []string{"absent", "file", "symlink", "active", "stale"} {
		t.Run(kind, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "sock")
			var active *net.UnixListener
			switch kind {
			case "file":
				if err := os.WriteFile(path, []byte("preserve"), 0600); err != nil {
					t.Fatal(err)
				}
			case "symlink":
				if err := os.Symlink("missing", path); err != nil {
					t.Fatal(err)
				}
			case "active", "stale":
				var err error
				active, err = net.ListenUnix("unix", &net.UnixAddr{Name: path, Net: "unix"})
				if err != nil {
					t.Fatal(err)
				}
				active.SetUnlinkOnClose(false)
				defer active.Close()
				if kind == "stale" {
					active.Close()
				}
			}
			l, err := Listen(path)
			wantErr := kind == "file" || kind == "symlink" || kind == "active"
			if (err != nil) != wantErr {
				t.Fatalf("Listen = %v, want error %v", err, wantErr)
			}
			if l != nil {
				info, err := os.Lstat(path)
				if err != nil {
					t.Fatal(err)
				}
				if info.Mode().Perm() != 0600 {
					t.Fatalf("mode = %v", info.Mode())
				}
				if err := l.Close(); err != nil {
					t.Fatal(err)
				}
				if _, err := os.Lstat(path); !os.IsNotExist(err) {
					t.Fatalf("socket survived Close: %v", err)
				}
			}
			switch kind {
			case "file":
				data, _ := os.ReadFile(path)
				if string(data) != "preserve" {
					t.Fatalf("file changed: %q", data)
				}
			case "symlink":
				target, _ := os.Readlink(path)
				if target != "missing" {
					t.Fatalf("symlink changed: %q", target)
				}
			case "active":
				c, err := net.Dial("unix", path)
				if err != nil {
					t.Fatal(err)
				}
				c.Close()
			}
		})
	}
}

func TestClosePreservesReplacement(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sock")
	l, err := Listen(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("replacement"), 0600); err != nil {
		t.Fatal(err)
	}
	l.Close()
	data, err := os.ReadFile(path)
	if err != nil || string(data) != "replacement" {
		t.Fatalf("replacement = %q, %v", data, err)
	}
}

func ExampleListen() {
	dir, err := os.MkdirTemp("", "localsocket-example-")
	if err != nil {
		fmt.Println(err)
		return
	}
	defer os.RemoveAll(dir)
	l, err := Listen(filepath.Join(dir, "control.sock"))
	if err != nil {
		fmt.Println(err)
		return
	}
	defer l.Close()
	fmt.Println(l.Addr().Network())
	// Output: unix
}
