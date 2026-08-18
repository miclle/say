package tts

import (
	"context"
	"encoding/binary"
	"errors"
	"math"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"
)

func TestNewEdgeUsesDefaultsAndMP3Output(t *testing.T) {
	synthesizer, err := NewEdge("", 0)
	if err != nil {
		t.Fatalf("NewEdge() error = %v", err)
	}
	if got, want := synthesizer.Name(), "Microsoft Edge TTS (Experimental, zh-CN-XiaoxiaoNeural)"; got != want {
		t.Fatalf("Name() = %q, want %q", got, want)
	}
	if got, want := synthesizer.Extension(), ".mp3"; got != want {
		t.Fatalf("Extension() = %q, want %q", got, want)
	}
}

func TestNewEdgeRejectsUnsupportedSpeed(t *testing.T) {
	for _, speed := range []float64{0.49, 2.01, math.NaN()} {
		if _, err := NewEdge("", speed); err == nil {
			t.Fatalf("NewEdge(_, %v) error = nil, want range error", speed)
		}
	}
}

func TestNewEdgeRejectsMalformedVoice(t *testing.T) {
	if _, err := NewEdge("not-a-voice", 1); err == nil {
		t.Fatal("NewEdge() error = nil, want invalid-voice error")
	}
}

func TestEdgeSupportsVoiceShortNamesWithScriptSubtags(t *testing.T) {
	const voice = "iu-Latn-CA-SiqiniqNeural"
	if _, err := NewEdge(voice, 1); err != nil {
		t.Fatalf("NewEdge() error = %v", err)
	}

	message, err := edgeSSMLMessage(edgeRequest{Text: "hello", Voice: voice, Speed: 1}, "request-id", time.Date(2026, 8, 18, 0, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("edgeSSMLMessage() error = %v", err)
	}
	if want := "name='Microsoft Server Speech Text to Speech Voice (iu-Latn-CA, SiqiniqNeural)'"; !strings.Contains(message, want) {
		t.Fatalf("SSML message = %q, want containing %q", message, want)
	}
}

func TestEdgeSynthesizerWritesCompleteAudio(t *testing.T) {
	wantAudio := []byte("complete mp3")
	var got edgeRequest
	synthesizer := newEdgeSynthesizer("en-US-AriaNeural", 1.25, time.Second, func(_ context.Context, request edgeRequest) ([]byte, error) {
		got = request
		return wantAudio, nil
	})
	outputPath := filepath.Join(t.TempDir(), "speech.mp3")

	if err := synthesizer.Synthesize(context.Background(), "hello", outputPath); err != nil {
		t.Fatalf("Synthesize() error = %v", err)
	}
	if got.Text != "hello" || got.Voice != "en-US-AriaNeural" || got.Speed != 1.25 {
		t.Fatalf("edge request = %#v", got)
	}
	audio, err := os.ReadFile(outputPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(audio) != string(wantAudio) {
		t.Fatalf("audio = %q, want %q", audio, wantAudio)
	}
}

func TestEdgeSynthesizerRejectsEmptyInputsAndAudio(t *testing.T) {
	synthesizer := newEdgeSynthesizer("voice", 1, time.Second, func(context.Context, edgeRequest) ([]byte, error) {
		return nil, nil
	})
	for _, tt := range []struct {
		name       string
		text       string
		outputPath string
		want       string
	}{
		{name: "text", text: " \n", outputPath: "speech.mp3", want: "speech text is empty"},
		{name: "output", text: "hello", outputPath: "", want: "output path is empty"},
		{name: "audio", text: "hello", outputPath: filepath.Join(t.TempDir(), "speech.mp3"), want: "returned no audio"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			err := synthesizer.Synthesize(context.Background(), tt.text, tt.outputPath)
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("Synthesize() error = %v, want containing %q", err, tt.want)
			}
		})
	}
}

