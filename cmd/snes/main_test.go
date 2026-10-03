package main

import (
	"bytes"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/tmc/snes"
	"github.com/tmc/snes/emulator"
	"github.com/tmc/snes/internal/snesagent"
)

func TestParseCheatLine(t *testing.T) {
	tests := []struct {
		name string
		line string
		want struct {
			name       string
			addr       uint32
			val        uint8
			hasCompare bool
			compare    uint8
		}
	}{
		{
			name: "simple",
			line: "7E1234=FF",
			want: struct {
				name       string
				addr       uint32
				val        uint8
				hasCompare bool
				compare    uint8
			}{addr: 0x7E1234, val: 0xFF},
		},
		{
			name: "compare",
			line: "7E0010?AA=55",
			want: struct {
				name       string
				addr       uint32
				val        uint8
				hasCompare bool
				compare    uint8
			}{addr: 0x7E0010, val: 0x55, hasCompare: true, compare: 0xAA},
		},
		{
			name: "named",
			line: "InfiniteHP: 7E0010=63",
			want: struct {
				name       string
				addr       uint32
				val        uint8
				hasCompare bool
				compare    uint8
			}{name: "InfiniteHP", addr: 0x7E0010, val: 0x63},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseCheatLine(tt.line)
			if err != nil {
				t.Fatalf("parseCheatLine: %v", err)
			}
			if got.Name != tt.want.name || got.Address != tt.want.addr || got.Value != tt.want.val ||
				got.HasCompare != tt.want.hasCompare || got.Compare != tt.want.compare {
				t.Fatalf("parseCheatLine(%q) = %+v", tt.line, got)
			}
		})
	}
}

func TestStateSlotPath(t *testing.T) {
	tests := []struct {
		name string
		base string
		slot int
		want string
	}{
		{name: "state extension", base: "game.state", slot: 1, want: filepath.Join("hash", "1.state")},
		{name: "keeps extension only", base: "/tmp/game.state", slot: 9, want: filepath.Join("hash", "9.state")},
		{name: "default extension", base: "/tmp/game", slot: 2, want: filepath.Join("hash", "2.state")},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := stateSlotPath("hash", tt.base, tt.slot)
			if got != tt.want {
				t.Fatalf("stateSlotPath(%q, %q, %d) = %q, want %q", "hash", tt.base, tt.slot, got, tt.want)
			}
		})
	}
}

