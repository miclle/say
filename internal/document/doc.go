package document

import (
	"context"
	"encoding/binary"
	"fmt"
	"io"
	"os"
	"strings"
	"unicode/utf16"

	"github.com/richardlehane/mscfb"
	"golang.org/x/text/encoding/charmap"
)

const (
	word97FIBIdentifier = 0xa5ec
	word97NFib          = 0x00c1
)

type docFileInformation struct {
	tableName string
	ccpText   uint32
	fcClx     uint32
	lcbClx    uint32
}

type docPiece struct {
	cpStart    uint32
	cpEnd      uint32
	fileOffset uint32
	compressed bool
}

type docNarrationDecoder struct {
	output strings.Builder
	fields []bool
}

func readDOC(ctx context.Context, path string) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	info, err := os.Stat(path)
	if err != nil {
		return "", fmt.Errorf("open DOC document: %w", err)
	}
	if !info.Mode().IsRegular() {
		return "", fmt.Errorf("open DOC document %q: not a regular file", path)
	}
	if info.Size() > maxWordInputBytes {
		return "", fmt.Errorf("read DOC document %q: input exceeds %d bytes", path, maxWordInputBytes)
	}

	file, err := os.Open(path)
	if err != nil {
		return "", fmt.Errorf("open DOC document: %w", err)
	}
	defer file.Close()
	if err := validateCFBDirectory(ctx, file, info.Size()); err != nil {
		return "", fmt.Errorf("open DOC compound file %q: %w", path, err)
	}
	guardedReader := &boundedCFBReaderAt{
		ctx:             ctx,
		reader:          file,
		size:            info.Size(),
		metadataLimited: true,
	}
	compound, err := mscfb.New(guardedReader)
	if err != nil {
		return "", fmt.Errorf("open DOC compound file %q: %w", path, err)
	}
	guardedReader.metadataLimited = false
	if err := ctx.Err(); err != nil {
		return "", err
	}

	streams := make(map[string]*mscfb.File, 3)
	for _, stream := range compound.File {
		if len(stream.Path) != 0 {
			continue
		}
		switch stream.Name {
		case "WordDocument", "0Table", "1Table":
			if streams[stream.Name] != nil {
				return "", fmt.Errorf("read DOC document %q: duplicate %s stream", path, stream.Name)
			}
			streams[stream.Name] = stream
		}
	}
	wordStream := streams["WordDocument"]
	if wordStream == nil {
		return "", fmt.Errorf("read DOC document %q: WordDocument stream is missing", path)
	}
	wordDocument, err := readDOCStream(ctx, wordStream, maxWordInputBytes)
	if err != nil {
		return "", fmt.Errorf("read DOC WordDocument stream %q: %w", path, err)
	}
	fib, err := parseDOCFileInformation(wordDocument)
	if err != nil {
		return "", fmt.Errorf("parse DOC file information %q: %w", path, err)
	}
	tableStream := streams[fib.tableName]
	if tableStream == nil {
		return "", fmt.Errorf("read DOC document %q: %s stream is missing", path, fib.tableName)
	}
	tableDocument, err := readDOCStream(ctx, tableStream, maxWordInputBytes)
	if err != nil {
		return "", fmt.Errorf("read DOC %s stream %q: %w", fib.tableName, path, err)
	}
	pieces, err := parseDOCPieceTable(tableDocument, fib)
	if err != nil {
		return "", fmt.Errorf("parse DOC piece table %q: %w", path, err)
	}
	narration, err := decodeDOCMainText(ctx, wordDocument, pieces, fib.ccpText)
	if err != nil {
		return "", fmt.Errorf("decode DOC main document %q: %w", path, err)
	}
	narration = normalizeNarration(narration)
	if strings.TrimSpace(narration) == "" {
		return "", fmt.Errorf("read DOC document %q: document is empty", path)
	}
	if len(narration) > maxWordNarrationBytes {
		return "", fmt.Errorf("read DOC document %q: narration exceeds %d bytes", path, maxWordNarrationBytes)
	}
	return narration, nil
}

