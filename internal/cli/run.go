package cli

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"time"

	"github.com/miclle/say/internal/audio"
	"github.com/miclle/say/internal/document"
	"github.com/miclle/say/internal/player"
	"github.com/miclle/say/internal/terminal"
	"github.com/miclle/say/internal/textchunk"
	"github.com/miclle/say/internal/tts"
)

const defaultMaxChars = 500

type synthesizerFactory func(options tts.Options) (tts.Synthesizer, error)
type transportFactory func() (audioTransport, error)
type durationReader func(path string) (time.Duration, error)
type terminalDetector func(value any) bool
type rawInputFactory func(input io.Reader) (restore func() error, err error)
type providerSelector func(ctx context.Context, input io.Reader, output io.Writer) (tts.Provider, error)

type audioTransport interface {
	player.Transport
	Close() error
}

type dependencies struct {
	input            io.Reader
	newSynthesizer   synthesizerFactory
	newTransport     transportFactory
	readDuration     durationReader
	supportsTerminal terminalDetector
	beginRaw         rawInputFactory
	selectProvider   providerSelector
}

// Run executes the say command and returns a process exit code.
func Run(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	return runWithDependencies(ctx, args, stdout, stderr, dependencies{
		input:          os.Stdin,
		newSynthesizer: tts.New,
		newTransport: func() (audioTransport, error) {
			return audio.New()
		},
		readDuration:     audio.Duration,
		supportsTerminal: isCharacterDevice,
		beginRaw: func(input io.Reader) (func() error, error) {
			file, ok := input.(*os.File)
			if !ok {
				return nil, fmt.Errorf("interactive input is not a terminal file")
			}
			return terminal.BeginRawInput(file)
		},
		selectProvider: selectTTSProvider,
	})
}

func runWithDependencies(ctx context.Context, args []string, stdout, stderr io.Writer, deps dependencies) int {
	if err := ctx.Err(); err != nil {
		fmt.Fprintf(stderr, "say: interrupted: %v\n", err)
		return 130
	}

	flags := flag.NewFlagSet("say", flag.ContinueOnError)
	flags.SetOutput(stderr)
	provider := flags.String("provider", "", "TTS provider: system or edge (interactive: choose; non-interactive: system)")
	voice := flags.String("voice", "", "provider voice name (default: provider voice)")
	rate := flags.Int("rate", 0, "system speech rate in words per minute (default: system rate)")
	speed := flags.Float64("speed", 1, "Edge TTS speed multiplier, from 0.5 to 2.0")
	maxChars := flags.Int("max-chars", defaultMaxChars, "maximum Unicode characters per TTS call")
	noColor := flags.Bool("no-color", false, "disable ANSI terminal colors")
	flags.Usage = func() {
		fmt.Fprintln(stderr, "Usage: say [flags] <document>")
		fmt.Fprintln(stderr)
		fmt.Fprintln(stderr, "Read a UTF-8 text document, print each speech unit, and play it with TTS.")
		fmt.Fprintln(stderr)
		fmt.Fprintln(stderr, "Flags:")
		flags.PrintDefaults()
	}

	if err := flags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}
	explicit := make(map[string]bool)
	flags.Visit(func(flag *flag.Flag) {
		explicit[flag.Name] = true
	})
	if *maxChars <= 0 {
		fmt.Fprintln(stderr, "say: max-chars must be greater than zero")
		return 2
	}
	if *rate < 0 {
		fmt.Fprintln(stderr, "say: rate must not be negative")
		return 2
	}
	if flags.NArg() != 1 {
		fmt.Fprintln(stderr, "say: exactly one document path is required")
		flags.Usage()
		return 2
	}
	interactive := deps.supportsTerminal(deps.input) && deps.supportsTerminal(stdout)
	selectedProvider := tts.ProviderSystem
	if explicit["provider"] {
		selectedProvider = tts.Provider(*provider)
	}
	if explicit["provider"] || !interactive {
		if err := validateProviderFlags(selectedProvider, *rate, *speed, explicit); err != nil {
			fmt.Fprintf(stderr, "say: %v\n", err)
			return 2
		}
	}

	title, text, err := document.Read(flags.Arg(0))
	if err != nil {
		fmt.Fprintf(stderr, "say: %v\n", err)
		return 1
	}
	chunks, err := textchunk.Split(text, *maxChars)
	if err != nil {
		fmt.Fprintf(stderr, "say: split document: %v\n", err)
		return 1
	}
	if !explicit["provider"] && interactive {
		restoreSelection, err := deps.beginRaw(deps.input)
		if err != nil {
			fmt.Fprintf(stderr, "say: enable provider selection: %v\n", err)
			return 1
		}
		selectedProvider, err = deps.selectProvider(ctx, deps.input, stdout)
		restoreErr := restoreSelection()
		if restoreErr != nil {
			fmt.Fprintf(stderr, "say: restore terminal after provider selection: %v\n", restoreErr)
			return 1
		}
		if err != nil {
			if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
				fmt.Fprintf(stderr, "say: provider selection interrupted: %v\n", err)
				return 130
			}
			fmt.Fprintf(stderr, "say: select TTS provider: %v\n", err)
			return 1
		}
		if err := validateProviderFlags(selectedProvider, *rate, *speed, explicit); err != nil {
			fmt.Fprintf(stderr, "say: %v\n", err)
			return 2
		}
	}
	options := tts.Options{Provider: selectedProvider, Voice: *voice}
	if selectedProvider == tts.ProviderSystem {
		options.Rate = *rate
	} else {
		options.Speed = *speed
	}
	synthesizer, err := deps.newSynthesizer(options)
	if err != nil {
		fmt.Fprintf(stderr, "say: initialize TTS: %v\n", err)
		return 1
	}

	view := terminal.New(stdout, !*noColor && deps.supportsTerminal(stdout), title, synthesizer.Name())
	view.SetControls(interactive)
	if err := view.Preparing(len(chunks)); err != nil {
		fmt.Fprintf(stderr, "say: render preparation: %v\n", err)
		return 1
	}

	tempDir, err := os.MkdirTemp("", "say-audio-*")
	if err != nil {
		fmt.Fprintf(stderr, "say: create temporary audio directory: %v\n", err)
		return 1
	}
	defer os.RemoveAll(tempDir)

	transport, err := deps.newTransport()
	if err != nil {
		fmt.Fprintf(stderr, "say: initialize audio playback: %v\n", err)
		return 1
	}
	defer transport.Close()

	var commands <-chan player.Command
	restore := func() error { return nil }
	var cancelCommands context.CancelFunc = func() {}
	if interactive {
		restore, err = deps.beginRaw(deps.input)
		if err != nil {
			fmt.Fprintf(stderr, "say: enable playback controls: %v\n", err)
			return 1
		}
		commandCtx, cancel := context.WithCancel(ctx)
		cancelCommands = cancel
		commands = terminal.ReadCommands(commandCtx, deps.input)
	}

	preparationCtx, cancelPreparation := context.WithCancel(ctx)
	results, preparationDone := prepareTracks(preparationCtx, chunks, tempDir, synthesizer, deps.readDuration)
	playbackErr := player.Play(ctx, len(chunks), results, transport, commands, view)
	cancelPreparation()
	<-preparationDone
	cancelCommands()
	restoreErr := restore()
	if playbackErr != nil {
		if ctxErr := ctx.Err(); ctxErr != nil && errors.Is(playbackErr, ctxErr) {
			fmt.Fprintf(stderr, "say: playback interrupted: %v\n", playbackErr)
			return 130
		}
		fmt.Fprintf(stderr, "say: playback failed: %v\n", playbackErr)
		return 1
	}
	if restoreErr != nil {
		fmt.Fprintf(stderr, "say: %v\n", restoreErr)
		return 1
	}
	return 0
}