func TestLoadCheats(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.cheats")
	data := `
# comment
7E1234=01
Life: 7E0010?AA=55
`
	if err := os.WriteFile(path, []byte(data), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	cheats, err := loadCheats(path)
	if err != nil {
		t.Fatalf("loadCheats: %v", err)
	}
	if len(cheats) != 2 {
		t.Fatalf("len(cheats) = %d, want 2", len(cheats))
	}
	if cheats[0].Address != 0x7E1234 || cheats[0].Value != 0x01 {
		t.Fatalf("cheats[0] = %+v", cheats[0])
	}
	if cheats[1].Name != "Life" || !cheats[1].HasCompare || cheats[1].Compare != 0xAA || cheats[1].Value != 0x55 {
		t.Fatalf("cheats[1] = %+v", cheats[1])
	}
}

func TestRunHeadlessFrames(t *testing.T) {
	rom := make([]byte, 0x8000)
	rom[0x7fd5] = 0x20
	rom[0x7ffc] = 0x00
	rom[0x7ffd] = 0x80
	for i := 0; i < 0x100; i++ {
		rom[i] = 0xea
	}

	sys := snes.NewSystem(nil)
	if err := sys.LoadROM(rom); err != nil {
		t.Fatalf("LoadROM: %v", err)
	}
	sys.Power()

	var out bytes.Buffer
	pngDir := t.TempDir()
	if err := runHeadlessFrames(sys, 2, &out, pngDir, 1, false, nil); err != nil {
		t.Fatalf("runHeadlessFrames: %v", err)
	}

	lines := strings.Split(strings.TrimSpace(out.String()), "\n")
	if len(lines) != 3 {
		t.Fatalf("line count = %d, want 3", len(lines))
	}
	if lines[0] != "frame,pc,cycles,fb_nonzero,fb_hash,fb_diff,audio_samples,audio_hash,audio_diff" {
		t.Fatalf("unexpected header: %q", lines[0])
	}
	if !strings.HasPrefix(lines[1], "0,") {
		t.Fatalf("first frame line malformed: %q", lines[1])
	}
	if !strings.HasPrefix(lines[2], "1,") {
		t.Fatalf("second frame line malformed: %q", lines[2])
	}

	png0 := filepath.Join(pngDir, "frame_000000.png")
	png1 := filepath.Join(pngDir, "frame_000001.png")
	if _, err := os.Stat(png0); err != nil {
		t.Fatalf("missing png0: %v", err)
	}
	if _, err := os.Stat(png1); err != nil {
		t.Fatalf("missing png1: %v", err)
	}

	f, err := os.Open(png0)
	if err != nil {
		t.Fatalf("open png: %v", err)
	}
	defer f.Close()
	img, err := png.Decode(f)
	if err != nil {
		t.Fatalf("decode png: %v", err)
	}
	b := img.Bounds()
	if b.Dx() != 256 || b.Dy() <= 0 {
		t.Fatalf("png bounds = %v, want width=256 height>0", b)
	}
}

func TestRunHeadlessFramesRejectsInvalidPNGCadence(t *testing.T) {
	sys := snes.NewSystem(nil)
	var out bytes.Buffer
	if err := runHeadlessFrames(sys, 1, &out, "", 0, false, nil); err == nil {
		t.Fatal("runHeadlessFrames accepted frame-png-every=0")
	}
}

func TestParseInputScriptLine(t *testing.T) {
	span, err := parseInputScriptLine("10-12: start + a + right")
	if err != nil {
		t.Fatalf("parseInputScriptLine: %v", err)
	}
	want := inputSpan{
		start: 10,
		end:   12,
		state: emulator.StandardButtonStart | emulator.StandardButtonA | emulator.StandardButtonRight,
	}
	if span != want {
		t.Fatalf("span = %+v, want %+v", span, want)
	}
	if got := inputStateAt([]inputSpan{span}, 9); got != 0 {
		t.Fatalf("inputStateAt before span = %04X, want 0", got)
	}
	if got := inputStateAt([]inputSpan{span}, 11); got != want.state {
		t.Fatalf("inputStateAt inside span = %04X, want %04X", got, want.state)
	}
}

func TestLoadSNESReplayRequestAndInputs(t *testing.T) {
	dir := t.TempDir()
	inputsPath := filepath.Join(dir, "inputs.json")
	if err := os.WriteFile(inputsPath, []byte(`[0,512,1024]`), 0o644); err != nil {
		t.Fatalf("write inputs: %v", err)
	}
	requestPath := filepath.Join(dir, "request.json")
	request := `{
  "rom_path": "game.sfc",
  "state_path": "start.state",
  "frames": 3,
  "inputs_path": "` + filepath.ToSlash(inputsPath) + `",
  "allow_state_rom_mismatch": true,
  "target": {"target": {"region": "mode", "from": 7, "to": 5}}
}`
	if err := os.WriteFile(requestPath, []byte(request), 0o644); err != nil {
		t.Fatalf("write request: %v", err)
	}
	req, err := loadSNESReplayRequest(requestPath)
	if err != nil {
		t.Fatalf("loadSNESReplayRequest: %v", err)
	}
	if req.ROMPath != "game.sfc" || req.StatePath != "start.state" || req.Frames != 3 || !req.AllowStateROMMismatch {
		t.Fatalf("request = %+v", req)
	}
	inputs, err := loadRequestInputs(req.InputsPath)
	if err != nil {
		t.Fatalf("loadRequestInputs: %v", err)
	}
	want := []uint16{0, 512, 1024}
	if len(inputs) != len(want) {
		t.Fatalf("len(inputs) = %d, want %d", len(inputs), len(want))
	}
	for i := range want {
		if inputs[i] != want[i] {
			t.Fatalf("inputs[%d] = %d, want %d", i, inputs[i], want[i])
		}
	}
}

func TestParseAgentActionLine(t *testing.T) {
	tests := []struct {
		name string
		line string
		want uint16
	}{
		{
			name: "name",
			line: `{"type":"action","action_name":"down_right"}`,
			want: emulator.StandardButtonDown | emulator.StandardButtonRight,
		},
		{
			name: "numeric action",
			line: `{"type":"action","action":1}`,
			want: emulator.StandardButtonUp,
		},
		{
			name: "string action",
			line: `{"type":"action","action":"a"}`,
			want: emulator.StandardButtonA,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseAgentActionLine([]byte(tt.line))
			if err != nil {
				t.Fatalf("parseAgentActionLine: %v", err)
			}
			if got != tt.want {
				t.Fatalf("parseAgentActionLine = %04X, want %04X", got, tt.want)
			}
		})
	}
}

