package cli

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
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

type synthesizerFactory func(voice string, rate int) (tts.Synthesizer, error)
type transportFactory func() (audioTransport, error)
type terminalDetector func(value any) bool
type rawInputFactory func(input io.Reader) (restore func() error, err error)

type audioTransport interface {
	player.Transport
	Duration(path string) (time.Duration, error)
	Close() error
}

type dependencies struct {
	input            io.Reader
	newSynthesizer   synthesizerFactory
	newTransport     transportFactory
	supportsTerminal terminalDetector
	beginRaw         rawInputFactory
}

// Run executes the say command and returns a process exit code.
func Run(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	return runWithDependencies(ctx, args, stdout, stderr, dependencies{
		input:          os.Stdin,
		newSynthesizer: tts.NewSystem,
		newTransport: func() (audioTransport, error) {
			return audio.New()
		},
		supportsTerminal: isCharacterDevice,
		beginRaw: func(input io.Reader) (func() error, error) {
			file, ok := input.(*os.File)
			if !ok {
				return nil, fmt.Errorf("interactive input is not a terminal file")
			}
			return terminal.BeginRawInput(file)
		},
	})
}

func runWithDependencies(ctx context.Context, args []string, stdout, stderr io.Writer, deps dependencies) int {
	if err := ctx.Err(); err != nil {
		fmt.Fprintf(stderr, "say: interrupted: %v\n", err)
		return 130
	}

	flags := flag.NewFlagSet("say", flag.ContinueOnError)
	flags.SetOutput(stderr)
	voice := flags.String("voice", "", "system voice name (default: System Settings)")
	rate := flags.Int("rate", 0, "speech rate in words per minute (default: system rate)")
	maxChars := flags.Int("max-chars", defaultMaxChars, "maximum Unicode characters per TTS call")
	noColor := flags.Bool("no-color", false, "disable ANSI terminal colors")
	flags.Usage = func() {
		fmt.Fprintln(stderr, "Usage: say [flags] <document>")
		fmt.Fprintln(stderr)
		fmt.Fprintln(stderr, "Read a UTF-8 text document, print each speech unit, and play it with system TTS.")
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
	synthesizer, err := deps.newSynthesizer(*voice, *rate)
	if err != nil {
		fmt.Fprintf(stderr, "say: initialize TTS: %v\n", err)
		return 1
	}

	interactive := deps.supportsTerminal(deps.input) && deps.supportsTerminal(stdout)
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

	tracks := make([]player.Track, 0, len(chunks))
	for i, chunk := range chunks {
		if err := ctx.Err(); err != nil {
			fmt.Fprintf(stderr, "say: playback interrupted: %v\n", err)
			return 130
		}
		outputPath := filepath.Join(tempDir, fmt.Sprintf("%06d.aiff", i+1))
		if err := synthesizer.Synthesize(ctx, chunk, outputPath); err != nil {
			if ctxErr := ctx.Err(); ctxErr != nil {
				err = ctxErr
			}
			fmt.Fprintf(stderr, "say: synthesize track %d of %d: %v\n", i+1, len(chunks), err)
			if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
				return 130
			}
			return 1
		}
		tracks = append(tracks, player.Track{Text: chunk, Path: outputPath})
	}

	transport, err := deps.newTransport()
	if err != nil {
		fmt.Fprintf(stderr, "say: initialize audio playback: %v\n", err)
		return 1
	}
	defer transport.Close()
	for i := range tracks {
		duration, err := transport.Duration(tracks[i].Path)
		if err != nil {
			fmt.Fprintf(stderr, "say: inspect track %d of %d: %v\n", i+1, len(tracks), err)
			return 1
		}
		if duration <= 0 {
			fmt.Fprintf(stderr, "say: inspect track %d of %d: audio duration must be greater than zero\n", i+1, len(tracks))
			return 1
		}
		tracks[i].Duration = duration
	}

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

	playbackErr := player.Play(ctx, tracks, transport, commands, view)
	cancelCommands()
	restoreErr := restore()
	if playbackErr != nil {
		if errors.Is(playbackErr, context.Canceled) || errors.Is(playbackErr, context.DeadlineExceeded) {
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

func isCharacterDevice(value any) bool {
	file, ok := value.(*os.File)
	if !ok {
		return false
	}
	info, err := file.Stat()
	return err == nil && info.Mode()&os.ModeCharDevice != 0
}
