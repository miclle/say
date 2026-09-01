//go:build darwin && cgo

package desktop

/*
#cgo CFLAGS: -x objective-c -fobjc-arc
#cgo LDFLAGS: -framework AppKit -framework Foundation -framework MediaPlayer

#include <stdlib.h>
#include "native_darwin.h"
*/
import "C"

import (
	"context"
	"runtime"
	"runtime/cgo"
	"time"
	"unsafe"

	"github.com/miclle/say/internal/player"
)

// AppKit must be initialized on the process startup thread. Go guarantees init
// functions run on that thread; locking here keeps main on it until exit.
func init() {
	runtime.LockOSThread()
}

const (
	nativeToggle   = int(C.SayDesktopCommandToggle)
	nativeBackward = int(C.SayDesktopCommandBackward)
	nativeForward  = int(C.SayDesktopCommandForward)
	nativeResume   = int(C.SayDesktopCommandResume)
	nativePause    = int(C.SayDesktopCommandPause)
)

type nativeBackend struct {
	handle C.SayDesktopHandle
}

func commandFromNative(value int) (player.Command, bool) {
	switch value {
	case nativeToggle:
		return player.Toggle, true
	case nativeBackward:
		return player.Backward, true
	case nativeForward:
		return player.Forward, true
	case nativeResume:
		return player.ResumePlayback, true
	case nativePause:
		return player.PausePlayback, true
	default:
		return 0, false
	}
}

//export sayDesktopEmit
func sayDesktopEmit(token C.uintptr_t, value C.int) {
	command, ok := commandFromNative(int(value))
	if !ok {
		return
	}
	defer func() { _ = recover() }()
	controls, ok := cgo.Handle(token).Value().(*controls)
	if ok {
		controls.emit(command)
	}
}

func (backend *nativeBackend) Render(snapshot Snapshot) error {
	document := C.CString(snapshot.Document)
	text := C.CString(snapshot.Text)
	display := C.CString(displayText(snapshot.Text))
	defer C.free(unsafe.Pointer(document))
	defer C.free(unsafe.Pointer(text))
	defer C.free(unsafe.Pointer(display))
	C.say_desktop_render(
		backend.handle,
		document,
		text,
		display,
		boolInt(snapshot.Playing),
		boolInt(snapshot.Busy),
		C.int(snapshot.QueueIndex),
		C.int(snapshot.QueueCount),
		C.double(durationSeconds(snapshot.Position)),
		C.double(durationSeconds(snapshot.Duration)),
	)
	return nil
}

func (backend *nativeBackend) Clear() error {
	C.say_desktop_clear(backend.handle)
	return nil
}

func boolInt(value bool) C.int {
	if value {
		return 1
	}
	return 0
}

func durationSeconds(value time.Duration) float64 {
	return float64(value) / float64(time.Second)
}

func nativeNowPlayingCleared() bool {
	return C.say_desktop_now_playing_is_clear() != 0
}

func (backend *nativeBackend) statusItemsVisible() bool {
	return C.say_desktop_status_items_visible(backend.handle) != 0
}

func (backend *nativeBackend) remoteCommandsRegistered() bool {
	return C.say_desktop_remote_commands_registered(backend.handle) != 0
}

func (backend *nativeBackend) statusTitleEquals(text string) bool {
	value := C.CString(text)
	defer C.free(unsafe.Pointer(value))
	return C.say_desktop_status_title_equals(backend.handle, value) != 0
}

func nativeNowPlayingTitleEquals(text string) bool {
	value := C.CString(text)
	defer C.free(unsafe.Pointer(value))
	return C.say_desktop_now_playing_title_equals(value) != 0
}

// Run owns the AppKit event loop on the process main thread while work runs in
// a Go worker. The caller must invoke Run from main before other AppKit calls.
func Run(_ context.Context, work func(Controls) int) int {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	backend := &nativeBackend{}
	controls := newControls(backend)
	token := cgo.NewHandle(controls)
	backend.handle = C.say_desktop_create(C.uintptr_t(token))
	if backend.handle == nil {
		token.Delete()
		return work(nil)
	}

	result := make(chan int, 1)
	go func() {
		code := work(controls)
		_ = controls.Close()
		result <- code
		C.say_desktop_stop()
	}()

	C.say_desktop_run()
	code := <-result
	C.say_desktop_destroy(backend.handle)
	token.Delete()
	return code
}
