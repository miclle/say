//go:build darwin

package terminal

import (
	"fmt"
	"os"
	"sync"
	"syscall"
	"unsafe"
)

// BeginRawInput enables immediate key reads while retaining Ctrl-C signal handling.
func BeginRawInput(file *os.File) (func() error, error) {
	if file == nil {
		return nil, fmt.Errorf("terminal input is required")
	}
	fd := file.Fd()
	var original syscall.Termios
	if err := ioctlTermios(fd, syscall.TIOCGETA, &original); err != nil {
		return nil, fmt.Errorf("read terminal mode: %w", err)
	}
	raw := original
	raw.Lflag &^= syscall.ICANON | syscall.ECHO
	raw.Cc[syscall.VMIN] = 1
	raw.Cc[syscall.VTIME] = 0
	if err := ioctlTermios(fd, syscall.TIOCSETA, &raw); err != nil {
		return nil, fmt.Errorf("enable raw terminal input: %w", err)
	}

	var once sync.Once
	var restoreErr error
	restore := func() error {
		once.Do(func() {
			if err := ioctlTermios(fd, syscall.TIOCSETA, &original); err != nil {
				restoreErr = fmt.Errorf("restore terminal mode: %w", err)
			}
		})
		return restoreErr
	}
	return restore, nil
}

func ioctlTermios(fd uintptr, request uintptr, termios *syscall.Termios) error {
	_, _, errno := syscall.Syscall(syscall.SYS_IOCTL, fd, request, uintptr(unsafe.Pointer(termios)))
	if errno != 0 {
		return errno
	}
	return nil
}
