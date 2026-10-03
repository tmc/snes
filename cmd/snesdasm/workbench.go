package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"time"

	"github.com/tmc/snes/internal/editor/cworkbench"
)

func runWorkbench(args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("snesdasm workbench", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.Usage = func() {
		subcommandUsage(fs,
			"snesdasm workbench -config file [flags]",
			"Serve pinned generated C with separate user annotations over HTTP.",
			"snesdasm workbench -config workbench.json",
			"snesdasm workbench -config workbench.json -http 127.0.0.1:8097",
		)
	}
	path := fs.String("config", "", "operator-selected workbench JSON")
	address := fs.String("http", "127.0.0.1:8097", "loopback listen address")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 0 || *path == "" {
		return fmt.Errorf("config is required; run 'snesdasm help workbench' for usage")
	}
	host, _, err := net.SplitHostPort(*address)
	if err != nil {
		return err
	}
	ip := net.ParseIP(host)
	if ip == nil || !ip.IsLoopback() {
		return fmt.Errorf("listen address must be a loopback IP")
	}
	f, err := os.Open(*path)
	if err != nil {
		return err
	}
	defer f.Close()
	d := json.NewDecoder(io.LimitReader(f, 1<<20))
	d.DisallowUnknownFields()
	var c cworkbench.Config
	if err := d.Decode(&c); err != nil {
		return err
	}
	if d.Decode(new(any)) != io.EOF {
		return fmt.Errorf("trailing config JSON")
	}
	w, err := cworkbench.Open(c)
	if err != nil {
		return err
	}
	s := http.Server{
		Addr:              *address,
		Handler:           w.Handler(),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       10 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       60 * time.Second,
	}
	fmt.Fprintf(stdout, "snesdasm workbench serving at http://%s\n", *address)
	return s.ListenAndServe()
}
