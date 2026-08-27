package document

import (
	"bytes"
	"context"
	"encoding/binary"
	"fmt"
	"io"
)

const (
	maxCFBDirectoryEntries = 4096
	maxCFBTraversalDepth   = 128
	maxCFBMetadataReads    = 16384
	maxCFBMetadataBytes    = 16 << 20

	cfbEndOfChain = uint32(0xfffffffe)
	cfbFreeSector = uint32(0xffffffff)
)

var cfbSignature = []byte{0xd0, 0xcf, 0x11, 0xe0, 0xa1, 0xb1, 0x1a, 0xe1}

type cfbDirectoryEntry struct {
	left       uint32
	right      uint32
	child      uint32
	objectType byte
}

type boundedCFBReaderAt struct {
	ctx             context.Context
	reader          io.ReaderAt
	size            int64
	metadataLimited bool
	reads           int
	bytes           int64
}

func (r *boundedCFBReaderAt) ReadAt(destination []byte, offset int64) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	if offset < 0 || int64(len(destination)) > r.size-offset {
		return 0, io.ErrUnexpectedEOF
	}
	if r.metadataLimited {
		r.reads++
		r.bytes += int64(len(destination))
		if r.reads > maxCFBMetadataReads || r.bytes > maxCFBMetadataBytes {
			return 0, fmt.Errorf("compound-file metadata exceeds parser limits")
		}
	}
	return r.reader.ReadAt(destination, offset)
}

func validateCFBDirectory(ctx context.Context, reader io.ReaderAt, size int64) error {
	header := make([]byte, 512)
	if err := readAtContext(ctx, reader, header, 0); err != nil {
		return err
	}
	if !bytes.Equal(header[:8], cfbSignature) {
		return fmt.Errorf("invalid compound-file signature")
	}
	majorVersion := binary.LittleEndian.Uint16(header[26:28])
	sectorShift := binary.LittleEndian.Uint16(header[30:32])
	if (majorVersion != 3 || sectorShift != 9) && (majorVersion != 4 || sectorShift != 12) {
		return fmt.Errorf("unsupported compound-file sector format")
	}
	sectorSize := int64(1) << sectorShift
	if size < sectorSize*2 || size%sectorSize != 0 {
		return fmt.Errorf("invalid compound-file length")
	}
	sectorCount := uint32(size/sectorSize - 1)
	numberOfDirectorySectors := binary.LittleEndian.Uint32(header[40:44])
	maximumDirectorySectors := uint32((maxCFBDirectoryEntries*128 + int(sectorSize) - 1) / int(sectorSize))
	if majorVersion == 3 && numberOfDirectorySectors != 0 {
		return fmt.Errorf("invalid version 3 directory sector count")
	}
	if majorVersion == 4 && (numberOfDirectorySectors == 0 || numberOfDirectorySectors > maximumDirectorySectors) {
		return fmt.Errorf("invalid version 4 directory sector count")
	}
	numberOfFATSectors := binary.LittleEndian.Uint32(header[44:48])
	fatEntriesPerSector := uint32(sectorSize / 4)
	maximumFATSectors := (sectorCount + fatEntriesPerSector - 1) / fatEntriesPerSector
	if numberOfFATSectors == 0 || numberOfFATSectors > maximumFATSectors {
		return fmt.Errorf("invalid compound-file FAT sector count")
	}

	fatSectors := make([]uint32, 0, numberOfFATSectors)
	for offset := 76; offset < 512 && uint32(len(fatSectors)) < numberOfFATSectors; offset += 4 {
		sector := binary.LittleEndian.Uint32(header[offset : offset+4])
		if sector != cfbFreeSector {
			fatSectors = append(fatSectors, sector)
		}
	}
	numberOfDIFATSectors := binary.LittleEndian.Uint32(header[72:76])
	if numberOfDIFATSectors > sectorCount {
		return fmt.Errorf("invalid compound-file DIFAT sector count")
	}
	currentDIFAT := binary.LittleEndian.Uint32(header[68:72])
	visitedDIFAT := make(map[uint32]struct{}, numberOfDIFATSectors)
	for index := uint32(0); index < numberOfDIFATSectors; index++ {
		if err := validateCFBSector(currentDIFAT, sectorCount); err != nil {
			return fmt.Errorf("invalid DIFAT chain: %w", err)
		}
		if _, duplicate := visitedDIFAT[currentDIFAT]; duplicate {
			return fmt.Errorf("invalid DIFAT chain: cycle detected")
		}
		visitedDIFAT[currentDIFAT] = struct{}{}
		sector, err := readCFBSector(ctx, reader, size, sectorSize, currentDIFAT)
		if err != nil {
			return err
		}
		for offset := 0; offset < len(sector)-4 && uint32(len(fatSectors)) < numberOfFATSectors; offset += 4 {
			fatSector := binary.LittleEndian.Uint32(sector[offset : offset+4])
			if fatSector != cfbFreeSector {
				fatSectors = append(fatSectors, fatSector)
			}
		}
		currentDIFAT = binary.LittleEndian.Uint32(sector[len(sector)-4:])
	}
	if numberOfDIFATSectors > 0 && currentDIFAT != cfbEndOfChain {
		return fmt.Errorf("invalid DIFAT chain terminator")
	}
	if uint32(len(fatSectors)) != numberOfFATSectors {
		return fmt.Errorf("compound-file FAT is incomplete")
	}

	fat := make([]uint32, 0, int(numberOfFATSectors)*int(sectorSize/4))
	seenFATSectors := make(map[uint32]struct{}, len(fatSectors))
	for _, fatSector := range fatSectors {
		if err := validateCFBSector(fatSector, sectorCount); err != nil {
			return fmt.Errorf("invalid FAT sector: %w", err)
		}
		if _, duplicate := seenFATSectors[fatSector]; duplicate {
			return fmt.Errorf("duplicate FAT sector")
		}
		seenFATSectors[fatSector] = struct{}{}
		sector, err := readCFBSector(ctx, reader, size, sectorSize, fatSector)
		if err != nil {
			return err
		}
		for offset := 0; offset < len(sector); offset += 4 {
			fat = append(fat, binary.LittleEndian.Uint32(sector[offset:offset+4]))
		}
	}

	directorySector := binary.LittleEndian.Uint32(header[48:52])
	visitedDirectory := make(map[uint32]struct{})
	entries := make([]cfbDirectoryEntry, 0, 32)
	for directorySector != cfbEndOfChain {
		if err := validateCFBSector(directorySector, sectorCount); err != nil {
			return fmt.Errorf("invalid directory chain: %w", err)
		}
		if _, duplicate := visitedDirectory[directorySector]; duplicate {
			return fmt.Errorf("invalid directory chain: cycle detected")
		}
		visitedDirectory[directorySector] = struct{}{}
		sector, err := readCFBSector(ctx, reader, size, sectorSize, directorySector)
		if err != nil {
			return err
		}
		for offset := 0; offset < len(sector); offset += 128 {
			entry := sector[offset : offset+128]
			entries = append(entries, cfbDirectoryEntry{
				left:       binary.LittleEndian.Uint32(entry[68:72]),
				right:      binary.LittleEndian.Uint32(entry[72:76]),
				child:      binary.LittleEndian.Uint32(entry[76:80]),
				objectType: entry[66],
			})
			if len(entries) > maxCFBDirectoryEntries {
				return fmt.Errorf("compound-file directory contains more than %d entries", maxCFBDirectoryEntries)
			}
		}
		if uint64(directorySector) >= uint64(len(fat)) {
			return fmt.Errorf("directory sector is outside FAT")
		}
		directorySector = fat[directorySector]
	}
	return validateCFBTraversal(entries)
}

