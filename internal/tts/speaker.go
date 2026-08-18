package tts

import "context"

// Speaker converts one bounded text unit to audible speech and blocks until it finishes.
type Speaker interface {
	Name() string
	Speak(ctx context.Context, text string) error
}
