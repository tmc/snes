package main

import (
	"bytes"
	"strings"
	"testing"
)

func TestRunRejectsUnknownCommand(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := run([]string{"bogus"}, strings.NewReader(""), &stdout, &stderr)
	if code != 2 {
		t.Fatalf("code = %d, want 2", code)
	}
	if !strings.Contains(stderr.String(), "unknown command") {
		t.Fatalf("stderr = %q", stderr.String())
	}
}

func TestHashJSONStable(t *testing.T) {
	a := hashJSON(map[string]uint64{"x": 1, "y": 2})
	b := hashJSON(map[string]uint64{"y": 2, "x": 1})
	if a != b {
		t.Fatalf("hashJSON order changed: %s != %s", a, b)
	}
}