func validateCFBTraversal(entries []cfbDirectoryEntry) error {
	if len(entries) == 0 || entries[0].objectType != 5 {
		return fmt.Errorf("compound-file root directory entry is missing")
	}
	type pendingEntry struct {
		id    uint32
		depth int
	}
	pending := []pendingEntry{{id: 0, depth: 1}}
	visited := make(map[uint32]struct{}, len(entries))
	for len(pending) > 0 {
		last := len(pending) - 1
		current := pending[last]
		pending = pending[:last]
		if current.depth > maxCFBTraversalDepth {
			return fmt.Errorf("compound-file directory traversal depth exceeds %d", maxCFBTraversalDepth)
		}
		if uint64(current.id) >= uint64(len(entries)) {
			return fmt.Errorf("compound-file directory references an invalid entry")
		}
		if _, duplicate := visited[current.id]; duplicate {
			return fmt.Errorf("compound-file directory graph contains a cycle or duplicate reference")
		}
		visited[current.id] = struct{}{}
		entry := entries[current.id]
		if entry.objectType == 0 {
			return fmt.Errorf("compound-file directory references an unused entry")
		}
		for _, next := range []uint32{entry.right, entry.child, entry.left} {
			if next != cfbFreeSector {
				pending = append(pending, pendingEntry{id: next, depth: current.depth + 1})
			}
		}
	}
	return nil
}

func readCFBSector(ctx context.Context, reader io.ReaderAt, size, sectorSize int64, sector uint32) ([]byte, error) {
	offset := (int64(sector) + 1) * sectorSize
	if offset < 0 || sectorSize > size-offset {
		return nil, fmt.Errorf("compound-file sector is outside input")
	}
	contents := make([]byte, int(sectorSize))
	if err := readAtContext(ctx, reader, contents, offset); err != nil {
		return nil, err
	}
	return contents, nil
}

func readAtContext(ctx context.Context, reader io.ReaderAt, destination []byte, offset int64) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	read, err := reader.ReadAt(destination, offset)
	if err != nil && err != io.EOF {
		return err
	}
	if read != len(destination) {
		return io.ErrUnexpectedEOF
	}
	return ctx.Err()
}

func validateCFBSector(sector, sectorCount uint32) error {
	if sector >= sectorCount {
		return fmt.Errorf("sector %d is outside input", sector)
	}
	return nil
}
