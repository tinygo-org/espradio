// Fix Xtensa relocations for LLD compatibility.
//
// Two issues are addressed:
//
//  1. LLD ignores R_XTENSA_ASM_EXPAND relocations, which carry the correct
//     addend for call targets to static (file-scope) functions. The paired
//     R_XTENSA_32 relocation on the literal pool entry has addend=0, causing
//     LLD to fill the literal with the section start instead of the function
//     entry point. This tool propagates the addend from R_XTENSA_ASM_EXPAND
//     to the corresponding R_XTENSA_32 literal pool entry.
//
//  2. The Xtensa GCC assembler stores addends in-place in section data even
//     for RELA relocations (where r_addend should carry the addend). LLD
//     follows standard RELA convention and ignores in-place values, causing
//     jump tables and other section-relative R_XTENSA_32 relocations to
//     resolve to the section base instead of the correct target. This tool
//     moves in-place addends into the RELA r_addend field.
//
// Usage:
//
//	go run fix_xtensa_literals.go [-n] blobs/libs/esp32s3/*.a
//
//go:build ignore

package main

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// ELF constants
const (
	etREL      = 1
	emXtensa   = 94
	shtRELA    = 4
	shtSYMTAB  = 2
	sttSECTION = 3
)

// Xtensa relocation types
const (
	rXtensa32        = 1
	rXtensaASMExpand = 11
	rXtensaSLOT0OP   = 20
)

func readU16(data []byte, off int) uint16 {
	return binary.LittleEndian.Uint16(data[off:])
}

func readU32(data []byte, off int) uint32 {
	return binary.LittleEndian.Uint32(data[off:])
}

func readI32(data []byte, off int) int32 {
	return int32(binary.LittleEndian.Uint32(data[off:]))
}

func writeI32(data []byte, off int, val int32) {
	binary.LittleEndian.PutUint32(data[off:], uint32(val))
}

func getString(data []byte, strtabOff, idx int) string {
	start := strtabOff + idx
	end := bytes.IndexByte(data[start:], 0)
	if end < 0 {
		return ""
	}
	return string(data[start : start+end])
}

type section struct {
	nameIdx   uint32
	typ       uint32
	flags     uint32
	addr      uint32
	offset    uint32
	size      uint32
	link      uint32
	info      uint32
	addralign uint32
	entsize   uint32
	index     int
	name      string
}

type relaEntry struct {
	fileOff int
	rOffset uint32
	rType   uint8
	rSym    uint32
	rAddend int32
	index   int
}

