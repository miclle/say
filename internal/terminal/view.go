package terminal

import (
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/clipperhouse/uax29/v2/graphemes"
	"github.com/mattn/go-runewidth"
	"github.com/miclle/say/internal/textchunk"
	"golang.org/x/term"
)

const (
	ansiReset        = "\x1b[0m"
	ansiBold         = "\x1b[1m"
	ansiReverse      = "\x1b[7m"
	ansiCyan         = "\x1b[36m"
	ansiGreen        = "\x1b[32m"
	ansiRed          = "\x1b[31m"
	playbackControls = "Space Play/Pause · ←/→ Sentence · ↑/↓ Chapter"
)

// View renders terminal playback progress.
type View struct {
	writer           io.Writer
	color            bool
	title            string
	engine           string
	header           bool
	controls         bool
	started          bool
	width            int
	height           int
	current          *speechLine
	rowsBelowCurrent int
	chapters         []chapterLine
	activeChapter    int
	activeSentence   int
	activeComplete   bool
	playing          bool
	chapterRows      int
	chapterStatus    string
	chapterFrame     string
	terminalSize     func() (int, int, error)
}

type speechLine struct {
	index int
	total int
	text  string
	rows  int
}

type chapterLine struct {
	text      string
	sentences []string
	completed bool
}

type chapterTextLine struct {
	prefix   string
	segments []chapterTextSegment
}

type chapterTextSegment struct {
	text     string
	sentence int
}

type terminalSizer interface {
	TerminalSize() (int, int, error)
}

// New creates a terminal playback view.
func New(writer io.Writer, color bool, title, engine string) *View {
	width, height := 80, 24
	var size func() (int, int, error)
	if provider, ok := writer.(terminalSizer); ok {
		size = provider.TerminalSize
	} else if file, ok := writer.(*os.File); ok {
		size = func() (int, int, error) {
			return term.GetSize(int(file.Fd()))
		}
	}
	if size != nil {
		if detectedWidth, detectedHeight, err := size(); err == nil {
			if detectedWidth > 0 {
				width = detectedWidth
			}
			if detectedHeight > 0 {
				height = detectedHeight
			}
		}
	}
	return &View{
		writer:         writer,
		color:          color,
		title:          title,
		engine:         engine,
		controls:       true,
		width:          width,
		height:         height,
		activeChapter:  -1,
		activeSentence: -1,
		playing:        true,
		terminalSize:   size,
	}
}

