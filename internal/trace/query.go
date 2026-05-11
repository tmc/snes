package trace

type Query struct {
	Events []Event
}

func (q Query) Writers(r Range) []Event {
	var out []Event
	for _, e := range q.Events {
		if e.Kind == "bus" && e.Op == "write" && r.Contains(e.Space, e.Addr) {
			out = append(out, e)
		}
		if e.Kind == "watch" && r.Contains(e.Space, e.Addr) {
			out = append(out, e)
		}
	}
	return out
}

func (q Query) Readers(r Range) []Event {
	var out []Event
	for _, e := range q.Events {
		if e.Kind == "bus" && e.Op == "read" && r.Contains(e.Space, e.Addr) {
			out = append(out, e)
		}
	}
	return out
}

func (q Query) DMAForDest(r Range) []Event {
	var out []Event
	for _, e := range q.Events {
		if e.Kind == "dma" && e.Dest.Intersects(r) {
			out = append(out, e)
		}
	}
	return out
}

func (q Query) BusForPC(r Range) []Event {
	var out []Event
	for _, e := range q.Events {
		if e.PC == nil || (e.Kind != "bus" && e.Kind != "mmio" && e.Kind != "dma") {
			continue
		}
		pc := uint32(e.PC.Bank)<<16 | uint32(e.PC.Addr)
		if r.Contains("cpu", pc) {
			out = append(out, e)
		}
	}
	return out
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
