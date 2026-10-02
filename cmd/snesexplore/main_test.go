package main

import (
	"bytes"
	"testing"
)

func TestInvalidArguments(t *testing.T) {
	for _, args := range [][]string{nil, {"unexpected"}, {"-unknown"}} {
		var b bytes.Buffer
		if e := run(args, &b); e == nil {
			t.Fatalf("accepted %v", args)
		}
		if b.Len() != 0 {
			t.Fatal("invalid invocation published stdout")
		}
	}
}
