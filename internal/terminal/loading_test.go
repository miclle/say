package terminal

import (
	"bytes"
	"errors"
	"strings"
	"testing"

	"github.com/mattn/go-runewidth"
)

func TestLoaderAnimatesUpdatesAndClearsOneRow(t *testing.T) {
	var output bytes.Buffer
	loader := NewLoader(&output, false, true)

	if err := loader.Start("Reading webpage"); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	if err := loader.Advance(); err != nil {
		t.Fatalf("Advance() error = %v", err)
	}
	if err := loader.Update("Extracting webpage content"); err != nil {
		t.Fatalf("Update() error = %v", err)
	}
	if err := loader.Finish(); err != nil {
		t.Fatalf("Finish() error = %v", err)
	}

	want := "\r\x1b[2K⠋ Reading webpage…" +
		"\r\x1b[2K⠙ Reading webpage…" +
		"\r\x1b[2K⠙ Extracting webpage content…" +
		"\r\x1b[2K"
	if got := output.String(); got != want {
		t.Fatalf("loader output = %q, want %q", got, want)
	}
}

func TestLoaderDisabledWritesNothing(t *testing.T) {
	var output bytes.Buffer
	loader := NewLoader(&output, false, false)

	if err := loader.Start("Reading file"); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	if err := loader.Advance(); err != nil {
		t.Fatalf("Advance() error = %v", err)
	}
	if err := loader.Update("Parsing document"); err != nil {
		t.Fatalf("Update() error = %v", err)
	}
	if err := loader.Finish(); err != nil {
		t.Fatalf("Finish() error = %v", err)
	}
	if output.Len() != 0 {
		t.Fatalf("disabled loader output = %q, want empty", output.String())
	}
}

func TestLoaderEscapesStageMessage(t *testing.T) {
	var output bytes.Buffer
	loader := NewLoader(&output, false, true)

	if err := loader.Start("Reading\x1b[2J webpage"); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	if got := output.String(); !strings.Contains(got, `Reading\u001B[2J webpage`) {
		t.Fatalf("loader output = %q, want escaped message", got)
	}
}

func TestLoaderReturnsOutputFailure(t *testing.T) {
	wantErr := errors.New("output closed")
	loader := NewLoader(errorWriter{err: wantErr}, false, true)

	if err := loader.Start("Reading file"); !errors.Is(err, wantErr) {
		t.Fatalf("Start() error = %v, want %v", err, wantErr)
	}
}

func TestLoaderFitsWithinTerminalWidth(t *testing.T) {
	output := &sizedLoaderBuffer{width: 18}
	loader := NewLoader(output, false, true)

	if err := loader.Start("Extracting webpage content"); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	rendered := strings.TrimPrefix(output.String(), "\r\x1b[2K")
	if width := runewidth.StringWidth(rendered); width > output.width {
		t.Fatalf("loader width = %d, want at most %d: %q", width, output.width, rendered)
	}
}

type sizedLoaderBuffer struct {
	bytes.Buffer
	width int
}

func (b *sizedLoaderBuffer) TerminalSize() (int, int, error) {
	return b.width, 24, nil
}
