package terminal

import (
	"fmt"
	"io"
	"strings"
)

const (
	ansiReset = "\x1b[0m"
	ansiBold  = "\x1b[1m"
	ansiCyan  = "\x1b[36m"
	ansiGreen = "\x1b[32m"
	ansiRed   = "\x1b[31m"
)

// View renders an append-only terminal playback transcript.
type View struct {
	writer io.Writer
	color  bool
	title  string
	engine string
}

// New creates a terminal playback view.
func New(writer io.Writer, color bool, title, engine string) *View {
	return &View{writer: writer, color: color, title: title, engine: engine}
}

func (v *View) Start(total int) error {
	if _, err := fmt.Fprintf(v.writer, "%s  %s\n", v.style(ansiBold, "say"), safe(v.title)); err != nil {
		return err
	}
	_, err := fmt.Fprintf(v.writer, "%s  %s · %d speech %s\n\n", v.style(ansiCyan, "TTS"), safe(v.engine), total, unitWord(total))
	return err
}

func (v *View) Speaking(index, total int, text string) error {
	_, err := fmt.Fprintf(v.writer, "[%d/%d] %s %s\n", index+1, total, v.style(ansiCyan, "▶"), safe(text))
	return err
}

func (v *View) Spoken(_, _ int) error {
	_, err := fmt.Fprintf(v.writer, "      %s played\n", v.style(ansiGreen, "✓"))
	return err
}

func (v *View) Failed(_, _ int, playbackErr error) error {
	_, err := fmt.Fprintf(v.writer, "      %s %s\n", v.style(ansiRed, "✗"), safe(playbackErr.Error()))
	return err
}

func (v *View) Finish(total int) error {
	_, err := fmt.Fprintf(v.writer, "\n%s Finished %d speech %s.\n", v.style(ansiGreen, "✓"), total, unitWord(total))
	return err
}

func (v *View) style(code, text string) string {
	if !v.color {
		return text
	}
	return code + text + ansiReset
}

func unitWord(total int) string {
	if total == 1 {
		return "unit"
	}
	return "units"
}

func safe(text string) string {
	var escaped strings.Builder
	for _, r := range text {
		if r < 0x20 || r >= 0x7f && r <= 0x9f {
			fmt.Fprintf(&escaped, "\\u%04X", r)
			continue
		}
		escaped.WriteRune(r)
	}
	return escaped.String()
}
