package player

import (
	"testing"
	"time"
)

func navigationPlayer(t *testing.T, playing bool, prepared int) (*streamPlayer, *fakeTransport) {
	t.Helper()
	transport := newFakeTransport(map[string]time.Duration{
		"one.aiff": 10 * time.Second, "two.aiff": 20 * time.Second, "three.aiff": 30 * time.Second,
	})
	p := &streamPlayer{
		total: 3, transport: transport, view: &recordingView{events: &transport.events},
		sentenceCounts: []int{1, 1, 1},
		offsets:        []time.Duration{0}, current: -1, currentSentence: -1, playing: playing,
	}
	for _, result := range []TrackResult{
		trackResult("One.", "one.aiff", 10*time.Second),
		trackResult("Two.", "two.aiff", 20*time.Second),
		trackResult("Three.", "three.aiff", 30*time.Second),
	}[:prepared] {
		if _, err := p.addTrack(result.Track); err != nil {
			t.Fatal(err)
		}
	}
	return p, transport
}

func commandOK(t *testing.T, p *streamPlayer, command Command) {
	t.Helper()
	if finished, err := p.handleCommands(command); err != nil || finished {
		t.Fatalf("command %d: finished=%t, err=%v", command, finished, err)
	}
}

func TestChapterCommandsJumpToAdjacentChapterStarts(t *testing.T) {
	for _, playing := range []bool{true, false} {
		t.Run(map[bool]string{true: "playing", false: "paused"}[playing], func(t *testing.T) {
			p, transport := navigationPlayer(t, playing, 3)
			transport.setPosition(7 * time.Second)
			for _, step := range []struct {
				command Command
				path    string
				index   int
			}{
				{NextChapter, "two.aiff", 1},
				{NextChapter, "three.aiff", 2},
				{PreviousChapter, "two.aiff", 1},
				{PreviousChapter, "one.aiff", 0},
			} {
				commandOK(t, p, step.command)
				path, position := transport.current()
				if path != step.path || position != 0 || p.current != step.index || p.currentSentence != 0 {
					t.Fatalf("navigation = %s at %s, chapter %d sentence %d", path, position, p.current, p.currentSentence)
				}
				if transport.isPlaying() != playing {
					t.Fatal("chapter navigation changed playback state")
				}
				transport.setPosition(3 * time.Second)
			}
		})
	}
}

func TestChapterCommandsAtDocumentEdgesAreNoOps(t *testing.T) {
	p, transport := navigationPlayer(t, true, 3)
	transport.setPosition(3 * time.Second)
	commandOK(t, p, PreviousChapter)
	if path, position := transport.current(); path != "one.aiff" || position != 3*time.Second {
		t.Fatal("up at first chapter changed playback")
	}
	commandOK(t, p, NextChapter)
	commandOK(t, p, NextChapter)
	transport.setPosition(3 * time.Second)
	commandOK(t, p, NextChapter)
	if path, position := transport.current(); path != "three.aiff" || position != 3*time.Second {
		t.Fatal("down at last chapter changed playback")
	}
}

func TestChapterCommandsWaitForSelectedChapterWithoutPlayingIntermediateAudio(t *testing.T) {
	for _, playing := range []bool{true, false} {
		t.Run(map[bool]string{true: "playing", false: "paused"}[playing], func(t *testing.T) {
			p, transport := navigationPlayer(t, playing, 1)
			commandOK(t, p, NextChapter)
			commandOK(t, p, NextChapter)
			if transport.isPlaying() {
				t.Fatal("old audio still playing while waiting for selected chapter")
			}
			if _, err := p.addTrack(trackResult("Two.", "two.aiff", 20*time.Second).Track); err != nil {
				t.Fatal(err)
			}
			if path, _ := transport.current(); path != "one.aiff" {
				t.Fatal("loaded intermediate chapter instead of waiting for selection")
			}
			if _, err := p.addTrack(trackResult("Three.", "three.aiff", 30*time.Second).Track); err != nil {
				t.Fatal(err)
			}
			if path, position := transport.current(); path != "three.aiff" || position != 0 || transport.isPlaying() != playing {
				t.Fatalf("prepared target = %s at %s, playing=%t", path, position, transport.isPlaying())
			}
		})
	}
}

