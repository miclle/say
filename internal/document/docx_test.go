package document

import (
	"archive/zip"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestReadDOCXExtractsNarratableMainDocumentContent(t *testing.T) {
	path := writeDOCXFixture(t, "guide.docx", map[string]string{
		"[Content_Types].xml": `<?xml version="1.0"?><Types xmlns="http://schemas.openxmlformats.org/package/2006/content-types"/>`,
		"word/document.xml": `<?xml version="1.0" encoding="UTF-8"?>
<w:document xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main">
  <w:body>
    <w:p><w:r><w:t>Guide</w:t></w:r></w:p>
    <w:p><w:hyperlink><w:r><w:t>Read the visible link.</w:t><w:br/><w:t>Next line.</w:t></w:r></w:hyperlink></w:p>
    <w:p><w:pPr><w:numPr><w:numId w:val="1"/></w:numPr></w:pPr><w:r><w:t>First item</w:t></w:r></w:p>
    <w:p><w:pPr><w:numPr><w:numId w:val="1"/></w:numPr></w:pPr><w:r><w:t>Second item</w:t></w:r></w:p>
    <w:p><w:del><w:r><w:delText>Removed text</w:delText></w:r></w:del><w:ins><w:r><w:t>Added text</w:t></w:r></w:ins></w:p>
    <w:tbl>
      <w:tr><w:tc><w:p><w:r><w:t>Name</w:t></w:r></w:p></w:tc><w:tc><w:p><w:r><w:t>Status</w:t></w:r></w:p></w:tc></w:tr>
      <w:tr><w:tc><w:p><w:r><w:t>ready</w:t></w:r></w:p></w:tc><w:tc><w:p><w:r><w:t>yes</w:t></w:r></w:p></w:tc></w:tr>
    </w:tbl>
  </w:body>
</w:document>`,
	})

	text, err := readDOCX(context.Background(), path)
	if err != nil {
		t.Fatalf("readDOCX() error = %v", err)
	}
	want := "Guide\n\n" +
		"Read the visible link.\nNext line.\n\n" +
		"First item\nSecond item\n\n" +
		"Added text\n\n" +
		"Name，Status\nready，yes"
	if text != want {
		t.Fatalf("readDOCX() text = %q, want %q", text, want)
	}
}

func TestReadDOCXSupportsStrictWordprocessingML(t *testing.T) {
	path := writeDOCXFixture(t, "strict.docx", map[string]string{
		"word/document.xml": `<?xml version="1.0"?><w:document xmlns:w="http://purl.oclc.org/ooxml/wordprocessingml/main"><w:body><w:p><w:r><w:t>Strict text</w:t></w:r></w:p></w:body></w:document>`,
	})

	text, err := readDOCX(context.Background(), path)
	if err != nil {
		t.Fatalf("readDOCX() error = %v", err)
	}
	if text != "Strict text" {
		t.Fatalf("readDOCX() text = %q, want %q", text, "Strict text")
	}
}

func TestReadDOCXUsesFallbackAndOmitsMovedEmbeddedContent(t *testing.T) {
	path := writeDOCXFixture(t, "filtered.docx", map[string]string{
		"word/document.xml": `<?xml version="1.0"?>
<w:document xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main"
 xmlns:mc="http://schemas.openxmlformats.org/markup-compatibility/2006">
  <w:body>
    <w:p>
      <w:r><w:t>Before </w:t></w:r>
      <w:moveFrom><w:r><w:t>moved away</w:t></w:r></w:moveFrom>
      <w:drawing><w:txbxContent><w:p><w:r><w:t>textbox</w:t></w:r></w:p></w:txbxContent></w:drawing>
		<mc:AlternateContent>
			<mc:Choice Requires="w14"><w:r><w:t>unsupported choice</w:t></w:r></mc:Choice>
			<mc:Choice Requires="w"><w:r><w:t>second choice</w:t></w:r></mc:Choice>
			<mc:Fallback><w:r><w:t>fallback</w:t></w:r></mc:Fallback>
		</mc:AlternateContent>
      <w:r><w:t> after.</w:t></w:r>
    </w:p>
  </w:body>
</w:document>`,
	})

	text, err := readDOCX(context.Background(), path)
	if err != nil {
		t.Fatalf("readDOCX() error = %v", err)
	}
	if text != "Before fallback after." {
		t.Fatalf("readDOCX() text = %q, want %q", text, "Before fallback after.")
	}
}

func TestReadDOCXOmitsDeletedRowsAndCells(t *testing.T) {
	path := writeDOCXFixture(t, "deleted-table-content.docx", map[string]string{
		"word/document.xml": `<?xml version="1.0"?>
<w:document xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main">
  <w:body>
    <w:tbl>
      <w:tr><w:trPr><w:del/></w:trPr><w:tc><w:p><w:r><w:t>deleted row</w:t></w:r></w:p></w:tc></w:tr>
      <w:tr>
        <w:tc><w:tcPr><w:cellDel/></w:tcPr><w:p><w:r><w:t>deleted cell</w:t></w:r></w:p></w:tc>
        <w:tc><w:p><w:r><w:t>kept cell</w:t></w:r></w:p></w:tc>
      </w:tr>
    </w:tbl>
  </w:body>
</w:document>`,
	})

	text, err := readDOCX(context.Background(), path)
	if err != nil {
		t.Fatalf("readDOCX() error = %v", err)
	}
	if text != "kept cell" {
		t.Fatalf("readDOCX() text = %q, want %q", text, "kept cell")
	}
}

func TestReadDOCXRejectsUnusablePackages(t *testing.T) {
	tests := []struct {
		name    string
		path    func(*testing.T) string
		wantErr string
	}{
		{
			name: "not a ZIP package",
			path: func(t *testing.T) string {
				return writeTestFile(t, "broken.docx", []byte("not a ZIP package"))
			},
			wantErr: "open DOCX package",
		},
		{
			name: "missing main document",
			path: func(t *testing.T) string {
				return writeDOCXFixture(t, "missing.docx", map[string]string{
					"[Content_Types].xml": `<Types xmlns="http://schemas.openxmlformats.org/package/2006/content-types"/>`,
				})
			},
			wantErr: "word/document.xml is missing",
		},
		{
			name: "malformed XML",
			path: func(t *testing.T) string {
				return writeDOCXFixture(t, "malformed.docx", map[string]string{
					"word/document.xml": `<w:document xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main"><w:body><w:p>`,
				})
			},
			wantErr: "parse DOCX main document",
		},
		{
			name: "oversized main document",
			path: func(t *testing.T) string {
				return writeDOCXFixture(t, "large.docx", map[string]string{
					"word/document.xml": strings.Repeat("x", (16<<20)+1),
				})
			},
			wantErr: "main document exceeds 16777216 bytes",
		},
		{
			name: "empty document",
			path: func(t *testing.T) string {
				return writeDOCXFixture(t, "empty.docx", map[string]string{
					"word/document.xml": `<w:document xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main"><w:body><w:p/></w:body></w:document>`,
				})
			},
			wantErr: "document is empty",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := readDOCX(context.Background(), tt.path(t))
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("readDOCX() error = %v, want containing %q", err, tt.wantErr)
			}
		})
	}
}

func TestReadDOCXHonorsCanceledContext(t *testing.T) {
	path := writeDOCXFixture(t, "canceled.docx", map[string]string{
		"word/document.xml": `<w:document xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main"><w:body><w:p><w:r><w:t>text</w:t></w:r></w:p></w:body></w:document>`,
	})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := readDOCX(ctx, path)
	if err != context.Canceled {
		t.Fatalf("readDOCX() error = %v, want context.Canceled", err)
	}
}

func writeDOCXFixture(t *testing.T, name string, entries map[string]string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	file, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	archive := zip.NewWriter(file)
	for entryName, content := range entries {
		entry, err := archive.Create(entryName)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := entry.Write([]byte(content)); err != nil {
			t.Fatal(err)
		}
	}
	if err := archive.Close(); err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	return path
}
