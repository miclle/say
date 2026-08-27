package document

import (
	"context"
	"encoding/binary"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf16"
)

const (
	testCFBSectorSize = 512
	testCFBEndOfChain = 0xfffffffe
	testCFBFreeSector = 0xffffffff
	testCFBFATSector  = 0xfffffffd
)

type docFixtureOptions struct {
	text              string
	compressedText    []byte
	encrypted         bool
	clxMarker         byte
	textOffset        uint32
	wordDocumentName  string
	tableDocumentName string
}

func TestReadDOCExtractsWord97MainDocumentText(t *testing.T) {
	path := writeDOCFixture(t, "guide.doc", docFixtureOptions{
		text: "标题\r第一段。\rName\x07Status\rready\x07yes\r",
	})

	text, err := readDOC(context.Background(), path)
	if err != nil {
		t.Fatalf("readDOC() error = %v", err)
	}
	want := "标题\n\n第一段。\n\nName，Status\n\nready，yes"
	if text != want {
		t.Fatalf("readDOC() text = %q, want %q", text, want)
	}
}

func TestReadDOCDecodesCompressedWindows1252Text(t *testing.T) {
	path := writeDOCFixture(t, "compressed.doc", docFixtureOptions{
		compressedText: []byte{'C', 'a', 'f', 0xe9, ' ', 0x97, ' ', 'r', 'e', 'a', 'd', 'y', '\r'},
	})

	text, err := readDOC(context.Background(), path)
	if err != nil {
		t.Fatalf("readDOC() error = %v", err)
	}
	if text != "Café — ready" {
		t.Fatalf("readDOC() text = %q, want %q", text, "Café — ready")
	}
}

func TestReadDOCSupportsVersion4CompoundFiles(t *testing.T) {
	wordDocument := make([]byte, 4096)
	tableDocument := make([]byte, 4096)
	text := "Version four\r"
	units := utf16.Encode([]rune(text))
	textBytes := make([]byte, len(units)*2)
	for index, unit := range units {
		binary.LittleEndian.PutUint16(textBytes[index*2:], unit)
	}
	writeWord97FIB(wordDocument, uint32(len(units)), false, 0, 21)
	writeSinglePieceCLX(tableDocument, 0x02, uint32(len(units)), 1024, false)
	copy(wordDocument[1024:], textBytes)
	path := writeTestFile(t, "version4.doc", buildTestCFBVersion4(wordDocument, tableDocument))

	got, err := readDOC(context.Background(), path)
	if err != nil {
		t.Fatalf("readDOC() error = %v", err)
	}
	if got != "Version four" {
		t.Fatalf("readDOC() text = %q, want %q", got, "Version four")
	}
}

func TestReadDOCKeepsFieldResultsAndSkipsInstructions(t *testing.T) {
	path := writeDOCFixture(t, "field.doc", docFixtureOptions{
		text: "Before \x13HYPERLINK \"https://example.com\"\x14visible link\x15 after.\r",
	})

	text, err := readDOC(context.Background(), path)
	if err != nil {
		t.Fatalf("readDOC() error = %v", err)
	}
	if text != "Before visible link after." {
		t.Fatalf("readDOC() text = %q, want %q", text, "Before visible link after.")
	}
}

