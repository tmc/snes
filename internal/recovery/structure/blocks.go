package structure

import (
	"fmt"
	"sort"

	"github.com/tmc/snes/internal/recovery"
)

// ExtractBasicBlocks analyzes doc and partitions instructions into contiguous basic blocks.
func ExtractBasicBlocks(doc *recovery.Document) []*BasicBlock {
	if doc == nil || len(doc.Instructions) == 0 {
		return nil
	}

	insnByAddr := make(map[uint32]recovery.Instruction, len(doc.Instructions))
	addrs := make([]uint32, 0, len(doc.Instructions))
	for _, inst := range doc.Instructions {
		insnByAddr[inst.Address] = inst
		addrs = append(addrs, inst.Address)
	}
	sort.Slice(addrs, func(i, j int) bool { return addrs[i] < addrs[j] })

	// Step 1: Identify leader addresses
	leaders := make(map[uint32]bool)
	if len(addrs) > 0 {
		leaders[addrs[0]] = true
	}

	// Any destination of an edge is a leader
	for _, edge := range doc.Edges {
		if edge.Destination != 0 {
			if _, ok := insnByAddr[edge.Destination]; ok {
				leaders[edge.Destination] = true
			}
		}
	}

	// Any instruction following a jump, branch, call, or return is a leader
	for i, addr := range addrs {
		inst := insnByAddr[addr]
		if isBlockTerminator(inst.Opcode) && i+1 < len(addrs) {
			leaders[addrs[i+1]] = true
		}
	}

	// Step 2: Assemble blocks
	var blocks []*BasicBlock
	var currentBlock *BasicBlock

	for _, addr := range addrs {
		inst := insnByAddr[addr]
		if leaders[addr] || currentBlock == nil {
			if currentBlock != nil && len(currentBlock.Instructions) > 0 {
				currentBlock.EndAddress = currentBlock.Instructions[len(currentBlock.Instructions)-1].Address
				currentBlock.EndOffset = currentBlock.Instructions[len(currentBlock.Instructions)-1].Offset
				blocks = append(blocks, currentBlock)
			}
			currentBlock = &BasicBlock{
				ID:           fmt.Sprintf("bb-%06x", addr),
				StartAddress: addr,
				StartOffset:  inst.Offset,
				Instructions: []recovery.Instruction{inst},
			}
		} else {
			currentBlock.Instructions = append(currentBlock.Instructions, inst)
		}

		if isBlockTerminator(inst.Opcode) {
			currentBlock.EndAddress = inst.Address
			currentBlock.EndOffset = inst.Offset
			blocks = append(blocks, currentBlock)
			currentBlock = nil
		}
	}

	if currentBlock != nil && len(currentBlock.Instructions) > 0 {
		currentBlock.EndAddress = currentBlock.Instructions[len(currentBlock.Instructions)-1].Address
		currentBlock.EndOffset = currentBlock.Instructions[len(currentBlock.Instructions)-1].Offset
		blocks = append(blocks, currentBlock)
	}

	// Step 3: Link successors and predecessors from doc.Edges
	blockByAddr := make(map[uint32]*BasicBlock)
	for _, b := range blocks {
		blockByAddr[b.StartAddress] = b
	}

	for _, b := range blocks {
		if len(b.Instructions) == 0 {
			continue
		}
		lastInst := b.Instructions[len(b.Instructions)-1]
		for _, edge := range doc.Edges {
			if edge.Source == lastInst.ID && edge.Destination != 0 {
				b.Successors = append(b.Successors, edge.Destination)
				if targetBlock, ok := blockByAddr[edge.Destination]; ok {
					targetBlock.Predecessors = append(targetBlock.Predecessors, b.StartAddress)
				}
			}
		}
	}

	return blocks
}

func isBlockTerminator(opcode byte) bool {
	switch opcode {
	case 0x4C, 0x5C, 0x6C, 0x7C, 0xDC, // JMP, JML
		0x20, 0x22, // JSR, JSL
		0x80, 0x82, // BRA, BRL
		0x10, 0x30, 0x50, 0x70, 0x90, 0xB0, 0xD0, 0xF0, // conditional branches
		0x60, 0x6B, 0x40, // RTS, RTL, RTI
		0xDB, 0xCB:       // STP, WAI
		return true
	default:
		return false
	}
}
