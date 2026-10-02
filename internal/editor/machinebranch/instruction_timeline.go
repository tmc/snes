package machinebranch

import "fmt"

// InstructionTimeline counts completed compiled instructions by host-relative
// frame. It covers only replaced instructions, not all game code or pixels.
// A present frame with no sites means a measured zero; nil means unavailable.
type InstructionTimeline struct {
	Frames []InstructionFrame `json:"frames"`
	frame  int
}

// InstructionFrame contains measured executions during one RunFrame call.
type InstructionFrame struct {
	RelativeFrame int               `json:"relative_frame"`
	Sites         []InstructionSite `json:"sites"`
}

// InstructionSite identifies a physical CPU address, not a source line.
type InstructionSite struct {
	Address uint32 `json:"address"`
	Count   uint64 `json:"count"`
}

func newInstructionTimeline(frames int) *InstructionTimeline {
	t := &InstructionTimeline{Frames: make([]InstructionFrame, frames)}
	for i := range t.Frames {
		t.Frames[i].RelativeFrame = i
	}
	return t
}

func (t *InstructionTimeline) record(at uint32) error {
	if t.frame < 0 || t.frame >= len(t.Frames) || at > 0xffffff {
		return fmt.Errorf("instruction timeline outside bounded frame or address")
	}
	f := &t.Frames[t.frame]
	for i := range f.Sites {
		if f.Sites[i].Address == at {
			if f.Sites[i].Count == ^uint64(0) {
				return fmt.Errorf("instruction timeline count overflow")
			}
			f.Sites[i].Count++
			return nil
		}
	}
	if len(f.Sites) >= 14 {
		return fmt.Errorf("instruction timeline site budget exhausted")
	}
	f.Sites = append(f.Sites, InstructionSite{Address: at, Count: 1})
	return nil
}

// CheckInstructionTimeline checks bounded counts against the compiled total.
// Nil is an unavailable capture and does not imply zero executions.
func CheckInstructionTimeline(t *InstructionTimeline, frames int, total uint64) error {
	if t == nil {
		return nil
	}
	if frames < 1 || frames > 600 || len(t.Frames) != frames {
		return fmt.Errorf("instruction timeline frame bounds differ")
	}
	var count uint64
	for i, f := range t.Frames {
		if f.RelativeFrame != i || len(f.Sites) > 14 {
			return fmt.Errorf("invalid instruction timeline frame")
		}
		seen := make(map[uint32]bool)
		for _, s := range f.Sites {
			switch s.Address {
			case 0x0cc45b, 0x0cc45e, 0x0cc461, 0x0cc463, 0x0cc465, 0x0cc468, 0x0cc46b, 0x0cc46c, 0x0cc46e, 0x0cc471, 0x0cc474, 0x0cc475, 0x0cc477, 0x0cc47a:
			default:
				return fmt.Errorf("instruction timeline address outside supported region")
			}
			if seen[s.Address] || s.Count == 0 || s.Count > ^uint64(0)-count {
				return fmt.Errorf("invalid instruction timeline count")
			}
			seen[s.Address] = true
			count += s.Count
		}
	}
	if count != total {
		return fmt.Errorf("instruction timeline total differs from compiled execution")
	}
	return nil
}