func TestChapterCommandsCanReversePendingSelection(t *testing.T) {
	p, transport := navigationPlayer(t, true, 1)
	commandOK(t, p, NextChapter)
	commandOK(t, p, PreviousChapter)
	if path, position := transport.current(); path != "one.aiff" || position != 0 || !transport.isPlaying() {
		t.Fatal("up did not cancel pending chapter selection and return to the first chapter")
	}
	if _, err := p.addTrack(trackResult("Two.", "two.aiff", 20*time.Second).Track); err != nil {
		t.Fatal(err)
	}
	if path, _ := transport.current(); path != "one.aiff" {
		t.Fatal("canceled chapter selection was applied when audio arrived")
	}
}

func TestSentenceNavigationContinuesFromPendingChapterSelection(t *testing.T) {
	p, transport := navigationPlayer(t, false, 1)
	commandOK(t, p, NextChapter)
	commandOK(t, p, Forward)
	if _, err := p.addTrack(trackResult("Two.", "two.aiff", 20*time.Second).Track); err != nil {
		t.Fatal(err)
	}
	if path, _ := transport.current(); path != "one.aiff" {
		t.Fatal("loaded intermediate chapter instead of the selected sentence")
	}
	if _, err := p.addTrack(trackResult("Three.", "three.aiff", 30*time.Second).Track); err != nil {
		t.Fatal(err)
	}
	if path, pos := transport.current(); path != "three.aiff" || pos != 0 || transport.isPlaying() {
		t.Fatalf("sentence target = %s at %s, playing=%t", path, pos, transport.isPlaying())
	}
}

func TestChapterSelectionStartsAtFirstStreamedSentenceAndKeepsPauseToggle(t *testing.T) {
	p, transport := navigationPlayer(t, true, 1)
	p.sentenceCounts[1] = 2
	commandOK(t, p, NextChapter)
	commandOK(t, p, Toggle)
	partial := Track{
		Text: "Two. More.", Sentences: []SentenceTrack{{Path: "two.aiff", Duration: 20 * time.Second}},
		Duration: 20 * time.Second,
	}
	if _, err := p.addTrack(partial); err != nil {
		t.Fatal(err)
	}
	if path, position := transport.current(); path != "two.aiff" || position != 0 || transport.isPlaying() {
		t.Fatal("selection did not load the first ready sentence while preserving pause")
	}
	commandOK(t, p, Toggle)
	if !transport.isPlaying() {
		t.Fatal("could not resume selected partial chapter")
	}
}

func TestQueuedSentenceSeeksAreCoalescedWithoutLoadingIntermediateAudio(t *testing.T) {
	p, transport := navigationPlayer(t, false, 3)
	commands := make(chan Command, 3)
	commands <- Forward
	commands <- Forward
	commands <- Toggle
	p.commands = commands
	if finished, err := p.handleCommands(Forward); err != nil || finished {
		t.Fatalf("handleCommands: finished=%t, err=%v", finished, err)
	}
	if path, position := transport.current(); path != "three.aiff" || position != 0 || !transport.isPlaying() {
		t.Fatalf("queued seeks = %s at %s, playing=%t", path, position, transport.isPlaying())
	}
	if got := transport.countEvent("seeked:"); got != 1 {
		t.Fatalf("seek renders = %d, want one final target for repeated keys", got)
	}
}

func TestQueuedChapterCommandsClampAndPreserveDirectionChanges(t *testing.T) {
	p, transport := navigationPlayer(t, false, 3)
	commands := make(chan Command, 4)
	commands <- NextChapter
	commands <- NextChapter
	commands <- NextChapter
	commands <- PreviousChapter
	p.commands = commands
	if finished, err := p.handleCommands(NextChapter); err != nil || finished {
		t.Fatalf("handleCommands: finished=%t, err=%v", finished, err)
	}
	if path, position := transport.current(); path != "two.aiff" || position != 0 || transport.isPlaying() {
		t.Fatalf("queued chapters = %s at %s, playing=%t", path, position, transport.isPlaying())
	}
	if got := transport.countEvent("seeked:"); got != 2 {
		t.Fatalf("seek renders = %d, want one for each direction", got)
	}
}