func TestEdgeSynthesizerReportsOutputWriteFailure(t *testing.T) {
	synthesizer := newEdgeSynthesizer("voice", 1, time.Second, func(context.Context, edgeRequest) ([]byte, error) {
		return []byte("mp3"), nil
	})
	err := synthesizer.Synthesize(context.Background(), "hello", filepath.Join(t.TempDir(), "missing", "speech.mp3"))
	if err == nil || !strings.Contains(err.Error(), "write Edge TTS audio") {
		t.Fatalf("Synthesize() error = %v, want output-write error", err)
	}
}

func TestEdgeSynthesizerHonorsRequestTimeout(t *testing.T) {
	synthesizer := newEdgeSynthesizer("voice", 1, 20*time.Millisecond, func(ctx context.Context, _ edgeRequest) ([]byte, error) {
		<-ctx.Done()
		return nil, ctx.Err()
	})
	started := time.Now()
	err := synthesizer.Synthesize(context.Background(), "hello", filepath.Join(t.TempDir(), "speech.mp3"))
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Synthesize() error = %v, want context.DeadlineExceeded", err)
	}
	if elapsed := time.Since(started); elapsed > time.Second {
		t.Fatalf("Synthesize() took %v after timeout", elapsed)
	}
}

func TestEdgeGECTokenUsesFiveMinuteWindows(t *testing.T) {
	now := time.Date(2026, time.August, 18, 0, 0, 42, 0, time.UTC)
	if got, want := edgeGECToken(now), "239B9D28F00427DEA5116F1C61BD26DA1472A0B3731D97AAD23867CFCBDDDE06"; got != want {
		t.Fatalf("edgeGECToken() = %q, want %q", got, want)
	}
	if got := edgeGECToken(now.Add(4 * time.Minute)); got != edgeGECToken(now) {
		t.Fatalf("token changed inside five-minute window: %q", got)
	}
}

