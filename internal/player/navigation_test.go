package player

import (
	"testing"
	"testing/synctest"
	"time"
)

func TestNavigationPreservesPauseAndUsesCachedAudio(t *testing.T) {
	for _, playing := range []bool{true, false} {
		t.Run(map[bool]string{true: "playing", false: "paused"}[playing], func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				h := newDemandHarness(t, []string{"One. Two.", "Three."})
				h.ready(Target{}, "one.aiff")
				h.ready(Target{0, 1}, "two.aiff")
				h.ready(Target{1, 0}, "three.aiff")
				if !playing {
					h.command(Toggle)
				}
				for _, step := range []struct {
					command Command
					path    string
				}{
					{Forward, "two.aiff"}, {Forward, "three.aiff"}, {Backward, "two.aiff"}, {PreviousChapter, "two.aiff"},
					{NextChapter, "three.aiff"}, {PreviousChapter, "one.aiff"},
				} {
					h.command(step.command)
					h.settle()
					h.assertPath(step.path, playing)
				}
				h.assertCount("buffering:", 0)
			})
		})
	}
}

func TestNavigationMixedCommandsAndReversalUseTextBoundaries(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		h := newDemandHarness(t, []string{"One. Two.", "Three. Four.", "Five."})
		h.ready(Target{}, "one.aiff")
		for _, command := range []Command{NextChapter, NextChapter, Backward, PreviousChapter, Forward} {
			h.command(command)
			time.Sleep(20 * time.Millisecond)
		}
		h.assertCount("request:", 1)
		h.settle()
		h.assertCount("request:0:1", 1)
		h.ready(Target{2, 0}, "five.aiff")
		h.assertCount("load:", 1)
		h.ready(Target{0, 1}, "two.aiff")
		h.assertPath("two.aiff", true)
	})
}

func TestNavigationAtEdgesDoesNotRestartAudioButExtendsActiveSelection(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		h := newDemandHarness(t, []string{"One.", "Two."})
		h.ready(Target{}, "one.aiff")
		h.transport.setPosition(time.Second)
		h.command(Backward)
		h.command(PreviousChapter)
		h.assertCount("suspend", 0)
		if h.transport.Position() != time.Second {
			t.Fatal("edge restarted audio")
		}
		h.command(NextChapter)
		time.Sleep(150 * time.Millisecond)
		h.command(NextChapter)
		time.Sleep(100 * time.Millisecond)
		synctest.Wait()
		h.assertCount("request:", 1)
		h.settle()
		h.ready(Target{1, 0}, "two.aiff")
		h.transport.setPosition(time.Second)
		h.command(Forward)
		h.command(NextChapter)
		if h.transport.Position() != time.Second {
			t.Fatal("last edge restarted audio")
		}
	})
}

func TestNavigationCanSkipBeforeFirstAudioAndFinishWithoutSkippedChapters(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		h := newDemandHarness(t, []string{"One.", "Two.", "Three."})
		h.command(NextChapter)
		h.command(NextChapter)
		h.command(Toggle)
		h.settle()
		h.ready(Target{}, "one.aiff")
		h.assertCount("load:", 0)
		h.ready(Target{2, 0}, "three.aiff")
		h.assertPath("three.aiff", false)
		h.command(Toggle)
		h.finishSentence()
		if err := <-h.done; err != nil {
			t.Fatal(err)
		}
		h.assertCount("finish:3", 1)
	})
}

func TestNavigationSpaceDuringDebounceDoesNotResumeOldAudio(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		h := newDemandHarness(t, []string{"One.", "Two."})
		h.ready(Target{}, "one.aiff")
		h.command(NextChapter)
		h.command(Toggle)
		h.command(Toggle)
		h.assertCount("play:one.aiff", 1)
		h.settle()
		h.command(Toggle)
		h.ready(Target{1, 0}, "two.aiff")
		h.assertPath("two.aiff", false)
	})
}

func TestNavigationRemotePlaybackCommandsAreIdempotent(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		h := newDemandHarness(t, []string{"One."})
		h.ready(Target{}, "one.aiff")

		h.command(ResumePlayback)
		h.command(ResumePlayback)
		h.assertPath("one.aiff", true)
		h.assertCount("play:one.aiff", 1)

		h.command(PausePlayback)
		h.command(PausePlayback)
		h.assertPath("one.aiff", false)
		h.assertCount("paused:0", 1)

		h.command(ResumePlayback)
		h.command(ResumePlayback)
		h.assertPath("one.aiff", true)
		h.assertCount("resumed:0", 1)
	})
}

func TestNavigationRemotePlaybackIntentUpdatesViewDuringSelection(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		h := newDemandHarness(t, []string{"One.", "Two."})
		h.ready(Target{}, "one.aiff")
		h.command(NextChapter)

		h.command(PausePlayback)
		h.assertCount("paused:1", 1)
		h.command(ResumePlayback)
		h.assertCount("resumed:1", 1)
	})
}

func TestNavigationQueuedArrowsSelectOnlyFinalAudio(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		h := newDemandHarness(t, []string{"One.", "Two.", "Three."})
		h.ready(Target{}, "one.aiff")
		h.ready(Target{1, 0}, "two.aiff")
		h.ready(Target{2, 0}, "three.aiff")
		h.commands <- NextChapter
		h.commands <- NextChapter
		h.commands <- PreviousChapter
		synctest.Wait()
		h.settle()
		h.assertPath("two.aiff", true)
		h.assertCount("load:", 2)
		h.assertCount("request:", 2)
	})
}
