package dma

import "testing"

// Semantic tests enter the same timed controller as the system. The fixture
// has no CPU thread, so its two eight-clock acquisition edges are explicit.
func runDMA(t *testing.T, d *DMA, mask uint8) {
	t.Helper()
	d.Request(mask)
	runDMAEdges(t, d)
}

func runHDMA(t *testing.T, d *DMA, setup bool) {
	t.Helper()
	d.RequestHDMA(0, setup)
	runDMAEdges(t, d)
}

func runDMAEdges(t *testing.T, d *DMA) {
	t.Helper()
	d.BeginEdge(8)
	d.BeginEdge(8)
	timedDrain(t, d)
}
