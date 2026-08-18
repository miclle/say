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

func TestReadConvertsMarkdownToNarration(t *testing.T) {
	path := writeTestFile(t, "guide.md", []byte(`---
title: Hidden metadata
---
# 安装 *say*

这是 **正文**，参见 [使用说明](https://example.com/docs)。

- 第一项
- 第二项，使用 `+"`--provider edge`"+`。

> 注意：不要朗读标记。

`+"```sh"+`
say notes.md
`+"```"+`

![播放器截图](player.png)
`))

	name, text, err := Read(path)
	if err != nil {
		t.Fatalf("Read() error = %v", err)
	}
	if name != "guide.md" {
		t.Fatalf("Read() name = %q, want %q", name, "guide.md")
	}
	want := "安装 say\n\n" +
		"这是 正文，参见 使用说明。\n\n" +
		"第一项\n" +
		"第二项，使用 --provider edge。\n\n" +
		"注意：不要朗读标记。\n\n" +
		"播放器截图"
	if text != want {
		t.Fatalf("Read() text = %q, want %q", text, want)
	}
}

func TestReadConvertsGFMStructuresToNarration(t *testing.T) {
	path := writeTestFile(t, "providers.markdown", []byte(`## Providers

| Provider | Status |
| :--- | ---: |
| system | ready |
| edge | experimental |

- [x] **System**
- [ ] ~~Legacy~~

Visit <https://example.com> and press <kbd>Space</kbd>.
`))

	_, text, err := Read(path)
	if err != nil {
		t.Fatalf("Read() error = %v", err)
	}
	want := "Providers\n\n" +
		"Provider，Status\n" +
		"system，ready\n" +
		"edge，experimental\n\n" +
		"System\n" +
		"Legacy\n\n" +
		"Visit and press Space."
	if text != want {
		t.Fatalf("Read() text = %q, want %q", text, want)
	}
}

func TestReadPreservesVisibleTextFromHTMLBlocks(t *testing.T) {
	path := writeTestFile(t, "details.md", []byte(`# Notes

<details>
<summary>Deployment notes</summary>
<p>Use <strong>system</strong> TTS &amp; keep prose.</p>
<script>window.secret()</script>
<style>.hidden { display: none }</style>
<!-- internal note -->
<div>Second line<br>continues.</div>
</details>
`))

	_, text, err := Read(path)
	if err != nil {
		t.Fatalf("Read() error = %v", err)
	}
	want := "Notes\n\n" +
		"Deployment notes\n" +
		"Use system TTS & keep prose.\n" +
		"Second line\n" +
		"continues."
	if text != want {
		t.Fatalf("Read() text = %q, want %q", text, want)
	}
}

func TestReadKeepsProseCodeBlocksAndSkipsStructuredCode(t *testing.T) {
	path := writeTestFile(t, "architecture.md", []byte("# Architecture\n\n"+
		"Use `PUBLIC` prices.\n\n"+
		"```text\nSupplier plus model\n```\n\n"+
		"```\nraw syntax\n```\n\n"+
		"```mermaid\nflowchart LR\nA --> B\n```\n\n"+
		"```go\nfmt.Println(\"not narration\")\n```\n"))

	_, text, err := Read(path)
	if err != nil {
		t.Fatalf("Read() error = %v", err)
	}
	want := "Architecture\n\nUse PUBLIC prices.\n\nSupplier plus model"
	if text != want {
		t.Fatalf("Read() text = %q, want %q", text, want)
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
