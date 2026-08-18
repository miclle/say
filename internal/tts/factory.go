package tts

import "fmt"

// Provider identifies a speech synthesis backend.
type Provider string

const (
	ProviderSystem Provider = "system"
	ProviderEdge   Provider = "edge"
)

// Options contains provider-neutral and provider-specific synthesis settings.
type Options struct {
	Provider Provider
	Voice    string
	Rate     int
	Speed    float64
}

// New constructs the requested speech synthesizer. An empty provider selects
// the local system service for backward compatibility.
func New(options Options) (Synthesizer, error) {
	switch options.Provider {
	case "", ProviderSystem:
		if options.Speed != 0 {
			return nil, fmt.Errorf("speed is only supported by the edge provider")
		}
		return NewSystem(options.Voice, options.Rate)
	case ProviderEdge:
		if options.Rate != 0 {
			return nil, fmt.Errorf("rate is only supported by the system provider")
		}
		return NewEdge(options.Voice, options.Speed)
	default:
		return nil, fmt.Errorf("unsupported TTS provider %q", options.Provider)
	}
}
