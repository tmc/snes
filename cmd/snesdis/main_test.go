package main

import (
	"bytes"
	"strings"
	"testing"
)

func TestRun(t *testing.T) {
	tests := []struct {
		name     string
		args     []string
		stdin    []byte
		wantCode int
		wantSubs []string // all must appear in stdout (in any order)
		wantMiss []string // none may appear (used to confirm width mode)
	}{
		{
			name:     "65816 LDA immediate 8-bit default",
			args:     nil,
			stdin:    []byte{0xA9, 0x05},
			wantSubs: []string{"LDA #$05"},
			wantMiss: []string{"LDA #$0005"},
		},
		{
			name:     "65816 LDA immediate 16-bit with --mx=0",
			args:     []string{"--mx=0"},
			stdin:    []byte{0xA9, 0x05, 0x00},
			wantSubs: []string{"LDA #$0005"},
		},
		{
			name: "65816 SEP/REP toggles immediate width mid-stream",
			args: nil,
			// REP #$30 (clear m,x) ; LDA #$1234 ; SEP #$20 (set m) ; LDA #$55
			stdin: []byte{0xC2, 0x30, 0xA9, 0x34, 0x12, 0xE2, 0x20, 0xA9, 0x55},
			wantSubs: []string{
				"REP #$30",
				"LDA #$1234",
				"SEP #$20",
				"LDA #$55",
			},
		},
		{
			name:     "65816 --base prefixes absolute address",
			args:     []string{"--base=0x7E8000"},
			stdin:    []byte{0xEA}, // NOP
			wantSubs: []string{"7E:8000", "NOP"},
		},
		{
			name:     "65816 --emulation forces 8-bit immediates even with --mx=0",
			args:     []string{"--emulation", "--mx=0"},
			stdin:    []byte{0xA9, 0x42},
			wantSubs: []string{"LDA #$42"},
			wantMiss: []string{"LDA #$0042"},
		},
		{
			name:     "65816 --count halts after N instructions",
			args:     []string{"--count=1"},
			stdin:    []byte{0xEA, 0xEA, 0xEA}, // three NOPs
			wantSubs: []string{"NOP"},
		},
		{
			name:     "65816 --offset skips leading bytes",
			args:     []string{"--offset=1"},
			stdin:    []byte{0xFF, 0xEA}, // first byte skipped, then NOP
			wantSubs: []string{"NOP"},
		},
		{
			name:     "65816 truncated final instruction emits a comment",
			args:     nil,
			stdin:    []byte{0xAD, 0x34}, // LDA abs needs 3 bytes; only 2 provided
			wantSubs: []string{"truncated"},
		},
		{
			name:     "spc700 basic walk",
			args:     []string{"--cpu=spc700", "--base=0x0800"},
			stdin:    []byte{0xE8, 0x42, 0xE5, 0x40, 0x21},
			wantSubs: []string{"0800", "MOV A, #$42", "0802", "MOV A, $2140"},
		},
		{
			name:     "spc700 --count",
			args:     []string{"--cpu=spc700", "--count=1"},
			stdin:    []byte{0xE8, 0x01, 0xE8, 0x02},
			wantSubs: []string{"MOV A, #$01"},
			wantMiss: []string{"MOV A, #$02"},
		},
		{
			name:     "unknown --cpu is a usage error",
			args:     []string{"--cpu=arm"},
			stdin:    nil,
			wantCode: 2,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			code := run(tc.args, bytes.NewReader(tc.stdin), &stdout, &stderr)
			if code != tc.wantCode {
				t.Fatalf("exit code = %d, want %d\nstderr: %s", code, tc.wantCode, stderr.String())
			}
			got := stdout.String()
			for _, want := range tc.wantSubs {
				if !strings.Contains(got, want) {
					t.Errorf("output missing %q\n got: %s", want, got)
				}
			}
			for _, bad := range tc.wantMiss {
				if strings.Contains(got, bad) {
					t.Errorf("output unexpectedly contains %q\n got: %s", bad, got)
				}
			}
		})
	}
}
