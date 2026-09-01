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
	"sync"
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
type documentReader func(ctx context.Context, source string, progress document.ProgressFunc) (name string, text string, err error)
type terminalDetector func(value any) bool
type rawInputFactory func(input io.Reader) (restore func() error, err error)
type providerSelector func(ctx context.Context, input io.Reader, output io.Writer) (tts.Provider, error)

type audioTransport interface {
	player.Transport
	Close() error
}

// PlaybackControls is an optional non-terminal playback surface.
type PlaybackControls interface {
	player.View
	Configure(title string, chapters []string) error
	Commands() <-chan player.Command
	Close() error
}

type dependencies struct {
	input            io.Reader
	readDocument     documentReader
	newSynthesizer   synthesizerFactory
	newTransport     transportFactory
	readDuration     durationReader
	supportsTerminal terminalDetector
	beginRaw         rawInputFactory
	selectProvider   providerSelector
	controls         PlaybackControls
}

// Run executes the say command and returns a process exit code.
func Run(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	return runWithDependencies(ctx, args, stdout, stderr, defaultDependencies(nil))
}

// RunWithControls executes say with a desktop playback surface.
func RunWithControls(ctx context.Context, args []string, stdout, stderr io.Writer, controls PlaybackControls) int {
	return runWithDependencies(ctx, args, stdout, stderr, defaultDependencies(controls))
}

func defaultDependencies(controls PlaybackControls) dependencies {
	return dependencies{
		input:          os.Stdin,
		readDocument:   document.ReadSourceWithProgress,
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
		controls:       controls,
	}
}

type commandFlags struct {
	provider  *string
	voice     *string
	rate      *int
	speed     *float64
	maxChars  *int
	noColor   *bool
	noMenuBar *bool
}

func registerCommandFlags(flags *flag.FlagSet) commandFlags {
	return commandFlags{
		provider:  flags.String("provider", "", "TTS provider: system or edge (interactive: choose; non-interactive: system)"),
		voice:     flags.String("voice", "", "provider voice name (default: provider voice)"),
		rate:      flags.Int("rate", 0, "system speech rate in words per minute (default: system rate)"),
		speed:     flags.Float64("speed", 1, "Edge TTS speed multiplier, from 0.5 to 2.0"),
		maxChars:  flags.Int("max-chars", defaultMaxChars, "maximum Unicode characters per TTS call"),
		noColor:   flags.Bool("no-color", false, "disable ANSI terminal colors"),
		noMenuBar: flags.Bool("no-menu-bar", false, "disable macOS menu bar and system media controls"),
	}
}

func (options commandFlags) basicError() error {
	if *options.maxChars <= 0 {
		return fmt.Errorf("max-chars must be greater than zero")
	}
	if *options.rate < 0 {
		return fmt.Errorf("rate must not be negative")
	}
	return nil
}

func explicitFlags(flags *flag.FlagSet) map[string]bool {
	explicit := make(map[string]bool)
	flags.Visit(func(flag *flag.Flag) { explicit[flag.Name] = true })
	return explicit
}

// WantsMenuBar reports whether args describe a playback invocation that has
// not explicitly disabled the desktop surface.
func WantsMenuBar(args []string) bool {
	interactive := isCharacterDevice(os.Stdin) && isCharacterDevice(os.Stdout)
	return wantsMenuBar(args, interactive)
}

