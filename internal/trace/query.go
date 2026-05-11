package trace

type Query struct {
	Events []Event
}

func (q Query) Writers(r Range) []Event {
	return q.WritersInFrameRange(r, -1, -1)
}

func (q Query) WritersInFrameRange(r Range, startFrame, endFrame int) []Event {
	var out []Event
	for _, e := range q.Events {
		if !inFrameRange(e.Frame, startFrame, endFrame) {
			continue
		}
		if isWriteEvent(e) && r.Contains(e.Space, e.Addr) {
			out = append(out, e)
		}
		if e.Kind == "watch" && r.Contains(e.Space, e.Addr) {
			out = append(out, e)
		}
	}
	return out
}

func (q Query) ExplainWriters(r Range, startFrame, endFrame int) []Event {
	var out []Event
	for _, e := range q.Events {
		if !inFrameRange(e.Frame, startFrame, endFrame) {
			continue
		}
		if isWriteEvent(e) && r.Contains(e.Space, e.Addr) {
			out = append(out, e)
		}
		if isDMAEvent(e) && e.Dest.Intersects(r) {
			out = append(out, e)
		}
	}
	return out
}

func (q Query) LastWriterAtFrame(r Range, frame int) []Event {
	var last *Event
	for _, e := range q.ExplainWriters(r, -1, frame) {
		ev := e
		last = &ev
	}
	if last == nil {
		return nil
	}
	return []Event{*last}
}

func (q Query) Readers(r Range) []Event {
	return q.ReadersInFrameRange(r, -1, -1)
}

func (q Query) ReadersInFrameRange(r Range, startFrame, endFrame int) []Event {
	var out []Event
	for _, e := range q.Events {
		if !inFrameRange(e.Frame, startFrame, endFrame) {
			continue
		}
		if e.Kind == "bus" && e.Op == "read" && r.Contains(e.Space, e.Addr) {
			out = append(out, e)
		}
	}
	return out
}

func (q Query) DMAForDest(r Range) []Event {
	return q.DMAForDestInFrameRange(r, -1, -1)
}

func (q Query) DMAForDestInFrameRange(r Range, startFrame, endFrame int) []Event {
	var out []Event
	for _, e := range q.Events {
		if !inFrameRange(e.Frame, startFrame, endFrame) {
			continue
		}
		if isDMAEvent(e) && e.Dest.Intersects(r) {
			out = append(out, e)
		}
	}
	return out
}

func (q Query) BusForPC(r Range) []Event {
	return q.BusForPCInFrameRange(r, -1, -1)
}

func (q Query) BusForPCInFrameRange(r Range, startFrame, endFrame int) []Event {
	var out []Event
	for _, e := range q.Events {
		if !inFrameRange(e.Frame, startFrame, endFrame) {
			continue
		}
		if e.PC == nil || (e.Kind != "bus" && e.Kind != "mmio" && e.Kind != "apu" && e.Kind != "ppu" && !isDMAEvent(e)) {
			continue
		}
		pc := uint32(e.PC.Bank)<<16 | uint32(e.PC.Addr)
		if r.Contains("cpu", pc) {
			out = append(out, e)
		}
	}
	return out
}

func isDMAEvent(e Event) bool {
	return e.Kind == "dma" || e.Kind == "hdma"
}

func isWriteEvent(e Event) bool {
	return (e.Kind == "bus" || e.Kind == "ppu") && e.Op == "write"
}

func inFrameRange(frame, startFrame, endFrame int) bool {
	if startFrame >= 0 && frame < startFrame {
		return false
	}
	if endFrame >= 0 && frame > endFrame {
		return false
	}
	return true
}

func (q Query) TraceWindow(id uint64, before, after int) []Event {
	index := -1
	for i, e := range q.Events {
		if e.ID == id {
			index = i
			break
		}
	}
	if index < 0 {
		return nil
	}
	start := index - before
	if start < 0 {
		start = 0
	}
	end := index + after + 1
	if end > len(q.Events) {
		end = len(q.Events)
	}
	return q.Events[start:end]
}

func (q Query) FrameSummary(frame int) []Event {
	var out []Event
	for _, e := range q.Events {
		if e.Frame == frame && (e.Kind == "frame" || e.Kind == "watch" || e.Kind == "input") {
			out = append(out, e)
		}
	}
	return out
}
