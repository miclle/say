package document

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf8"
)

// Stage identifies one user-visible source loading phase.
type Stage uint8

// Source loading stages reported by ReadSourceWithProgress.
const (
	StageReadingDocument Stage = iota
	StageParsingDocument
	StageReadingWebPage
	StageExtractingWebPage
)

// ProgressFunc receives source loading stage changes.
type ProgressFunc func(Stage)

// Read loads and validates a supported local document.
func Read(path string) (name string, text string, err error) {
	return readLocal(context.Background(), path, nil)
}

func readLocal(ctx context.Context, path string, progress ProgressFunc) (name string, text string, err error) {
	if err := ctx.Err(); err != nil {
		return "", "", err
	}
	report(progress, StageReadingDocument)
	info, err := os.Stat(path)
	if err != nil {
		return "", "", fmt.Errorf("open document: %w", err)
	}
	if !info.Mode().IsRegular() {
		return "", "", fmt.Errorf("open document %q: not a regular file", path)
	}
	extension := strings.ToLower(filepath.Ext(path))
	if extension == ".docx" || extension == ".doc" {
		report(progress, StageParsingDocument)
		if extension == ".docx" {
			text, err = readDOCX(ctx, path)
		} else {
			text, err = readDOC(ctx, path)
		}
		if err != nil {
			return "", "", err
		}
		return filepath.Base(path), text, nil
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return "", "", fmt.Errorf("read document: %w", err)
	}
	if !utf8.Valid(data) {
		return "", "", fmt.Errorf("read document %q: content is not valid UTF-8", path)
	}

	text = strings.TrimPrefix(string(data), "\ufeff")
	text = strings.ReplaceAll(text, "\r\n", "\n")
	text = strings.ReplaceAll(text, "\r", "\n")
	if extension == ".md" || extension == ".markdown" {
		report(progress, StageParsingDocument)
		text = markdownToNarration(text)
	}
	if strings.TrimSpace(text) == "" {
		return "", "", fmt.Errorf("read document %q: document is empty", path)
	}

	return filepath.Base(path), text, nil
}

func report(progress ProgressFunc, stage Stage) {
	if progress != nil {
		progress(stage)
	}
}
