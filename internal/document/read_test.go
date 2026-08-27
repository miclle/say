package document

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"slices"
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

func TestReadSourceKeepsLocalDocumentBehavior(t *testing.T) {
	path := writeTestFile(t, "lesson.txt", []byte("first line\r\nsecond line"))

	name, text, err := ReadSource(context.Background(), path)
	if err != nil {
		t.Fatalf("ReadSource() error = %v", err)
	}
	if name != "lesson.txt" || text != "first line\nsecond line" {
		t.Fatalf("ReadSource() = %q, %q; want local document output", name, text)
	}
}

func TestReadSourceReportsTextDocumentStage(t *testing.T) {
	path := writeTestFile(t, "lesson.txt", []byte("plain text"))
	var stages []Stage

	name, text, err := ReadSourceWithProgress(context.Background(), path, func(stage Stage) {
		stages = append(stages, stage)
	})
	if err != nil {
		t.Fatalf("ReadSourceWithProgress() error = %v", err)
	}
	if name != "lesson.txt" || text != "plain text" {
		t.Fatalf("ReadSourceWithProgress() = %q, %q; want text document", name, text)
	}
	if want := []Stage{StageReadingDocument}; !slices.Equal(stages, want) {
		t.Fatalf("stages = %#v, want %#v", stages, want)
	}
}

func TestReadSourceReportsMarkdownStages(t *testing.T) {
	path := writeTestFile(t, "guide.md", []byte("# Guide\n\nReadable paragraph."))
	var stages []Stage

	name, text, err := ReadSourceWithProgress(context.Background(), path, func(stage Stage) {
		stages = append(stages, stage)
	})
	if err != nil {
		t.Fatalf("ReadSourceWithProgress() error = %v", err)
	}
	if name != "guide.md" || text != "Guide\n\nReadable paragraph." {
		t.Fatalf("ReadSourceWithProgress() = %q, %q; want narrated Markdown", name, text)
	}
	if want := []Stage{StageReadingDocument, StageParsingDocument}; !slices.Equal(stages, want) {
		t.Fatalf("stages = %#v, want %#v", stages, want)
	}
}

func TestReadSourceReportsWordDocumentStages(t *testing.T) {
	tests := []struct {
		name     string
		path     func(*testing.T) string
		wantName string
		wantText string
	}{
		{
			name: "DOCX extension is case insensitive",
			path: func(t *testing.T) string {
				return writeDOCXFixture(t, "Guide.DOCX", map[string]string{
					"word/document.xml": `<w:document xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main"><w:body><w:p><w:r><w:t>DOCX text</w:t></w:r></w:p></w:body></w:document>`,
				})
			},
			wantName: "Guide.DOCX",
			wantText: "DOCX text",
		},
		{
			name: "DOC extension is case insensitive",
			path: func(t *testing.T) string {
				return writeDOCFixture(t, "Legacy.DOC", docFixtureOptions{text: "Legacy text\r"})
			},
			wantName: "Legacy.DOC",
			wantText: "Legacy text",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var stages []Stage
			name, text, err := ReadSourceWithProgress(context.Background(), tt.path(t), func(stage Stage) {
				stages = append(stages, stage)
			})
			if err != nil {
				t.Fatalf("ReadSourceWithProgress() error = %v", err)
			}
			if name != tt.wantName || text != tt.wantText {
				t.Fatalf("ReadSourceWithProgress() = %q, %q; want %q, %q", name, text, tt.wantName, tt.wantText)
			}
			if want := []Stage{StageReadingDocument, StageParsingDocument}; !slices.Equal(stages, want) {
				t.Fatalf("stages = %#v, want %#v", stages, want)
			}
		})
	}
}

func TestReadSourceWordDocumentHonorsCanceledContext(t *testing.T) {
	path := writeDOCXFixture(t, "canceled.docx", map[string]string{
		"word/document.xml": `<w:document xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main"><w:body><w:p><w:r><w:t>text</w:t></w:r></w:p></w:body></w:document>`,
	})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, _, err := ReadSource(ctx, path)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("ReadSource() error = %v, want context.Canceled", err)
	}
}

func TestReadSourceReportsWebStages(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte(`<!doctype html><html><head><title>Loading article</title></head><body><article>
<p>This article paragraph contains enough prose for the real readability extractor to keep it as the primary content of the page.</p>
<p>A second paragraph verifies that the extracted result remains useful after both progress stages have been delivered.</p>
</article></body></html>`))
	}))
	defer server.Close()
	var stages []Stage

	name, text, err := ReadSourceWithProgress(context.Background(), server.URL, func(stage Stage) {
		stages = append(stages, stage)
	})
	if err != nil {
		t.Fatalf("ReadSourceWithProgress() error = %v", err)
	}
	if name != "Loading article" || !strings.Contains(text, "real readability extractor") {
		t.Fatalf("ReadSourceWithProgress() = %q, %q; want extracted article", name, text)
	}
	if want := []Stage{StageReadingWebPage, StageExtractingWebPage}; !slices.Equal(stages, want) {
		t.Fatalf("stages = %#v, want %#v", stages, want)
	}
}