func TestEdgeSSMLEscapesTextAndMapsSpeed(t *testing.T) {
	message, err := edgeSSMLMessage(edgeRequest{Text: `<hello & "world">`, Voice: "en-US-AriaNeural", Speed: 1.25}, "request-id", time.Date(2026, 8, 18, 0, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("edgeSSMLMessage() error = %v", err)
	}
	for _, want := range []string{
		`xml:lang='en-US'`,
		`name='Microsoft Server Speech Text to Speech Voice (en-US, AriaNeural)'`,
		`rate='+25%'`,
		`&lt;hello &amp; &quot;world&quot;&gt;`,
	} {
		if !strings.Contains(message, want) {
			t.Fatalf("SSML message = %q, want containing %q", message, want)
		}
	}
}

func TestEdgeSSMLRoundsRatesAndSupportsRegionalVoiceNames(t *testing.T) {
	message, err := edgeSSMLMessage(edgeRequest{
		Text:  "你好",
		Voice: "zh-CN-liaoning-XiaobeiNeural",
		Speed: 1.2,
	}, "request-id", time.Date(2026, 8, 18, 0, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("edgeSSMLMessage() error = %v", err)
	}
	for _, want := range []string{
		`xml:lang='en-US'`,
		`Microsoft Server Speech Text to Speech Voice (zh-CN-liaoning, XiaobeiNeural)`,
		`rate='+20%'`,
	} {
		if !strings.Contains(message, want) {
			t.Fatalf("SSML message = %q, want containing %q", message, want)
		}
	}
}

func TestEdgeAudioPayloadParsesBinaryFrame(t *testing.T) {
	header := []byte("X-RequestId:id\r\nPath:audio\r\nContent-Type:audio/mpeg\r\n")
	frame := make([]byte, 2+len(header)+4)
	binary.BigEndian.PutUint16(frame[:2], uint16(len(header)))
	copy(frame[2:], header)
	copy(frame[2+len(header):], []byte{0xff, 0xfb, 0x90, 0x64})

	payload, audio, err := edgeAudioPayload(frame)
	if err != nil {
		t.Fatalf("edgeAudioPayload() error = %v", err)
	}
	if !audio || len(payload) != 4 || payload[0] != 0xff {
		t.Fatalf("edgeAudioPayload() = %v, %v", payload, audio)
	}
	if _, _, err := edgeAudioPayload([]byte{0, 100, 1}); err == nil {
		t.Fatal("edgeAudioPayload() error = nil for truncated header")
	}
}

func TestEdgeClientExchangesProtocolAndCollectsAudio(t *testing.T) {
	var mu sync.Mutex
	var messages []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("TrustedClientToken") != edgeTrustedClientToken || r.URL.Query().Get("Sec-MS-GEC") == "" {
			http.Error(w, "missing signed query", http.StatusBadRequest)
			return
		}
		connection, err := websocket.Accept(w, r, &websocket.AcceptOptions{InsecureSkipVerify: true})
		if err != nil {
			return
		}
		defer connection.CloseNow()
		for range 2 {
			messageType, data, err := connection.Read(r.Context())
			if err != nil || messageType != websocket.MessageText {
				return
			}
			mu.Lock()
			messages = append(messages, string(data))
			mu.Unlock()
		}
		header := []byte("X-RequestId:test-request\r\nPath:audio\r\nContent-Type:audio/mpeg\r\n")
		frame := make([]byte, 2+len(header)+3)
		binary.BigEndian.PutUint16(frame[:2], uint16(len(header)))
		copy(frame[2:], header)
		copy(frame[2+len(header):], []byte("mp3"))
		_ = connection.Write(r.Context(), websocket.MessageBinary, frame)
		_ = connection.Write(r.Context(), websocket.MessageText, []byte("X-RequestId:test-request\r\nPath:turn.end\r\n\r\n"))
	}))
	defer server.Close()

	client := edgeClient{
		endpoint: strings.Replace(server.URL, "http://", "ws://", 1),
		now:      func() time.Time { return time.Date(2026, 8, 18, 0, 0, 0, 0, time.UTC) },
		newID:    func() (string, error) { return "test-request", nil },
	}
	audio, err := client.synthesize(context.Background(), edgeRequest{Text: "hello", Voice: "en-US-AriaNeural", Speed: 1})
	if err != nil {
		t.Fatalf("synthesize() error = %v", err)
	}
	if string(audio) != "mp3" {
		t.Fatalf("audio = %q, want mp3", audio)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(messages) != 2 || !strings.Contains(messages[0], "speech.config") || !strings.Contains(messages[1], "Path:ssml") {
		t.Fatalf("messages = %#v", messages)
	}
}

func TestEdgeClientStopsBlockedReadWhenCanceled(t *testing.T) {
	ready := make(chan struct{})
	release := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		connection, err := websocket.Accept(w, r, &websocket.AcceptOptions{InsecureSkipVerify: true})
		if err != nil {
			return
		}
		defer connection.CloseNow()
		for range 2 {
			if _, _, err := connection.Read(r.Context()); err != nil {
				return
			}
		}
		close(ready)
		<-release
	}))
	defer server.Close()

	client := edgeClient{
		endpoint: strings.Replace(server.URL, "http://", "ws://", 1),
		now:      time.Now,
		newID:    func() (string, error) { return "test-request", nil },
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		_, err := client.synthesize(ctx, edgeRequest{Text: "hello", Voice: "en-US-AriaNeural", Speed: 1})
		done <- err
	}()
	select {
	case <-ready:
	case <-time.After(time.Second):
		close(release)
		t.Fatal("Edge client did not begin reading the response")
	}
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			close(release)
			t.Fatalf("synthesize() error = %v, want context.Canceled", err)
		}
	case <-time.After(time.Second):
		close(release)
		t.Fatal("Edge client did not stop after cancellation")
	}
	close(release)
}
