package document

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestReadNormalizesUTF8TextDocument(t *testing.T) {
	path := writeTestFile(t, "lesson.txt", []byte("\xef\xbb\xbf第一句。\r\nSecond sentence.\r"))

	name, text, err := Read(path)
	if err != nil {
		t.Fatalf("Read() error = %v", err)
	}
	if name != "lesson.txt" {
		t.Fatalf("Read() name = %q, want %q", name, "lesson.txt")
	}
	if text != "第一句。\nSecond sentence.\n" {
		t.Fatalf("Read() text = %q", text)
	}
}

func TestReadRejectsUnusableDocuments(t *testing.T) {
	tests := []struct {
		name    string
		content []byte
		wantErr string
	}{
		{name: "empty", content: nil, wantErr: "document is empty"},
		{name: "whitespace", content: []byte(" \n\t"), wantErr: "document is empty"},
		{name: "invalid UTF-8", content: []byte{0xff, 0xfe}, wantErr: "not valid UTF-8"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := writeTestFile(t, "bad.txt", tt.content)
			_, _, err := Read(path)
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("Read() error = %v, want containing %q", err, tt.wantErr)
			}
		})
	}
}

func TestReadRejectsDirectory(t *testing.T) {
	_, _, err := Read(t.TempDir())
	if err == nil || !strings.Contains(err.Error(), "not a regular file") {
		t.Fatalf("Read() error = %v, want non-regular-file error", err)
	}
}

func writeTestFile(t *testing.T, name string, content []byte) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, content, 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}
