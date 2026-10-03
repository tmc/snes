package server

import (
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strconv"

	prov "github.com/tmc/snes/internal/provenance"
	"github.com/tmc/snes/internal/trace"
)

// Window returns the configured observation window.
func (p *Provenance) Window() *prov.Window {
	if p == nil {
		return nil
	}
	if p.ObservationWindow != nil {
		return p.ObservationWindow
	}
	if p.server != nil {
		return p.server.ObservationWindow
	}
	return nil
}

// WindowPin returns the canonical observation window pin.
func (p *Provenance) WindowPin() string {
	if p == nil {
		return ""
	}
	if p.ObservationWindowPin != "" {
		return p.ObservationWindowPin
	}
	if p.server != nil {
		return p.server.ObservationWindowPin
	}
	return ""
}

// WindowFileSHA returns the raw observation window file SHA.
func (p *Provenance) WindowFileSHA() string {
	if p == nil {
		return ""
	}
	if p.ObservationWindowFileSHA != "" {
		return p.ObservationWindowFileSHA
	}
	if p.server != nil {
		return p.server.ObservationWindowFileSHA
	}
	return ""
}

// OccurrencesIndex returns the occurrence index.
func (p *Provenance) OccurrencesIndex() *OccurrenceIndex {
	if p == nil {
		return nil
	}
	if p.Occurrences != nil {
		return p.Occurrences
	}
	if p.server != nil {
		return p.server.Occurrences
	}
	return nil
}

// ByteIntervalReport represents the API response for /api/provenance/byte-interval.
type ByteIntervalReport struct {
	Status               string                     `json:"status"` // "available" or "unavailable"
	Reason               string                     `json:"reason,omitempty"`
	RawWindowFileSHA256  string                     `json:"raw_window_file_sha256,omitempty"`
	WindowSHA256         string                     `json:"window_sha256,omitempty"`
	StreamSHA256         string                     `json:"stream_sha256,omitempty"`
	Schema               string                     `json:"schema,omitempty"`
	PhysicalAddress      uint32                     `json:"physical_address,omitempty"`
	PhysicalAddressHex   string                     `json:"physical_address_hex,omitempty"`
	Value                uint8                      `json:"value,omitempty"`
	InitialStore         *prov.IntervalTransaction  `json:"initial_store,omitempty"`
	Readers              []prov.IntervalTransaction `json:"readers,omitempty"`
	Replacement          *prov.IntervalTransaction  `json:"replacement,omitempty"`
	Termination          string                     `json:"termination,omitempty"`
	HostFrames           *prov.FrameSpan            `json:"host_frames,omitempty"`
	HostFrameOffset      int                        `json:"host_frame_offset"`
	PPUFrames            *prov.FrameSpan            `json:"ppu_frames,omitempty"`
	Cycles               *prov.CycleSpan            `json:"cycles,omitempty"`
	CapturedProofEligible bool                      `json:"captured_proof_eligible"`
	CorrespondenceStatus string                     `json:"correspondence_status,omitempty"`
	Interval             *prov.ByteInterval         `json:"interval,omitempty"`
	Limitations          []string                   `json:"limitations,omitempty"`
}

// IntervalBridge handles HTTP inspection requests for byte intervals.
type IntervalBridge struct {
	prov *Provenance
}

// NewIntervalBridge creates a new IntervalBridge.
func NewIntervalBridge(p *Provenance) *IntervalBridge {
	return &IntervalBridge{prov: p}
}

// RegisterIntervalBridgeRoutes registers HTTP routes for byte interval correspondence.
func RegisterIntervalBridgeRoutes(mux *http.ServeMux, p *Provenance) {
	bridge := NewIntervalBridge(p)
	mux.HandleFunc("/api/provenance/byte-interval", bridge.HandleByteInterval)
}