func TestParseAgentLineCommand(t *testing.T) {
	input, cmd, err := parseAgentLine([]byte(`{"type":"load_state","path":"/tmp/a.state"}`))
	if err != nil {
		t.Fatalf("parseAgentLine: %v", err)
	}
	if input != nil {
		t.Fatalf("input = %v, want nil", *input)
	}
	if cmd == nil || cmd.Type != "load_state" || cmd.Path != "/tmp/a.state" {
		t.Fatalf("cmd = %+v, want load_state /tmp/a.state", cmd)
	}
	if _, err := parseAgentActionLine([]byte(`{"type":"save_state","path":"/tmp/b.state"}`)); err == nil {
		t.Fatal("parseAgentActionLine accepted command")
	}
}

func TestAgentProtoReaderWriter(t *testing.T) {
	action := uint32(1)
	var in bytes.Buffer
	pw := snesagent.NewProtoWriter(&in)
	if err := pw.Write(snesagent.Observation{
		Type:            "observation",
		Frame:           3,
		Width:           256,
		Height:          224,
		FrameBuffer:     []byte{1, 2},
		Actor:           "policy",
		ExecutedButtons: []string{"up"},
		ExecutedAction:  "up",
	}); err != nil {
		t.Fatalf("Write observation: %v", err)
	}
	if in.Len() == 0 {
		t.Fatal("empty proto observation")
	}

	data := snesagent.MarshalClientMessageProto(snesagent.ClientMessage{Type: "action", Action: action})
	var framed bytes.Buffer
	framed.WriteByte(byte(len(data)))
	framed.Write(data)
	msg, err := newAgentMessageReader(&framed, snesagent.FormatProto).Read()
	if err != nil {
		t.Fatalf("Read proto action: %v", err)
	}
	input, cmd, err := parseAgentMessage(msg)
	if err != nil {
		t.Fatalf("parseAgentMessage: %v", err)
	}
	if cmd != nil {
		t.Fatalf("cmd = %+v, want nil", cmd)
	}
	if input == nil || *input != emulator.StandardButtonUp {
		t.Fatalf("input = %v, want up", input)
	}
}

func TestParseAgentRAMRange(t *testing.T) {
	off, length, err := parseAgentRAMRange("0x10:32")
	if err != nil {
		t.Fatalf("parseAgentRAMRange: %v", err)
	}
	if off != 0x10 || length != 32 {
		t.Fatalf("parseAgentRAMRange = %d,%d, want 16,32", off, length)
	}
	off, length, err = parseAgentRAMRange("")
	if err != nil {
		t.Fatalf("parseAgentRAMRange empty: %v", err)
	}
	if off != 0 || length != 0 {
		t.Fatalf("parseAgentRAMRange empty = %d,%d, want 0,0", off, length)
	}
}

func TestEncodeU16LEBytes(t *testing.T) {
	got := encodeU16LEBytes([]uint16{0x1234, 0x00ff})
	want := []byte{0x34, 0x12, 0xff, 0x00}
	if !bytes.Equal(got, want) {
		t.Fatalf("encodeU16LEBytes = %v, want %v", got, want)
	}
}

func TestAudioStreamReadWaitsForSamples(t *testing.T) {
	stream := &AudioStream{}
	go func() {
		time.Sleep(time.Millisecond)
		stream.enqueue([]int16{0x1234, -2})
	}()

	buf := make([]byte, 4)
	if n, err := stream.Read(buf); err != nil || n != len(buf) {
		t.Fatalf("Read = %d, %v, want %d, nil", n, err, len(buf))
	}
	if got := int16(uint16(buf[0]) | uint16(buf[1])<<8); got != 0x1234 {
		t.Fatalf("left sample = %04X, want 1234", uint16(got))
	}
	if got := int16(uint16(buf[2]) | uint16(buf[3])<<8); got != -2 {
		t.Fatalf("right sample = %d, want -2", got)
	}
}

func TestAudioStreamReadHoldsLastSampleOnUnderrun(t *testing.T) {
	stream := &AudioStream{
		lastFrame: [2]int16{-7, -9},
	}
	stream.enqueue([]int16{11})

	buf := make([]byte, 6)
	if _, err := stream.Read(buf); err != nil {
		t.Fatalf("Read: %v", err)
	}
	for i, want := range []int16{11, -9, 11} {
		got := int16(uint16(buf[i*2]) | uint16(buf[i*2+1])<<8)
		if got != want {
			t.Fatalf("sample %d = %d, want %d", i, got, want)
		}
	}
}

func TestAudioReadTimeoutIsShort(t *testing.T) {
	if audioReadTimeout > 10*time.Millisecond {
		t.Fatalf("audioReadTimeout = %v, want a short real-time wait", audioReadTimeout)
	}
}

func TestAudioStreamPrebuffersAheadOfPlayer(t *testing.T) {
	if audioStartBuffer <= audioPlayerBuffer {
		t.Fatalf("audioStartBuffer = %v, want more than player buffer %v", audioStartBuffer, audioPlayerBuffer)
	}
}
