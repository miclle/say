package terminal

import (
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/mattn/go-runewidth"
	"golang.org/x/term"
)

var loaderFrames = [...]string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}

// Loader renders an animated, single-row loading state.
type Loader struct {
	writer  io.Writer
	color   bool
	enabled bool
	started bool
	frame   int
	message string
	width   int
}

// NewLoader creates a loading renderer that is a no-op when disabled.
func NewLoader(writer io.Writer, color, enabled bool) *Loader {
	return &Loader{writer: writer, color: color, enabled: enabled, width: loaderTerminalWidth(writer)}
}

// Start displays the first loading frame and message.
func (l *Loader) Start(message string) error {
	if !l.enabled {
		return nil
	}
	l.started = true
	l.frame = 0
	l.message = message
	return l.render()
}

// Update replaces the loading message without advancing the frame.
func (l *Loader) Update(message string) error {
	if !l.enabled {
		return nil
	}
	if !l.started {
		return l.Start(message)
	}
	l.message = message
	return l.render()
}

// Advance rotates to the next loading frame.
func (l *Loader) Advance() error {
	if !l.enabled || !l.started {
		return nil
	}
	l.frame = (l.frame + 1) % len(loaderFrames)
	return l.render()
}

// Finish clears the loading row.
func (l *Loader) Finish() error {
	if !l.enabled || !l.started {
		return nil
	}
	l.started = false
	_, err := fmt.Fprint(l.writer, "\r\x1b[2K")
	return err
}

func (l *Loader) render() error {
	frame := loaderFrames[l.frame]
	line := fmt.Sprintf("%s %s…", frame, safe(l.message))
	if l.width > 0 {
		line = runewidth.Truncate(line, l.width, "…")
	}
	if l.color && strings.HasPrefix(line, frame) {
		line = ansiCyan + frame + ansiReset + strings.TrimPrefix(line, frame)
	}
	_, err := fmt.Fprintf(l.writer, "\r\x1b[2K%s", line)
	return err
}

func loaderTerminalWidth(writer io.Writer) int {
	if provider, ok := writer.(terminalSizer); ok {
		width, _, err := provider.TerminalSize()
		if err == nil && width > 0 {
			return width
		}
	}
	if file, ok := writer.(*os.File); ok {
		width, _, err := term.GetSize(int(file.Fd()))
		if err == nil && width > 0 {
			return width
		}
	}
	return 0
}
