//go:build !darwin

package tts

import (
	"fmt"
	"runtime"
)

// NewSystem reports that the initial system TTS adapter is macOS-only.
func NewSystem(_ string, rate int) (Synthesizer, error) {
	if rate < 0 {
		return nil, fmt.Errorf("rate must not be negative")
	}
	return nil, fmt.Errorf("system TTS is not supported on %s; this release supports macOS", runtime.GOOS)
}
