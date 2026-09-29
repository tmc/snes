package recovery_test

import (
	"bytes"
	"fmt"

	"github.com/tmc/snes/internal/recovery"
)

func ExampleNewDocument() {
	ident := recovery.ROMIdentity{
		OriginalSHA256:   "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855",
		OriginalSize:     32768,
		NormalizedSHA256: "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855",
		NormalizedSize:   32768,
		Normalization:    "none",
		Mapper:           "lorom",
	}

	doc := recovery.NewDocument(ident)
	fmt.Println("Format:", doc.Format)
	fmt.Println("Schema:", doc.Schema)
	fmt.Println("Mapper:", doc.ROM.Mapper)

	// Output:
	// Format: snes-recovery
	// Schema: 1
	// Mapper: lorom
}

func ExampleEncode() {
	ident := recovery.ROMIdentity{
		OriginalSHA256:   "abc",
		OriginalSize:     32768,
		NormalizedSHA256: "abc",
		NormalizedSize:   32768,
		Normalization:    "none",
		Mapper:           "lorom",
	}
	doc := recovery.NewDocument(ident)

	var buf bytes.Buffer
	if err := recovery.Encode(&buf, doc); err != nil {
		fmt.Println("Error:", err)
		return
	}

	decoded, err := recovery.Decode(&buf)
	if err != nil {
		fmt.Println("Error:", err)
		return
	}
	fmt.Println("Decoded format:", decoded.Format)

	// Output:
	// Decoded format: snes-recovery
}