// HandleByteInterval handles GET /api/provenance/byte-interval.
func (b *IntervalBridge) HandleByteInterval(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	if r.Method != http.MethodGet {
		w.WriteHeader(http.StatusMethodNotAllowed)
		json.NewEncoder(w).Encode(ByteIntervalReport{
			Status: "unavailable",
			Reason: "method not allowed",
		})
		return
	}

	win := b.prov.Window()
	pin := b.prov.WindowPin()
	rawSHA := b.prov.WindowFileSHA()
	occ := b.prov.OccurrencesIndex()

	streamSHA := ""
	if occ != nil {
		streamSHA = occ.StreamSHA256
	}

	if win == nil {
		reason := "no observation window configured"
		if b.prov != nil && b.prov.server != nil && b.prov.server.ObservationWindowLoadReason != "" {
			reason = b.prov.server.ObservationWindowLoadReason
		}
		json.NewEncoder(w).Encode(ByteIntervalReport{
			Status: "unavailable",
			Reason: reason,
		})
		return
	}

	writerParam := r.URL.Query().Get("writer_id")
	addrParam := r.URL.Query().Get("addr")

	var writerID uint64
	var hasWriterID bool

	if writerParam != "" {
		id, err := strconv.ParseUint(writerParam, 10, 64)
		if err != nil {
			w.WriteHeader(http.StatusBadRequest)
			json.NewEncoder(w).Encode(ByteIntervalReport{
				Status:              "unavailable",
				Reason:              fmt.Sprintf("invalid writer_id %q: %v", writerParam, err),
				RawWindowFileSHA256: rawSHA,
				WindowSHA256:        pin,
				StreamSHA256:        streamSHA,
			})
			return
		}
		writerID = id
		hasWriterID = true
	}

	var expectedAddr uint32
	var hasExpectedAddr bool
	if addrParam != "" {
		a, err := parseAddress(addrParam)
		if err != nil {
			w.WriteHeader(http.StatusBadRequest)
			json.NewEncoder(w).Encode(ByteIntervalReport{
				Status:              "unavailable",
				Reason:              fmt.Sprintf("invalid addr %q: %v", addrParam, err),
				RawWindowFileSHA256: rawSHA,
				WindowSHA256:        pin,
				StreamSHA256:        streamSHA,
			})
			return
		}
		expectedAddr = a
		hasExpectedAddr = true
	}

	if !hasWriterID && hasExpectedAddr {
		// Find first write to this address
		found := false
		for i, ev := range win.Events {
			if ev.Op == "write" && (ev.Actor == "cpu" || ev.Actor == "dma_or_hdma") {
				normAddr := ev.Addr
				if ev.Kind == "wram_port" {
					normAddr = 0x7E0000 + ev.Addr
				} else if ev.Kind == "bus" {
					space, off := trace.CPUSpace(ev.Addr)
					if space == "wram" {
						normAddr = 0x7E0000 + off
					}
				}
				if normAddr == expectedAddr {
					writerID = uint64(i)
					hasWriterID = true
					found = true
					break
				}
			}
		}
		if !found {
			json.NewEncoder(w).Encode(ByteIntervalReport{
				Status:              "unavailable",
				Reason:              fmt.Sprintf("no attributed write found for address $%06X", expectedAddr),
				RawWindowFileSHA256: rawSHA,
				WindowSHA256:        pin,
				StreamSHA256:        streamSHA,
			})
			return
		}
	} else if !hasWriterID {
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(ByteIntervalReport{
			Status:              "unavailable",
			Reason:              "writer_id query parameter is required (e.g. ?writer_id=21601)",
			RawWindowFileSHA256: rawSHA,
			WindowSHA256:        pin,
			StreamSHA256:        streamSHA,
		})
		return
	}

	// Verify addr matches if both are provided
	if hasExpectedAddr && writerID < uint64(len(win.Events)) {
		ev := win.Events[writerID]
		normAddr := ev.Addr
		if ev.Kind == "wram_port" {
			normAddr = 0x7E0000 + ev.Addr
		} else if ev.Kind == "bus" {
			space, off := trace.CPUSpace(ev.Addr)
			if space == "wram" {
				normAddr = 0x7E0000 + off
			}
		}
		if normAddr != expectedAddr {
			w.WriteHeader(http.StatusBadRequest)
			json.NewEncoder(w).Encode(ByteIntervalReport{
				Status:              "unavailable",
				Reason:              fmt.Sprintf("writer event %d address $%06X does not match requested address $%06X", writerID, normAddr, expectedAddr),
				RawWindowFileSHA256: rawSHA,
				WindowSHA256:        pin,
				StreamSHA256:        streamSHA,
			})
			return
		}
	}

	correlator := b.prov.Correlator
	if correlator == nil && occ != nil {
		correlator = occ.Correlator()
	}

	interval, err := prov.BuildByteInterval(*win, pin, writerID, correlator)
	if err != nil {
		json.NewEncoder(w).Encode(ByteIntervalReport{
			Status:              "unavailable",
			Reason:              err.Error(),
			RawWindowFileSHA256: rawSHA,
			WindowSHA256:        pin,
			StreamSHA256:        streamSHA,
		})
		return
	}

	rep := ByteIntervalReport{
		Status:               "available",
		RawWindowFileSHA256:  rawSHA,
		WindowSHA256:         pin,
		StreamSHA256:         streamSHA,
		Schema:               interval.Schema,
		PhysicalAddress:      interval.PhysicalAddress,
		PhysicalAddressHex:   interval.PhysicalAddressHex,
		Value:                interval.Value,
		InitialStore:         &interval.InitialStore,
		Readers:              interval.Readers,
		Replacement:          interval.Replacement,
		Termination:          interval.Termination,
		HostFrames:           &interval.HostFrames,
		HostFrameOffset:      interval.HostFrameOffset,
		PPUFrames:            &interval.PPUFrames,
		Cycles:               &interval.Cycles,
		CapturedProofEligible: interval.CapturedProofEligible,
		CorrespondenceStatus: interval.CorrespondenceStatus,
		Interval:             &interval,
		Limitations:          interval.Limitations,
	}
	json.NewEncoder(w).Encode(rep)
}