func fixObject(data []byte) ([]byte, int) {
	data = append([]byte(nil), data...) // copy

	if len(data) < 52 || string(data[0:4]) != "\x7fELF" {
		return data, 0
	}
	if data[4] != 1 { // ELF32
		return data, 0
	}
	if readU16(data, 16) != etREL || readU16(data, 18) != emXtensa {
		return data, 0
	}

	shOff := int(readU32(data, 32))
	shEntSize := int(readU16(data, 46))
	shNum := int(readU16(data, 48))
	shStrNdx := int(readU16(data, 50))

	sections := make([]section, shNum)
	for i := 0; i < shNum; i++ {
		off := shOff + i*shEntSize
		sections[i] = section{
			nameIdx:   readU32(data, off),
			typ:       readU32(data, off+4),
			flags:     readU32(data, off+8),
			addr:      readU32(data, off+12),
			offset:    readU32(data, off+16),
			size:      readU32(data, off+20),
			link:      readU32(data, off+24),
			info:      readU32(data, off+28),
			addralign: readU32(data, off+32),
			entsize:   readU32(data, off+36),
			index:     i,
		}
	}

	// Section name string table
	shstrtabOff := int(sections[shStrNdx].offset)
	for i := range sections {
		sections[i].name = getString(data, shstrtabOff, int(sections[i].nameIdx))
	}

	totalFixes := 0

	// --- Pass 1: Propagate ASM_EXPAND addends to literal pool R_XTENSA_32 ---

	symSecIdx := func(symtabOff, symtabEntSize int, symIdx uint32) uint16 {
		off := symtabOff + int(symIdx)*symtabEntSize
		return readU16(data, off+14)
	}

	for _, relaSec := range sections {
		if relaSec.typ != shtRELA {
			continue
		}

		relaOff := int(relaSec.offset)
		relaSize := int(relaSec.size)
		relaEntSize := int(relaSec.entsize)
		if relaEntSize == 0 {
			relaEntSize = 12
		}
		numEntries := relaSize / relaEntSize

		symtabSec := sections[relaSec.link]
		symtabOff := int(symtabSec.offset)
		symtabEntSize := int(symtabSec.entsize)
		if symtabEntSize == 0 {
			symtabEntSize = 16
		}

		entries := make([]relaEntry, numEntries)
		for i := 0; i < numEntries; i++ {
			entOff := relaOff + i*relaEntSize
			rInfo := readU32(data, entOff+4)
			entries[i] = relaEntry{
				fileOff: entOff,
				rOffset: readU32(data, entOff),
				rType:   uint8(rInfo & 0xFF),
				rSym:    rInfo >> 8,
				rAddend: readI32(data, entOff+8),
				index:   i,
			}
		}

		// Index: offset -> entries at that offset
		byOffset := map[uint32][]int{}
		for i, e := range entries {
			byOffset[e.rOffset] = append(byOffset[e.rOffset], i)
		}

		// Index: offset -> R_XTENSA_32 entries
		r32ByOffset := map[uint32][]int{}
		for i, e := range entries {
			if e.rType == rXtensa32 {
				r32ByOffset[e.rOffset] = append(r32ByOffset[e.rOffset], i)
			}
		}

		for _, e := range entries {
			if e.rType != rXtensaASMExpand || e.rAddend == 0 {
				continue
			}

			asmTargetSec := symSecIdx(symtabOff, symtabEntSize, e.rSym)
			if int(asmTargetSec) == 0 || int(asmTargetSec) >= len(sections) {
				continue
			}
			if !strings.HasPrefix(sections[asmTargetSec].name, ".text.") {
				continue
			}

			// Find SLOT0_OP at the same offset
			var slot0 *relaEntry
			for _, idx := range byOffset[e.rOffset] {
				if entries[idx].rType == rXtensaSLOT0OP {
					slot0 = &entries[idx]
					break
				}
			}
			if slot0 == nil {
				continue
			}

			literalOffset := uint32(slot0.rAddend)

			// Find R_XTENSA_32 at the literal offset targeting same section
			for _, idx := range r32ByOffset[literalOffset] {
				r32 := &entries[idx]
				r32Target := symSecIdx(symtabOff, symtabEntSize, r32.rSym)
				if r32Target == asmTargetSec && r32.rAddend == 0 {
					writeI32(data, r32.fileOff+8, e.rAddend)
					r32.rAddend = e.rAddend

					// Clear in-place literal value
					litSecIdx := int(relaSec.info)
					litSec := sections[litSecIdx]
					inplaceOff := int(litSec.offset) + int(r32.rOffset)
					if int(r32.rOffset)+4 <= int(litSec.size) {
						writeI32(data, inplaceOff, 0)
					}
					totalFixes++
					break
				}
			}
		}
	}

	// --- Pass 2: Move in-place addends into RELA r_addend for R_XTENSA_32 ---

	for _, relaSec := range sections {
		if relaSec.typ != shtRELA {
			continue
		}

		relaOff := int(relaSec.offset)
		relaSize := int(relaSec.size)
		relaEntSize := int(relaSec.entsize)
		if relaEntSize == 0 {
			relaEntSize = 12
		}
		numEntries := relaSize / relaEntSize

		targetSecIdx := int(relaSec.info)
		if targetSecIdx == 0 || targetSecIdx >= len(sections) {
			continue
		}
		targetSec := sections[targetSecIdx]
		targetDataOff := int(targetSec.offset)
		targetDataSize := int(targetSec.size)

		symtabSec := sections[relaSec.link]
		symtabOff := int(symtabSec.offset)
		symtabEntSize := int(symtabSec.entsize)
		if symtabEntSize == 0 {
			symtabEntSize = 16
		}

		for i := 0; i < numEntries; i++ {
			entOff := relaOff + i*relaEntSize
			rOffset := readU32(data, entOff)
			rInfo := readU32(data, entOff+4)
			rAddend := readI32(data, entOff+8)
			rType := uint8(rInfo & 0xFF)
			rSym := rInfo >> 8

			if rType != rXtensa32 {
				continue
			}

			if int(rOffset)+4 > targetDataSize {
				continue
			}

			inplaceVal := readI32(data, targetDataOff+int(rOffset))
			if inplaceVal == 0 {
				continue
			}

			// Check if symbol is a section symbol (STT_SECTION)
			symOff := symtabOff + int(rSym)*symtabEntSize
			stInfo := data[symOff+12]
			stType := stInfo & 0xf
			if stType != sttSECTION {
				continue
			}

			newAddend := rAddend + inplaceVal
			writeI32(data, entOff+8, newAddend)
			writeI32(data, targetDataOff+int(rOffset), 0)
			totalFixes++
		}
	}

	return data, totalFixes
}

// ar archive parsing

