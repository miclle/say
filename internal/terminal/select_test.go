package terminal

import (
	"bytes"
	"context"
	"errors"
	"testing"
	"time"
)

func TestSelectMovesDownAndConfirms(t *testing.T) {
	var output bytes.Buffer
	selected, err := Select(
		context.Background(),
		bytes.NewBufferString("\x1b[B\r"),
		&output,
		"TTS provider",
		[]string{"macOS system TTS", "Microsoft Edge TTS (Experimental)"},
	)
	if err != nil {
		t.Fatalf("Select() error = %v", err)
	}
	if selected != 1 {
		t.Fatalf("Select() = %d, want 1", selected)
	}
	want := "TTS provider  › macOS system TTS  (↑/↓ choose · Enter confirm)" +
		"\r\x1b[2KTTS provider  › Microsoft Edge TTS (Experimental)  (↑/↓ choose · Enter confirm)\n"
	if got := output.String(); got != want {
		t.Fatalf("output = %q, want %q", got, want)
	}
}

func TestSelectMovesUpWithWraparound(t *testing.T) {
	selected, err := Select(
		context.Background(),
		bytes.NewBufferString("\x1b[A\n"),
		&bytes.Buffer{},
		"TTS provider",
		[]string{"system", "edge"},
	)
	if err != nil {
		t.Fatalf("Select() error = %v", err)
	}
	if selected != 1 {
		t.Fatalf("Select() = %d, want wrapped index 1", selected)
	}
}

func TestSelectEnterConfirmsInitialOption(t *testing.T) {
	selected, err := Select(context.Background(), bytes.NewBufferString("\r"), &bytes.Buffer{}, "TTS provider", []string{"system", "edge"})
	if err != nil {
		t.Fatalf("Select() error = %v", err)
	}
	if selected != 0 {
		t.Fatalf("Select() = %d, want initial index 0", selected)
	}
}

func TestSelectReturnsCanceledForControlC(t *testing.T) {
	_, err := Select(context.Background(), bytes.NewBuffer([]byte{0x03}), &bytes.Buffer{}, "TTS provider", []string{"system", "edge"})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Select() error = %v, want context.Canceled", err)
	}
}

func TestSelectReturnsWhenContextIsCanceledDuringRead(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		_, err := Select(ctx, blockingSelectionReader{}, &bytes.Buffer{}, "TTS provider", []string{"system", "edge"})
		done <- err
	}()
	cancel()

	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("Select() error = %v, want context.Canceled", err)
		}
	case <-time.After(time.Second):
		t.Fatal("Select() did not return after context cancellation")
	}
}

type blockingSelectionReader struct{}

func (blockingSelectionReader) Read([]byte) (int, error) {
	select {}
}
