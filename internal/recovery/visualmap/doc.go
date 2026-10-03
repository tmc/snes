// Package visualmap provides a bidirectional causal indexing engine that maps
// visible screen coordinates back to PPU OAM/VRAM slices, VBLANK DMA transfers,
// shadow WRAM buffers, writing CPU instructions, and decompiled C statements.
//
// The primary entry point is [Engine], which ingests trace events and answers
// coordinate queries with [Engine.Query].
package visualmap
