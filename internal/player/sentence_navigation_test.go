package player

import (
	"context"
	"slices"
	"strings"
	"testing"
	"time"
)

func sentenceNavigationPlayer(t *testing.T, playing bool, complete bool) (*streamPlayer, *fakeTransport) {
	t.Helper()
	p, transport := navigationPlayer(t, playing, 0)
	p.total = 2
	p.sentenceCounts = []int{3, 1}
	first := sentenceTrackResult("First. Second. Third.", []SentenceTrack{
		{Path: "first.aiff", Duration: 9 * time.Second},
		{Path: "second.aiff", Duration: 3 * time.Second},
		{Path: "third.aiff", Duration: 15 * time.Second},
	}).Track
	if !complete {
		first.Sentences = first.Sentences[:1]
		first.Duration = 9 * time.Second
		first.Complete = false
	}
	if _, err := p.addTrack(first); err != nil {
		t.Fatal(err)
	}
	if complete {
		if _, err := p.addTrack(trackResult("Fourth.", "fourth.aiff", 8*time.Second).Track); err != nil {
			t.Fatal(err)
		}
	}
	return p, transport
}

func TestArrowCommandsNavigateWholeSentences(t *testing.T) {
	for _, playing := range []bool{false, true} {
		p, transport := sentenceNavigationPlayer(t, playing, true)
		transport.setPosition(7 * time.Second)
		for _, step := range []struct {
			command Command
			path    string
		}{
			{Forward, "second.aiff"}, {Forward, "third.aiff"},
			{Forward, "fourth.aiff"}, {Backward, "third.aiff"},
			{Backward, "second.aiff"}, {Backward, "first.aiff"},
		} {
			commandOK(t, p, step.command)
			if path, pos := transport.current(); path != step.path || pos != 0 || transport.isPlaying() != playing {
				t.Fatalf("command %d: %s at %s, playing=%t; want %s at its start, playing=%t", step.command, path, pos, transport.isPlaying(), step.path, playing)
			}
			transport.setPosition(time.Second)
		}
	}
}

func TestSentenceArrowAtDocumentEdgesDoesNotRestartOrFinish(t *testing.T) {
	p, transport := sentenceNavigationPlayer(t, true, true)
	transport.setPosition(2 * time.Second)
	commandOK(t, p, Backward)
	if transport.Position() != 2*time.Second {
		t.Fatal("left at first sentence restarted it")
	}
	commandOK(t, p, NextChapter)
	transport.setPosition(2 * time.Second)
	commandOK(t, p, Forward)
	if path, pos := transport.current(); path != "fourth.aiff" || pos != 2*time.Second {
		t.Fatal("right at last sentence changed playback")
	}
}

func TestSentenceArrowsWaitForExactStreamedSentenceAndCanReverse(t *testing.T) {
	p, transport := sentenceNavigationPlayer(t, false, false)
	commandOK(t, p, Forward)
	commandOK(t, p, Forward)
	second := sentenceTrackResult("First. Second. Third.", []SentenceTrack{
		{Path: "first.aiff", Duration: 9 * time.Second},
		{Path: "second.aiff", Duration: 3 * time.Second},
	}).Track
	second.Complete = false
	if _, err := p.addTrack(second); err != nil {
		t.Fatal(err)
	}
	if path, _ := transport.current(); path != "first.aiff" {
		t.Fatal("played an intermediate sentence while waiting for the third")
	}
	commandOK(t, p, Backward)
	if path, pos := transport.current(); path != "second.aiff" || pos != 0 || transport.isPlaying() {
		t.Fatal("left did not reverse pending sentence selection while paused")
	}
}

func TestQueuedSentenceArrowsSkipIntermediateAudio(t *testing.T) {
	p, transport := sentenceNavigationPlayer(t, true, true)
	commands := make(chan Command, 2)
	commands <- Forward
	commands <- Forward
	p.commands = commands
	commandOK(t, p, Forward)
	if path, pos := transport.current(); path != "fourth.aiff" || pos != 0 {
		t.Fatalf("queued sentence target = %s at %s", path, pos)
	}
	if got := transport.countEvent("load:"); got != 2 {
		t.Fatalf("loads=%d, want initial sentence and final selection only", got)
	}
}

func TestChapterSelectionRendersBeforeNativeAudioLoad(t *testing.T) {
	p, transport := navigationPlayer(t, true, 3)
	commandOK(t, p, NextChapter)
	events := transport.snapshot()
	render := slices.Index(events, "seeked:1:0:0s:10s:1m0s:true")
	load := slices.Index(events, "load:two.aiff")
	if render < 0 || load < 0 || render > load {
		t.Fatalf("selection was blocked by audio loading: %v", events)
	}
}

func TestMixedNavigationDoesNotDependOnAudioPreparation(t *testing.T) {
	for _, prepared := range []int{1, 3} {
		p, transport := navigationPlayer(t, false, prepared)
		commandOK(t, p, NextChapter)
		commandOK(t, p, NextChapter)
		commandOK(t, p, Backward)
		commandOK(t, p, PreviousChapter)
		if path, position := transport.current(); path != "one.aiff" || position != 0 || p.pending != nil {
			t.Fatalf("prepared=%d: mixed navigation = %s at %s, pending=%+v; want first chapter", prepared, path, position, p.pending)
		}
	}
}

func TestPlayRejectsAudioThatDoesNotMatchSourceSentenceCount(t *testing.T) {
	transport := newFakeTransport(nil)
	results := make(chan TrackResult, 1)
	results <- trackResult("First. Second.", "one.aiff", time.Second)
	close(results)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	err := Play(ctx, []string{"First. Second."}, results, transport, nil, &recordingView{events: &transport.events})
	if err == nil || !strings.Contains(err.Error(), "sentence count") {
		t.Fatalf("Play() error = %v, want source/audio sentence count mismatch", err)
	}
}

func TestBufferedChapterKeepsPausedStateWhenAudioArrives(t *testing.T) {
	p, transport := navigationPlayer(t, true, 1)
	transport.finishCurrent()
	if _, err := p.handleTick(); err != nil {
		t.Fatal(err)
	}
	commandOK(t, p, Toggle)
	if _, err := p.addTrack(trackResult("Two.", "two.aiff", 20*time.Second).Track); err != nil {
		t.Fatal(err)
	}
	if transport.isPlaying() || transport.countEvent("paused:1") != 1 {
		t.Fatalf("next buffered chapter did not render paused: %v", transport.snapshot())
	}
}
