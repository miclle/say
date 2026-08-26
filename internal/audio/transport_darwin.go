//go:build darwin && cgo

package audio

/*
#cgo CFLAGS: -x objective-c -fobjc-arc
#cgo LDFLAGS: -framework AVFAudio -framework Foundation

#import <AVFAudio/AVFAudio.h>
#import <Foundation/Foundation.h>
#include <stdlib.h>
#include <string.h>

typedef void* SayAudioPlayer;

static void say_copy_error(NSError *error, char *buffer, int length) {
	if (buffer == NULL || length <= 0) {
		return;
	}
	const char *message = error == nil ? "unknown AVAudioPlayer error" : [[error localizedDescription] UTF8String];
	strncpy(buffer, message, (size_t)length - 1);
	buffer[length - 1] = '\0';
}

static SayAudioPlayer say_audio_create(const char *path, char *error_buffer, int error_length) {
	@autoreleasepool {
		NSString *filePath = [NSString stringWithUTF8String:path];
		NSURL *url = [NSURL fileURLWithPath:filePath];
		NSError *error = nil;
		AVAudioPlayer *player = [[AVAudioPlayer alloc] initWithContentsOfURL:url error:&error];
		if (player == nil) {
			say_copy_error(error, error_buffer, error_length);
			return NULL;
		}
		if (![player prepareToPlay]) {
			say_copy_error(nil, error_buffer, error_length);
			return NULL;
		}
		return (__bridge_retained void *)player;
	}
}

static void say_audio_destroy(SayAudioPlayer handle) {
	if (handle != NULL) {
		CFRelease(handle);
	}
}

static double say_audio_duration(SayAudioPlayer handle) {
	return [(__bridge AVAudioPlayer *)handle duration];
}

static double say_audio_position(SayAudioPlayer handle) {
	return [(__bridge AVAudioPlayer *)handle currentTime];
}

static void say_audio_seek(SayAudioPlayer handle, double seconds) {
	[(__bridge AVAudioPlayer *)handle setCurrentTime:seconds];
}

static int say_audio_play(SayAudioPlayer handle) {
	return [(__bridge AVAudioPlayer *)handle play] ? 1 : 0;
}

static void say_audio_pause(SayAudioPlayer handle) {
	[(__bridge AVAudioPlayer *)handle pause];
}

static void say_audio_stop(SayAudioPlayer handle) {
	[(__bridge AVAudioPlayer *)handle stop];
}

static int say_audio_prepare(SayAudioPlayer handle) {
	return [(__bridge AVAudioPlayer *)handle prepareToPlay] ? 1 : 0;
}

static int say_audio_is_playing(SayAudioPlayer handle) {
	return [(__bridge AVAudioPlayer *)handle isPlaying] ? 1 : 0;
}
*/
import "C"

import (
	"fmt"
	"time"
	"unsafe"
)

const nativeErrorBufferSize = 1024
const cachedPlayers = 8

type cachedPlayer struct {
	path   string
	handle C.SayAudioPlayer
}

// Transport controls local audio files through macOS AVFoundation.
type Transport struct {
	handle C.SayAudioPlayer
	// Recent immutable sentence files, ordered from least to most recently used.
	cache []cachedPlayer
}

// New constructs an unloaded native audio transport.
func New() (*Transport, error) {
	return &Transport{}, nil
}

// Duration returns the playable duration of an audio file.
func (t *Transport) Duration(path string) (time.Duration, error) {
	return Duration(path)
}

// Duration returns the playable duration of an audio file without loading it for playback.
func Duration(path string) (time.Duration, error) {
	handle, err := createPlayer(path)
	if err != nil {
		return 0, err
	}
	defer C.say_audio_destroy(handle)
	return secondsToDuration(C.say_audio_duration(handle)), nil
}

// Load replaces the active file after validating the new file.
func (t *Transport) Load(path string) error {
	for index, entry := range t.cache {
		if entry.path != path {
			continue
		}
		if entry.handle != t.handle {
			// Prepare the destination while the old sentence is still audible.
			// Reserve pause for user-initiated pause/resume, not replacement.
			C.say_audio_seek(entry.handle, 0)
			if C.say_audio_prepare(entry.handle) == 0 {
				return fmt.Errorf("prepare audio %q for playback", path)
			}
		}
		if t.handle != nil {
			C.say_audio_stop(t.handle)
		}
		if entry.handle == t.handle {
			C.say_audio_seek(entry.handle, 0)
		}
		t.handle = entry.handle
		copy(t.cache[index:], t.cache[index+1:])
		t.cache[len(t.cache)-1] = entry
		return nil
	}
	handle, err := createPlayer(path)
	if err != nil {
		return err
	}
	if t.handle != nil {
		C.say_audio_stop(t.handle)
	}
	t.handle = handle
	t.cache = append(t.cache, cachedPlayer{path: path, handle: handle})
	if len(t.cache) > cachedPlayers {
		C.say_audio_destroy(t.cache[0].handle)
		t.cache = t.cache[1:]
	}
	return nil
}

// Play starts or resumes the active file.
func (t *Transport) Play() error {
	if t.handle == nil {
		return fmt.Errorf("no audio file is loaded")
	}
	if C.say_audio_play(t.handle) == 0 {
		return fmt.Errorf("AVAudioPlayer could not start playback")
	}
	return nil
}

// Pause pauses the active file without changing its position.
func (t *Transport) Pause() {
	if t.handle != nil {
		C.say_audio_pause(t.handle)
	}
}

// Seek moves the playhead within the active file.
func (t *Transport) Seek(position time.Duration) error {
	if t.handle == nil {
		return fmt.Errorf("no audio file is loaded")
	}
	if position < 0 {
		position = 0
	}
	duration := secondsToDuration(C.say_audio_duration(t.handle))
	if position > duration {
		position = duration
	}
	C.say_audio_seek(t.handle, C.double(durationToSeconds(position)))
	return nil
}

// Position returns the current playhead position.
func (t *Transport) Position() time.Duration {
	if t.handle == nil {
		return 0
	}
	return secondsToDuration(C.say_audio_position(t.handle))
}

// IsPlaying reports whether the active file is currently advancing.
func (t *Transport) IsPlaying() bool {
	return t.handle != nil && C.say_audio_is_playing(t.handle) != 0
}

// Close releases native playback resources.
func (t *Transport) Close() error {
	t.release()
	return nil
}

func (t *Transport) release() {
	t.Pause()
	for _, entry := range t.cache {
		C.say_audio_destroy(entry.handle)
	}
	t.cache = nil
	t.handle = nil
}

func createPlayer(path string) (C.SayAudioPlayer, error) {
	if path == "" {
		return nil, fmt.Errorf("audio path is empty")
	}
	cPath := C.CString(path)
	defer C.free(unsafe.Pointer(cPath))
	errorBuffer := (*C.char)(C.calloc(nativeErrorBufferSize, 1))
	defer C.free(unsafe.Pointer(errorBuffer))

	handle := C.say_audio_create(cPath, errorBuffer, nativeErrorBufferSize)
	if handle == nil {
		message := C.GoString(errorBuffer)
		if message == "" {
			message = "unknown AVAudioPlayer error"
		}
		return nil, fmt.Errorf("open audio %q: %s", path, message)
	}
	return handle, nil
}

func secondsToDuration(seconds C.double) time.Duration {
	if seconds <= 0 {
		return 0
	}
	return time.Duration(float64(seconds) * float64(time.Second))
}

func durationToSeconds(duration time.Duration) float64 {
	return float64(duration) / float64(time.Second)
}
