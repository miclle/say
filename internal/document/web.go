package document

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/url"
	"strings"
	"time"

	readability "github.com/miclle/readability.go"
	"golang.org/x/net/html"
	"golang.org/x/net/html/charset"
)

const (
	webRequestTimeout = 15 * time.Second
	maxWebBodyBytes   = 10 << 20
	webUserAgent      = "say/1.0"
)

type httpDoer interface {
	Do(*http.Request) (*http.Response, error)
}

// ReadSource loads either a local document or an HTTP(S) web article.
func ReadSource(ctx context.Context, source string) (name string, text string, err error) {
	return ReadSourceWithProgress(ctx, source, nil)
}

// ReadSourceWithProgress loads a source and reports its reading and parsing stages.
func ReadSourceWithProgress(ctx context.Context, source string, progress ProgressFunc) (name string, text string, err error) {
	parsed, parseErr := url.Parse(source)
	if parseErr == nil && (parsed.Scheme == "http" || parsed.Scheme == "https") {
		if parsed.Host == "" {
			return "", "", fmt.Errorf("open web page %q: URL host is required", parsed.Redacted())
		}
		return readWeb(ctx, source, &http.Client{Timeout: webRequestTimeout}, progress)
	}
	if parseErr == nil && parsed.Scheme != "" && strings.Contains(source, "://") {
		return "", "", fmt.Errorf("open web page %q: unsupported URL scheme %q", parsed.Redacted(), parsed.Scheme)
	}
	return readLocal(ctx, source, progress)
}

func readWeb(ctx context.Context, source string, client httpDoer, progress ProgressFunc) (name string, text string, err error) {
	displaySource := redactedWebSource(source)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, source, nil)
	if err != nil {
		return "", "", fmt.Errorf("open web page %q: %w", displaySource, err)
	}
	req.Header.Set("User-Agent", webUserAgent)

	report(progress, StageReadingWebPage)
	response, err := client.Do(req)
	if err != nil {
		return "", "", fmt.Errorf("fetch web page %q: %w", displaySource, err)
	}
	defer response.Body.Close()

	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		return "", "", fmt.Errorf("fetch web page %q: HTTP %d %s", displaySource, response.StatusCode, http.StatusText(response.StatusCode))
	}
	contentType := response.Header.Get("Content-Type")
	if !isHTMLContentType(contentType) {
		return "", "", fmt.Errorf("fetch web page %q: unsupported content type %q", displaySource, contentType)
	}

	data, err := io.ReadAll(io.LimitReader(response.Body, maxWebBodyBytes+1))
	if err != nil {
		return "", "", fmt.Errorf("read web page %q: %w", displaySource, err)
	}
	if len(data) > maxWebBodyBytes {
		return "", "", fmt.Errorf("read web page %q: response body exceeds %d bytes", displaySource, maxWebBodyBytes)
	}

	pageURL := source
	if response.Request != nil && response.Request.URL != nil {
		pageURL = response.Request.URL.String()
	}
	report(progress, StageExtractingWebPage)
	decoded, err := charset.NewReader(bytes.NewReader(data), contentType)
	if err != nil {
		return "", "", fmt.Errorf("decode web page %q: %w", displaySource, err)
	}
	article, err := readability.FromReader(decoded, pageURL, nil)
	if err != nil {
		return "", "", fmt.Errorf("extract readable content from %q: %w", displaySource, err)
	}
	narration := articleHTMLToNarration(article.Content)
	if strings.TrimSpace(narration) == "" {
		return "", "", fmt.Errorf("extract readable content from %q: web page has no readable content", displaySource)
	}

	title := strings.TrimSpace(article.Title)
	if title == "" {
		if parsed, parseErr := url.Parse(pageURL); parseErr == nil {
			title = parsed.Hostname()
		}
	}
	if title == "" {
		title = source
	}
	return title, narration, nil
}

func redactedWebSource(source string) string {
	parsed, err := url.Parse(source)
	if err != nil {
		return "<invalid URL>"
	}
	return parsed.Redacted()
}

func isHTMLContentType(value string) bool {
	if value == "" {
		return true
	}
	mediaType, _, err := mime.ParseMediaType(value)
	if err != nil {
		return false
	}
	return mediaType == "text/html" || mediaType == "application/xhtml+xml"
}

func articleHTMLToNarration(source string) string {
	tokenizer := html.NewTokenizer(strings.NewReader(source))
	var output strings.Builder
	hiddenDepth := 0

	for {
		tokenType := tokenizer.Next()
		if tokenType == html.ErrorToken {
			return normalizeNarration(output.String())
		}
		token := tokenizer.Token()

		switch tokenType {
		case html.TextToken:
			if hiddenDepth == 0 {
				output.WriteString(token.Data)
			}
		case html.StartTagToken:
			if isHiddenHTMLTag(token.Data) {
				hiddenDepth++
			} else if hiddenDepth == 0 && token.Data == "br" {
				output.WriteByte('\n')
			}
		case html.EndTagToken:
			if isHiddenHTMLTag(token.Data) {
				if hiddenDepth > 0 {
					hiddenDepth--
				}
				continue
			}
			if hiddenDepth > 0 {
				continue
			}
			switch {
			case isArticleParagraphTag(token.Data):
				output.WriteString("\n\n")
			case token.Data == "li" || token.Data == "tr":
				output.WriteByte('\n')
			case token.Data == "td" || token.Data == "th":
				output.WriteString("，")
			}
		case html.SelfClosingTagToken:
			if hiddenDepth == 0 && token.Data == "br" {
				output.WriteByte('\n')
			}
		}
	}
}

func isArticleParagraphTag(tag string) bool {
	switch tag {
	case "address", "article", "aside", "blockquote", "details", "div", "dl", "figure", "footer",
		"h1", "h2", "h3", "h4", "h5", "h6", "header", "main", "p", "pre", "section", "summary":
		return true
	default:
		return false
	}
}