func TestReadDOCRejectsUnusableDocuments(t *testing.T) {
	tests := []struct {
		name    string
		path    func(*testing.T) string
		wantErr string
	}{
		{
			name: "not a compound file",
			path: func(t *testing.T) string {
				return writeTestFile(t, "broken.doc", []byte("not a compound file"))
			},
			wantErr: "open DOC compound file",
		},
		{
			name: "missing WordDocument stream",
			path: func(t *testing.T) string {
				return writeDOCFixture(t, "missing-word.doc", docFixtureOptions{
					text:             "text\r",
					wordDocumentName: "OtherStream",
				})
			},
			wantErr: "WordDocument stream is missing",
		},
		{
			name: "missing table stream",
			path: func(t *testing.T) string {
				return writeDOCFixture(t, "missing-table.doc", docFixtureOptions{
					text:              "text\r",
					tableDocumentName: "OtherTable",
				})
			},
			wantErr: "1Table stream is missing",
		},
		{
			name: "encrypted",
			path: func(t *testing.T) string {
				return writeDOCFixture(t, "encrypted.doc", docFixtureOptions{
					text:      "secret\r",
					encrypted: true,
				})
			},
			wantErr: "encrypted documents are not supported",
		},
		{
			name: "malformed CLX",
			path: func(t *testing.T) string {
				return writeDOCFixture(t, "malformed-clx.doc", docFixtureOptions{
					text:      "text\r",
					clxMarker: 0x03,
				})
			},
			wantErr: "invalid Pcdt marker",
		},
		{
			name: "piece outside WordDocument",
			path: func(t *testing.T) string {
				return writeDOCFixture(t, "bad-piece.doc", docFixtureOptions{
					text:       "text\r",
					textOffset: 5000,
				})
			},
			wantErr: "piece text is outside WordDocument stream",
		},
		{
			name: "empty document",
			path: func(t *testing.T) string {
				return writeDOCFixture(t, "empty.doc", docFixtureOptions{})
			},
			wantErr: "document is empty",
		},
		{
			name: "oversized input",
			path: func(t *testing.T) string {
				path := filepath.Join(t.TempDir(), "large.doc")
				file, err := os.Create(path)
				if err != nil {
					t.Fatal(err)
				}
				if err := file.Truncate((64 << 20) + 1); err != nil {
					t.Fatal(err)
				}
				if err := file.Close(); err != nil {
					t.Fatal(err)
				}
				return path
			},
			wantErr: "input exceeds 67108864 bytes",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := readDOC(context.Background(), tt.path(t))
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("readDOC() error = %v, want containing %q", err, tt.wantErr)
			}
		})
	}
}

func TestReadDOCHonorsCanceledContext(t *testing.T) {
	path := writeDOCFixture(t, "canceled.doc", docFixtureOptions{text: "text\r"})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := readDOC(ctx, path)
	if err != context.Canceled {
		t.Fatalf("readDOC() error = %v, want context.Canceled", err)
	}
}

func TestValidateCFBTraversalRejectsDeepDirectoryGraphs(t *testing.T) {
	entries := make([]cfbDirectoryEntry, maxCFBTraversalDepth+2)
	for index := range entries {
		entries[index].left = testCFBFreeSector
		entries[index].right = testCFBFreeSector
		entries[index].child = testCFBFreeSector
		entries[index].objectType = 1
	}
	entries[0].objectType = 5
	entries[0].child = 1
	for index := 1; index < len(entries)-1; index++ {
		entries[index].child = uint32(index + 1)
	}

	err := validateCFBTraversal(entries)
	if err == nil || !strings.Contains(err.Error(), "traversal depth") {
		t.Fatalf("validateCFBTraversal() error = %v, want traversal depth error", err)
	}
}

func TestReadDOCRejectsDuplicateRootStreams(t *testing.T) {
	path := writeTestFile(t, "duplicate.doc", buildTestCFB(
		"WordDocument", make([]byte, 4096),
		"WordDocument", make([]byte, 4096),
	))

	_, err := readDOC(context.Background(), path)
	if err == nil || !strings.Contains(err.Error(), "duplicate WordDocument stream") {
		t.Fatalf("readDOC() error = %v, want duplicate WordDocument stream", err)
	}
}

func TestReadDOCRejectsInvalidVersion3DirectorySectorCount(t *testing.T) {
	contents := buildTestCFB(
		"WordDocument", make([]byte, 4096),
		"1Table", make([]byte, 4096),
	)
	binary.LittleEndian.PutUint32(contents[40:44], 1)
	path := writeTestFile(t, "bad-directory-count.doc", contents)

	_, err := readDOC(context.Background(), path)
	if err == nil || !strings.Contains(err.Error(), "version 3 directory sector count") {
		t.Fatalf("readDOC() error = %v, want version 3 directory sector count", err)
	}
}

func writeDOCFixture(t *testing.T, name string, options docFixtureOptions) string {
	t.Helper()
	if options.wordDocumentName == "" {
		options.wordDocumentName = "WordDocument"
	}
	if options.tableDocumentName == "" {
		options.tableDocumentName = "1Table"
	}
	if options.clxMarker == 0 {
		options.clxMarker = 0x02
	}
	if options.textOffset == 0 {
		options.textOffset = 1024
	}

	wordDocument := make([]byte, 4096)
	tableDocument := make([]byte, 4096)
	var textBytes []byte
	var characterCount uint32
	compressed := options.compressedText != nil
	if compressed {
		textBytes = append([]byte(nil), options.compressedText...)
		characterCount = uint32(len(textBytes))
	} else {
		units := utf16.Encode([]rune(options.text))
		textBytes = make([]byte, len(units)*2)
		for index, unit := range units {
			binary.LittleEndian.PutUint16(textBytes[index*2:], unit)
		}
		characterCount = uint32(len(units))
	}

	writeWord97FIB(wordDocument, characterCount, options.encrypted, 0, 21)
	writeSinglePieceCLX(tableDocument, options.clxMarker, characterCount, options.textOffset, compressed)
	if int(options.textOffset)+len(textBytes) <= len(wordDocument) {
		copy(wordDocument[options.textOffset:], textBytes)
	}

	contents := buildTestCFB(
		options.wordDocumentName,
		wordDocument,
		options.tableDocumentName,
		tableDocument,
	)
	return writeTestFile(t, name, contents)
}

