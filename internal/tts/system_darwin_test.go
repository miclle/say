//go:build darwin

package tts

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestSystemSynthesizerSendsOutputAndOptionsAsArgumentsAndTextOnStdin(t *testing.T) {
	dir := t.TempDir()
	argsPath := filepath.Join(dir, "args")
	stdinPath := filepath.Join(dir, "stdin")
	t.Setenv("SAY_TEST_ARGS", argsPath)
	t.Setenv("SAY_TEST_STDIN", stdinPath)
	executable := writeExecutable(t, dir, "recorder", `#!/bin/sh
printf '%s\n' "$@" > "$SAY_TEST_ARGS"
cat > "$SAY_TEST_STDIN"
`)

	synthesizer := newSystemSynthesizer(executable, "Tingting", 210)
	outputPath := filepath.Join(dir, "speech.aiff")
	if err := synthesizer.Synthesize(context.Background(), "你好；$(touch should-not-run)", outputPath); err != nil {
		t.Fatalf("Synthesize() error = %v", err)
	}

	args, err := os.ReadFile(argsPath)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := string(args), "-o\n"+outputPath+"\n-v\nTingting\n-r\n210\n"; got != want {
		t.Fatalf("arguments = %q, want %q", got, want)
	}
	stdin, err := os.ReadFile(stdinPath)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := string(stdin), "你好；$(touch should-not-run)"; got != want {
		t.Fatalf("stdin = %q, want %q", got, want)
	}
}

func TestSystemSynthesizerReturnsCommandDiagnostics(t *testing.T) {
	executable := writeExecutable(t, t.TempDir(), "failure", "#!/bin/sh\necho 'voice unavailable' >&2\nexit 7\n")

	err := newSystemSynthesizer(executable, "", 0).Synthesize(context.Background(), "hello", filepath.Join(t.TempDir(), "speech.aiff"))
	if err == nil || !strings.Contains(err.Error(), "voice unavailable") {
		t.Fatalf("Synthesize() error = %v, want command diagnostic", err)
	}
}

func TestSystemSynthesizerRejectsEmptyInput(t *testing.T) {
	synthesizer := newSystemSynthesizer("unused", "", 0)

	for _, tt := range []struct {
		name       string
		text       string
		outputPath string
		want       string
	}{
		{name: "text", text: " \n", outputPath: "speech.aiff", want: "speech text is empty"},
		{name: "output", text: "hello", outputPath: "", want: "output path is empty"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			err := synthesizer.Synthesize(context.Background(), tt.text, tt.outputPath)
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("Synthesize() error = %v, want containing %q", err, tt.want)
			}
		})
	}
}

func TestNewSystemRejectsNegativeRate(t *testing.T) {
	_, err := NewSystem("", -1)
	if err == nil || !strings.Contains(err.Error(), "rate must not be negative") {
		t.Fatalf("NewSystem() error = %v, want negative-rate error", err)
	}
}

func TestSystemSynthesizerTerminatesProcessWhenCanceled(t *testing.T) {
	dir := t.TempDir()
	readyPath := filepath.Join(dir, "ready")
	t.Setenv("SAY_TEST_READY", readyPath)
	executable := writeExecutable(t, dir, "blocking", "#!/bin/sh\ntouch \"$SAY_TEST_READY\"\nexec sleep 30\n")
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)

	go func() {
		done <- newSystemSynthesizer(executable, "", 0).Synthesize(ctx, "hello", filepath.Join(dir, "speech.aiff"))
	}()
	waitForFile(t, readyPath)
	cancel()

	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("Synthesize() error = %v, want context.Canceled", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Synthesize() did not terminate promptly after cancellation")
	}
}

func waitForFile(t *testing.T, path string) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(path); err == nil {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", path)
}

func writeExecutable(t *testing.T, dir, name, content string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(content), 0o700); err != nil {
		t.Fatal(err)
	}
	return path
}
