package aputest

// HiganSPC700ROM describes a reference-visible SPC700 test ROM from
// higan-snes-test-roms. The ROM bytes are not embedded here; parity should load
// them from SourcePathHint or a reviewed copy in its own testdata.
type HiganSPC700ROM struct {
	Name           string
	ROMFilename    string
	SourcePathHint string
	SHA256         string

	// The SPC program reports each subtest through CPUIO0. Values below $80 are
	// pass markers; the matching value with bit 7 set is the fail marker.
	PassCount      int
	FinalPassPort0 uint8
}

// HiganSPC700ROMs returns the SPC700 arithmetic and logic ROM fixtures that are
// ready for a reference-visible parity harness.
func HiganSPC700ROMs() []HiganSPC700ROM {
	const root = "/Users/tmc/go/src/github.com/peteward44/higan-snes-test-roms/PeterLemon/SNES-CPUTest-SPC700"
	return []HiganSPC700ROM{
		{
			Name:           "ADC",
			ROMFilename:    "SPC700ADC.sfc",
			SourcePathHint: root + "/ADC/SPC700ADC.sfc",
			SHA256:         "4fa785418a109c0ef052cb9b71f55d37ee56834fca170fe1a0da784ee3df0e2e",
			PassCount:      26,
			FinalPassPort0: 0x1A,
		},
		{
			Name:           "AND",
			ROMFilename:    "SPC700AND.sfc",
			SourcePathHint: root + "/AND/SPC700AND.sfc",
			SHA256:         "a4e7a9d6a926f5b575b2c3006b301bf837ed36072c9617776aab527706c3242d",
			PassCount:      28,
			FinalPassPort0: 0x1C,
		},
		{
			Name:           "DEC",
			ROMFilename:    "SPC700DEC.sfc",
			SourcePathHint: root + "/DEC/SPC700DEC.sfc",
			SHA256:         "fe9254720e62b2aa953a42f50827facc12f7549ad8ca3775c89a5b65f4b55fdf",
			PassCount:      14,
			FinalPassPort0: 0x0E,
		},
		{
			Name:           "EOR",
			ROMFilename:    "SPC700EOR.sfc",
			SourcePathHint: root + "/EOR/SPC700EOR.sfc",
			SHA256:         "061572e2ba5599f78c3573be3b538ba2ab67703529f7518eee8e8ce3e41372f1",
			PassCount:      26,
			FinalPassPort0: 0x1A,
		},
		{
			Name:           "INC",
			ROMFilename:    "SPC700INC.sfc",
			SourcePathHint: root + "/INC/SPC700INC.sfc",
			SHA256:         "467376b5ab3cf83197553ec598f91645beef628dadbd43932b6e96d2fa17776b",
			PassCount:      14,
			FinalPassPort0: 0x0E,
		},
		{
			Name:           "ORA",
			ROMFilename:    "SPC700ORA.sfc",
			SourcePathHint: root + "/ORA/SPC700ORA.sfc",
			SHA256:         "aefb416d0f681eed0975cfbee70fd3048f59154cd5502f550e48e80f789a0ab2",
			PassCount:      28,
			FinalPassPort0: 0x1C,
		},
		{
			Name:           "SBC",
			ROMFilename:    "SPC700SBC.sfc",
			SourcePathHint: root + "/SBC/SPC700SBC.sfc",
			SHA256:         "fcc6017db0560ce12ae029fa97947eedbd8eaf35dd32200c445281ca94cbae9b",
			PassCount:      26,
			FinalPassPort0: 0x1A,
		},
	}
}
