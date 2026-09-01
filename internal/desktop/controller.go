package desktop

import (
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/miclle/say/internal/player"
	"github.com/miclle/say/internal/textchunk"
)

const commandBuffer = 32
const menuTextRunes = 36

// Snapshot is the complete user-visible desktop playback state.
type Snapshot struct {
	Visible    bool
	Document   string
	Text       string
	Playing    bool
	Busy       bool
	Chapter    int
	Chapters   int
	Sentence   int
	Sentences  int
	QueueIndex int
	QueueCount int
	Position   time.Duration
	Duration   time.Duration
}

func displayText(text string) string {
	text = strings.TrimSpace(text)
	runes := []rune(text)
	if len(runes) <= menuTextRunes {
		return text
	}
	return string(runes[:menuTextRunes-1]) + "…"
}

// Controls joins desktop rendering and command input to player.Play.
type Controls interface {
	player.View
	player.TrackView
	Configure(title string, chapters []string) error
	Commands() <-chan player.Command
	Close() error
}

func (controls *controls) Track(index, total, sentence int, _ string, playing bool, position, duration time.Duration) error {
	return controls.selectSentence(index, total, sentence, false, func(state *Snapshot) {
		state.Playing = playing
		state.Position = position
		state.Duration = duration
	})
}

type backend interface {
	Render(Snapshot) error
	Clear() error
}

type controls struct {
	mu       sync.Mutex
	backend  backend
	commands chan player.Command
	state    Snapshot
	texts    [][]string
	offsets  []int
}

func newControls(renderer backend) *controls {
	return &controls{backend: renderer, commands: make(chan player.Command, commandBuffer)}
}

func (controls *controls) Configure(title string, chapters []string) error {
	if len(chapters) == 0 {
		return fmt.Errorf("desktop controls require at least one chapter")
	}
	texts := make([][]string, len(chapters))
	offsets := make([]int, len(chapters))
	queueCount := 0
	for index, chapter := range chapters {
		texts[index] = textchunk.Sentences(chapter)
		if len(texts[index]) == 0 {
			return fmt.Errorf("desktop controls chapter %d has no sentences", index+1)
		}
		offsets[index] = queueCount
		queueCount += len(texts[index])
	}
	controls.mu.Lock()
	controls.texts = texts
	controls.offsets = offsets
	controls.state = Snapshot{
		Document: title, Text: texts[0][0], Playing: true,
		Chapters: len(chapters), Sentences: len(texts[0]), QueueCount: queueCount,
	}
	controls.mu.Unlock()
	return nil
}

func (controls *controls) Commands() <-chan player.Command { return controls.commands }

func (controls *controls) emit(command player.Command) bool {
	switch command {
	case player.Toggle, player.Backward, player.Forward, player.ResumePlayback, player.PausePlayback:
	default:
		return false
	}
	select {
	case controls.commands <- command:
		return true
	default:
		return false
	}
}

func (controls *controls) Prepared(int, int) error { return nil }

func (controls *controls) Start(int) error {
	return controls.update(func(state *Snapshot) { state.Visible = true })
}

func (controls *controls) Speaking(index, total int, _ string) error {
	return controls.selectSentence(index, total, 0, false, nil)
}

func (controls *controls) Progress(index, total, sentence int) error {
	return controls.selectSentence(index, total, sentence, false, nil)
}

func (controls *controls) Spoken(int, int) error { return nil }

func (controls *controls) Paused(int, int) error {
	return controls.update(func(state *Snapshot) { state.Playing = false })
}

func (controls *controls) Resumed(int, int) error {
	return controls.update(func(state *Snapshot) { state.Playing = true })
}

func (controls *controls) Buffering(index, total int) error {
	return controls.update(func(state *Snapshot) {
		state.Busy = true
		if index == state.Chapter || index < 0 || index >= len(controls.texts) {
			return
		}
		state.Chapter = index
		state.Chapters = total
		state.Sentence = 0
		state.Sentences = len(controls.texts[index])
		state.QueueIndex = controls.offsets[index]
		state.Text = controls.texts[index][0]
		state.Position = 0
		state.Duration = 0
	})
}

func (controls *controls) Selected(index, total int, _ string, sentence int) error {
	return controls.selectSentence(index, total, sentence, true, nil)
}

func (controls *controls) Seeked(index, total int, _ string, sentence int, playing bool, _, position, duration time.Duration, _ bool) error {
	return controls.selectSentence(index, total, sentence, false, func(state *Snapshot) {
		state.Playing = playing
		state.Position = position
		state.Duration = duration
	})
}

func (controls *controls) Failed(int, int, error) error { return controls.clear() }

func (controls *controls) Finish(int) error { return controls.clear() }

func (controls *controls) Close() error { return controls.clear() }

func (controls *controls) selectSentence(index, total, sentence int, busy bool, amend func(*Snapshot)) error {
	return controls.update(func(state *Snapshot) {
		if index < 0 || index >= len(controls.texts) || sentence < 0 || sentence >= len(controls.texts[index]) {
			return
		}
		state.Chapter = index
		state.Chapters = total
		state.Sentence = sentence
		state.Sentences = len(controls.texts[index])
		state.QueueIndex = controls.offsets[index] + sentence
		state.Text = controls.texts[index][sentence]
		state.Busy = busy
		state.Position = 0
		state.Duration = 0
		if amend != nil {
			amend(state)
		}
	})
}

func (controls *controls) update(change func(*Snapshot)) error {
	controls.mu.Lock()
	change(&controls.state)
	snapshot := controls.state
	controls.mu.Unlock()
	if !snapshot.Visible {
		return nil
	}
	return controls.backend.Render(snapshot)
}

func (controls *controls) clear() error {
	controls.mu.Lock()
	if !controls.state.Visible {
		controls.mu.Unlock()
		return nil
	}
	controls.state.Visible = false
	controls.mu.Unlock()
	return controls.backend.Clear()
}
