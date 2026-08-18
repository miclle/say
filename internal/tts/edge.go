package tts

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"math"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/coder/websocket"
)

const (
	edgeTrustedClientToken = "6A5AA1D4EAFF4E9FB37E23D68491D6F4"
	edgeEndpoint           = "wss://speech.platform.bing.com/consumer/speech/synthesize/readaloud/edge/v1"
	edgeChromiumVersion    = "143.0.3650.75"
	edgeDefaultVoice       = "zh-CN-XiaoxiaoNeural"
	edgeDefaultTimeout     = 45 * time.Second
	edgeWindowsEpoch       = int64(11644473600)
	edgeTicksPerSecond     = int64(10000000)
)

var edgeVoicePattern = regexp.MustCompile(`^([a-z]{2,3}(?:-[A-Za-z0-9]{2,8})*)-([A-Za-z][A-Za-z0-9:]*Neural)$`)

type edgeRequest struct {
	Text  string
	Voice string
	Speed float64
}

type edgeService func(context.Context, edgeRequest) ([]byte, error)

type edgeSynthesizer struct {
	voice      string
	speed      float64
	timeout    time.Duration
	synthesize edgeService
}

type edgeClient struct {
	endpoint string
	now      func() time.Time
	newID    func() (string, error)
}

// NewEdge constructs an experimental Microsoft Edge Read Aloud synthesizer.
func NewEdge(voice string, speed float64) (Synthesizer, error) {
	if voice == "" {
		voice = edgeDefaultVoice
	}
	if speed == 0 {
		speed = 1
	}
	if math.IsNaN(speed) || math.IsInf(speed, 0) || speed < 0.5 || speed > 2 {
		return nil, fmt.Errorf("speed must be between 0.5 and 2.0")
	}
	if !edgeVoicePattern.MatchString(voice) {
		return nil, fmt.Errorf("invalid Edge TTS voice %q; expected a name such as zh-CN-XiaoxiaoNeural", voice)
	}
	client := edgeClient{
		endpoint: edgeEndpoint,
		now:      time.Now,
		newID:    edgeRandomID,
	}
	return newEdgeSynthesizer(voice, speed, edgeDefaultTimeout, client.synthesize), nil
}

func newEdgeSynthesizer(voice string, speed float64, timeout time.Duration, synthesize edgeService) *edgeSynthesizer {
	return &edgeSynthesizer{
		voice:      voice,
		speed:      speed,
		timeout:    timeout,
		synthesize: synthesize,
	}
}

func (s *edgeSynthesizer) Name() string {
	return fmt.Sprintf("Microsoft Edge TTS (Experimental, %s)", s.voice)
}

func (s *edgeSynthesizer) Extension() string {
	return ".mp3"
}

func (s *edgeSynthesizer) Synthesize(ctx context.Context, text, outputPath string) error {
	if strings.TrimSpace(text) == "" {
		return fmt.Errorf("speech text is empty")
	}
	if strings.TrimSpace(outputPath) == "" {
		return fmt.Errorf("output path is empty")
	}
	requestCtx, cancel := context.WithTimeout(ctx, s.timeout)
	defer cancel()
	audio, err := s.synthesize(requestCtx, edgeRequest{Text: text, Voice: s.voice, Speed: s.speed})
	if err != nil {
		return fmt.Errorf("Edge TTS synthesis failed: %w", err)
	}
	if len(audio) == 0 {
		return fmt.Errorf("Edge TTS returned no audio")
	}
	if err := writeAudioFile(outputPath, audio); err != nil {
		return fmt.Errorf("write Edge TTS audio: %w", err)
	}
	return nil
}

