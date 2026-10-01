package main

import (
	"bytes"
	"testing"
)

func TestFlags(t *testing.T) {
	for _, args := range [][]string{{}, {"-task", "task", "-config", "config", "-config-sha256", "sha", "-policy", "policy"}, {"-task", "task", "-config", "config", "-config-sha256", "sha", "-timeout", "0s"}, {"-task", "task", "-config", "config", "-config-sha256", "sha", "extra"}} {
		var out, errout bytes.Buffer
		if err := run(args, &out, &errout); err == nil {
			t.Fatalf("accepted %v", args)
		}
		if out.Len() != 0 {
			t.Fatal("printed unverified state")
		}
	}
}
