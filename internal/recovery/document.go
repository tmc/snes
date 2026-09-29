package recovery

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
)

var (
	// ErrInvalidFormat indicates the document format is unrecognized.
	ErrInvalidFormat = errors.New("invalid recovery format: expected 'snes-recovery'")
	// ErrUnsupportedSchema indicates an unsupported schema integer.
	ErrUnsupportedSchema = errors.New("unsupported schema version: only schema 1 is supported")
)

// Document is the root recovery document envelope.
type Document struct {
	Format       string        `json:"format"`
	Schema       int           `json:"schema"`
	ROM          ROMIdentity   `json:"rom"`
	Producer     ProducerInfo  `json:"producer"`
	Evidence     []Evidence    `json:"evidence"`
	Instructions []Instruction `json:"instructions"`
	Edges        []Edge        `json:"edges"`
	Objects      []Object      `json:"objects"`
	Issues       []Issue       `json:"issues"`
}

// NewDocument creates a new initialized Document for a ROM identity.
func NewDocument(identity ROMIdentity) *Document {
	return &Document{
		Format: Format,
		Schema: SchemaVersion,
		ROM:    identity,
		Producer: ProducerInfo{
			Tool:    "snesdasm",
			Version: "1.0.0",
		},
		Evidence:     []Evidence{},
		Instructions: []Instruction{},
		Edges:        []Edge{},
		Objects:      []Object{},
		Issues:       []Issue{},
	}
}

// Decode reads and validates a recovery Document from r.
func Decode(r io.Reader) (*Document, error) {
	if r == nil {
		return nil, errors.New("recovery: reader is nil")
	}
	return decodeDocument(r)
}

func decodeDocument(r io.Reader) (*Document, error) {
	var doc Document
	dec := json.NewDecoder(r)
	dec.DisallowUnknownFields()
	if err := dec.Decode(&doc); err != nil {
		return nil, fmt.Errorf("recovery: decode: %w", err)
	}
	var trailing json.RawMessage
	if err := dec.Decode(&trailing); err != io.EOF {
		return nil, fmt.Errorf("recovery: trailing JSON data after document")
	}

	if doc.Format != Format {
		return nil, fmt.Errorf("recovery: %w (got %q)", ErrInvalidFormat, doc.Format)
	}
	if doc.Schema != SchemaVersion {
		return nil, fmt.Errorf("recovery: %w (got %d)", ErrUnsupportedSchema, doc.Schema)
	}

	// Initialize slices to empty if nil for deterministic state.
	if doc.Evidence == nil {
		doc.Evidence = []Evidence{}
	}
	if doc.Instructions == nil {
		doc.Instructions = []Instruction{}
	}
	if doc.Edges == nil {
		doc.Edges = []Edge{}
	}
	if doc.Objects == nil {
		doc.Objects = []Object{}
	}
	if doc.Issues == nil {
		doc.Issues = []Issue{}
	}

	return &doc, nil
}

// Encode writes doc to w as indented JSON.
func Encode(w io.Writer, doc *Document) error {
	if w == nil {
		return errors.New("recovery: writer is nil")
	}
	if doc == nil {
		return errors.New("recovery: document is nil")
	}
	return encodeDocument(w, doc)
}

func encodeDocument(w io.Writer, doc *Document) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	if err := enc.Encode(doc); err != nil {
		return fmt.Errorf("recovery: encode: %w", err)
	}
	return nil
}
