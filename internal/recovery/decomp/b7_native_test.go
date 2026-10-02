package decomp

import (
	"context"
	"testing"

	"github.com/tmc/snes/internal/recovery"
)

func TestLDAIndirectLongYNativeSupport(t *testing.T) {
	for _, tt := range []struct {
		name, m, x string
		p          uint8
		a, want    uint16
	}{
		{"native8", "set", "set", 0x30, 0xab00, 0xab34},
		{"native16", "clear", "clear", 0, 0xab00, 0x1234},
	} {
		t.Run(tt.name, func(t *testing.T) {
			region, err := DecodeRegionFromBytes([]byte{0xb7, 0xff, 0x60}, 0x008000, recovery.Context{E: "clear", M: tt.m, X: tt.x, C: "unknown"}, nil, 0, 100)
			if err != nil {
				t.Fatal(err)
			}
			source, err := GenerateRegionC(region)
			if err != nil {
				t.Fatal(err)
			}
			memory := []MemoryCell{{0x7e00ff, 0}, {0x7e0100, 0x1e}, {0x7e0101, 0x7e}, {0x7e1e02, 0x34}, {0x7e1e03, 0x12}, {0x7e01fe, 0xff}, {0x7e01ff, 0x8f}}
			c := ReplayCase{CaseID: tt.name, InitialState: CPUState{A: tt.a, Y: 2, S: 0x1fd, PC: 0x8000, DB: 0x0c, P: tt.p}, InitialMemory: memory}
			r, err := compileAndRunRegionWithROM(context.Background(), t, source, "execute_"+region.Name, nil, []ReplayCase{c})
			if err != nil {
				t.Fatal(err)
			}
			if r[0].MissingRead || r[0].State.A != tt.want || r[0].State.P&0x82 != 0 || r[0].NextPC != 0x009000 {
				t.Fatalf("B7 native execution: %+v", r[0])
			}
			t.Logf("A=%04x pointer=$00ff/$0100/$0101 target=$7e1e02 next=$%06x", r[0].State.A, r[0].NextPC)
			c.InitialMemory = memory[1:]
			r, err = compileAndRunRegionWithROM(context.Background(), t, source, "execute_"+region.Name, nil, []ReplayCase{c})
			if err != nil {
				t.Fatal(err)
			}
			if !r[0].MissingRead {
				t.Fatal("missing pointer byte did not refuse")
			}
		})
	}
}