// Correlator builds or returns an OccurrenceCorrelator matching bus events to CPU retirements.
func (idx *OccurrenceIndex) Correlator() prov.OccurrenceCorrelator {
	if idx == nil {
		return nil
	}
	if len(idx.retainedEvents) > 0 {
		events := make([]trace.Event, 0, len(idx.retainedEvents))
		for _, ev := range idx.retainedEvents {
			events = append(events, ev)
		}
		sort.Slice(events, func(i, j int) bool {
			return events[i].ID < events[j].ID
		})
		base := prov.TraceCorrelatorFromEvents(events)
		return &indexCorrelator{
			base:    base,
			reports: idx.reports,
		}
	}
	if len(idx.reports) > 0 {
		return &indexCorrelator{
			reports: idx.reports,
		}
	}
	return nil
}

type indexCorrelator struct {
	base    prov.OccurrenceCorrelator
	reports []*OccurrenceReport
}

func (c *indexCorrelator) CorrelateBus(cycle uint64, addr uint32, op string, val uint8) (*prov.RetirementCorrespondence, bool) {
	if c.base != nil {
		if res, ok := c.base.CorrelateBus(cycle, addr, op, val); ok && res != nil {
			return res, true
		}
	}
	for _, rep := range c.reports {
		if rep == nil || rep.OperandBus == nil {
			continue
		}
		ob := rep.OperandBus
		if ob.Cycle == cycle && ob.Op == op && ob.Value == val {
			wramAddr := ob.Address
			if ob.Space == "wram" && wramAddr < 0x20000 {
				wramAddr = 0x7E0000 + wramAddr
			}
			if addr == wramAddr || addr == ob.Address {
				return &prov.RetirementCorrespondence{
					TraceBusID:         ob.ID,
					RetirementID:       rep.RetirementID,
					Seq:                rep.Seq,
					Instruction:        rep.Instruction,
					InstructionID:      rep.InstructionID,
					InstructionAddress: rep.Address,
					InstructionCycles: prov.CycleSpan{
						Start: rep.Cycles.Entry,
						End:   rep.Cycles.Exit,
					},
					RegisterChanges: rep.Changes,
					Status:          "correlated_occurrence_report",
				}, true
			}
		}
	}
	return nil, false
}
