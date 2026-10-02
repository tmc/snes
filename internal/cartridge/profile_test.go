package cartridge

import (
	"fmt"
	"testing"
)

func ExampleNewWithCoprocessor() {
	c, err := NewWithCoprocessor(make([]byte, 1<<15), "cx4")
	fmt.Println(c.CoprocessorID, err)
	// Output: cx4 <nil>
}

func TestExplicitCoprocessorProfile(t *testing.T) {
	for _, tt := range []struct {
		name      string
		id        string
		want      string
		wantError bool
	}{
		{"header", "", "", false},
		{"override", "cx4", "cx4", false},
		{"unknown", "unknown-device", "", true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			rom := makeROM(0x20000)
			c, err := NewWithCoprocessor(rom, tt.id)
			if (err != nil) != tt.wantError {
				t.Fatalf("NewWithCoprocessor: %v", err)
			}
			if err == nil && c.CoprocessorID != tt.want {
				t.Fatalf("coprocessor = %q, want %q", c.CoprocessorID, tt.want)
			}
		})
	}
}
