package main

import (
	"bytes"
	"strings"
	"testing"
)

func TestFlags(t *testing.T) {
	for _, args := range [][]string{nil, {"-config", "missing", "extra"}, {"-config", "missing", "-timeout", "0"}, {"-config", "missing", "-timeout", "11m"}} {
		var out, errout bytes.Buffer
		if err := run(args, &out, &errout); err == nil {
			t.Fatalf("invalid flags accepted: %v", args)
		}
	}
	var out, errout bytes.Buffer
	if err := run([]string{"-h"}, &out, &errout); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(errout.String(), "edited C never inherits captured proof") {
		t.Fatal("help omits experimental scope")
	}
}