func validateProviderFlags(provider tts.Provider, rate int, speed float64, explicit map[string]bool) error {
	if provider != tts.ProviderSystem && provider != tts.ProviderEdge {
		return fmt.Errorf(`provider must be "system" or "edge"`)
	}
	if provider == tts.ProviderEdge && explicit["rate"] {
		return fmt.Errorf("rate is only supported by the system provider")
	}
	if provider == tts.ProviderSystem && explicit["speed"] {
		return fmt.Errorf("speed is only supported by the edge provider")
	}
	if provider == tts.ProviderEdge && (math.IsNaN(speed) || math.IsInf(speed, 0) || speed < 0.5 || speed > 2) {
		return fmt.Errorf("speed must be between 0.5 and 2.0")
	}
	return nil
}

func selectTTSProvider(ctx context.Context, input io.Reader, output io.Writer) (tts.Provider, error) {
	providers := []tts.Provider{tts.ProviderSystem, tts.ProviderEdge}
	selected, err := terminal.Select(ctx, input, output, "TTS provider", []string{
		"macOS system TTS",
		"Edge TTS (experimental · online)",
	})
	if err != nil {
		return "", err
	}
	return providers[selected], nil
}

func prepareTracks(
	ctx context.Context,
	chunks []string,
	tempDir string,
	synthesizer tts.Synthesizer,
	readDuration durationReader,
) (<-chan player.TrackResult, <-chan struct{}) {
	results := make(chan player.TrackResult)
	done := make(chan struct{})
	go func() {
		defer close(done)
		defer close(results)
		for i, chunk := range chunks {
			if ctx.Err() != nil {
				return
			}
			outputPath := filepath.Join(tempDir, fmt.Sprintf("%06d%s", i+1, synthesizer.Extension()))
			if err := synthesizer.Synthesize(ctx, chunk, outputPath); err != nil {
				if ctxErr := ctx.Err(); ctxErr != nil {
					return
				}
				sendTrackResult(ctx, results, player.TrackResult{
					Err: fmt.Errorf("synthesize track %d of %d: %w", i+1, len(chunks), err),
				})
				return
			}
			duration, err := readDuration(outputPath)
			if err != nil {
				sendTrackResult(ctx, results, player.TrackResult{
					Err: fmt.Errorf("inspect track %d of %d: %w", i+1, len(chunks), err),
				})
				return
			}
			if duration <= 0 {
				sendTrackResult(ctx, results, player.TrackResult{
					Err: fmt.Errorf("inspect track %d of %d: audio duration must be greater than zero", i+1, len(chunks)),
				})
				return
			}
			if !sendTrackResult(ctx, results, player.TrackResult{
				Track: player.Track{Text: chunk, Path: outputPath, Duration: duration},
			}) {
				return
			}
		}
	}()
	return results, done
}

func sendTrackResult(ctx context.Context, results chan<- player.TrackResult, result player.TrackResult) bool {
	select {
	case results <- result:
		return true
	case <-ctx.Done():
		return false
	}
}

func isCharacterDevice(value any) bool {
	file, ok := value.(*os.File)
	if !ok {
		return false
	}
	info, err := file.Stat()
	return err == nil && info.Mode()&os.ModeCharDevice != 0
}