func writeWord97FIB(destination []byte, characterCount uint32, encrypted bool, fcClx, lcbClx uint32) {
	binary.LittleEndian.PutUint16(destination[0:], 0xa5ec)
	binary.LittleEndian.PutUint16(destination[2:], 0x00c1)
	binary.LittleEndian.PutUint16(destination[6:], 0x0409)
	flags := uint16(1 << 9)
	if encrypted {
		flags |= 1 << 8
	}
	binary.LittleEndian.PutUint16(destination[10:], flags)
	binary.LittleEndian.PutUint16(destination[12:], 0x00bf)
	binary.LittleEndian.PutUint16(destination[32:], 14)
	binary.LittleEndian.PutUint16(destination[62:], 22)
	binary.LittleEndian.PutUint32(destination[76:], characterCount)
	binary.LittleEndian.PutUint16(destination[152:], 93)
	binary.LittleEndian.PutUint32(destination[154+66*4:], fcClx)
	binary.LittleEndian.PutUint32(destination[154+67*4:], lcbClx)
}

func writeSinglePieceCLX(destination []byte, marker byte, characterCount, textOffset uint32, compressed bool) {
	destination[0] = marker
	binary.LittleEndian.PutUint32(destination[1:], 16)
	binary.LittleEndian.PutUint32(destination[5:], 0)
	binary.LittleEndian.PutUint32(destination[9:], characterCount)
	encodedOffset := textOffset
	if compressed {
		encodedOffset = textOffset*2 | 1<<30
	}
	binary.LittleEndian.PutUint32(destination[15:], encodedOffset)
}

func buildTestCFB(firstName string, firstData []byte, secondName string, secondData []byte) []byte {
	const (
		fatSector       = 0
		directorySector = 1
		firstStart      = 2
		secondStart     = 10
		sectorCount     = 18
	)
	contents := make([]byte, (sectorCount+1)*testCFBSectorSize)
	header := contents[:testCFBSectorSize]
	copy(header[:8], []byte{0xd0, 0xcf, 0x11, 0xe0, 0xa1, 0xb1, 0x1a, 0xe1})
	binary.LittleEndian.PutUint16(header[24:], 0x003e)
	binary.LittleEndian.PutUint16(header[26:], 3)
	binary.LittleEndian.PutUint16(header[28:], 0xfffe)
	binary.LittleEndian.PutUint16(header[30:], 9)
	binary.LittleEndian.PutUint16(header[32:], 6)
	binary.LittleEndian.PutUint32(header[44:], 1)
	binary.LittleEndian.PutUint32(header[48:], directorySector)
	binary.LittleEndian.PutUint32(header[56:], 4096)
	binary.LittleEndian.PutUint32(header[60:], testCFBEndOfChain)
	binary.LittleEndian.PutUint32(header[68:], testCFBEndOfChain)
	for offset := 76; offset < testCFBSectorSize; offset += 4 {
		binary.LittleEndian.PutUint32(header[offset:], testCFBFreeSector)
	}
	binary.LittleEndian.PutUint32(header[76:], fatSector)

	fat := contents[testCFBSectorSize : 2*testCFBSectorSize]
	for offset := 0; offset < len(fat); offset += 4 {
		binary.LittleEndian.PutUint32(fat[offset:], testCFBFreeSector)
	}
	setTestFATEntry(fat, fatSector, testCFBFATSector)
	setTestFATEntry(fat, directorySector, testCFBEndOfChain)
	setTestFATChain(fat, firstStart, 8)
	setTestFATChain(fat, secondStart, 8)

	directory := contents[(directorySector+1)*testCFBSectorSize : (directorySector+2)*testCFBSectorSize]
	writeTestDirectoryEntry(directory[0:128], "Root Entry", 5, 1, testCFBFreeSector, testCFBFreeSector, 1, testCFBEndOfChain, 0)
	writeTestDirectoryEntry(directory[128:256], firstName, 2, 1, testCFBFreeSector, 2, testCFBFreeSector, firstStart, uint64(len(firstData)))
	writeTestDirectoryEntry(directory[256:384], secondName, 2, 0, testCFBFreeSector, testCFBFreeSector, testCFBFreeSector, secondStart, uint64(len(secondData)))

	copy(contents[(firstStart+1)*testCFBSectorSize:], firstData)
	copy(contents[(secondStart+1)*testCFBSectorSize:], secondData)
	return contents
}

