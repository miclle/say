package terminal

import (
	"fmt"
	"io"
	"strings"
	"time"
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
	writer   io.Writer
	color    bool
	title    string
	engine   string
	header   bool
	controls bool
	started  bool
}

// New creates a terminal playback view.
func New(writer io.Writer, color bool, title, engine string) *View {
	return &View{writer: writer, color: color, title: title, engine: engine, controls: true}
}

// SetControls configures whether interactive shortcut help is rendered.
func (v *View) SetControls(enabled bool) {
	v.controls = enabled
}

// Preparing announces that ordered audio preparation is starting.
func (v *View) Preparing(total int) error {
	if err := v.writeHeader(total); err != nil {
		return err
	}
	_, err := fmt.Fprintf(v.writer, "… preparing audio · 0/%d ready\n", total)
	return err
}

// Prepared renders preparation progress until playback starts.
func (v *View) Prepared(prepared, total int) error {
	if v.started {
		return nil
	}
	_, err := fmt.Fprintf(v.writer, "… ready to play · %d/%d prepared\n", prepared, total)
	return err
}

func (v *View) Start(total int) error {
	if err := v.writeHeader(total); err != nil {
		return err
	}
	if v.controls {
		if _, err := fmt.Fprintln(v.writer, "Space 播放/暂停 · ← 回退 5s · → 快进 5s"); err != nil {
			return err
		}
	}
	_, err := fmt.Fprintln(v.writer)
	if err == nil {
		v.started = true
	}
	return err
}

func (v *View) writeHeader(total int) error {
	if v.header {
		return nil
	}
	if _, err := fmt.Fprintf(v.writer, "%s  %s\n", v.style(ansiBold, "say"), safe(v.title)); err != nil {
		return err
	}
	if _, err := fmt.Fprintf(v.writer, "%s  %s · %d speech %s\n\n", v.style(ansiCyan, "TTS"), safe(v.engine), total, unitWord(total)); err != nil {
		return err
	}
	v.header = true
	return nil
}

func (v *View) Speaking(index, total int, text string) error {
	_, err := fmt.Fprintf(v.writer, "[%d/%d] %s %s\n", index+1, total, v.style(ansiCyan, "▶"), safe(text))
	return err
}

func (v *View) Spoken(_, _ int) error {
	_, err := fmt.Fprintf(v.writer, "      %s played\n", v.style(ansiGreen, "✓"))
	return err
}

func (v *View) Paused(_, _ int) error {
	_, err := fmt.Fprintln(v.writer, "      ⏸ paused")
	return err
}

func (v *View) Resumed(_, _ int) error {
	_, err := fmt.Fprintf(v.writer, "      %s resumed\n", v.style(ansiCyan, "▶"))
	return err
}

// Buffering reports that playback reached audio which is still being prepared.
func (v *View) Buffering(index, total int) error {
	_, err := fmt.Fprintf(v.writer, "      … buffering speech unit %d/%d\n", index+1, total)
	return err
}

func (v *View) Seeked(_, _ int, delta, position, duration time.Duration, complete bool) error {
	icon := "↶"
	if delta > 0 {
		icon = "↷"
	}
	suffix := ""
	if !complete {
		suffix = "+"
	}
	_, err := fmt.Fprintf(v.writer, "      %s %s · %s / %s%s\n", icon, signedSeconds(delta), clock(position), clock(duration), suffix)
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

func signedSeconds(duration time.Duration) string {
	return fmt.Sprintf("%+ds", int64(duration/time.Second))
}

func clock(duration time.Duration) string {
	if duration < 0 {
		duration = 0
	}
	seconds := int64(duration / time.Second)
	hours := seconds / 3600
	minutes := seconds % 3600 / 60
	seconds %= 60
	if hours > 0 {
		return fmt.Sprintf("%d:%02d:%02d", hours, minutes, seconds)
	}
	return fmt.Sprintf("%02d:%02d", minutes, seconds)
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