func processArchive(archivePath string, dryRun bool) (int, error) {
	f, err := os.Open(archivePath)
	if err != nil {
		return 0, err
	}
	defer f.Close()

	// Read the entire archive
	archiveData, err := io.ReadAll(f)
	if err != nil {
		return 0, err
	}
	f.Close()

	if len(archiveData) < 8 || string(archiveData[:8]) != "!<arch>\n" {
		return 0, fmt.Errorf("%s: not an ar archive", archivePath)
	}

	type member struct {
		name string
		data []byte
	}

	var members []member
	totalFixes := 0
	modified := false

	pos := 8
	for pos < len(archiveData) {
		if pos%2 == 1 {
			pos++ // ar entries are 2-byte aligned
		}
		if pos+60 > len(archiveData) {
			break
		}
		header := archiveData[pos : pos+60]
		if string(header[58:60]) != "`\n" {
			break
		}

		nameField := strings.TrimRight(string(header[0:16]), " ")
		sizeStr := strings.TrimRight(string(header[48:58]), " ")
		var size int
		fmt.Sscanf(sizeStr, "%d", &size)

		pos += 60
		if pos+size > len(archiveData) {
			break
		}

		memberData := archiveData[pos : pos+size]
		pos += size

		// Resolve extended name (GNU ar uses /NNN for long names)
		name := nameField
		if strings.HasSuffix(name, "/") {
			name = name[:len(name)-1]
		}

		// Skip symbol table and string table pseudo-members
		if name == "/" || name == "//" || name == "" {
			members = append(members, member{name: nameField, data: memberData})
			continue
		}

		fixedData, fixes := fixObject(memberData)
		totalFixes += fixes
		if fixes > 0 {
			modified = true
			members = append(members, member{name: nameField, data: fixedData})
		} else {
			members = append(members, member{name: nameField, data: memberData})
		}
	}

	if modified && !dryRun {
		// Rewrite the archive
		var buf bytes.Buffer
		buf.WriteString("!<arch>\n")

		for _, m := range members {
			if buf.Len()%2 == 1 {
				buf.WriteByte('\n')
			}

			// Reconstruct header
			nameField := m.name
			for len(nameField) < 16 {
				nameField += " "
			}
			sizeStr := fmt.Sprintf("%-10d", len(m.data))

			buf.WriteString(nameField)
			buf.WriteString("0           ") // mtime (12 bytes)
			buf.WriteString("0     ")       // uid (6 bytes)
			buf.WriteString("0     ")       // gid (6 bytes)
			buf.WriteString("100644  ")     // mode (8 bytes)
			buf.WriteString(sizeStr)
			buf.WriteString("`\n")
			buf.Write(m.data)
		}

		if err := os.WriteFile(archivePath, buf.Bytes(), 0644); err != nil {
			return totalFixes, fmt.Errorf("writing %s: %w", archivePath, err)
		}
	}

	return totalFixes, nil
}

func processObject(path string, dryRun bool) (int, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return 0, err
	}

	fixedData, fixes := fixObject(data)
	if fixes > 0 && !dryRun {
		if err := os.WriteFile(path, fixedData, 0644); err != nil {
			return fixes, err
		}
	}
	return fixes, nil
}

func main() {
	args := os.Args[1:]
	if len(args) == 0 {
		fmt.Fprintf(os.Stderr, "Usage: %s [-n] <archive.a> [archive2.a ...]\n", os.Args[0])
		fmt.Fprintf(os.Stderr, "  -n  dry run (report fixes without applying)\n")
		os.Exit(1)
	}

	dryRun := false
	if args[0] == "-n" {
		dryRun = true
		args = args[1:]
	}

	grandTotal := 0
	for _, path := range args {
		if _, err := os.Stat(path); os.IsNotExist(err) {
			fmt.Fprintf(os.Stderr, "Warning: %s not found, skipping\n", path)
			continue
		}

		base := filepath.Base(path)
		var fixes int
		var err error

		switch {
		case strings.HasSuffix(path, ".a"):
			fixes, err = processArchive(path, dryRun)
		case strings.HasSuffix(path, ".o"):
			fixes, err = processObject(path, dryRun)
		default:
			fmt.Fprintf(os.Stderr, "Skipping %s (not .a or .o)\n", path)
			continue
		}

		if err != nil {
			fmt.Fprintf(os.Stderr, "Error processing %s: %v\n", path, err)
			os.Exit(1)
		}

		if fixes > 0 {
			action := "fixed"
			if dryRun {
				action = "would fix"
			}
			fmt.Printf("%s: %s %d literal pool relocation(s)\n", base, action, fixes)
		}
		grandTotal += fixes
	}

	action := "Total fixes applied"
	if dryRun {
		action = "Total fixes needed"
	}
	fmt.Printf("%s: %d\n", action, grandTotal)
}