func TestReadWebExtractsArticleTitleAndParagraphs(t *testing.T) {
	const firstParagraph = "The first paragraph contains enough original prose to identify the primary article body while navigation and scripts remain outside the spoken result."
	const secondParagraph = "The second paragraph stays separate so the player can preserve natural pauses before applying its existing maximum character limit."
	var userAgent string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		userAgent = r.Header.Get("User-Agent")
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte(`<!doctype html>
<html><head><title>Fallback title</title><meta property="og:title" content="Readable article"></head>
<body><nav>Hidden navigation</nav><article><h1>Readable article</h1>
<p>` + firstParagraph + `</p><p>` + secondParagraph + `</p>
<script>hiddenScript()</script></article></body></html>`))
	}))
	defer server.Close()

	name, text, err := ReadSource(context.Background(), server.URL)
	if err != nil {
		t.Fatalf("ReadSource() error = %v", err)
	}
	if name != "Readable article" {
		t.Fatalf("ReadSource() name = %q, want %q", name, "Readable article")
	}
	if !strings.Contains(text, firstParagraph+"\n\n"+secondParagraph) {
		t.Fatalf("ReadSource() text = %q, want paragraph boundary", text)
	}
	if strings.Contains(text, "Hidden navigation") || strings.Contains(text, "hiddenScript") {
		t.Fatalf("ReadSource() text = %q, want navigation and scripts removed", text)
	}
	if !strings.HasPrefix(userAgent, "say/") {
		t.Fatalf("User-Agent = %q, want say prefix", userAgent)
	}
}

func TestReadWebDecodesDeclaredCharset(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=iso-8859-1")
		_, _ = w.Write([]byte("<!doctype html><html><head><title>Caf\xe9 report</title></head><body><article>" +
			"<h1>Caf\xe9 report</h1>" +
			"<p>This Caf\xe9 report contains enough prose to remain readable after the declared response charset is converted to UTF-8.</p>" +
			"<p>A second paragraph confirms that accented article text reaches narration without replacement characters.</p>" +
			"</article></body></html>"))
	}))
	defer server.Close()

	name, text, err := ReadSource(context.Background(), server.URL)
	if err != nil {
		t.Fatalf("ReadSource() error = %v", err)
	}
	if name != "Café report" || !strings.Contains(text, "This Café report") {
		t.Fatalf("ReadSource() = %q, %q; want decoded ISO-8859-1 title and narration", name, text)
	}
	if strings.ContainsRune(name+text, '\ufffd') {
		t.Fatalf("ReadSource() = %q, %q; want no replacement characters", name, text)
	}
}

func TestReadWebRedactsPasswordFromErrors(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer server.Close()
	parsed, err := url.Parse(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	parsed.User = url.UserPassword("reader", "top-secret")

	_, _, err = ReadSource(context.Background(), parsed.String())
	if err == nil {
		t.Fatal("ReadSource() error = nil, want HTTP status failure")
	}
	if strings.Contains(err.Error(), "top-secret") {
		t.Fatalf("ReadSource() error = %q, want password redacted", err)
	}
	if !strings.Contains(err.Error(), "HTTP 503 Service Unavailable") {
		t.Fatalf("ReadSource() error = %q, want HTTP status", err)
	}
}

func TestReadWebRejectsUnusableResponses(t *testing.T) {
	tests := []struct {
		name        string
		status      int
		contentType string
		body        string
		wantErr     string
	}{
		{
			name:        "HTTP error",
			status:      http.StatusServiceUnavailable,
			contentType: "text/html",
			body:        "temporarily unavailable",
			wantErr:     "HTTP 503 Service Unavailable",
		},
		{
			name:        "non HTML",
			status:      http.StatusOK,
			contentType: "application/pdf",
			body:        "%PDF-1.7",
			wantErr:     `unsupported content type "application/pdf"`,
		},
		{
			name:        "oversized body",
			status:      http.StatusOK,
			contentType: "text/html",
			body:        strings.Repeat("x", (10<<20)+1),
			wantErr:     "response body exceeds 10485760 bytes",
		},
		{
			name:        "no readable content",
			status:      http.StatusOK,
			contentType: "text/html",
			body:        "<html><head><title>Empty</title></head><body><nav>menu</nav></body></html>",
			wantErr:     "web page has no readable content",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", tt.contentType)
				w.WriteHeader(tt.status)
				_, _ = w.Write([]byte(tt.body))
			}))
			defer server.Close()

			_, _, err := readWeb(context.Background(), server.URL, server.Client(), nil)
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("readWeb() error = %v, want containing %q", err, tt.wantErr)
			}
		})
	}
}

func TestReadSourceRejectsUnsupportedURLScheme(t *testing.T) {
	_, _, err := ReadSource(context.Background(), "ftp://example.com/article")
	if err == nil || !strings.Contains(err.Error(), `unsupported URL scheme "ftp"`) {
		t.Fatalf("ReadSource() error = %v, want unsupported scheme", err)
	}
}

func TestReadWebHonorsCanceledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, _, err := readWeb(ctx, "https://example.com/article", http.DefaultClient, nil)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("readWeb() error = %v, want context.Canceled", err)
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
