package decomp

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
)

// ByteSymbol names one observed WRAM byte. Evidence identifies the source of
// the address assertion; it does not establish a game-level meaning or extent.
type ByteSymbol struct {
	Name     string `json:"name"`
	Address  uint32 `json:"address"`
	Evidence string `json:"evidence"`
}

// NamedRegionSource contains a complete C translation unit and the identical
// accessor text separately for use as variables.h by consumers.
type NamedRegionSource struct {
	Source     string `json:"source"`
	VariablesH string `json:"variables_h"`
}

var byteSymbolName = regexp.MustCompile(`^[A-Za-z][A-Za-z_0-9]*$`)

// staticByteAddress recognizes addresses fixed by the region's D=0 entry
// contract. It deliberately leaves indexed and data-bank accesses unnamed.
func staticByteAddress(e Expr) (uint32, bool) {
	switch address := e.(type) {
	case *ConstExpr:
		return address.Value, true
	case *BinaryExpr:
		if address.Op != OpAdd || address.Width != Width16 {
			return 0, false
		}
		if reg, ok := address.Left.(*RegExpr); ok && reg.Reg == RegD {
			if offset, ok := address.Right.(*ConstExpr); ok {
				return offset.Value & 0xffff, true
			}
		}
	}
	return 0, false
}

// GenerateNamedRegionC names constant-address byte accesses in a RegionIR.
// Runtime addresses still go through the ordinary bus helpers. Dynamic and
// word accesses retain the ordinary lowering.
func GenerateNamedRegionC(region *RegionIR, symbols []ByteSymbol) (NamedRegionSource, error) {
	if len(symbols) == 0 {
		return NamedRegionSource{}, fmt.Errorf("named region C: no byte symbols")
	}
	if region == nil {
		return NamedRegionSource{}, fmt.Errorf("named region C: nil region")
	}
	for _, block := range region.Blocks {
		if block == nil {
			return NamedRegionSource{}, fmt.Errorf("named region C: nil block")
		}
		for _, statement := range block.Statements {
			if statement.TargetReg == RegD {
				return NamedRegionSource{}, fmt.Errorf("named region C: direct page register changes within region")
			}
		}
	}
	ordered := append([]ByteSymbol(nil), symbols...)
	sort.Slice(ordered, func(i, j int) bool {
		if ordered[i].Address != ordered[j].Address {
			return ordered[i].Address < ordered[j].Address
		}
		return ordered[i].Name < ordered[j].Name
	})
	names := make(map[uint32]string, len(ordered))
	used := make(map[string]bool, len(ordered))
	var header strings.Builder
	header.WriteString("/* Byte accessors generated from observed WRAM addresses. */\n")
	for _, symbol := range ordered {
		if !byteSymbolName.MatchString(symbol.Name) || used[symbol.Name] {
			return NamedRegionSource{}, fmt.Errorf("named region C: invalid or repeated name %q", symbol.Name)
		}
		if symbol.Address < 0x7E0000 || symbol.Address > 0x7FFFFF || names[symbol.Address] != "" {
			return NamedRegionSource{}, fmt.Errorf("named region C: invalid or repeated WRAM byte $%06X", symbol.Address)
		}
		if symbol.Evidence == "" {
			return NamedRegionSource{}, fmt.Errorf("named region C: missing address evidence for %s", symbol.Name)
		}
		used[symbol.Name] = true
		names[symbol.Address] = symbol.Name
		fmt.Fprintf(&header, "static inline uint8_t %s_read(exec_result_t *res, uint32_t original_addr, mem_read_fn read_cb, void *mem_ctx) {\n    return mem_read8_raw(res, original_addr, read_cb, mem_ctx);\n}\n", symbol.Name)
		fmt.Fprintf(&header, "static inline void %s_write(exec_result_t *res, uint32_t original_addr, uint8_t value) {\n    mem_write8(res, original_addr, value);\n}\n", symbol.Name)
	}
	source, err := generateRegionCWithNames(region, nil, names, header.String())
	if err != nil {
		return NamedRegionSource{}, err
	}
	for _, symbol := range ordered {
		if strings.Count(source, symbol.Name+"_read(&res,")+strings.Count(source, symbol.Name+"_write(&res,") == 0 {
			return NamedRegionSource{}, fmt.Errorf("named region C: no supported byte access uses %s", symbol.Name)
		}
	}
	return NamedRegionSource{Source: source, VariablesH: header.String()}, nil
}