func writeAudioFile(outputPath string, audio []byte) error {
	directory := filepath.Dir(outputPath)
	temporary, err := os.CreateTemp(directory, ".say-edge-*.part")
	if err != nil {
		return err
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)
	if err := temporary.Chmod(0o600); err != nil {
		temporary.Close()
		return err
	}
	if _, err := temporary.Write(audio); err != nil {
		temporary.Close()
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	return os.Rename(temporaryPath, outputPath)
}

func (c edgeClient) synthesize(ctx context.Context, request edgeRequest) ([]byte, error) {
	connectionID, err := c.newID()
	if err != nil {
		return nil, fmt.Errorf("generate connection ID: %w", err)
	}
	requestID, err := c.newID()
	if err != nil {
		return nil, fmt.Errorf("generate request ID: %w", err)
	}
	muid, err := c.newID()
	if err != nil {
		return nil, fmt.Errorf("generate client ID: %w", err)
	}
	now := c.now().UTC()
	endpoint, err := edgeSignedURL(c.endpoint, connectionID, now)
	if err != nil {
		return nil, err
	}
	headers := http.Header{
		"Pragma":          {"no-cache"},
		"Cache-Control":   {"no-cache"},
		"Origin":          {"chrome-extension://jdiccldimpdaibmpdkjnbmckianbfold"},
		"Accept-Language": {"en-US,en;q=0.9"},
		"User-Agent":      {edgeUserAgent()},
		"Cookie":          {"MUID=" + strings.ToUpper(muid)},
	}
	connection, response, err := websocket.Dial(ctx, endpoint, &websocket.DialOptions{HTTPHeader: headers})
	if err != nil {
		if response != nil {
			return nil, fmt.Errorf("connect to Edge TTS: HTTP %s: %w", response.Status, err)
		}
		return nil, fmt.Errorf("connect to Edge TTS: %w", err)
	}
	defer connection.CloseNow()

	if err := connection.Write(ctx, websocket.MessageText, []byte(edgeSpeechConfigMessage(now))); err != nil {
		return nil, fmt.Errorf("send Edge TTS configuration: %w", err)
	}
	ssml, err := edgeSSMLMessage(request, requestID, now)
	if err != nil {
		return nil, err
	}
	if err := connection.Write(ctx, websocket.MessageText, []byte(ssml)); err != nil {
		return nil, fmt.Errorf("send Edge TTS SSML: %w", err)
	}

	var audio []byte
	for {
		messageType, data, err := connection.Read(ctx)
		if err != nil {
			return nil, fmt.Errorf("read Edge TTS response: %w", err)
		}
		switch messageType {
		case websocket.MessageBinary:
			payload, isAudio, err := edgeAudioPayload(data)
			if err != nil {
				return nil, err
			}
			if isAudio {
				audio = append(audio, payload...)
			}
		case websocket.MessageText:
			if edgeMessagePath(data) == "turn.end" {
				if len(audio) == 0 {
					return nil, fmt.Errorf("Edge TTS completed without audio")
				}
				return audio, nil
			}
		}
	}
}

func edgeSignedURL(endpoint, connectionID string, now time.Time) (string, error) {
	parsed, err := url.Parse(endpoint)
	if err != nil {
		return "", fmt.Errorf("parse Edge TTS endpoint: %w", err)
	}
	query := parsed.Query()
	query.Set("TrustedClientToken", edgeTrustedClientToken)
	query.Set("ConnectionId", connectionID)
	query.Set("Sec-MS-GEC", edgeGECToken(now))
	query.Set("Sec-MS-GEC-Version", "1-"+edgeChromiumVersion)
	parsed.RawQuery = query.Encode()
	return parsed.String(), nil
}

func edgeGECToken(now time.Time) string {
	seconds := now.Unix() + edgeWindowsEpoch
	seconds -= seconds % 300
	ticks := seconds * edgeTicksPerSecond
	digest := sha256.Sum256([]byte(fmt.Sprintf("%d%s", ticks, edgeTrustedClientToken)))
	return strings.ToUpper(hex.EncodeToString(digest[:]))
}

func edgeSpeechConfigMessage(now time.Time) string {
	return fmt.Sprintf("X-Timestamp:%s\r\nContent-Type:application/json; charset=utf-8\r\nPath:speech.config\r\n\r\n%s",
		edgeTimestamp(now),
		`{"context":{"synthesis":{"audio":{"metadataoptions":{"sentenceBoundaryEnabled":"false","wordBoundaryEnabled":"false"},"outputFormat":"audio-24khz-48kbitrate-mono-mp3"}}}}`,
	)
}

func edgeSSMLMessage(request edgeRequest, requestID string, now time.Time) (string, error) {
	matches := edgeVoicePattern.FindStringSubmatch(request.Voice)
	if len(matches) != 3 {
		return "", fmt.Errorf("invalid Edge TTS voice %q; expected a name such as zh-CN-XiaoxiaoNeural", request.Voice)
	}
	rate := int(math.Round((request.Speed - 1) * 100))
	rateText := fmt.Sprintf("%+d%%", rate)
	ssml := fmt.Sprintf(
		"<speak version='1.0' xmlns='http://www.w3.org/2001/10/synthesis' xml:lang='%s'><voice name='Microsoft Server Speech Text to Speech Voice (%s, %s)'><prosody pitch='+0Hz' rate='%s' volume='+0%%'>%s</prosody></voice></speak>",
		"en-US", matches[1], matches[2], rateText, edgeEscapeXML(request.Text),
	)
	return fmt.Sprintf("X-RequestId:%s\r\nContent-Type:application/ssml+xml\r\nX-Timestamp:%sZ\r\nPath:ssml\r\n\r\n%s", requestID, edgeTimestamp(now), ssml), nil
}

func edgeEscapeXML(value string) string {
	return strings.NewReplacer(
		"&", "&amp;",
		"<", "&lt;",
		">", "&gt;",
		`"`, "&quot;",
		"'", "&apos;",
	).Replace(value)
}

func edgeAudioPayload(frame []byte) ([]byte, bool, error) {
	if len(frame) < 2 {
		return nil, false, fmt.Errorf("invalid Edge TTS audio frame: missing header length")
	}
	headerLength := int(binary.BigEndian.Uint16(frame[:2]))
	if headerLength > len(frame)-2 {
		return nil, false, fmt.Errorf("invalid Edge TTS audio frame: header length %d exceeds frame size", headerLength)
	}
	header := frame[2 : 2+headerLength]
	if edgeMessagePath(header) != "audio" {
		return nil, false, nil
	}
	return frame[2+headerLength:], true, nil
}

func edgeMessagePath(message []byte) string {
	for _, line := range strings.Split(string(message), "\r\n") {
		name, value, ok := strings.Cut(line, ":")
		if ok && strings.EqualFold(strings.TrimSpace(name), "Path") {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func edgeTimestamp(now time.Time) string {
	return now.UTC().Format("Mon, 02 Jan 2006 15:04:05") + " GMT+0000 (Coordinated Universal Time)"
}

func edgeUserAgent() string {
	return "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/" + edgeChromiumVersion + " Safari/537.36 Edg/" + edgeChromiumVersion
}

func edgeRandomID() (string, error) {
	value := make([]byte, 16)
	if _, err := rand.Read(value); err != nil {
		return "", err
	}
	return hex.EncodeToString(value), nil
}