func buildTestCFBVersion4(wordDocument, tableDocument []byte) []byte {
	const (
		sectorSize      = 4096
		fatSector       = 0
		directorySector = 1
		wordStart       = 2
		tableStart      = 3
		sectorCount     = 4
	)
	contents := make([]byte, (sectorCount+1)*sectorSize)
	header := contents[:512]
	copy(header[:8], []byte{0xd0, 0xcf, 0x11, 0xe0, 0xa1, 0xb1, 0x1a, 0xe1})
	binary.LittleEndian.PutUint16(header[24:], 0x003e)
	binary.LittleEndian.PutUint16(header[26:], 4)
	binary.LittleEndian.PutUint16(header[28:], 0xfffe)
	binary.LittleEndian.PutUint16(header[30:], 12)
	binary.LittleEndian.PutUint16(header[32:], 6)
	binary.LittleEndian.PutUint32(header[40:], 1)
	binary.LittleEndian.PutUint32(header[44:], 1)
	binary.LittleEndian.PutUint32(header[48:], directorySector)
	binary.LittleEndian.PutUint32(header[56:], 4096)
	binary.LittleEndian.PutUint32(header[60:], testCFBEndOfChain)
	binary.LittleEndian.PutUint32(header[68:], testCFBEndOfChain)
	for offset := 76; offset < len(header); offset += 4 {
		binary.LittleEndian.PutUint32(header[offset:], testCFBFreeSector)
	}
	binary.LittleEndian.PutUint32(header[76:], fatSector)

	fat := contents[sectorSize : 2*sectorSize]
	for offset := 0; offset < len(fat); offset += 4 {
		binary.LittleEndian.PutUint32(fat[offset:], testCFBFreeSector)
	}
	setTestFATEntry(fat, fatSector, testCFBFATSector)
	setTestFATEntry(fat, directorySector, testCFBEndOfChain)
	setTestFATEntry(fat, wordStart, testCFBEndOfChain)
	setTestFATEntry(fat, tableStart, testCFBEndOfChain)

	directory := contents[(directorySector+1)*sectorSize : (directorySector+2)*sectorSize]
	writeTestDirectoryEntry(directory[0:128], "Root Entry", 5, 1, testCFBFreeSector, testCFBFreeSector, 1, testCFBEndOfChain, 0)
	writeTestDirectoryEntry(directory[128:256], "WordDocument", 2, 1, testCFBFreeSector, 2, testCFBFreeSector, wordStart, uint64(len(wordDocument)))
	writeTestDirectoryEntry(directory[256:384], "1Table", 2, 0, testCFBFreeSector, testCFBFreeSector, testCFBFreeSector, tableStart, uint64(len(tableDocument)))
	copy(contents[(wordStart+1)*sectorSize:], wordDocument)
	copy(contents[(tableStart+1)*sectorSize:], tableDocument)
	return contents
}

func setTestFATEntry(fat []byte, sector, value uint32) {
	binary.LittleEndian.PutUint32(fat[sector*4:], value)
}

func setTestFATChain(fat []byte, start, count uint32) {
	for index := uint32(0); index < count; index++ {
		value := start + index + 1
		if index == count-1 {
			value = testCFBEndOfChain
		}
		setTestFATEntry(fat, start+index, value)
	}
}

func writeTestDirectoryEntry(destination []byte, name string, objectType, color byte, left, right, child, start uint32, size uint64) {
	units := utf16.Encode([]rune(name + "\x00"))
	for index, unit := range units {
		binary.LittleEndian.PutUint16(destination[index*2:], unit)
	}
	binary.LittleEndian.PutUint16(destination[64:], uint16(len(units)*2))
	destination[66] = objectType
	destination[67] = color
	binary.LittleEndian.PutUint32(destination[68:], left)
	binary.LittleEndian.PutUint32(destination[72:], right)
	binary.LittleEndian.PutUint32(destination[76:], child)
	binary.LittleEndian.PutUint32(destination[116:], start)
	binary.LittleEndian.PutUint64(destination[120:], size)
}