func readDOCStream(ctx context.Context, stream *mscfb.File, limit int64) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if stream.Size < 0 || stream.Size > limit {
		return nil, fmt.Errorf("stream exceeds %d bytes", limit)
	}
	data := make([]byte, int(stream.Size))
	if len(data) == 0 {
		return data, nil
	}
	const chunkSize = 64 << 10
	for offset := 0; offset < len(data); offset += chunkSize {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		end := min(offset+chunkSize, len(data))
		read, err := stream.ReadAt(data[offset:end], int64(offset))
		if err != nil && err != io.EOF {
			return nil, err
		}
		if read != end-offset {
			return nil, io.ErrUnexpectedEOF
		}
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return data, nil
}

func parseDOCFileInformation(wordDocument []byte) (docFileInformation, error) {
	if len(wordDocument) < 34 {
		return docFileInformation{}, io.ErrUnexpectedEOF
	}
	if binary.LittleEndian.Uint16(wordDocument[0:2]) != word97FIBIdentifier {
		return docFileInformation{}, fmt.Errorf("invalid WordDocument FIB identifier")
	}
	if binary.LittleEndian.Uint16(wordDocument[2:4]) < word97NFib {
		return docFileInformation{}, fmt.Errorf("Word versions before Word 97 are not supported")
	}
	flags := binary.LittleEndian.Uint16(wordDocument[10:12])
	if flags&(1<<8) != 0 || flags&(1<<15) != 0 {
		return docFileInformation{}, fmt.Errorf("encrypted documents are not supported")
	}
	tableName := "0Table"
	if flags&(1<<9) != 0 {
		tableName = "1Table"
	}

	cswBytes := int(binary.LittleEndian.Uint16(wordDocument[32:34])) * 2
	fibRgLwHeader := 34 + cswBytes
	if fibRgLwHeader < 34 || fibRgLwHeader+2 > len(wordDocument) {
		return docFileInformation{}, fmt.Errorf("invalid FibRgW length")
	}
	cslwBytes := int(binary.LittleEndian.Uint16(wordDocument[fibRgLwHeader:fibRgLwHeader+2])) * 4
	fibRgLwStart := fibRgLwHeader + 2
	if cslwBytes < 16 || fibRgLwStart > len(wordDocument)-cslwBytes {
		return docFileInformation{}, fmt.Errorf("invalid FibRgLw length")
	}
	ccpText := binary.LittleEndian.Uint32(wordDocument[fibRgLwStart+12 : fibRgLwStart+16])

	fibRgFcLcbHeader := fibRgLwStart + cslwBytes
	if fibRgFcLcbHeader+2 > len(wordDocument) {
		return docFileInformation{}, fmt.Errorf("missing FibRgFcLcb")
	}
	pairCount := int(binary.LittleEndian.Uint16(wordDocument[fibRgFcLcbHeader : fibRgFcLcbHeader+2]))
	fibRgFcLcbStart := fibRgFcLcbHeader + 2
	declaredEnd := fibRgFcLcbStart + pairCount*8
	const clxFieldsEnd = 68 * 4
	if pairCount < 34 || declaredEnd < fibRgFcLcbStart || declaredEnd > len(wordDocument) || fibRgFcLcbStart+clxFieldsEnd > declaredEnd {
		return docFileInformation{}, fmt.Errorf("invalid FibRgFcLcb length")
	}
	fcClx := binary.LittleEndian.Uint32(wordDocument[fibRgFcLcbStart+66*4 : fibRgFcLcbStart+67*4])
	lcbClx := binary.LittleEndian.Uint32(wordDocument[fibRgFcLcbStart+67*4 : fibRgFcLcbStart+68*4])
	return docFileInformation{tableName: tableName, ccpText: ccpText, fcClx: fcClx, lcbClx: lcbClx}, nil
}

