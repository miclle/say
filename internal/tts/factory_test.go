package tts

import "testing"

func TestNewDefaultsToSystemProvider(t *testing.T) {
	synthesizer, err := New(Options{})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	if got, want := synthesizer.Extension(), ".aiff"; got != want {
		t.Fatalf("Extension() = %q, want %q", got, want)
	}
}

func TestNewRejectsUnknownProvider(t *testing.T) {
	if _, err := New(Options{Provider: "unknown"}); err == nil {
		t.Fatal("New() error = nil, want unsupported-provider error")
	}
}

func TestNewSelectsEdgeProvider(t *testing.T) {
	synthesizer, err := New(Options{Provider: ProviderEdge})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	if got, want := synthesizer.Extension(), ".mp3"; got != want {
		t.Fatalf("Extension() = %q, want %q", got, want)
	}
}

func TestNewRejectsOptionsFromAnotherProvider(t *testing.T) {
	for _, tt := range []struct {
		name    string
		options Options
	}{
		{name: "speed with system", options: Options{Provider: ProviderSystem, Speed: 1.25}},
		{name: "rate with edge", options: Options{Provider: ProviderEdge, Rate: 210}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := New(tt.options); err == nil {
				t.Fatalf("New(%#v) error = nil, want provider-option error", tt.options)
			}
		})
	}
}
