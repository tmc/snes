// Package main provides the snescorrelate CLI tool.
//
// Deprecated: Use 'snesdasm correlate' instead.
package main

import (
	"fmt"
	"io"
	"os"

	"github.com/tmc/snes/internal/provenance/dmacorrelate"
)

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

func run(args []string, stdout, stderr io.Writer) int {
	fmt.Fprintln(stderr, "warning: snescorrelate is deprecated; use 'snesdasm correlate' instead")
	return dmacorrelate.RunExit(args, stdout, stderr)
}
