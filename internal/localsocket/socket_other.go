//go:build !darwin && !dragonfly && !freebsd && !linux && !netbsd && !openbsd

package localsocket

import (
	"fmt"
	"net"
	"runtime"
)

// Listen returns an unsupported-platform error without creating files.
func Listen(string) (net.Listener, error) {
	return nil, fmt.Errorf("local control sockets are unsupported on %s", runtime.GOOS)
}
