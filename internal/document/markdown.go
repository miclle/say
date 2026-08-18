package document

import (
	"strings"

	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/extension"
	extensionast "github.com/yuin/goldmark/extension/ast"
	"github.com/yuin/goldmark/text"
	"github.com/yuin/goldmark/util"
	"golang.org/x/net/html"
)

func markdownToNarration(markdown string) string {
	source := []byte(stripMarkdownFrontMatter(markdown))
	document := goldmark.New(goldmark.WithExtensions(extension.GFM)).Parser().Parse(text.NewReader(source))

	var output strings.Builder
	_ = ast.Walk(document, func(node ast.Node, entering bool) (ast.WalkStatus, error) {
		switch current := node.(type) {
		case *ast.Text:
			if entering {
				writeMarkdownText(&output, current.Value(source))
				if current.SoftLineBreak() || current.HardLineBreak() {
					output.WriteByte(' ')
				}
			}
		case *ast.String:
			if entering {
				writeMarkdownText(&output, current.Value)
			}
		case *ast.CodeSpan:
			if entering {
				for child := current.FirstChild(); child != nil; child = child.NextSibling() {
					value := child.(*ast.Text).Value(source)
					output.WriteString(strings.TrimSuffix(string(value), "\n"))
					if len(value) > 0 && value[len(value)-1] == '\n' {
						output.WriteByte(' ')
					}
				}
				return ast.WalkSkipChildren, nil
			}
		case *ast.CodeBlock:
			if entering {
				return ast.WalkSkipChildren, nil
			}
			output.WriteString("\n\n")
		case *ast.FencedCodeBlock:
			if entering {
				if isNarratableCodeLanguage(string(current.Language(source))) {
					output.Write(current.Lines().Value(source))
				}
				return ast.WalkSkipChildren, nil
			}
			output.WriteString("\n\n")
		case *ast.AutoLink, *ast.RawHTML:
			if entering {
				return ast.WalkSkipChildren, nil
			}
		case *ast.HTMLBlock:
			if entering {
				output.WriteString(visibleHTMLText(htmlBlockSource(current, source)))
				output.WriteString("\n\n")
				return ast.WalkSkipChildren, nil
			}
		case *extensionast.TableCell:
			if !entering && current.NextSibling() != nil {
				output.WriteString("，")
			}
		case *extensionast.TableHeader, *extensionast.TableRow:
			if !entering {
				output.WriteByte('\n')
			}
		case *extensionast.Table:
			if !entering {
				output.WriteString("\n\n")
			}
		case *ast.TextBlock:
			if !entering {
				output.WriteByte('\n')
			}
		case *ast.List:
			if !entering {
				output.WriteByte('\n')
			}
		case *ast.Paragraph:
			if !entering {
				if hasAncestor(current, ast.KindListItem) {
					output.WriteByte('\n')
				} else {
					output.WriteString("\n\n")
				}
			}
		case *ast.Heading:
			if !entering {
				output.WriteString("\n\n")
			}
		}
		return ast.WalkContinue, nil
	})

	return normalizeNarration(output.String())
}

func htmlBlockSource(block *ast.HTMLBlock, source []byte) string {
	var value strings.Builder
	for index := range block.Lines().Len() {
		segment := block.Lines().At(index)
		value.Write(segment.Value(source))
	}
	if block.HasClosure() {
		value.Write(block.ClosureLine.Value(source))
	}
	return value.String()
}

func visibleHTMLText(source string) string {
	tokenizer := html.NewTokenizer(strings.NewReader(source))
	var output strings.Builder
	pendingBreak := false
	hiddenTag := ""

	for {
		tokenType := tokenizer.Next()
		if tokenType == html.ErrorToken {
			return output.String()
		}
		token := tokenizer.Token()

		switch tokenType {
		case html.TextToken:
			if hiddenTag != "" || strings.TrimSpace(token.Data) == "" {
				continue
			}
			if pendingBreak && output.Len() > 0 {
				output.WriteByte('\n')
			}
			output.WriteString(token.Data)
			pendingBreak = false
		case html.StartTagToken:
			if hiddenTag != "" {
				continue
			}
			if isHiddenHTMLTag(token.Data) {
				hiddenTag = token.Data
				continue
			}
			if isHTMLBlockBreak(token.Data) {
				pendingBreak = true
			}
		case html.EndTagToken:
			if hiddenTag != "" {
				if token.Data == hiddenTag {
					hiddenTag = ""
					pendingBreak = true
				}
				continue
			}
			if isHTMLBlockBreak(token.Data) {
				pendingBreak = true
			}
		case html.SelfClosingTagToken:
			if hiddenTag == "" && isHTMLBlockBreak(token.Data) {
				pendingBreak = true
			}
		}
	}
}

func isHiddenHTMLTag(tag string) bool {
	switch tag {
	case "script", "style", "template":
		return true
	default:
		return false
	}
}

func isHTMLBlockBreak(tag string) bool {
	switch tag {
	case "address", "article", "aside", "blockquote", "br", "details", "div", "dl", "dt", "dd",
		"fieldset", "figcaption", "figure", "footer", "form", "h1", "h2", "h3", "h4", "h5", "h6",
		"header", "hr", "li", "main", "nav", "ol", "p", "pre", "section", "summary", "table",
		"tbody", "td", "tfoot", "th", "thead", "tr", "ul":
		return true
	default:
		return false
	}
}

func isNarratableCodeLanguage(language string) bool {
	switch strings.ToLower(strings.TrimSpace(language)) {
	case "text", "plaintext":
		return true
	default:
		return false
	}
}

func hasAncestor(node ast.Node, kind ast.NodeKind) bool {
	for parent := node.Parent(); parent != nil; parent = parent.Parent() {
		if parent.Kind() == kind {
			return true
		}
	}
	return false
}

func stripMarkdownFrontMatter(markdown string) string {
	lines := strings.Split(markdown, "\n")
	if len(lines) == 0 || strings.TrimSpace(lines[0]) != "---" {
		return markdown
	}
	for index := 1; index < len(lines); index++ {
		line := strings.TrimSpace(lines[index])
		if line == "---" || line == "..." {
			return strings.Join(lines[index+1:], "\n")
		}
	}
	return markdown
}

func writeMarkdownText(output *strings.Builder, value []byte) {
	value = util.UnescapePunctuations(value)
	value = util.ResolveNumericReferences(value)
	value = util.ResolveEntityNames(value)
	output.Write(value)
}

func normalizeNarration(value string) string {
	lines := strings.Split(value, "\n")
	normalized := make([]string, 0, len(lines))
	for _, line := range lines {
		line = strings.Join(strings.Fields(line), " ")
		if line == "" {
			if len(normalized) > 0 && normalized[len(normalized)-1] != "" {
				normalized = append(normalized, "")
			}
			continue
		}
		normalized = append(normalized, line)
	}
	return strings.TrimSpace(strings.Join(normalized, "\n"))
}
