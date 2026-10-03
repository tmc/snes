package analysis

import (
	"encoding/binary"
	"testing"

	"github.com/tmc/snes/internal/recovery"
)

func makeTestROM(baseAddr uint32, code []byte) []byte {
	rom := make([]byte, 32*1024)
	offset, ok := LoROMToOffset(baseAddr, len(rom))
	if ok && int(offset)+len(code) <= len(rom) {
		copy(rom[offset:], code)
	}
	// Also set reset vector in case LoROM header is inspected
	binary.LittleEndian.PutUint16(rom[0x7FC0+0x3C:], 0x8000)
	return rom
}

func TestInferCallReturnSummary(t *testing.T) {
	tests := []struct {
		name        string
		callOp      byte
		target      uint32
		entryCtx    recovery.Context
		code        []byte
		wantKnown   bool
		wantM       string
		wantX       string
		wantC       string
		wantErr     bool
		errContains string
	}{
		{
			name:   "preserve flags acyclic leaf",
			callOp: 0x20, // JSR
			target: 0x8010,
			entryCtx: recovery.Context{
				E: "clear",
				M: "set",
				X: "clear",
				C: "set",
			},
			code: []byte{
				0xEA, // NOP
				0x60, // RTS
			},
			wantKnown: true,
			wantM:     "set",
			wantX:     "clear",
			wantC:     "set",
		},
		{
			name:   "explicitly set M and X via REP",
			callOp: 0x20,
			target: 0x8010,
			entryCtx: recovery.Context{
				E: "clear",
				M: "set",
				X: "set",
				C: "unknown",
			},
			code: []byte{
				0xC2, 0x30, // REP #$30 -> M=clear, X=clear
				0x60, // RTS
			},
			wantKnown: true,
			wantM:     "clear",
			wantX:     "clear",
		},
		{
			name:   "explicitly set M and X via SEP",
			callOp: 0x20,
			target: 0x8010,
			entryCtx: recovery.Context{
				E: "clear",
				M: "clear",
				X: "clear",
				C: "unknown",
			},
			code: []byte{
				0xE2, 0x30, // SEP #$30 -> M=set, X=set
				0x60, // RTS
			},
			wantKnown: true,
			wantM:     "set",
			wantX:     "set",
		},
		{
			name:   "conditional branches with convergent flags",
			callOp: 0x20,
			target: 0x8010,
			entryCtx: recovery.Context{
				E: "clear",
				M: "clear",
				X: "clear",
				C: "unknown",
			},
			code: []byte{
				// $8010: CMP #$00
				0xC9, 0x00, 0x00,
				// $8013: BEQ $8017 (+2)
				0xF0, 0x02,
				// $8015: SEP #$20 (fallthrough)
				0xE2, 0x20,
				// $8017: SEP #$20 (taken)
				0xE2, 0x20,
				// $8019: RTS
				0x60,
			},
			wantKnown: true,
			wantM:     "set",
			wantX:     "clear",
		},
		{
			name:   "conditional branches with divergent flags",
			callOp: 0x20,
			target: 0x8010,
			entryCtx: recovery.Context{
				E: "clear",
				M: "clear",
				X: "clear",
				C: "unknown",
			},
			code: []byte{
				// $8010: CMP #$00
				0xC9, 0x00, 0x00,
				// $8013: BEQ $8017 (+2)
				0xF0, 0x02,
				// $8015: SEP #$20 -> M=set (fallthrough)
				0xE2, 0x20,
				// $8017: RTS (fallthrough branch returns with M=set, taken branch returns with M=clear)
				0x60,
			},
			wantKnown: false, // M becomes unknown due to divergence
			wantM:     "unknown",
		},
		{
			name:   "carry cleared deterministically with CLC",
			callOp: 0x20,
			target: 0x8010,
			entryCtx: recovery.Context{
				E: "clear",
				M: "set",
				X: "set",
				C: "unknown",
			},
			code: []byte{
				0x18, // CLC
				0x60, // RTS
			},
			wantKnown: true,
			wantM:     "set",
			wantX:     "set",
			wantC:     "clear",
		},
		{
			name:   "carry set deterministically with SEC",
			callOp: 0x20,
			target: 0x8010,
			entryCtx: recovery.Context{
				E: "clear",
				M: "set",
				X: "set",
				C: "unknown",
			},
			code: []byte{
				0x38, // SEC
				0x60, // RTS
			},
			wantKnown: true,
			wantM:     "set",
			wantX:     "set",
			wantC:     "set",
		},
		{
			name:   "carry invalidated by CMP",
			callOp: 0x20,
			target: 0x8010,
			entryCtx: recovery.Context{
				E: "clear",
				M: "set",
				X: "set",
				C: "set",
			},
			code: []byte{
				0xC9, 0x10, // CMP #$10 (8-bit)
				0x60, // RTS
			},
			wantKnown: true,
			wantM:     "set",
			wantX:     "set",
			wantC:     "unknown",
		},
		{
			name:   "cyclically complex self loop",
			callOp: 0x20,
			target: 0x8010,
			entryCtx: recovery.Context{
				E: "clear",
				M: "set",
				X: "set",
				C: "unknown",
			},
			code: []byte{
				0x80, 0xFE, // BRA $8010 (offset -2 -> self)
			},
			wantErr:     true,
			errContains: "cycle detected",
		},
		{
			name:   "cyclically complex backward branch",
			callOp: 0x20,
			target: 0x8010,
			entryCtx: recovery.Context{
				E: "clear",
				M: "set",
				X: "set",
				C: "unknown",
			},
			code: []byte{
				0xCA,       // $8010: DEX
				0xD0, 0xFD, // $8011: BNE $8010 (-3)
				0x60, // $8013: RTS
			},
			wantErr:     true,
			errContains: "cycle detected",
		},
		{
			name:   "unresolved indirect jump",
			callOp: 0x20,
			target: 0x8010,
			entryCtx: recovery.Context{
				E: "clear",
				M: "set",
				X: "set",
				C: "unknown",
			},
			code: []byte{
				0x6C, 0x00, 0x12, // JMP ($1200)
			},
			wantErr:     true,
			errContains: "unresolved indirect control flow",
		},
		{
			name:   "unbalanced stack delta push without pop",
			callOp: 0x20,
			target: 0x8010,
			entryCtx: recovery.Context{
				E: "clear",
				M: "set",
				X: "set",
				C: "unknown",
			},
			code: []byte{
				0x48, // PHA
				0x60, // RTS
			},
			wantErr:     true,
			errContains: "unbalanced stack delta",
		},
		{
			name:   "stack underflow pop without push",
			callOp: 0x20,
			target: 0x8010,
			entryCtx: recovery.Context{
				E: "clear",
				M: "set",
				X: "set",
				C: "unknown",
			},
			code: []byte{
				0x68, // PLA
				0x60, // RTS
			},
			wantErr:     true,
			errContains: "stack underflow",
		},
		{
			name:   "direct page modified without restore",
			callOp: 0x20,
			target: 0x8010,
			entryCtx: recovery.Context{
				E: "clear",
				M: "set",
				X: "set",
				C: "unknown",
			},
			code: []byte{
				0x5B, // TCD
				0x60, // RTS
			},
			wantErr:     true,
			errContains: "direct page not preserved",
		},
		{
			name:   "direct page saved and restored",
			callOp: 0x20,
			target: 0x8010,
			entryCtx: recovery.Context{
				E: "clear",
				M: "set",
				X: "set",
				C: "unknown",
			},
			code: []byte{
				0x0B, // PHD
				0x5B, // TCD
				0x2B, // PLD
				0x60, // RTS
			},
			wantKnown: true,
			wantM:     "set",
			wantX:     "set",
		},
		{
			name:   "JSL long call with RTL return",
			callOp: 0x22, // JSL
			target: 0x8010,
			entryCtx: recovery.Context{
				E: "clear",
				M: "set",
				X: "set",
				C: "unknown",
			},
			code: []byte{
				0xEA, // NOP
				0x6B, // RTL
			},
			wantKnown: true,
			wantM:     "set",
			wantX:     "set",
		},
		{
			name:   "JSR call returning with RTL mismatch",
			callOp: 0x20, // JSR expects RTS
			target: 0x8010,
			entryCtx: recovery.Context{
				E: "clear",
				M: "set",
				X: "set",
				C: "unknown",
			},
			code: []byte{
				0x6B, // RTL
			},
			wantErr:     true,
			errContains: "returned with opcode 0x6B",
		},
		{
			name:   "JSL call returning with RTS mismatch",
			callOp: 0x22, // JSL expects RTL
			target: 0x8010,
			entryCtx: recovery.Context{
				E: "clear",
				M: "set",
				X: "set",
				C: "unknown",
			},
			code: []byte{
				0x60, // RTS
			},
			wantErr:     true,
			errContains: "returned with opcode 0x60",
		},
		{
			name:   "callee halts without return",
			callOp: 0x20,
			target: 0x8010,
			entryCtx: recovery.Context{
				E: "clear",
				M: "set",
				X: "set",
				C: "unknown",
			},
			code: []byte{
				0xDB, // STP
			},
			wantErr:     true,
			errContains: "processor stop/wait",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rom := makeTestROM(tt.target, tt.code)
			summary, err := InferCallReturnSummary(rom, tt.callOp, tt.target, tt.entryCtx)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("expected error, got nil")
				}
				if tt.errContains != "" && !containsSubstr(err.Error(), tt.errContains) {
					t.Errorf("error %q does not contain %q", err.Error(), tt.errContains)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if summary.Known() != tt.wantKnown {
				t.Errorf("Known() = %v, want %v", summary.Known(), tt.wantKnown)
			}
			if tt.wantM != "" && summary.ReturnContext.M != tt.wantM {
				t.Errorf("M flag = %q, want %q", summary.ReturnContext.M, tt.wantM)
			}
			if tt.wantX != "" && summary.ReturnContext.X != tt.wantX {
				t.Errorf("X flag = %q, want %q", summary.ReturnContext.X, tt.wantX)
			}
			if tt.wantC != "" && summary.ReturnContext.C != tt.wantC {
				t.Errorf("C flag = %q, want %q", summary.ReturnContext.C, tt.wantC)
			}
		})
	}
}

