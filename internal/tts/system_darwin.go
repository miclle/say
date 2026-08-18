//go:build darwin

package tts

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
)

const systemSayPath = "/usr/bin/say"

type systemSpeaker struct {
	executable string
	voice      string
	rate       int
}

// NewSystem constructs the macOS system speech synthesizer.
func NewSystem(voice string, rate int) (Speaker, error) {
	if rate < 0 {
		return nil, fmt.Errorf("rate must not be negative")
	}
	return newSystemSpeaker(systemSayPath, voice, rate), nil
}

func newSystemSpeaker(executable, voice string, rate int) *systemSpeaker {
	return &systemSpeaker{executable: executable, voice: voice, rate: rate}
}

func (s *systemSpeaker) Name() string {
	if s.voice == "" {
		return "macOS say (system voice)"
	}
	return fmt.Sprintf("macOS say (%s)", s.voice)
}

func (s *systemSpeaker) Speak(ctx context.Context, text string) error {
	if strings.TrimSpace(text) == "" {
		return fmt.Errorf("speech text is empty")
	}

	args := make([]string, 0, 4)
	if s.voice != "" {
		args = append(args, "-v", s.voice)
	}
	if s.rate > 0 {
		args = append(args, "-r", strconv.Itoa(s.rate))
	}

	cmd := exec.CommandContext(ctx, s.executable, args...)
	cmd.Stdin = strings.NewReader(text)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return ctxErr
		}
		diagnostic := strings.TrimSpace(stderr.String())
		if diagnostic != "" {
			return fmt.Errorf("system TTS failed: %w: %s", err, diagnostic)
		}
		return fmt.Errorf("system TTS failed: %w", err)
	}
	return nil
}
