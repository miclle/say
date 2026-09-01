package desktop

import (
	"errors"
	"testing"
	"time"

	"github.com/miclle/say/internal/player"
)

func TestControlsReducePlaybackEventsIntoDesktopSnapshot(t *testing.T) {
	backend := &recordingBackend{}
	controls := newControls(backend)
	if err := controls.Configure("Lesson", []string{"One. Two.", "Three."}); err != nil {
		t.Fatal(err)
	}
	if len(backend.snapshots) != 0 {
		t.Fatalf("configuration rendered hidden state: %#v", backend.snapshots)
	}

	if err := controls.Start(2); err != nil {
		t.Fatal(err)
	}
	assertSnapshot(t, backend.last(), Snapshot{
		Visible: true, Document: "Lesson", Text: "One.", Playing: true,
		Chapter: 0, Chapters: 2, Sentence: 0, Sentences: 2, QueueIndex: 0, QueueCount: 3,
	})
	if err := controls.Track(0, 2, 0, "One.", true, 0, 3*time.Second); err != nil {
		t.Fatal(err)
	}
	if snapshot := backend.last(); snapshot.Duration != 3*time.Second || snapshot.Position != 0 {
		t.Fatalf("track snapshot=%#v", snapshot)
	}

	if err := controls.Selected(0, 2, "One. Two.", 1); err != nil {
		t.Fatal(err)
	}
	selected := backend.last()
	if selected.Text != "Two." || !selected.Busy || !selected.Playing || selected.QueueIndex != 1 {
		t.Fatalf("selected snapshot=%#v", selected)
	}

	if err := controls.Buffering(0, 2); err != nil {
		t.Fatal(err)
	}
	if snapshot := backend.last(); !snapshot.Busy || snapshot.Text != "Two." {
		t.Fatalf("buffering snapshot=%#v", snapshot)
	}
	if err := controls.Buffering(1, 2); err != nil {
		t.Fatal(err)
	}
	if snapshot := backend.last(); !snapshot.Busy || snapshot.Text != "Three." || snapshot.Chapter != 1 || snapshot.QueueIndex != 2 {
		t.Fatalf("cross-chapter buffering snapshot=%#v", snapshot)
	}

	if err := controls.Seeked(1, 2, "Three.", 0, false, 0, time.Second, 3*time.Second, false); err != nil {
		t.Fatal(err)
	}
	seeked := backend.last()
	if seeked.Text != "Three." || seeked.Playing || seeked.Busy || seeked.QueueIndex != 2 {
		t.Fatalf("seeked snapshot=%#v", seeked)
	}

	if err := controls.Resumed(1, 2); err != nil {
		t.Fatal(err)
	}
	if !backend.last().Playing {
		t.Fatal("resume did not update playback state")
	}
	if err := controls.Paused(1, 2); err != nil {
		t.Fatal(err)
	}
	if snapshot := backend.last(); snapshot.Playing || snapshot.Position != time.Second {
		t.Fatalf("paused snapshot=%#v; want stopped playback with preserved position", snapshot)
	}
}

func TestControlsClearDesktopOnFinishAndFailure(t *testing.T) {
	for _, finish := range []bool{true, false} {
		backend := &recordingBackend{}
		controls := newControls(backend)
		if err := controls.Configure("Lesson", []string{"One."}); err != nil {
			t.Fatal(err)
		}
		if err := controls.Start(1); err != nil {
			t.Fatal(err)
		}
		var err error
		if finish {
			err = controls.Finish(1)
		} else {
			err = controls.Failed(0, 1, errors.New("failed"))
		}
		if err != nil {
			t.Fatal(err)
		}
		if backend.clears != 1 {
			t.Fatalf("clear calls=%d want=1", backend.clears)
		}
	}
}

func TestControlsDeliverOnlySupportedBufferedCommands(t *testing.T) {
	controls := newControls(&recordingBackend{})
	for _, command := range []player.Command{
		player.Toggle, player.Backward, player.Forward, player.ResumePlayback, player.PausePlayback,
	} {
		if !controls.emit(command) {
			t.Fatalf("emit(%v)=false", command)
		}
		if got := <-controls.Commands(); got != command {
			t.Fatalf("command=%v want=%v", got, command)
		}
	}
	if controls.emit(player.Command(255)) {
		t.Fatal("unsupported command was accepted")
	}
}

type recordingBackend struct {
	snapshots []Snapshot
	clears    int
}

func (backend *recordingBackend) Render(snapshot Snapshot) error {
	backend.snapshots = append(backend.snapshots, snapshot)
	return nil
}

func (backend *recordingBackend) Clear() error {
	backend.clears++
	return nil
}

func (backend *recordingBackend) last() Snapshot {
	return backend.snapshots[len(backend.snapshots)-1]
}

func assertSnapshot(t *testing.T, got, want Snapshot) {
	t.Helper()
	if got != want {
		t.Fatalf("snapshot=%#v want=%#v", got, want)
	}
}
