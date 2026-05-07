package updsp

import "testing"

// TestIO_DRRoundtrip walks the CPU-side two-byte DR write protocol and the
// DSP-side response through one 16-bit exchange.
//
// Protocol contract (SR/DR half-read quirk, see design_doc §5.5):
//   - After reset, SR.RQM=1 -> CPU may write.
//   - CPU writes DR low byte, then high byte. RQM clears after high byte.
//   - DSP processes and calls SetDSPResult; RQM reasserts.
//   - CPU reads DR low byte (first), then high byte. RQM clears after high.
//
// A reader that skips RQM and races the halves observes wrong bytes — this
// test pins the deterministic byte order the core provides.
func TestIO_DRRoundtrip(t *testing.T) {
	io := NewIO(NewCore())

	if io.ReadSR()&0x80 == 0 {
		t.Fatalf("post-reset RQM not asserted: SR=%#02x", io.ReadSR())
	}

	// CPU writes 0xBEEF low-first: 0xEF then 0xBE.
	io.WriteDR(0xEF)
	if io.ReadSR()&0x80 == 0 {
		t.Errorf("after low-byte write, RQM should still be high (awaiting high byte), SR=%#02x", io.ReadSR())
	}
	io.WriteDR(0xBE)
	if io.Core.DR != 0xBEEF {
		t.Errorf("after 2-byte write, Core.DR=%#04x, want 0xBEEF", io.Core.DR)
	}
	if io.ReadSR()&0x80 != 0 {
		t.Errorf("after full write, RQM should be low, SR=%#02x", io.ReadSR())
	}

	// DSP produces a result 0x1234.
	io.SetDSPResult(0x1234)
	if io.ReadSR()&0x80 == 0 {
		t.Errorf("after DSP result, RQM should reassert, SR=%#02x", io.ReadSR())
	}

	lo := io.ReadDR()
	if lo != 0x34 {
		t.Errorf("DR low read=%#02x, want 0x34", lo)
	}
	hi := io.ReadDR()
	if hi != 0x12 {
		t.Errorf("DR high read=%#02x, want 0x12", hi)
	}
	if io.ReadSR()&0x80 != 0 {
		t.Errorf("after full read, RQM should be low, SR=%#02x", io.ReadSR())
	}
}

// TestIO_ResetProtocol clears a half-pending transfer and reasserts RQM.
func TestIO_ResetProtocol(t *testing.T) {
	io := NewIO(NewCore())
	io.WriteDR(0xAA) // leave mid-transfer
	io.ResetProtocol()
	if io.drHighNext {
		t.Errorf("ResetProtocol did not clear drHighNext")
	}
	if io.ReadSR()&0x80 == 0 {
		t.Errorf("ResetProtocol did not reassert RQM, SR=%#02x", io.ReadSR())
	}
}

// TestIO_ReadStartsFreshTransfer: a read following a reset begins on the low
// byte. If the half-pointer leaked across a reset the first CPU read would
// return the high byte instead.
func TestIO_ReadStartsFreshTransfer(t *testing.T) {
	io := NewIO(NewCore())
	io.SetDSPResult(0xCAFE)
	if got := io.ReadDR(); got != 0xFE {
		t.Errorf("first ReadDR=%#02x, want 0xFE (low byte first)", got)
	}
	if got := io.ReadDR(); got != 0xCA {
		t.Errorf("second ReadDR=%#02x, want 0xCA (high byte)", got)
	}
}

func TestIO_DRSHalfTransferStatus(t *testing.T) {
	tests := []struct {
		name  string
		first func(*IO)
	}{
		{
			name: "read",
			first: func(io *IO) {
				io.SetDSPResult(0x1234)
				io.ReadDR()
			},
		},
		{
			name: "write",
			first: func(io *IO) {
				io.WriteDR(0x34)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			io := NewIO(NewCore())
			tt.first(io)

			sr := io.ReadSR()
			if sr&0x10 == 0 {
				t.Fatalf("mid-transfer SR=%02X, want DRS set", sr)
			}
			if sr&0x04 != 0 {
				t.Fatalf("mid-transfer SR=%02X, want DRC clear for 16-bit transfer", sr)
			}
		})
	}
}

func TestIO_ReadSRMasksDRSIn8BitMode(t *testing.T) {
	io := NewIO(NewCore())
	io.Core.SR = srRQM | srDRS | srDRC

	if got := io.ReadSR(); got != 0x84 {
		t.Fatalf("ReadSR with DRC set = %02X, want 84", got)
	}
	if io.Core.SR != srRQM|srDRS|srDRC {
		t.Fatalf("ReadSR mutated SR: %04X", io.Core.SR)
	}
}
