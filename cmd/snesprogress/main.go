// Snesprogress displays recorded recovery progress from pinned inputs.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"net"
	"net/http"
	"os"
	"time"

	"github.com/tmc/snes/internal/editor/web"
	"github.com/tmc/snes/internal/recovery/progress"
)

func main() {
	config := flag.String("config", "", "explicit progress config")
	sha := flag.String("config-sha256", "", "external config SHA-256")
	listen := flag.String("listen", "", "loopback HTTP address; empty writes JSON")
	flag.Parse()
	if e := run(*config, *sha, *listen); e != nil {
		fmt.Fprintln(os.Stderr, "snesprogress:", e)
		os.Exit(1)
	}
}
func run(config, sha, listen string) error {
	if listen != "" {
		host, _, e := net.SplitHostPort(listen)
		ip := net.ParseIP(host)
		if e != nil || ip == nil || !ip.IsLoopback() {
			return fmt.Errorf("listen address must be literal loopback")
		}
	}
	report, e := progress.Load(context.Background(), config, sha)
	if e != nil {
		return e
	}
	if listen == "" {
		return json.NewEncoder(os.Stdout).Encode(report)
	}
	// Construct one frozen handler, not a report reread on every request.
	h := web.ProgressHandler(report)
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		host, _, e := net.SplitHostPort(r.Host)
		ip := net.ParseIP(host)
		if e != nil || ip == nil || !ip.IsLoopback() {
			http.Error(w, "literal loopback host required", 403)
			return
		}
		h.ServeHTTP(w, r)
	})
	return (&http.Server{Addr: listen, Handler: handler, ReadHeaderTimeout: 5 * time.Second}).ListenAndServe()
}
