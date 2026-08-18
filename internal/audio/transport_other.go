//go:build !darwin || !cgo

package audio

import (
	"fmt"
	"runtime"
	"time"
)

// Transport is unavailable outside macOS builds with cgo enabled.
type Transport struct{}

// New reports the native playback platform requirement.
func New() (*Transport, error) {
	return nil, fmt.Errorf("native audio playback requires macOS with cgo enabled (running on %s)", runtime.GOOS)
}

func (t *Transport) Duration(string) (time.Duration, error) { return 0, unsupported() }
func (t *Transport) Load(string) error                      { return unsupported() }
func (t *Transport) Play() error                            { return unsupported() }
func (t *Transport) Pause()                                 {}
func (t *Transport) Seek(time.Duration) error               { return unsupported() }
func (t *Transport) Position() time.Duration                { return 0 }
func (t *Transport) IsPlaying() bool                        { return false }
func (t *Transport) Close() error                           { return nil }

// Duration reports the native playback platform requirement.
func Duration(string) (time.Duration, error) { return 0, unsupported() }

func unsupported() error {
	return fmt.Errorf("native audio playback requires macOS with cgo enabled")
}
