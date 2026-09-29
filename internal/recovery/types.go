package recovery

// Format is the identifier for recovery documents.
const Format = "snes-recovery"

// SchemaVersion is the supported schema version.
const SchemaVersion = 1

// ProducerInfo identifies the tool and version generating the recovery document.
type ProducerInfo struct {
	Tool    string `json:"tool"`
	Version string `json:"version"`
}

// Evidence records supporting facts for interpretations.
type Evidence struct {
	ID      string `json:"id"`
	Kind    string `json:"kind"`
	Details string `json:"details,omitempty"`
}

// Context records 65C816 status flags (E, M, X, C) at an instruction entry.
type Context struct {
	E string `json:"e"`
	M string `json:"m"`
	X string `json:"x"`
	C string `json:"c"`
}

// Instruction represents a decoded instruction candidate.
type Instruction struct {
	ID           string   `json:"id"`
	Architecture string   `json:"architecture"`
	Address      uint32   `json:"address"`
	Offset       uint32   `json:"offset"`
	Bytes        string   `json:"bytes"`
	Opcode       byte     `json:"opcode"`
	Mnemonic     string   `json:"mnemonic"`
	Mode         string   `json:"mode"`
	Context      Context  `json:"context"`
	Evidence     []string `json:"evidence"`
}

// Edge represents a control flow edge.
type Edge struct {
	ID          string   `json:"id"`
	Kind        string   `json:"kind"`
	Source      string   `json:"source"`
	Destination uint32   `json:"destination,omitempty"`
	Evidence    []string `json:"evidence"`
}

// Object represents a contiguous or structured data/routine candidate.
type Object struct {
	ID       string   `json:"id"`
	Kind     string   `json:"kind"`
	Offset   uint32   `json:"offset"`
	Length   uint32   `json:"length"`
	Evidence []string `json:"evidence"`
}

// Issue records unresolved ambiguity or decode issues.
type Issue struct {
	ID       string `json:"id"`
	Offset   uint32 `json:"offset"`
	Address  uint32 `json:"address,omitempty"`
	Reason   string `json:"reason"`
	Blocking bool   `json:"blocking"`
}

// Annotation represents human-provided guidance or overrides.
type Annotation struct {
	ID            string `json:"id"`
	Target        string `json:"target"`
	Operation     string `json:"operation"`
	Rationale     string `json:"rationale"`
	ExpectedBytes string `json:"expected_bytes,omitempty"`
}
