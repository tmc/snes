package trace

import (
	"bufio"
	"fmt"
	"io"
	"strconv"
	"strings"
)

type Watch struct {
	Name  string
	Width int
	Range Range
}

func ParseWatches(r io.Reader) ([]Watch, error) {
	var watches []Watch
	sc := bufio.NewScanner(r)
	for line := 1; sc.Scan(); line++ {
		text := strings.TrimSpace(sc.Text())
		if text == "" || strings.HasPrefix(text, "#") {
			continue
		}
		name, expr, ok := strings.Cut(text, "=")
		if !ok {
			return nil, fmt.Errorf("line %d: missing =", line)
		}
		name = strings.TrimSpace(name)
		expr = strings.TrimSpace(expr)
		open := strings.IndexByte(expr, '(')
		close := strings.LastIndexByte(expr, ')')
		if open < 0 || close <= open {
			return nil, fmt.Errorf("line %d: watch must look like u8(addr) or u16(addr)", line)
		}
		widthText := expr[:open]
		widthBits, err := strconv.Atoi(strings.TrimPrefix(widthText, "u"))
		if err != nil || (widthBits != 8 && widthBits != 16) {
			return nil, fmt.Errorf("line %d: unsupported width %q", line, widthText)
		}
		addr, err := ParseAddress(expr[open+1 : close])
		if err != nil {
			return nil, fmt.Errorf("line %d: %w", line, err)
		}
		watches = append(watches, Watch{
			Name:  name,
			Width: widthBits / 8,
			Range: Range{Space: addr.Space, Start: addr.Addr, End: addr.Addr + uint32(widthBits/8) - 1},
		})
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	return watches, nil
}