func parseDOCPieceTable(tableDocument []byte, fib docFileInformation) ([]docPiece, error) {
	clxStart := uint64(fib.fcClx)
	clxEnd := clxStart + uint64(fib.lcbClx)
	if clxEnd < clxStart || clxEnd > uint64(len(tableDocument)) {
		return nil, fmt.Errorf("CLX is outside %s stream", fib.tableName)
	}
	clx := tableDocument[clxStart:clxEnd]
	offset := 0
	for offset < len(clx) && clx[offset] == 0x01 {
		if offset+3 > len(clx) {
			return nil, fmt.Errorf("truncated Prc record")
		}
		length := int(binary.LittleEndian.Uint16(clx[offset+1 : offset+3]))
		if length == 0 || offset+3+length > len(clx) {
			return nil, fmt.Errorf("invalid Prc record length")
		}
		offset += 3 + length
	}
	if offset >= len(clx) || clx[offset] != 0x02 {
		return nil, fmt.Errorf("invalid Pcdt marker")
	}
	if offset+5 > len(clx) {
		return nil, fmt.Errorf("truncated Pcdt header")
	}
	lcb := int(binary.LittleEndian.Uint32(clx[offset+1 : offset+5]))
	if lcb < 4 || (lcb-4)%12 != 0 || offset+5+lcb > len(clx) {
		return nil, fmt.Errorf("invalid PlcPcd length")
	}
	pieceCount := (lcb - 4) / 12
	cpCount := pieceCount + 1
	plcStart := offset + 5
	pcdStart := plcStart + cpCount*4
	pieces := make([]docPiece, pieceCount)
	previousCP := uint32(0)
	for index := 0; index < cpCount; index++ {
		cpOffset := plcStart + index*4
		cp := binary.LittleEndian.Uint32(clx[cpOffset : cpOffset+4])
		if index == 0 && cp != 0 {
			return nil, fmt.Errorf("piece table does not start at CP zero")
		}
		if index > 0 && cp < previousCP {
			return nil, fmt.Errorf("piece table CP values are not monotonic")
		}
		if index > 0 {
			pieces[index-1].cpEnd = cp
		}
		if index < pieceCount {
			pieces[index].cpStart = cp
		}
		previousCP = cp
	}
	if previousCP < fib.ccpText {
		return nil, fmt.Errorf("piece table does not cover main document text")
	}
	for index := range pieces {
		pcdOffset := pcdStart + index*8
		rawOffset := binary.LittleEndian.Uint32(clx[pcdOffset+2 : pcdOffset+6])
		pieces[index].compressed = rawOffset&(1<<30) != 0
		rawOffset &= (1 << 30) - 1
		if pieces[index].compressed {
			rawOffset /= 2
		}
		pieces[index].fileOffset = rawOffset
	}
	return pieces, nil
}

func decodeDOCMainText(ctx context.Context, wordDocument []byte, pieces []docPiece, ccpText uint32) (string, error) {
	decoder := &docNarrationDecoder{}
	for _, piece := range pieces {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		startCP := piece.cpStart
		endCP := piece.cpEnd
		if startCP >= ccpText || endCP <= startCP {
			continue
		}
		if endCP > ccpText {
			endCP = ccpText
		}
		characterCount := uint64(endCP - startCP)
		start := uint64(piece.fileOffset)
		length := characterCount
		if !piece.compressed {
			length *= 2
		}
		end := start + length
		if end < start || end > uint64(len(wordDocument)) {
			return "", fmt.Errorf("piece text is outside WordDocument stream")
		}
		if piece.compressed {
			decoded, err := charmap.Windows1252.NewDecoder().Bytes(wordDocument[start:end])
			if err != nil {
				return "", fmt.Errorf("decode compressed piece: %w", err)
			}
			decoder.writeString(string(decoded))
		} else {
			pieceBytes := wordDocument[start:end]
			units := make([]uint16, len(pieceBytes)/2)
			for index := range units {
				units[index] = binary.LittleEndian.Uint16(pieceBytes[index*2 : index*2+2])
			}
			decoder.writeString(string(utf16.Decode(units)))
		}
		if decoder.output.Len() > maxWordNarrationBytes {
			return "", fmt.Errorf("narration exceeds %d bytes", maxWordNarrationBytes)
		}
	}
	return decoder.output.String(), nil
}

func (d *docNarrationDecoder) writeString(value string) {
	for _, current := range value {
		switch current {
		case 0x13:
			d.fields = append(d.fields, false)
			continue
		case 0x14:
			if len(d.fields) > 0 {
				d.fields[len(d.fields)-1] = true
			}
			continue
		case 0x15:
			if len(d.fields) > 0 {
				d.fields = d.fields[:len(d.fields)-1]
			}
			continue
		}
		if !d.visible() {
			continue
		}
		switch current {
		case 0x07:
			d.output.WriteRune('，')
		case '\t':
			d.output.WriteByte(' ')
		case '\n', 0x0b:
			d.output.WriteByte('\n')
		case 0x0c, '\r':
			d.output.WriteString("\n\n")
		default:
			if current >= 0x20 && current != 0x7f {
				d.output.WriteRune(current)
			}
		}
	}
}

func (d *docNarrationDecoder) visible() bool {
	for _, result := range d.fields {
		if !result {
			return false
		}
	}
	return true
}
