package document

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/xml"
	"fmt"
	"io"
	"os"
	"strings"
)

const (
	maxWordInputBytes        = 64 << 20
	maxDOCXEntries           = 4096
	maxDOCXUncompressedBytes = 256 << 20
	maxDOCXMainXMLBytes      = 16 << 20
	maxWordNarrationBytes    = 16 << 20
)

const (
	transitionalWordprocessingML = "http://schemas.openxmlformats.org/wordprocessingml/2006/main"
	strictWordprocessingML       = "http://purl.oclc.org/ooxml/wordprocessingml/main"
	markupCompatibility          = "http://schemas.openxmlformats.org/markup-compatibility/2006"
)

type docxSegmentKind uint8

const (
	docxParagraph docxSegmentKind = iota
	docxListItem
	docxTableRow
)

type docxSegment struct {
	kind docxSegmentKind
	text string
}

type docxParagraphState struct {
	text strings.Builder
	list bool
}

type docxExtractor struct {
	segments       []docxSegment
	paragraph      *docxParagraphState
	tableDepth     int
	bodyDepth      int
	ignoredDepth   int
	rowProperties  int
	cellProperties int
	rowDeleted     bool
	cellDeleted    bool
	cellParagraphs []string
	rowCells       []string
}

type contextReader struct {
	ctx    context.Context
	reader io.Reader
}

func (r contextReader) Read(destination []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	read, err := r.reader.Read(destination)
	if err == nil {
		err = r.ctx.Err()
	}
	return read, err
}

func readDOCX(ctx context.Context, path string) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	info, err := os.Stat(path)
	if err != nil {
		return "", fmt.Errorf("open DOCX document: %w", err)
	}
	if !info.Mode().IsRegular() {
		return "", fmt.Errorf("open DOCX document %q: not a regular file", path)
	}
	if info.Size() > maxWordInputBytes {
		return "", fmt.Errorf("read DOCX document %q: input exceeds %d bytes", path, maxWordInputBytes)
	}

	file, err := os.Open(path)
	if err != nil {
		return "", fmt.Errorf("open DOCX document: %w", err)
	}
	defer file.Close()

	archive, err := zip.NewReader(file, info.Size())
	if err != nil {
		return "", fmt.Errorf("open DOCX package %q: %w", path, err)
	}
	if len(archive.File) > maxDOCXEntries {
		return "", fmt.Errorf("open DOCX package %q: archive contains more than %d entries", path, maxDOCXEntries)
	}

	var mainDocument *zip.File
	var totalUncompressed uint64
	for _, entry := range archive.File {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		if entry.UncompressedSize64 > maxDOCXUncompressedBytes-totalUncompressed {
			return "", fmt.Errorf("open DOCX package %q: uncompressed content exceeds %d bytes", path, maxDOCXUncompressedBytes)
		}
		totalUncompressed += entry.UncompressedSize64
		if entry.Name == "word/document.xml" {
			mainDocument = entry
		}
	}
	if mainDocument == nil {
		return "", fmt.Errorf("open DOCX package %q: word/document.xml is missing", path)
	}
	if mainDocument.UncompressedSize64 > maxDOCXMainXMLBytes {
		return "", fmt.Errorf("read DOCX package %q: main document exceeds %d bytes", path, maxDOCXMainXMLBytes)
	}

	mainReader, err := mainDocument.Open()
	if err != nil {
		return "", fmt.Errorf("read DOCX main document %q: %w", path, err)
	}
	mainXML, readErr := io.ReadAll(io.LimitReader(contextReader{ctx: ctx, reader: mainReader}, maxDOCXMainXMLBytes+1))
	closeErr := mainReader.Close()
	if readErr != nil {
		return "", fmt.Errorf("read DOCX main document %q: %w", path, readErr)
	}
	if closeErr != nil {
		return "", fmt.Errorf("read DOCX main document %q: %w", path, closeErr)
	}
	if len(mainXML) > maxDOCXMainXMLBytes {
		return "", fmt.Errorf("read DOCX package %q: main document exceeds %d bytes", path, maxDOCXMainXMLBytes)
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}

	narration, err := extractDOCXNarration(ctx, mainXML)
	if err != nil {
		return "", fmt.Errorf("parse DOCX main document %q: %w", path, err)
	}
	if strings.TrimSpace(narration) == "" {
		return "", fmt.Errorf("read DOCX document %q: document is empty", path)
	}
	if len(narration) > maxWordNarrationBytes {
		return "", fmt.Errorf("read DOCX document %q: narration exceeds %d bytes", path, maxWordNarrationBytes)
	}
	return narration, nil
}