func wantsMenuBar(args []string, interactive bool) bool {
	flags := flag.NewFlagSet("say", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	options := registerCommandFlags(flags)
	if err := flags.Parse(args); err != nil || *options.noMenuBar || flags.NArg() != 1 || options.basicError() != nil {
		return false
	}
	explicit := explicitFlags(flags)
	if explicit["provider"] {
		if err := validateProviderFlags(tts.Provider(*options.provider), *options.rate, *options.speed, explicit); err != nil {
			return false
		}
	} else if !interactive {
		if err := validateProviderFlags(tts.ProviderSystem, *options.rate, *options.speed, explicit); err != nil {
			return false
		}
	} else if explicit["rate"] && explicit["speed"] {
		return false
	} else if explicit["speed"] {
		if err := validateProviderFlags(tts.ProviderEdge, *options.rate, *options.speed, explicit); err != nil {
			return false
		}
	}
	return true
}

func runWithDependencies(ctx context.Context, args []string, stdout, stderr io.Writer, deps dependencies) int {
	if err := ctx.Err(); err != nil {
		fmt.Fprintf(stderr, "say: interrupted: %v\n", err)
		return 130
	}

	flags := flag.NewFlagSet("say", flag.ContinueOnError)
	flags.SetOutput(stderr)
	options := registerCommandFlags(flags)
	flags.Usage = func() {
		fmt.Fprintln(stderr, "Usage: say [flags] <document-or-url>")
		fmt.Fprintln(stderr)
		fmt.Fprintln(stderr, "Read a local text, Markdown, or Word document, or HTTP(S) web article, print each speech unit, and play it with TTS.")
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
	explicit := explicitFlags(flags)
	if err := options.basicError(); err != nil {
		fmt.Fprintf(stderr, "say: %v\n", err)
		return 2
	}
	if flags.NArg() != 1 {
		fmt.Fprintln(stderr, "say: exactly one document path or web URL is required")
		flags.Usage()
		return 2
	}
	terminalOutput := deps.supportsTerminal(stdout)
	interactive := deps.supportsTerminal(deps.input) && terminalOutput
	selectedProvider := tts.ProviderSystem
	if explicit["provider"] {
		selectedProvider = tts.Provider(*options.provider)
	}
	if explicit["provider"] || !interactive {
		if err := validateProviderFlags(selectedProvider, *options.rate, *options.speed, explicit); err != nil {
			fmt.Fprintf(stderr, "say: %v\n", err)
			return 2
		}
	}

	title, text, err := readDocumentWithLoading(ctx, flags.Arg(0), stdout, !*options.noColor && terminalOutput, terminalOutput, deps.readDocument)
	if err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil && errors.Is(err, ctxErr) {
			fmt.Fprintf(stderr, "say: source loading interrupted: %v\n", err)
			return 130
		}
		fmt.Fprintf(stderr, "say: %v\n", err)
		return 1
	}
	chunks, err := textchunk.Split(text, *options.maxChars)
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
		if err := validateProviderFlags(selectedProvider, *options.rate, *options.speed, explicit); err != nil {
			fmt.Fprintf(stderr, "say: %v\n", err)
			return 2
		}
	}
	ttsOptions := tts.Options{Provider: selectedProvider, Voice: *options.voice}
	if selectedProvider == tts.ProviderSystem {
		ttsOptions.Rate = *options.rate
	} else {
		ttsOptions.Speed = *options.speed
	}
	synthesizer, err := deps.newSynthesizer(ttsOptions)
	if err != nil {
		fmt.Fprintf(stderr, "say: initialize TTS: %v\n", err)
		return 1
	}

	terminalView := terminal.New(stdout, !*options.noColor && terminalOutput, title, synthesizer.Name())
	terminalView.SetChapters(chunks)
	terminalView.SetControls(interactive)
	if err := terminalView.Preparing(len(chunks)); err != nil {
		fmt.Fprintf(stderr, "say: render preparation: %v\n", err)
		return 1
	}
	var view player.View = terminalView
	if deps.controls != nil {
		if err := deps.controls.Configure(title, chunks); err != nil {
			fmt.Fprintf(stderr, "say: initialize desktop controls: %v\n", err)
			return 1
		}
		defer deps.controls.Close()
		view = player.CombineViews(terminalView, deps.controls)
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

	var commandSources []<-chan player.Command
	restore := func() error { return nil }
	commandCtx, cancelCommands := context.WithCancel(ctx)
	defer cancelCommands()
	if interactive {
		restore, err = deps.beginRaw(deps.input)
		if err != nil {
			fmt.Fprintf(stderr, "say: enable playback controls: %v\n", err)
			return 1
		}
		commandSources = append(commandSources, terminal.ReadCommands(commandCtx, deps.input))
	}
	if deps.controls != nil {
		commandSources = append(commandSources, deps.controls.Commands())
	}
	commands := mergeCommands(commandCtx, commandSources...)

	preparation := prepareAudio(ctx, chunks, tempDir, synthesizer, deps.readDuration)
	playbackErr := player.Play(ctx, chunks, preparation, transport, commands, view)
	preparation.Close()
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

func mergeCommands(ctx context.Context, sources ...<-chan player.Command) <-chan player.Command {
	if len(sources) == 0 {
		return nil
	}
	commands := make(chan player.Command, 32)
	var workers sync.WaitGroup
	for _, source := range sources {
		if source == nil {
			continue
		}
		workers.Add(1)
		go func(source <-chan player.Command) {
			defer workers.Done()
			for {
				select {
				case <-ctx.Done():
					return
				case command, ok := <-source:
					if !ok {
						return
					}
					select {
					case commands <- command:
					case <-ctx.Done():
						return
					}
				}
			}
		}(source)
	}
	go func() {
		workers.Wait()
		close(commands)
	}()
	return commands
}

type documentResult struct {
	name string
	text string
	err  error
}

func readDocumentWithLoading(
	ctx context.Context,
	source string,
	output io.Writer,
	color bool,
	enabled bool,
	read documentReader,
) (name string, text string, err error) {
	if !enabled {
		name, text, err := read(ctx, source, nil)
		if ctxErr := ctx.Err(); ctxErr != nil {
			return "", "", ctxErr
		}
		return name, text, err
	}

	loader := terminal.NewLoader(output, color, true)
	if err := loader.Start("Reading content"); err != nil {
		return "", "", fmt.Errorf("render source loading: %w", err)
	}

	readCtx, cancelRead := context.WithCancel(ctx)
	defer cancelRead()
	progress := make(chan document.Stage, 4)
	result := make(chan documentResult, 1)
	go func() {
		name, text, err := read(readCtx, source, func(stage document.Stage) {
			select {
			case progress <- stage:
			case <-readCtx.Done():
			}
		})
		result <- documentResult{name: name, text: text, err: err}
	}()

	ticker := time.NewTicker(80 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case stage := <-progress:
			if err := loader.Update(sourceStageMessage(stage)); err != nil {
				cancelRead()
				_ = loader.Finish()
				return "", "", fmt.Errorf("render source loading: %w", err)
			}
		case <-ticker.C:
			if err := loader.Advance(); err != nil {
				cancelRead()
				_ = loader.Finish()
				return "", "", fmt.Errorf("render source loading: %w", err)
			}
		case loaded := <-result:
			for {
				select {
				case stage := <-progress:
					if err := loader.Update(sourceStageMessage(stage)); err != nil {
						return "", "", fmt.Errorf("render source loading: %w", err)
					}
				default:
					if err := loader.Finish(); err != nil {
						return "", "", fmt.Errorf("render source loading: %w", err)
					}
					if ctxErr := ctx.Err(); ctxErr != nil {
						return "", "", ctxErr
					}
					return loaded.name, loaded.text, loaded.err
				}
			}
		case <-ctx.Done():
			cancelRead()
			_ = loader.Finish()
			return "", "", ctx.Err()
		}
	}
}

func sourceStageMessage(stage document.Stage) string {
	switch stage {
	case document.StageReadingDocument:
		return "Reading file"
	case document.StageParsingDocument:
		return "Parsing document"
	case document.StageReadingWebPage:
		return "Reading webpage"
	case document.StageExtractingWebPage:
		return "Extracting webpage content"
	default:
		return "Reading content"
	}
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

func prepareAudio(
	ctx context.Context,
	chunks []string,
	tempDir string,
	synthesizer tts.Synthesizer,
	readDuration durationReader,
) *player.Preparation {
	return player.NewPreparation(ctx, chunks, func(ctx context.Context, target player.Target, text string) (player.SentenceTrack, error) {
		outputPath := filepath.Join(tempDir, fmt.Sprintf("%06d-%03d%s", target.Chapter+1, target.Sentence+1, synthesizer.Extension()))
		keep := false
		defer func() {
			if !keep {
				_ = os.Remove(outputPath)
			}
		}()
		if err := synthesizer.Synthesize(ctx, text, outputPath); err != nil {
			return player.SentenceTrack{}, fmt.Errorf("synthesize track %d of %d sentence %d: %w", target.Chapter+1, len(chunks), target.Sentence+1, err)
		}
		if err := ctx.Err(); err != nil {
			return player.SentenceTrack{}, err
		}
		duration, err := readDuration(outputPath)
		if err != nil {
			return player.SentenceTrack{}, fmt.Errorf("inspect track %d of %d sentence %d: %w", target.Chapter+1, len(chunks), target.Sentence+1, err)
		}
		if duration <= 0 {
			return player.SentenceTrack{}, fmt.Errorf("inspect track %d of %d: audio duration must be greater than zero", target.Chapter+1, len(chunks))
		}
		if err := ctx.Err(); err != nil {
			return player.SentenceTrack{}, err
		}
		keep = true
		return player.SentenceTrack{Path: outputPath, Duration: duration}, nil
	})
}

func isCharacterDevice(value any) bool {
	file, ok := value.(*os.File)
	if !ok {
		return false
	}
	info, err := file.Stat()
	return err == nil && info.Mode()&os.ModeCharDevice != 0
}
