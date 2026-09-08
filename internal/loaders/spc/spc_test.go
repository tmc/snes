package spc

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"testing"
	"testing/iotest"
)

// Header and layout follow snes9x apu/bapu/smp/smp_state.cpp: SMP::save_spc.
func snapshot() []byte {
	b := make([]byte, 0x10180)
	copy(b, "SNES-SPC700 Sound File Data v0.30")
	copy(b[0x21:], []byte{0x1a, 0x1a, 0x1b, 30, 0x34, 0x12, 1, 2, 3, 4, 5})
	b[0x100], b[0x100ff], b[0x10100], b[0x1017f] = 6, 7, 8, 9
	return b
}

func TestDecode(t *testing.T) {
	for _, reader := range []struct {
		name string
		wrap func(io.Reader) io.Reader
	}{
		{"buffered", func(r io.Reader) io.Reader { return r }},
		{"short-reads", iotest.OneByteReader},
	} {
		t.Run(reader.name, func(t *testing.T) {
			s, err := Decode(reader.wrap(bytes.NewReader(snapshot())))
			if err != nil {
				t.Fatal(err)
			}
			if s.PC != 0x1234 || s.A != 1 || s.X != 2 || s.Y != 3 || s.PSW != 4 || s.SP != 5 {
				t.Fatalf("incorrect register extraction: PC=%04x A/X/Y/PSW/SP=%d/%d/%d/%d/%d", s.PC, s.A, s.X, s.Y, s.PSW, s.SP)
			}
			if s.RAM[0] != 6 || s.RAM[65535] != 7 || s.DSPRAM[0] != 8 || s.DSPRAM[127] != 9 {
				t.Fatal("incorrect memory extraction")
			}
		})
	}
}

func TestDecodeInvalid(t *testing.T) {
	for _, n := range []int{0, 0x100, 0x10100, 0x1017f} {
		t.Run(fmt.Sprintf("short-%x", n), func(t *testing.T) {
			_, err := Decode(bytes.NewReader(snapshot()[:n]))
			if !errors.Is(err, io.EOF) && !errors.Is(err, io.ErrUnexpectedEOF) {
				t.Fatalf("error=%v", err)
			}
		})
	}
	for _, offset := range []int{0, 0x21, 0x22, 0x23, 0x24} {
		t.Run(fmt.Sprintf("header-%x", offset), func(t *testing.T) {
			b := snapshot()
			b[offset] = 0
			if _, err := Decode(bytes.NewReader(b)); err == nil {
				t.Fatal("accepted invalid header")
			}
		})
	}
}

func ExampleDecode() {
	s, err := Decode(bytes.NewReader(snapshot()))
	if err != nil {
		fmt.Println(err)
		return
	}
	fmt.Printf("PC=%04x A=%d\n", s.PC, s.A)
	// Output: PC=1234 A=1
}
