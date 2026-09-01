//go:build darwin && cgo

package desktop

import (
	"context"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/miclle/say/internal/player"
)

const nativeLifecycleHelper = "SAY_NATIVE_LIFECYCLE_HELPER"

func TestMain(m *testing.M) {
	if os.Getenv(nativeLifecycleHelper) == "1" {
		os.Exit(runNativeLifecycleHelper())
	}
	os.Exit(m.Run())
}

func TestNativeLifecycleOnStartupThread(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, os.Args[0], "-test.run=^$")
	command.Env = append(os.Environ(), nativeLifecycleHelper+"=1")
	output, err := command.CombinedOutput()
	if ctx.Err() != nil {
		t.Fatalf("native lifecycle helper timed out: %v; output=%s", ctx.Err(), output)
	}
	if err != nil {
		t.Fatalf("native lifecycle helper failed: %v; output=%s", err, output)
	}
}

func runNativeLifecycleHelper() int {
	fullText := strings.Repeat("Long native sentence segment ", 4) + "end."
	code := Run(context.Background(), func(surface Controls) int {
		if surface == nil {
			return 10
		}
		if err := surface.Configure("Lifecycle", []string{fullText}); err != nil {
			return 11
		}
		if err := surface.Start(1); err != nil {
			return 12
		}
		if err := surface.Track(0, 1, 0, fullText, true, time.Second, 3*time.Second); err != nil {
			return 13
		}
		nativeControls := surface.(*controls)
		backend := nativeControls.backend.(*nativeBackend)
		if !backend.statusItemsVisible() || !backend.remoteCommandsRegistered() {
			return 18
		}
		if !backend.statusTitleEquals(displayText(fullText)) {
			return 19
		}
		if !nativeNowPlayingTitleEquals(fullText) {
			return 21
		}
		if err := surface.Paused(0, 1); err != nil {
			return 14
		}
		if err := surface.Resumed(0, 1); err != nil {
			return 15
		}
		if err := surface.Close(); err != nil {
			return 16
		}
		if backend.statusItemsVisible() || backend.remoteCommandsRegistered() || !nativeNowPlayingCleared() {
			return 20
		}
		return 0
	})
	if code != 0 {
		return code
	}
	if !nativeNowPlayingCleared() {
		return 17
	}
	return 0
}

func TestNativeCommandMapping(t *testing.T) {
	for _, test := range []struct {
		value int
		want  player.Command
		ok    bool
	}{
		{nativeToggle, player.Toggle, true},
		{nativeBackward, player.Backward, true},
		{nativeForward, player.Forward, true},
		{nativeResume, player.ResumePlayback, true},
		{nativePause, player.PausePlayback, true},
		{255, 0, false},
	} {
		got, ok := commandFromNative(test.value)
		if got != test.want || ok != test.ok {
			t.Fatalf("commandFromNative(%d)=(%v,%t) want=(%v,%t)", test.value, got, ok, test.want, test.ok)
		}
	}
}

func TestDisplayTextTruncatesLongUTF8ByRunes(t *testing.T) {
	text := strings.Repeat("界", menuTextRunes+5)
	got := displayText(text)
	if runes := []rune(got); len(runes) != menuTextRunes || runes[len(runes)-1] != '…' {
		t.Fatalf("displayText()=%q runes=%d", got, len(runes))
	}
	if got := displayText("  Short sentence.  "); got != "Short sentence." {
		t.Fatalf("displayText()=%q", got)
	}
}