func extractDOCXNarration(ctx context.Context, source []byte) (string, error) {
	decoder := xml.NewDecoder(bytes.NewReader(source))
	extractor := &docxExtractor{}
	for {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		token, err := decoder.Token()
		if err == io.EOF {
			return extractor.narration(), nil
		}
		if err != nil {
			return "", err
		}
		switch current := token.(type) {
		case xml.StartElement:
			if extractor.ignoredDepth > 0 {
				extractor.ignoredDepth++
				continue
			}
			if isWordprocessingML(current.Name) {
				if current.Name.Local == "del" && extractor.rowProperties > 0 {
					extractor.rowDeleted = true
					extractor.ignoredDepth = 1
					continue
				}
				if current.Name.Local == "cellDel" && extractor.cellProperties > 0 {
					extractor.cellDeleted = true
					extractor.ignoredDepth = 1
					continue
				}
			}
			if isIgnoredDOCXElement(current.Name) {
				extractor.ignoredDepth = 1
				continue
			}
			if !isWordprocessingML(current.Name) {
				continue
			}
			if current.Name.Local == "body" {
				extractor.bodyDepth++
				continue
			}
			if extractor.bodyDepth == 0 {
				continue
			}
			switch current.Name.Local {
			case "tbl":
				extractor.tableDepth++
			case "tr":
				if extractor.tableDepth == 1 {
					extractor.rowCells = nil
					extractor.rowDeleted = false
				}
			case "tc":
				if extractor.tableDepth == 1 {
					extractor.cellParagraphs = nil
					extractor.cellDeleted = false
				}
			case "trPr":
				if extractor.tableDepth == 1 {
					extractor.rowProperties++
				}
			case "tcPr":
				if extractor.tableDepth == 1 {
					extractor.cellProperties++
				}
			case "p":
				extractor.paragraph = &docxParagraphState{}
			case "numPr":
				if extractor.paragraph != nil {
					extractor.paragraph.list = true
				}
			case "t":
				var value string
				if err := decoder.DecodeElement(&value, &current); err != nil {
					return "", err
				}
				if extractor.paragraph != nil {
					extractor.paragraph.text.WriteString(value)
				}
			case "tab":
				if extractor.paragraph != nil {
					extractor.paragraph.text.WriteByte(' ')
				}
			case "br", "cr":
				if extractor.paragraph != nil {
					extractor.paragraph.text.WriteByte('\n')
				}
			}
		case xml.EndElement:
			if extractor.ignoredDepth > 0 {
				extractor.ignoredDepth--
				continue
			}
			if !isWordprocessingML(current.Name) {
				continue
			}
			if current.Name.Local == "body" {
				if extractor.bodyDepth > 0 {
					extractor.bodyDepth--
				}
				continue
			}
			if extractor.bodyDepth == 0 {
				continue
			}
			switch current.Name.Local {
			case "p":
				extractor.finishParagraph()
			case "trPr":
				if extractor.rowProperties > 0 {
					extractor.rowProperties--
				}
			case "tcPr":
				if extractor.cellProperties > 0 {
					extractor.cellProperties--
				}
			case "tc":
				if extractor.tableDepth == 1 && !extractor.cellDeleted {
					extractor.rowCells = append(extractor.rowCells, strings.Join(extractor.cellParagraphs, " "))
				}
			case "tr":
				if extractor.tableDepth == 1 && !extractor.rowDeleted {
					extractor.addSegment(docxTableRow, strings.Join(extractor.rowCells, "，"))
				}
			case "tbl":
				if extractor.tableDepth > 0 {
					extractor.tableDepth--
				}
			}
		}
	}
}

func isIgnoredDOCXElement(name xml.Name) bool {
	if name.Space == markupCompatibility && name.Local == "Choice" {
		return true
	}
	if !isWordprocessingML(name) {
		return false
	}
	switch name.Local {
	case "del", "moveFrom", "drawing", "object", "pict", "txbxContent":
		return true
	default:
		return false
	}
}

func isWordprocessingML(name xml.Name) bool {
	return name.Space == transitionalWordprocessingML || name.Space == strictWordprocessingML
}

func (e *docxExtractor) finishParagraph() {
	if e.paragraph == nil {
		return
	}
	value := normalizeNarration(e.paragraph.text.String())
	if e.tableDepth > 0 {
		if value != "" && e.tableDepth == 1 {
			e.cellParagraphs = append(e.cellParagraphs, value)
		}
	} else if e.paragraph.list {
		e.addSegment(docxListItem, value)
	} else {
		e.addSegment(docxParagraph, value)
	}
	e.paragraph = nil
}

func (e *docxExtractor) addSegment(kind docxSegmentKind, value string) {
	value = strings.TrimSpace(value)
	if value != "" {
		e.segments = append(e.segments, docxSegment{kind: kind, text: value})
	}
}

func (e *docxExtractor) narration() string {
	var output strings.Builder
	for index, segment := range e.segments {
		if index > 0 {
			previous := e.segments[index-1]
			if previous.kind == segment.kind && (segment.kind == docxListItem || segment.kind == docxTableRow) {
				output.WriteByte('\n')
			} else {
				output.WriteString("\n\n")
			}
		}
		output.WriteString(segment.text)
	}
	return normalizeNarration(output.String())
}
