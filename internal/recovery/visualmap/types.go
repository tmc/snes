package visualmap

import (
	"github.com/tmc/snes/internal/recovery/decomp"
	"github.com/tmc/snes/internal/trace"
)

// PixelProvenance records geometric candidate selection and trace correlation connecting
// a screen coordinate to candidate OAM slices, VBLANK DMA transfers, shadow WRAM buffers,
// and writing CPU stores.
//
// Note: full rendered-pixel ownership requires compositor dot evidence (tile transparency,
// priority arbitration, OBSEL configuration, and color math) and is not proven by geometric
// selection alone.
type PixelProvenance struct {
	Query          QueryCoords         `json:"query"`
	VisualEntity   VisualEntityInfo    `json:"visual_entity"`
	DMATransfer    *DMATransferInfo    `json:"dma_transfer,omitempty"`
	CPUWrite       *CPUWriteInfo       `json:"cpu_write,omitempty"`
	CodeProvenance *CodeProvenanceInfo `json:"code_provenance,omitempty"`
}

// QueryCoords holds the input parameters for a pixel provenance query.
type QueryCoords struct {
	Frame int `json:"frame"`
	X     int `json:"x"`
	Y     int `json:"y"`
}

// PixelInfo holds the rendered visual properties of a single dot.
type PixelInfo struct {
	ColorBGR555  uint16 `json:"color_bgr555"`
	RGBHex       string `json:"rgb_hex"`
	Layer        string `json:"layer"`
	PaletteIndex uint8  `json:"palette_index"`
}

// VisualEntityInfo describes the hardware graphics entity drawn at the query coordinate.
type VisualEntityInfo struct {
	Kind        string      `json:"kind"` // "sprite", "tile", "backdrop"
	SpriteIndex int         `json:"sprite_index,omitempty"`
	BoundingBox BoundingBox `json:"bounding_box"`
	Attributes  SpriteAttrs `json:"attributes"`
	PhysicalOAM []uint8     `json:"physical_oam_bytes,omitempty"`
}

// BoundingBox defines a 2D bounding rectangle in SNES screen coordinates.
type BoundingBox struct {
	X      int `json:"x"`
	Y      int `json:"y"`
	Width  int `json:"width"`
	Height int `json:"height"`
}

// SpriteAttrs describes the hardware attributes of an OAM sprite.
type SpriteAttrs struct {
	Tile     int  `json:"tile"`
	Palette  int  `json:"palette"`
	Priority int  `json:"priority"`
	HFlip    bool `json:"hflip"`
	VFlip    bool `json:"vflip"`
	Large    bool `json:"large"`
}

// DMATransferInfo describes the VBLANK DMA transfer that uploaded the graphics slice.
type DMATransferInfo struct {
	Channel           int         `json:"channel"`
	Frame             int         `json:"frame"`
	Cycle             uint64      `json:"cycle"`
	TriggerPC         string      `json:"trigger_pc"`
	DestRegister      string      `json:"dest_register"`
	SourceRange       trace.Range `json:"source_range"`
	DestRange         trace.Range `json:"dest_range"`
	WRAMSourceAddress string      `json:"wram_source_address"`
}

// CPUWriteInfo describes the CPU instruction that wrote to the shadow WRAM buffer.
type CPUWriteInfo struct {
	Frame         int    `json:"frame"`
	Cycle         uint64 `json:"cycle"`
	PC            string `json:"pc"`
	Address       string `json:"address"`
	StoredValue   uint8  `json:"stored_value"`
	Disassembly   string `json:"disassembly,omitempty"`
	InstructionID string `json:"instruction_id,omitempty"`
}

// CodeProvenanceInfo links the write event to decompiled C and assembly source maps.
type CodeProvenanceInfo struct {
	RoutineID   string                  `json:"routine_id,omitempty"`
	RoutineName string                  `json:"routine_name,omitempty"`
	BlockID     string                  `json:"block_id"`
	SourceLine  int                     `json:"source_line"`
	Statement   string                  `json:"statement"`
	PseudoC     string                  `json:"pseudoc"`
	SourceMap   []decomp.SourceMapEntry `json:"source_map,omitempty"`
}
