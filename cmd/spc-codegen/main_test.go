package main

import (
	"bytes"
	"crypto/sha256"
	"fmt"
	"strings"
	"testing"
)

func opcodeSource() string {
	var b strings.Builder
	for i := 255; i >= 0; i-- {
		fmt.Fprintf(&b, "op(0x%02x, Operation%d, fp(OR), A)\n", i, i)
	}
	return b.String()
}

func TestParseOps(t *testing.T) {
	source := opcodeSource()
	for _, tt := range []struct {
		name, source string
		valid        bool
	}{
		{"complete", source, true},
		{"empty", "", false},
		{"duplicate", source + "op(0x01, Other)\n", false},
		{"out-of-range", source + "op(0x100, Other)\n", false},
		{"malformed", source + "op(broken)\n", false},
		{"missing", strings.Replace(source, "op(0x00, Operation0, fp(OR), A)\n", "", 1), false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			ops, err := parseOps(strings.NewReader(tt.source))
			if (err == nil) != tt.valid {
				t.Fatalf("error=%v, valid=%v", err, tt.valid)
			}
			if tt.valid && (len(ops) != 256 || ops[0].name != "Operation0" || ops[255].name != "Operation255") {
				t.Fatal("opcode order/content changed")
			}
		})
	}
}

func TestGenerateDeterministic(t *testing.T) {
	source := opcodeSource()
	ops, err := parseOps(strings.NewReader(source))
	if err != nil {
		t.Fatal(err)
	}
	hash := sha256.Sum256([]byte(source))
	a, err := generateGo(ops, hash)
	if err != nil {
		t.Fatal(err)
	}
	b, err := generateGo(ops, hash)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(a, b) || !bytes.Contains(a, []byte(fmt.Sprintf("%x", hash))) {
		t.Fatal("output is not deterministic or omits source identity")
	}
	if !bytes.Contains(a, []byte(`0xff: "Operation255"`)) {
		t.Fatal("last opcode missing")
	}
}
