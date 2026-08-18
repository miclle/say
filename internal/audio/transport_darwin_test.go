//go:build darwin && cgo

package audio

import (
	"encoding/binary"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestTransportLoadsPlaysPausesAndSeeks(t *testing.T) {
	path := filepath.Join(t.TempDir(), "silence.wav")
	writeSilentWAV(t, path, 500*time.Millisecond)

	transport, err := New()
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	defer transport.Close()

	duration, err := Duration(path)
	if err != nil {
		t.Fatalf("Duration() error = %v", err)
	}
	if duration < 450*time.Millisecond || duration > 550*time.Millisecond {
		t.Fatalf("Duration() = %s, want approximately 500ms", duration)
	}
	if err := transport.Load(path); err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if err := transport.Seek(200 * time.Millisecond); err != nil {
		t.Fatalf("Seek() error = %v", err)
	}
	if position := transport.Position(); position < 180*time.Millisecond || position > 220*time.Millisecond {
		t.Fatalf("Position() = %s, want approximately 200ms", position)
	}
	if err := transport.Play(); err != nil {
		t.Fatalf("Play() error = %v", err)
	}
	time.Sleep(20 * time.Millisecond)
	if !transport.IsPlaying() {
		t.Fatal("IsPlaying() = false immediately after Play()")
	}
	transport.Pause()
	if transport.IsPlaying() {
		t.Fatal("IsPlaying() = true after Pause()")
	}
	if err := transport.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	if err := transport.Close(); err != nil {
		t.Fatalf("second Close() error = %v", err)
	}
}

func TestTransportReportsInvalidAudio(t *testing.T) {
	transport, err := New()
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	defer transport.Close()

	_, err = transport.Duration(filepath.Join(t.TempDir(), "missing.aiff"))
	if err == nil {
		t.Fatal("Duration() error = nil, want invalid-audio error")
	}
	if err := transport.Load(filepath.Join(t.TempDir(), "missing.aiff")); err == nil {
		t.Fatal("Load() error = nil, want invalid-audio error")
	}
}

func writeSilentWAV(t *testing.T, path string, duration time.Duration) {
	t.Helper()
	const sampleRate = 8000
	const channels = 1
	const bitsPerSample = 16
	sampleCount := int64(duration) * sampleRate / int64(time.Second)
	dataSize := uint32(sampleCount * channels * bitsPerSample / 8)

	header := make([]byte, 44+dataSize)
	copy(header[0:4], "RIFF")
	binary.LittleEndian.PutUint32(header[4:8], 36+dataSize)
	copy(header[8:12], "WAVE")
	copy(header[12:16], "fmt ")
	binary.LittleEndian.PutUint32(header[16:20], 16)
	binary.LittleEndian.PutUint16(header[20:22], 1)
	binary.LittleEndian.PutUint16(header[22:24], channels)
	binary.LittleEndian.PutUint32(header[24:28], sampleRate)
	binary.LittleEndian.PutUint32(header[28:32], sampleRate*channels*bitsPerSample/8)
	binary.LittleEndian.PutUint16(header[32:34], channels*bitsPerSample/8)
	binary.LittleEndian.PutUint16(header[34:36], bitsPerSample)
	copy(header[36:40], "data")
	binary.LittleEndian.PutUint32(header[40:44], dataSize)

	if err := os.WriteFile(path, header, 0o600); err != nil {
		t.Fatal(err)
	}
}
