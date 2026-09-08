package cartridge

import (
	"fmt"
	"testing"

	"github.com/tmc/snes/internal/cartridge/chips/updsp"
)

func ExampleValidateHeader() {
	rom := make([]byte, 0x8000)
	rom[0x7fd5] = 0x20
	fmt.Println(ValidateHeader(rom))
	// Output: <nil>
}

func ExampleCartridge_LoadUPDSP() {
	cart := New(make([]byte, 0x8000))
	loader, err := cart.LoadUPDSP(updsp.VariantDSP1, nil, nil)
	if err != nil {
		fmt.Println(err)
		return
	}
	fmt.Println(loader.LoadedOK)
	// Output: false
}

func TestCartridgeHeaderRAMBounds(t *testing.T) {
	for _, tt := range []struct {
		encoding byte
		want     int
		valid    bool
	}{
		{0, 0, true}, {1, 2048, true}, {8, 256 * 1024, true}, {9, 0, false}, {31, 0, false}, {63, 0, false}, {255, 0, false},
	} {
		t.Run(fmt.Sprint(tt.encoding), func(t *testing.T) {
			rom := make([]byte, 0x8000)
			rom[0x7fd5] = 0x20
			rom[0x7fd8] = tt.encoding
			if err := ValidateHeader(rom); (err == nil) != tt.valid {
				t.Fatalf("ValidateHeader = %v, valid=%v", err, tt.valid)
			}
			// The unchecked constructor also bounds allocation for internal callers.
			cart := New(rom)
			if len(cart.RAM) != tt.want {
				t.Fatalf("RAM size = %d, want %d", len(cart.RAM), tt.want)
			}
		})
	}
	if err := ValidateHeader(make([]byte, 100)); err == nil {
		t.Fatal("truncated header accepted")
	}
}
