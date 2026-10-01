// Snesedit serves a local read-only editor evidence shell.
package main

import (
	"flag"
	"fmt"
	"net"
	"net/http"
	"os"

	"github.com/tmc/snes/internal/editor/web"
)

func main() {
	manifest := flag.String("manifest", "", "explicit pinned target observation manifest")
	address := flag.String("listen", "127.0.0.1:8460", "loopback HTTP address")
	flag.Parse()
	if err := run(*manifest, *address); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
func run(manifest, address string) error {
	host, _, err := net.SplitHostPort(address)
	if err != nil {
		return err
	}
	ip := net.ParseIP(host)
	if ip == nil || !ip.IsLoopback() {
		return fmt.Errorf("listen address must be loopback")
	}
	m, err := web.Load(manifest)
	if err != nil {
		return err
	}
	s := &http.Server{Addr: address, Handler: web.Handler(m), ReadHeaderTimeout: 5e9}
	return s.ListenAndServe()
}