// SetChapters configures the stable chapter list used by the interactive TUI.
func (v *View) SetChapters(texts []string) {
	v.chapters = make([]chapterLine, len(texts))
	for index, text := range texts {
		v.chapters[index].text = text
		v.chapters[index].sentences = textchunk.Sentences(text)
	}
	v.activeChapter = -1
	v.activeSentence = -1
	v.activeComplete = false
	v.chapterRows = 0
	v.chapterStatus = ""
	v.chapterFrame = ""
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
		if _, err := fmt.Fprintln(v.writer, playbackControls); err != nil {
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
	if v.chapterMode() {
		v.setChapterText(index, text)
		v.activeChapter = index
		v.activeSentence = 0
		v.activeComplete = false
		v.playing = true
		v.chapterStatus = ""
		return v.renderChapters()
	}
	_, err := fmt.Fprintf(v.writer, "%s%s\n", speechPrefix(index, total, v.style(ansiCyan, "▶")), safe(text))
	if err == nil {
		v.current = &speechLine{index: index, total: total, text: text, rows: v.displayRows(speechText(index, total, "▶", text))}
		v.rowsBelowCurrent = 0
	}
	return err
}

// Progress moves the active highlight to one sentence within a chapter.
func (v *View) Progress(index, _ int, sentence int) error {
	if !v.chapterMode() {
		return nil
	}
	sentence = v.validSentence(index, sentence)
	hadStatus := v.chapterStatus != ""
	v.chapterStatus = ""
	if index == v.activeChapter && sentence == v.activeSentence && !hadStatus {
		return nil
	}
	v.activeChapter = index
	v.activeSentence = sentence
	return v.renderChapters()
}

func (v *View) Spoken(index, _ int) error {
	if v.chapterMode() {
		if index >= 0 && index < len(v.chapters) {
			v.chapters[index].completed = true
		}
		v.activeChapter = index
		v.activeSentence = -1
		v.activeComplete = true
		return v.renderChapters()
	}
	return v.updateSpeechIcon(ansiGreen, "✓")
}

func (v *View) Paused(index, _ int) error {
	if v.chapterMode() {
		v.activeChapter = index
		v.playing = false
		return v.renderChapters()
	}
	return v.updateSpeechIcon("", "⏸")
}

func (v *View) Resumed(index, _ int) error {
	if v.chapterMode() {
		v.activeChapter = index
		v.playing = true
		return v.renderChapters()
	}
	return v.updateSpeechIcon(ansiCyan, "▶")
}

// Buffering reports that playback reached audio which is still being prepared.
func (v *View) Buffering(index, total int) error {
	if v.chapterMode() {
		v.chapterStatus = fmt.Sprintf("… buffering speech unit %d/%d", index+1, total)
		return v.renderChapters()
	}
	line := fmt.Sprintf("      … buffering speech unit %d/%d", index+1, total)
	_, err := fmt.Fprintln(v.writer, line)
	if err == nil && v.current != nil {
		v.rowsBelowCurrent += v.displayRows(line)
	}
	return err
}

func (v *View) Seeked(index, total int, text string, sentence int, playing bool, _ time.Duration, position, duration time.Duration, complete bool) error {
	if v.chapterMode() {
		v.setChapterText(index, text)
		v.activeChapter = index
		v.activeSentence = v.validSentence(index, sentence)
		v.activeComplete = complete && position >= duration
		v.playing = playing
		v.chapterStatus = ""
		if v.activeComplete && index >= 0 && index < len(v.chapters) {
			v.chapters[index].completed = true
		}
		return v.renderChapters()
	}
	color, icon := ansiCyan, "▶"
	if complete && position >= duration {
		color, icon = ansiGreen, "✓"
	} else if !playing {
		color, icon = "", "⏸"
	}
	return v.replaceSpeechLine(index, total, text, color, icon)
}

func (v *View) Failed(_, _ int, playbackErr error) error {
	if v.chapterMode() {
		v.chapterStatus = "✗ " + safe(playbackErr.Error())
		return v.renderChapters()
	}
	plain := "      ✗ " + safe(playbackErr.Error())
	_, err := fmt.Fprintf(v.writer, "      %s %s\n", v.style(ansiRed, "✗"), safe(playbackErr.Error()))
	if err == nil && v.current != nil {
		v.rowsBelowCurrent += v.displayRows(plain)
	}
	return err
}

func (v *View) Finish(total int) error {
	if v.chapterMode() && v.activeChapter >= 0 && v.activeChapter < len(v.chapters) {
		v.chapters[v.activeChapter].completed = true
		v.activeComplete = true
		v.chapterStatus = ""
		if err := v.renderChapters(); err != nil {
			return err
		}
	}
	_, err := fmt.Fprintf(v.writer, "\n%s Finished %d speech %s.\n", v.style(ansiGreen, "✓"), total, unitWord(total))
	return err
}

func (v *View) chapterMode() bool {
	return v.controls && len(v.chapters) > 0
}

func (v *View) setChapterText(index int, text string) {
	if index >= 0 && index < len(v.chapters) && v.chapters[index].text != text {
		v.chapters[index].text = text
		v.chapters[index].sentences = textchunk.Sentences(text)
	}
}

func (v *View) validSentence(index, sentence int) int {
	if index < 0 || index >= len(v.chapters) || len(v.chapters[index].sentences) == 0 {
		return 0
	}
	if sentence < 0 {
		return 0
	}
	if sentence >= len(v.chapters[index].sentences) {
		return len(v.chapters[index].sentences) - 1
	}
	return sentence
}

func (v *View) renderChapters() error {
	if v.refreshTerminalSize() && v.chapterRows > 0 {
		if err := v.repaintTUI(); err != nil {
			return err
		}
	}
	start, end := v.chapterRange()
	var content strings.Builder
	rows := 0
	for index := start; index < end; index++ {
		color, icon := v.chapterIcon(index)
		highlight := index == v.activeChapter && !v.activeComplete
		chapterRows, err := v.writeChapter(&content, index, color, icon, v.chapters[index].text, highlight)
		if err != nil {
			return err
		}
		rows += chapterRows
	}
	if v.chapterStatus != "" {
		fmt.Fprintln(&content, "      "+v.chapterStatus)
		rows += v.displayRows("      " + v.chapterStatus)
	}
	frame := content.String()
	if frame == v.chapterFrame {
		return nil
	}
	// Submit clearing and replacement together so the terminal never receives
	// a separate empty chapter list between playback states.
	var output strings.Builder
	if v.chapterRows > 0 {
		fmt.Fprintf(&output, "\x1b[%dA\r", v.chapterRows)
		for row := 0; row < v.chapterRows; row++ {
			output.WriteString("\x1b[2K")
			if row+1 < v.chapterRows {
				output.WriteString("\x1b[1B\r")
			}
		}
		if v.chapterRows > 1 {
			fmt.Fprintf(&output, "\x1b[%dA\r", v.chapterRows-1)
		}
	}
	output.WriteString(frame)
	if _, err := io.WriteString(v.writer, output.String()); err != nil {
		return err
	}
	v.chapterRows = rows
	v.chapterFrame = frame
	return nil
}

func (v *View) refreshTerminalSize() bool {
	if v.terminalSize == nil {
		return false
	}
	width, height, err := v.terminalSize()
	if err != nil || width <= 0 || height <= 0 || width == v.width && height == v.height {
		return false
	}
	v.width = width
	v.height = height
	return true
}

func (v *View) repaintTUI() error {
	if _, err := fmt.Fprint(v.writer, "\x1b[2J\x1b[H"); err != nil {
		return err
	}
	if _, err := fmt.Fprintf(v.writer, "%s  %s\n", v.style(ansiBold, "say"), safe(v.title)); err != nil {
		return err
	}
	if _, err := fmt.Fprintf(v.writer, "%s  %s · %d speech %s\n\n", v.style(ansiCyan, "TTS"), safe(v.engine), len(v.chapters), unitWord(len(v.chapters))); err != nil {
		return err
	}
	if _, err := fmt.Fprintln(v.writer, playbackControls); err != nil {
		return err
	}
	if _, err := fmt.Fprintln(v.writer); err != nil {
		return err
	}
	v.chapterRows = 0
	v.chapterFrame = ""
	return nil
}

func (v *View) chapterRange() (int, int) {
	if len(v.chapters) == 0 {
		return 0, 0
	}
	active := v.activeChapter
	if active < 0 || active >= len(v.chapters) {
		active = 0
	}
	budget := v.height - 6
	if budget < 1 {
		budget = 1
	}
	start, end := active, active+1
	used := v.chapterDisplayRows(active, "▶", v.chapters[active].text)
	for start > 0 {
		candidate := v.chapterDisplayRows(start-1, "·", v.chapters[start-1].text)
		if used+candidate > budget {
			break
		}
		start--
		used += candidate
	}
	for end < len(v.chapters) {
		candidate := v.chapterDisplayRows(end, "·", v.chapters[end].text)
		if used+candidate > budget {
			break
		}
		end++
		used += candidate
	}
	return start, end
}

func (v *View) chapterIcon(index int) (string, string) {
	if index == v.activeChapter {
		if v.activeComplete {
			return ansiGreen, "✓"
		}
		if !v.playing {
			return "", "⏸"
		}
		return ansiCyan, "▶"
	}
	if v.chapters[index].completed {
		return ansiGreen, "✓"
	}
	return "", "·"
}

func (v *View) updateSpeechIcon(color, icon string) error {
	if !v.controls || v.current == nil {
		return nil
	}
	rowsUp := v.rowsBelowCurrent + v.current.rows
	if _, err := fmt.Fprintf(v.writer, "\x1b[%dA\r\x1b[2K", rowsUp); err != nil {
		return err
	}
	line := v.current
	if _, err := fmt.Fprintf(v.writer, "%s%s\n", speechPrefix(line.index, line.total, v.style(color, icon)), safe(line.text)); err != nil {
		return err
	}
	line.rows = v.displayRows(speechText(line.index, line.total, icon, line.text))
	if v.rowsBelowCurrent > 0 {
		_, err := fmt.Fprintf(v.writer, "\x1b[%dB\r", v.rowsBelowCurrent)
		return err
	}
	return nil
}

func (v *View) replaceSpeechLine(index, total int, text, color, icon string) error {
	next := &speechLine{
		index: index,
		total: total,
		text:  text,
		rows:  v.displayRows(speechText(index, total, icon, text)),
	}
	if !v.controls || v.current == nil {
		v.current = next
		v.rowsBelowCurrent = 0
		return nil
	}

	rows := v.current.rows + v.rowsBelowCurrent
	if _, err := fmt.Fprintf(v.writer, "\x1b[%dA\r", rows); err != nil {
		return err
	}
	for row := 0; row < rows; row++ {
		if _, err := fmt.Fprint(v.writer, "\x1b[2K"); err != nil {
			return err
		}
		if row+1 < rows {
			if _, err := fmt.Fprint(v.writer, "\x1b[1B\r"); err != nil {
				return err
			}
		}
	}
	if rows > 1 {
		if _, err := fmt.Fprintf(v.writer, "\x1b[%dA\r", rows-1); err != nil {
			return err
		}
	}
	if _, err := fmt.Fprintf(v.writer, "%s%s\n", speechPrefix(index, total, v.style(color, icon)), safe(text)); err != nil {
		return err
	}
	v.current = next
	v.rowsBelowCurrent = 0
	return nil
}

func (v *View) style(code, text string) string {
	if !v.color || code == "" {
		return text
	}
	return code + text + ansiReset
}

func (v *View) displayRows(text string) int {
	if v.width <= 0 {
		return 1
	}
	columns := runewidth.StringWidth(text)
	if columns <= 0 {
		return 1
	}
	return (columns-1)/v.width + 1
}

func (v *View) writeChapter(writer io.Writer, index int, color, icon, text string, highlight bool) (int, error) {
	lines := v.chapterSpeechLines(index, icon, text)
	styledPrefix := speechPrefix(index, len(v.chapters), v.style(color, icon))
	rows := 0
	for lineIndex, line := range lines {
		rows += v.displayRows(line.prefix + plainChapterText(line.segments))
		prefix := line.prefix
		if lineIndex == 0 {
			prefix = styledPrefix
		}
		var content strings.Builder
		for _, segment := range line.segments {
			text := segment.text
			if highlight && segment.sentence == v.activeSentence {
				text = v.style(ansiReverse, text)
			}
			content.WriteString(text)
		}
		if _, err := fmt.Fprintln(writer, prefix+content.String()); err != nil {
			return 0, err
		}
	}
	return rows, nil
}

func (v *View) chapterDisplayRows(index int, icon, text string) int {
	rows := 0
	for _, line := range v.chapterSpeechLines(index, icon, text) {
		rows += v.displayRows(line.prefix + plainChapterText(line.segments))
	}
	return rows
}

func (v *View) chapterSpeechLines(index int, icon, text string) []chapterTextLine {
	prefix := speechPrefix(index, len(v.chapters), icon)
	indent := strings.Repeat(" ", runewidth.StringWidth(prefix))
	contentWidth := v.width - runewidth.StringWidth(prefix)
	sentences := textchunk.Sentences(text)
	if index >= 0 && index < len(v.chapters) && len(v.chapters[index].sentences) > 0 {
		sentences = v.chapters[index].sentences
	}
	wrapped := wrapChapterSegments(chapterSentenceSegments(text, sentences), contentWidth)
	lines := make([]chapterTextLine, len(wrapped))
	for lineIndex, segments := range wrapped {
		linePrefix := indent
		if lineIndex == 0 {
			linePrefix = prefix
		}
		lines[lineIndex] = chapterTextLine{prefix: linePrefix, segments: segments}
	}
	return lines
}

func chapterSentenceSegments(text string, sentences []string) []chapterTextSegment {
	if len(sentences) == 0 {
		return []chapterTextSegment{{text: safe(text), sentence: 0}}
	}

	segments := make([]chapterTextSegment, 0, len(sentences)*2+1)
	cursor := 0
	for sentenceIndex, sentence := range sentences {
		offset := strings.Index(text[cursor:], sentence)
		if offset < 0 {
			return []chapterTextSegment{{text: safe(text), sentence: 0}}
		}
		start := cursor + offset
		if start > cursor {
			appendChapterSegment(&segments, safe(text[cursor:start]), -1)
		}
		end := start + len(sentence)
		appendChapterSegment(&segments, safe(text[start:end]), sentenceIndex)
		cursor = end
	}
	if cursor < len(text) {
		appendChapterSegment(&segments, safe(text[cursor:]), -1)
	}
	return segments
}

func wrapChapterSegments(segments []chapterTextSegment, width int) [][]chapterTextSegment {
	if width <= 0 {
		return [][]chapterTextSegment{segments}
	}

	lines := [][]chapterTextSegment{{}}
	lineWidth := 0
	for _, segment := range segments {
		clusters := graphemes.FromString(segment.text)
		for clusters.Next() {
			cluster := clusters.Value()
			clusterWidth := runewidth.StringWidth(cluster)
			if len(lines[len(lines)-1]) > 0 && lineWidth+clusterWidth > width {
				lines = append(lines, nil)
				lineWidth = 0
			}
			appendChapterSegment(&lines[len(lines)-1], cluster, segment.sentence)
			lineWidth += clusterWidth
		}
	}
	return lines
}

func appendChapterSegment(segments *[]chapterTextSegment, text string, sentence int) {
	if text == "" {
		return
	}
	last := len(*segments) - 1
	if last >= 0 && (*segments)[last].sentence == sentence {
		(*segments)[last].text += text
		return
	}
	*segments = append(*segments, chapterTextSegment{text: text, sentence: sentence})
}

func plainChapterText(segments []chapterTextSegment) string {
	var text strings.Builder
	for _, segment := range segments {
		text.WriteString(segment.text)
	}
	return text.String()
}

func speechPrefix(index, total int, icon string) string {
	digits := len(strconv.Itoa(total))
	return fmt.Sprintf("[%0*d/%d] %s ", digits, index+1, total, icon)
}

func speechText(index, total int, icon, text string) string {
	return speechPrefix(index, total, icon) + safe(text)
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