func TestInferCallReturnSummary_NestedCall(t *testing.T) {
	// Subroutine 1 at $8010 calls Subroutine 2 at $8020
	// Subroutine 2 does: REP #$20 (M=0), RTS
	// Subroutine 1 does: JSR $8020, RTS
	rom := make([]byte, 32*1024)
	sub1Offset, _ := LoROMToOffset(0x8010, len(rom))
	sub2Offset, _ := LoROMToOffset(0x8020, len(rom))

	sub1Code := []byte{
		0x20, 0x20, 0x80, // JSR $8020
		0x60, // RTS
	}
	sub2Code := []byte{
		0xC2, 0x20, // REP #$20 -> M=clear
		0x60, // RTS
	}

	copy(rom[sub1Offset:], sub1Code)
	copy(rom[sub2Offset:], sub2Code)

	entryCtx := recovery.Context{E: "clear", M: "set", X: "set", C: "unknown"}
	summary, err := InferCallReturnSummary(rom, 0x20, 0x8010, entryCtx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !summary.Known() {
		t.Errorf("expected summary to be known")
	}
	if summary.ReturnContext.M != "clear" {
		t.Errorf("expected M=clear, got %s", summary.ReturnContext.M)
	}
	if summary.ReturnContext.X != "set" {
		t.Errorf("expected X=set, got %s", summary.ReturnContext.X)
	}
}

func containsSubstr(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
