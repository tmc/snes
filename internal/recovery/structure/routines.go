package structure

import (
	"fmt"
	"sort"

	"github.com/tmc/snes/internal/recovery"
)

// ExtractRoutines identifies candidate routines, their member basic blocks, callers, exits, and shared tails.
func ExtractRoutines(doc *recovery.Document) []*Routine {
	blocks := ExtractBasicBlocks(doc)
	if len(blocks) == 0 {
		return nil
	}

	blockByAddr := make(map[uint32]*BasicBlock)
	for _, b := range blocks {
		blockByAddr[b.StartAddress] = b
	}

	// Step 1: Identify routine entry addresses
	// - First block is entry candidate (e.g. reset vector)
	// - Any target of a "call" edge (JSR/JSL)
	// - Any target of an "interrupt" edge (NMI/IRQ)
	entries := make(map[uint32]bool)
	callers := make(map[uint32][]uint32)

	if len(blocks) > 0 {
		entries[blocks[0].StartAddress] = true
	}

	for _, edge := range doc.Edges {
		if edge.Kind == "call" && edge.Destination != 0 {
			entries[edge.Destination] = true
			// Find source address
			for _, inst := range doc.Instructions {
				if inst.ID == edge.Source {
					callers[edge.Destination] = append(callers[edge.Destination], inst.Address)
					break
				}
			}
		} else if edge.Kind == "interrupt" && edge.Destination != 0 {
			entries[edge.Destination] = true
		}
	}

	var entryAddrs []uint32
	for addr := range entries {
		if _, ok := blockByAddr[addr]; ok {
			entryAddrs = append(entryAddrs, addr)
		}
	}
	sort.Slice(entryAddrs, func(i, j int) bool { return entryAddrs[i] < entryAddrs[j] })

	// Step 2: Trace reachable blocks for each routine entry
	blockToRoutines := make(map[string][]string) // blockID -> routineIDs
	var routines []*Routine

	for _, entryAddr := range entryAddrs {
		entryBlock := blockByAddr[entryAddr]
		rID := fmt.Sprintf("rtn-%06x", entryAddr)
		rName := fmt.Sprintf("sub_%06x", entryAddr)
		if entryAddr == blocks[0].StartAddress {
			rName = fmt.Sprintf("reset_%06x", entryAddr)
		}

		visited := make(map[uint32]bool)
		queue := []uint32{entryAddr}
		var memberBlockIDs []string
		var exitAddrs []uint32
		contextMap := make(map[string]recovery.Context)
		insnCount := 0

		for len(queue) > 0 {
			currAddr := queue[0]
			queue = queue[1:]

			if visited[currAddr] {
				continue
			}
			visited[currAddr] = true

			b, ok := blockByAddr[currAddr]
			if !ok {
				continue
			}

			memberBlockIDs = append(memberBlockIDs, b.ID)
			blockToRoutines[b.ID] = append(blockToRoutines[b.ID], rID)
			insnCount += len(b.Instructions)

			for _, inst := range b.Instructions {
				ctxKey := fmt.Sprintf("%s:%s:%s:%s", inst.Context.E, inst.Context.M, inst.Context.X, inst.Context.C)
				contextMap[ctxKey] = inst.Context
				if isReturn(inst.Opcode) {
					exitAddrs = append(exitAddrs, inst.Address)
				}
			}

			// Traverse successors (except across call boundaries)
			for _, succAddr := range b.Successors {
				// Don't traverse into other routine entries
				if entries[succAddr] && succAddr != entryAddr {
					continue
				}
				if !visited[succAddr] {
					queue = append(queue, succAddr)
				}
			}
		}

		sort.Strings(memberBlockIDs)
		sort.Slice(exitAddrs, func(i, j int) bool { return exitAddrs[i] < exitAddrs[j] })

		var contexts []recovery.Context
		for _, ctx := range contextMap {
			contexts = append(contexts, ctx)
		}

		rtn := &Routine{
			ID:               rID,
			Name:             rName,
			EntryAddress:     entryAddr,
			EntryOffset:      entryBlock.StartOffset,
			Callers:          callers[entryAddr],
			Exits:            exitAddrs,
			BlockIDs:         memberBlockIDs,
			InstructionCount: insnCount,
			Contexts:         contexts,
		}
		routines = append(routines, rtn)
	}

	// Step 3: Identify shared tails
	for _, rtn := range routines {
		for _, bID := range rtn.BlockIDs {
			if len(blockToRoutines[bID]) > 1 {
				rtn.SharedTails = append(rtn.SharedTails, bID)
			}
		}
		sort.Strings(rtn.SharedTails)
	}

	return routines
}

func isReturn(opcode byte) bool {
	return opcode == 0x60 || opcode == 0x6B || opcode == 0x40 // RTS, RTL, RTI
}
