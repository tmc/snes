package libretro

import (
	"fmt"
	"os"
	"unsafe"

	"github.com/ebitengine/purego"
)

// Logger interface for redirecing output
type Logger interface {
	Logf(format string, args ...any)
}

// RetroGameInfo matches retro_game_info C struct (simplified)
type RetroGameInfo struct {
	Path *byte
	Data *byte
	Size uint32
	Meta *byte
}

// RetroSystemInfo matches retro_system_info
type RetroSystemInfo struct {
	LibraryName     *byte
	LibraryVersion  *byte
	ValidExtensions *byte
	NeedFullpath    bool
	BlockExtract    bool
}

// RetroSystemAvInfo matches retro_system_av_info (simplified)
type RetroSystemAvInfo struct {
	Geometry RetroGameGeometry
	Timing   RetroSystemTiming
}

type RetroGameGeometry struct {
	BaseWidth   uint32
	BaseHeight  uint32
	MaxWidth    uint32
	MaxHeight   uint32
	AspectRatio float32
}

type RetroSystemTiming struct {
	Fps        float64
	SampleRate float64
}

// EnvironmentCallback is the go callback passed to compiled core
type EnvironmentCallback func(cmd uint32, data unsafe.Pointer) bool

// Bridge wraps the Libretro dynamic library
type Bridge struct {
	lib    uintptr
	Logger Logger

	// Core API
	retroInit                func()
	retroDeinit              func()
	retroApiVersion          func() int
	retroGetSystemInfo       func(info *RetroSystemInfo)
	retroGetSystemAvInfo     func(info unsafe.Pointer)
	retroSetEnvironment      func(cb uintptr)
	retroSetVideoRefresh     func(cb uintptr)
	retroSetAudioSample      func(cb uintptr)
	retroSetAudioSampleBatch func(cb uintptr)
	retroSetInputPoll        func(cb uintptr)
	retroSetInputState       func(cb uintptr)
	retroLoadGame            func(game *RetroGameInfo) bool
	retroRun                 func()
	retroReset               func()
	retroUnloadGame          func()
	retroGetMemoryData       func(id uint32) unsafe.Pointer
	retroGetMemorySize       func(id uint32) uint64

	// Video Output
	Frame       []byte
	FrameWidth  uint32
	FrameHeight uint32
	FramePitch  uint32

	audioSamples []int16
	inputState   map[uint64]int16
	inputPolls   uint64
}

func New(libPath string) (*Bridge, error) {
	lib, err := purego.Dlopen(libPath, purego.RTLD_NOW|purego.RTLD_GLOBAL)
	if err != nil {
		return nil, fmt.Errorf("failed to load library: %w", err)
	}

	p := &Bridge{
		lib:        lib,
		inputState: make(map[uint64]int16),
	}

	purego.RegisterLibFunc(&p.retroInit, lib, "retro_init")
	purego.RegisterLibFunc(&p.retroDeinit, lib, "retro_deinit")
	purego.RegisterLibFunc(&p.retroApiVersion, lib, "retro_api_version")
	purego.RegisterLibFunc(&p.retroGetSystemInfo, lib, "retro_get_system_info")
	purego.RegisterLibFunc(&p.retroGetSystemAvInfo, lib, "retro_get_system_av_info")
	purego.RegisterLibFunc(&p.retroSetEnvironment, lib, "retro_set_environment")
	purego.RegisterLibFunc(&p.retroSetVideoRefresh, lib, "retro_set_video_refresh")
	purego.RegisterLibFunc(&p.retroSetAudioSample, lib, "retro_set_audio_sample")
	purego.RegisterLibFunc(&p.retroSetAudioSampleBatch, lib, "retro_set_audio_sample_batch")
	purego.RegisterLibFunc(&p.retroSetInputPoll, lib, "retro_set_input_poll")
	purego.RegisterLibFunc(&p.retroSetInputState, lib, "retro_set_input_state")
	purego.RegisterLibFunc(&p.retroLoadGame, lib, "retro_load_game")
	purego.RegisterLibFunc(&p.retroRun, lib, "retro_run")
	purego.RegisterLibFunc(&p.retroRun, lib, "retro_run")
	purego.RegisterLibFunc(&p.retroReset, lib, "retro_reset")
	purego.RegisterLibFunc(&p.retroUnloadGame, lib, "retro_unload_game")
	purego.RegisterLibFunc(&p.retroGetMemoryData, lib, "retro_get_memory_data")
	purego.RegisterLibFunc(&p.retroGetMemorySize, lib, "retro_get_memory_size")

	// Set callbacks
	p.retroSetEnvironment(purego.NewCallback(func(cmd uint32, data unsafe.Pointer) bool {
		// p.logf("retro_set_environment: cmd=%d\n", cmd)
		switch cmd {
		case 10: // RETRO_ENVIRONMENT_SET_PIXEL_FORMAT
			if data != nil {
				fmt := *(*uint32)(data)
				// 0=0RGB1555, 1=XRGB8888, 2=RGB565
				// fmt.Printf("RETRO_ENVIRONMENT_SET_PIXEL_FORMAT: %d\n", fmt)
				_ = fmt
			}
			return true
		case 9: // RETRO_ENVIRONMENT_GET_SYSTEM_DIRECTORY
			// Core asks for system directory. Returning false usually means "not set".
			// Some cores might fail, others fallback.
			return false
		case 27: // RETRO_ENVIRONMENT_GET_LOG_INTERFACE
			return false
		}
		return false
	}))
	p.retroSetVideoRefresh(purego.NewCallback(func(data unsafe.Pointer, width, height uint32, pitch uint32) {
		// fmt.Printf("VideoRefresh: %dx%d pitch=%d data=%v\n", width, height, pitch, data)
		if data == nil {
			return
		}
		// Capture frame
		// Size = height * pitch (in bytes)
		size := int(height * pitch)

		// Safety check
		if size <= 0 {
			return
		}

		// Copy data to Go slice
		// We can't use unsafe.Slice directly without import,
		// but we can cast to *[1 << 30]byte
		pixels := (*[1 << 30]byte)(data)[:size:size]

		// Store in Bridge (p)
		// Check if buffer needs resize
		if len(p.Frame) != size {
			p.Frame = make([]byte, size)
		}
		copy(p.Frame, pixels)
		p.FrameWidth = width
		p.FrameHeight = height
		p.FramePitch = pitch
	}))
	p.retroSetAudioSample(purego.NewCallback(func(left, right int16) {
		p.audioSamples = append(p.audioSamples, left, right)
	}))
	p.retroSetAudioSampleBatch(purego.NewCallback(func(data unsafe.Pointer, frames uint32) uint32 {
		if data == nil || frames == 0 {
			return 0
		}
		samples := (*[1 << 30]int16)(data)[: frames*2 : frames*2]
		p.audioSamples = append(p.audioSamples, samples...)
		return frames
	}))
	p.retroSetInputPoll(purego.NewCallback(func() {
		p.inputPolls++
	}))
	p.retroSetInputState(purego.NewCallback(func(port, device, index, id uint32) int16 {
		return p.inputState[inputKey(port, device, index, id)]
	}))

	return p, nil
}

