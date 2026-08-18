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

type systemSynthesizer struct {
	executable string
	voice      string
	rate       int
}

// NewSystem constructs the macOS system speech synthesizer.
func NewSystem(voice string, rate int) (Synthesizer, error) {
	if rate < 0 {
		return nil, fmt.Errorf("rate must not be negative")
	}
	return newSystemSynthesizer(systemSayPath, voice, rate), nil
}

func newSystemSynthesizer(executable, voice string, rate int) *systemSynthesizer {
	return &systemSynthesizer{executable: executable, voice: voice, rate: rate}
}

func (s *systemSynthesizer) Name() string {
	if s.voice == "" {
		return "macOS say (system voice)"
	}
	return fmt.Sprintf("macOS say (%s)", s.voice)
}

func (s *systemSynthesizer) Extension() string {
	return ".aiff"
}

func (s *systemSynthesizer) Synthesize(ctx context.Context, text, outputPath string) error {
	if strings.TrimSpace(text) == "" {
		return fmt.Errorf("speech text is empty")
	}
	if strings.TrimSpace(outputPath) == "" {
		return fmt.Errorf("output path is empty")
	}

	args := []string{"-o", outputPath}
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
			return fmt.Errorf("system TTS synthesis failed: %w: %s", err, diagnostic)
		}
		return fmt.Errorf("system TTS synthesis failed: %w", err)
	}
	return nil
}
