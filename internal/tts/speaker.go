package tts

import "context"

// Synthesizer converts one bounded text unit into an audio file.
type Synthesizer interface {
	Name() string
	Synthesize(ctx context.Context, text, outputPath string) error
}