func (p *Bridge) Init() {
	p.retroInit()
}

func (p *Bridge) LoadGame(path string) bool {
	data, err := os.ReadFile(path)
	if err != nil {
		p.logf("Failed to read ROM: %v\n", err)
		return false
	}

	pathBytes := append([]byte(path), 0)

	// Allocate C memory? PureGo might handle pointer to Go slice if we are careful about lifetime.
	// RetroGameInfo struct fields are pointers.
	// We strictly need to keep 'data' alive during the call.

	info := RetroGameInfo{
		Path: &pathBytes[0],
		Data: &data[0],
		Size: uint32(len(data)),
	}

	return p.retroLoadGame(&info)
}

func (p *Bridge) Run() {
	p.retroRun()
}

func (p *Bridge) PeekMemory(id uint32, offset uint32) uint8 {
	ptr := p.retroGetMemoryData(id)
	size := p.retroGetMemorySize(id)

	if ptr == nil || uint64(offset) >= size {
		return 0
	}
	data := (*[1 << 30]uint8)(ptr)
	return data[offset]
}

func (p *Bridge) PeekRAM(offset uint32) uint8 {
	return p.PeekMemory(2, offset)
}

func (p *Bridge) PeekWRAM(offset uint32) uint8 {
	return p.PeekMemory(2, offset)
}

func (p *Bridge) PeekVRAM(offset uint32) uint8 {
	return p.PeekMemory(3, offset)
}

func (p *Bridge) PeekCGRAM(offset uint32) uint8 {
	return p.PeekMemory(4, offset)
}

func (p *Bridge) GetMemorySize(id uint32) uint64 {
	return p.retroGetMemorySize(id)
}

func inputKey(port, device, index, id uint32) uint64 {
	return uint64(port)<<48 | uint64(device)<<32 | uint64(index)<<16 | uint64(id)
}

// SetInputState updates the polled state returned by the input callback.
func (p *Bridge) SetInputState(port, device, index, id uint32, value int16) {
	p.inputState[inputKey(port, device, index, id)] = value
}

// InputPolls returns how many times the input poll callback was invoked.
func (p *Bridge) InputPolls() uint64 {
	return p.inputPolls
}

// DrainAudio copies captured callback samples into dst and returns copied samples.
func (p *Bridge) DrainAudio(dst []int16) int {
	n := copy(dst, p.audioSamples)
	copy(p.audioSamples, p.audioSamples[n:])
	p.audioSamples = p.audioSamples[:len(p.audioSamples)-n]
	return n
}

func (p *Bridge) logf(format string, args ...any) {
	if p.Logger != nil {
		p.Logger.Logf(format, args...)
	} else {
		fmt.Printf(format, args...)
	}
}
