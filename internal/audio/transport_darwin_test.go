//go:build darwin && cgo

package audio

import (
	"encoding/binary"
	"fmt"
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

func TestTransportReusesRecentAudioAndRestartsAtZero(t *testing.T) {
	first := filepath.Join(t.TempDir(), "first.wav")
	second := filepath.Join(t.TempDir(), "second.wav")
	writeSilentWAV(t, first, time.Second)
	writeSilentWAV(t, second, time.Second)
	transport, err := New()
	if err != nil {
		t.Fatal(err)
	}
	defer transport.Close()
	if err := transport.Load(first); err != nil {
		t.Fatal(err)
	}
	if err := transport.Seek(500 * time.Millisecond); err != nil {
		t.Fatal(err)
	}
	if err := transport.Load(second); err != nil {
		t.Fatal(err)
	}
	// Synthesized audio is immutable for a playback session. Moving the file
	// proves a return seek uses the prepared player, not another disk load.
	if err := os.Rename(first, first+".moved"); err != nil {
		t.Fatal(err)
	}
	if err := transport.Load(first); err != nil {
		t.Fatalf("reload recent audio: %v", err)
	}
	if transport.Position() != 0 || transport.IsPlaying() {
		t.Fatal("reloaded audio must be paused at its beginning")
	}
}

func TestTransportCacheEvictsOldAudioAndClosesAllPlayers(t *testing.T) {
	dir := t.TempDir()
	transport, err := New()
	if err != nil {
		t.Fatal(err)
	}
	defer transport.Close()
	paths := make([]string, cachedPlayers+1)
	for i := range paths {
		paths[i] = filepath.Join(dir, fmt.Sprintf("%d.wav", i))
		writeSilentWAV(t, paths[i], time.Second)
		if err := transport.Load(paths[i]); err != nil {
			t.Fatal(err)
		}
	}
	if err := transport.Seek(200 * time.Millisecond); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(paths[0], paths[0]+".moved"); err != nil {
		t.Fatal(err)
	}
	if err := transport.Load(paths[0]); err == nil {
		t.Fatal("oldest audio was retained past the cache limit")
	}
	if position := transport.Position(); position < 180*time.Millisecond || position > 220*time.Millisecond {
		t.Fatalf("failed load changed current position to %s", position)
	}
	if err := transport.Close(); err != nil {
		t.Fatal(err)
	}
	if transport.handle != nil || len(transport.cache) != 0 {
		t.Fatal("Close retained native players")
	}
	if err := transport.Play(); err == nil {
		t.Fatal("closed transport can still play")
	}
}

func TestTransportChapterSwitchStopsOldAudioAndRetainsPauseResume(t *testing.T) {
	first := filepath.Join(t.TempDir(), "first.wav")
	second := filepath.Join(t.TempDir(), "second.wav")
	writeSilentWAV(t, first, 5*time.Second)
	writeSilentWAV(t, second, 5*time.Second)
	transport, err := New()
	if err != nil {
		t.Fatal(err)
	}
	defer transport.Close()
	if err := transport.Load(first); err != nil {
		t.Fatal(err)
	}
	if err := transport.Play(); err != nil {
		t.Fatal(err)
	}
	// This borrowed handle stays owned by the two-entry cache throughout the test.
	old := &Transport{handle: transport.handle}
	if err := transport.Load(second); err != nil {
		t.Fatal(err)
	}
	if old.IsPlaying() || transport.IsPlaying() || transport.Position() != 0 {
		t.Fatal("chapter load must stop the old audio and leave the new file paused at zero")
	}
	if err := transport.Play(); err != nil {
		t.Fatal(err)
	}
	if old.IsPlaying() || !transport.IsPlaying() {
		t.Fatal("chapter switch left overlapping or stopped audio")
	}
	transport.Pause()
	position := transport.Position()
	if err := transport.Play(); err != nil {
		t.Fatal(err)
	}
	if !transport.IsPlaying() || transport.Position() < position {
		t.Fatal("user pause/resume lost its position")
	}
	if err := transport.Load(first); err != nil {
		t.Fatal(err)
	}
	if transport.Position() != 0 || transport.IsPlaying() {
		t.Fatal("returning to a cached chapter did not reset it to its beginning")
	}
}

func BenchmarkTransportSwitch(b *testing.B) {
	paths := []string{filepath.Join(b.TempDir(), "one.wav"), filepath.Join(b.TempDir(), "two.wav")}
	for _, path := range paths {
		writeSilentWAV(b, path, 10*time.Second)
	}
	transport, err := New()
	if err != nil {
		b.Fatal(err)
	}
	defer transport.Close()
	b.ResetTimer()
	var loadTime, seekTime, playTime time.Duration
	for i := 0; i < b.N; i++ {
		start := time.Now()
		if err := transport.Load(paths[i%2]); err != nil {
			b.Fatal(err)
		}
		loadTime += time.Since(start)
		start = time.Now()
		if err := transport.Seek(time.Second); err != nil {
			b.Fatal(err)
		}
		seekTime += time.Since(start)
		start = time.Now()
		if err := transport.Play(); err != nil {
			b.Fatal(err)
		}
		playTime += time.Since(start)
	}
	b.ReportMetric(float64(loadTime.Nanoseconds())/float64(b.N), "load-ns/op")
	b.ReportMetric(float64(seekTime.Nanoseconds())/float64(b.N), "seek-ns/op")
	b.ReportMetric(float64(playTime.Nanoseconds())/float64(b.N), "play-ns/op")
}

func BenchmarkChapterSwitch(b *testing.B) {
	paths := []string{filepath.Join(b.TempDir(), "one.wav"), filepath.Join(b.TempDir(), "two.wav")}
	for _, path := range paths {
		writeSilentWAV(b, path, 10*time.Second)
	}
	transport, err := New()
	if err != nil {
		b.Fatal(err)
	}
	defer transport.Close()
	for _, path := range paths {
		if err := transport.Load(path); err != nil {
			b.Fatal(err)
		}
	}
	b.ResetTimer()
	var loadTime, playTime time.Duration
	for i := 0; i < b.N; i++ {
		start := time.Now()
		if err := transport.Load(paths[i%2]); err != nil {
			b.Fatal(err)
		}
		loadTime += time.Since(start)
		start = time.Now()
		if err := transport.Play(); err != nil {
			b.Fatal(err)
		}
		playTime += time.Since(start)
	}
	b.ReportMetric(float64(loadTime.Nanoseconds())/float64(b.N), "load-ns/op")
	b.ReportMetric(float64(playTime.Nanoseconds())/float64(b.N), "play-ns/op")
}

func writeSilentWAV(t testing.TB, path string, duration time.Duration) {
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
