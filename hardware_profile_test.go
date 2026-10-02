package snes

import "testing"

func TestLoadROMExplicitHardwareProfile(t *testing.T) {
	s := NewSystem(nil)
	rom := newBootableTestROM()
	if err := s.LoadROM(rom); err != nil {
		t.Fatal(err)
	}
	before := s.cart
	if err := s.LoadROMWithOptions(rom, LoadROMOptions{Coprocessor: "not-a-chip"}); err == nil {
		t.Fatal("unknown chip accepted")
	}
	if s.cart != before {
		t.Fatal("failed profile replaced installed cartridge")
	}
	if err := s.LoadROMWithOptions(rom, LoadROMOptions{Coprocessor: "cx4"}); err != nil {
		t.Fatal(err)
	}
	if s.cart.CoprocessorID != "cx4" {
		t.Fatal("explicit profile not installed")
	}
}
