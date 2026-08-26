package player

import (
	"slices"
	"testing"
	"testing/synctest"
	"time"
)

func TestSentencePreviewRendersBeforePauseAndLoadsOnlyExactTarget(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		h := newDemandHarness(t, []string{"One. Two. Three. Four. Five."})
		h.ready(Target{}, "one.aiff")
		for range 4 {
			h.command(Forward)
		}
		events := h.transport.snapshot()
		selected, paused := slices.Index(events, "selected:0:1"), slices.Index(events, "pause")
		if selected < 0 || paused < selected {
			t.Fatalf("preview waited for native pause: %v", events)
		}
		h.settle()
		h.assertCount("request:0:4", 1)
		h.ready(Target{0, 1}, "two.aiff")
		h.ready(Target{0, 4}, "five.aiff")
		h.assertPath("five.aiff", true)
		h.assertCount("load:", 2)
		events = h.transport.snapshot()
		selected, loaded := slices.Index(events, "selected:0:4"), slices.Index(events, "load:five.aiff")
		if selected < 0 || loaded < selected {
			t.Fatalf("audio load preceded selection: %v", events)
		}
		h.finishSentence()
		if err := <-h.done; err != nil {
			t.Fatal(err)
		}
	})
}

func TestNavigationDebouncesRequestsButImmediatelySelectsText(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		h := newDemandHarness(t, []string{"One.", "Two.", "Three."})
		h.ready(Target{}, "one.aiff")
		h.command(NextChapter)
		h.assertCount("selected:1:0", 1)
		h.assertCount("buffering:", 0)
		h.assertCount("load:", 1)
		h.assertCount("request:", 1)
		if h.transport.isPlaying() {
			t.Fatal("old audio still playing during selection")
		}
		time.Sleep(100 * time.Millisecond)
		h.ready(Target{1, 0}, "two.aiff") // A late result cannot activate a preview.
		h.command(NextChapter)
		h.assertCount("selected:2:0", 1)
		time.Sleep(199 * time.Millisecond)
		synctest.Wait()
		h.assertCount("request:", 1)
		h.assertCount("load:", 1)
		time.Sleep(time.Millisecond)
		synctest.Wait()
		h.assertCount("request:2:0", 1)
		h.assertCount("buffering:2:3", 1)
		h.ready(Target{2, 0}, "three.aiff")
		h.assertPath("three.aiff", true)
		h.assertCount("load:", 2)
	})
}
