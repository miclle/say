package textchunk

import (
	"fmt"
	"strings"
	"unicode"
)

// Split breaks text into natural speech units no longer than maxRunes.
func Split(text string, maxRunes int) ([]string, error) {
	if maxRunes <= 0 {
		return nil, fmt.Errorf("max runes must be greater than zero")
	}
	if strings.TrimSpace(text) == "" {
		return nil, fmt.Errorf("text is empty")
	}

	var chunks []string
	for _, paragraph := range paragraphs(text) {
		chunks = append(chunks, splitParagraph(paragraph, maxRunes)...)
	}
	return chunks, nil
}

func paragraphs(text string) []string {
	text = strings.ReplaceAll(text, "\r\n", "\n")
	text = strings.ReplaceAll(text, "\r", "\n")
	lines := strings.Split(text, "\n")
	var result []string
	var current []string

	flush := func() {
		if len(current) > 0 {
			result = append(result, strings.Join(current, "\n"))
		}
		current = current[:0]
	}

	for _, line := range lines {
		if strings.TrimSpace(line) == "" {
			flush()
			continue
		}
		current = append(current, line)
	}
	flush()
	return result
}

func splitParagraph(paragraph string, maxRunes int) []string {
	normalized := normalize(paragraph)
	if len([]rune(normalized)) <= maxRunes {
		return []string{normalized}
	}

	var pieces []string
	for _, sentence := range Sentences(paragraph) {
		pieces = append(pieces, limit(sentence, maxRunes)...)
	}
	return pack(pieces, maxRunes)
}

func pack(pieces []string, maxRunes int) []string {
	var chunks []string
	current := ""
	for _, piece := range pieces {
		if current == "" {
			current = piece
			continue
		}
		candidate := current + " " + piece
		if len([]rune(candidate)) <= maxRunes {
			current = candidate
			continue
		}
		chunks = append(chunks, current)
		current = piece
	}
	if current != "" {
		chunks = append(chunks, current)
	}
	return chunks
}

func sentences(text string) []string {
	text = strings.ReplaceAll(text, "\r\n", "\n")
	text = strings.ReplaceAll(text, "\r", "\n")
	runes := []rune(text)
	start := 0
	lastStart := 0
	var result []string

	appendRange := func(end int) {
		if sentence := normalize(string(runes[start:end])); sentence != "" {
			// TTS may return no audio for isolated punctuation. Keep it with
			// adjacent text, preserving the original spacing and punctuation.
			if last := len(result) - 1; last >= 0 && (isPunctuationOnly(sentence) || isPunctuationOnly(result[last])) {
				result[last] = normalize(string(runes[lastStart:end]))
				return
			}
			lastStart = start
			result = append(result, sentence)
		}
	}

	for i := 0; i < len(runes); i++ {
		if runes[i] == '\n' {
			appendRange(i)
			start = i + 1
			continue
		}
		if !isSentenceBoundary(runes, i) {
			continue
		}

		end := i + 1
		for end < len(runes) && (isHardPunctuation(runes[end]) || runes[end] == '…' || isClosingPunctuation(runes[end])) {
			end++
		}
		appendRange(end)
		start = end
		i = end - 1
	}
	appendRange(len(runes))
	return result
}

// Sentences splits text into normalized natural sentence units, keeping
// punctuation-only fragments with adjacent text when available.
func Sentences(text string) []string {
	return sentences(text)
}

func isPunctuationOnly(text string) bool {
	for _, r := range text {
		if !unicode.IsPunct(r) && !unicode.IsSpace(r) {
			return false
		}
	}
	return true
}

func isSentenceBoundary(runes []rune, index int) bool {
	switch runes[index] {
	case '。', '！', '？', '!', '?':
		return true
	case '…':
		return index+1 == len(runes) || runes[index+1] != '…'
	case '.':
		if index+1 < len(runes) && runes[index+1] == '.' {
			return false
		}
		if isCommonAbbreviation(runes, index) || isNumberedListMarker(runes, index) {
			return false
		}
		return followedBySpaceOrEnd(runes, index)
	default:
		return false
	}
}

func followedBySpaceOrEnd(runes []rune, index int) bool {
	for next := index + 1; next < len(runes); next++ {
		if isClosingPunctuation(runes[next]) {
			continue
		}
		return unicode.IsSpace(runes[next])
	}
	return true
}

func isCommonAbbreviation(runes []rune, index int) bool {
	start := index
	for start > 0 && (unicode.IsLetter(runes[start-1]) || runes[start-1] == '.') {
		start--
	}
	token := string(runes[start:index])
	if isDottedInitialism(token) || isSingleLetterInitial(runes, start, index) {
		return true
	}
	_, found := commonAbbreviations[strings.ToLower(token)]
	return found
}

func isDottedInitialism(token string) bool {
	parts := strings.Split(token, ".")
	if len(parts) < 2 {
		return false
	}
	for _, part := range parts {
		runes := []rune(part)
		if len(runes) != 1 || !unicode.IsLetter(runes[0]) {
			return false
		}
	}
	return true
}

func isSingleLetterInitial(runes []rune, start, period int) bool {
	token := runes[start:period]
	if len(token) != 1 || !unicode.IsLetter(token[0]) {
		return false
	}
	if start == 0 {
		return true
	}

	before := start - 1
	for before >= 0 && (runes[before] == ' ' || runes[before] == '\t') {
		before--
	}
	if before < 0 || runes[before] == '\n' || runes[before] == '.' {
		return true
	}

	next := period + 1
	for next < len(runes) && unicode.IsSpace(runes[next]) {
		next++
	}
	return next+1 < len(runes) && unicode.IsLetter(runes[next]) && runes[next+1] == '.'
}

var commonAbbreviations = map[string]struct{}{
	"dr": {}, "e.g": {}, "etc": {}, "i.e": {}, "jr": {}, "mr": {},
	"mrs": {}, "ms": {}, "prof": {}, "sr": {}, "st": {}, "vs": {},
}

func isNumberedListMarker(runes []rune, index int) bool {
	start := index
	for start > 0 && unicode.IsDigit(runes[start-1]) {
		start--
	}
	if start == index {
		return false
	}
	for before := start - 1; before >= 0; before-- {
		switch runes[before] {
		case '\n':
			return true
		case ' ', '\t':
			continue
		default:
			return false
		}
	}
	return true
}

func isHardPunctuation(r rune) bool {
	switch r {
	case '。', '！', '？', '!', '?':
		return true
	default:
		return false
	}
}

func isClosingPunctuation(r rune) bool {
	switch r {
	case '”', '’', '」', '』', '》', '）', ')', '】', ']', '}', '"', '\'':
		return true
	default:
		return false
	}
}

func normalize(text string) string {
	return strings.Join(strings.Fields(text), " ")
}

func limit(text string, maxRunes int) []string {
	remaining := []rune(text)
	chunks := make([]string, 0, len(remaining)/maxRunes+1)
	for len(remaining) > maxRunes {
		cut, resume := softCut(remaining, maxRunes)
		if chunk := strings.TrimSpace(string(remaining[:cut])); chunk != "" {
			chunks = append(chunks, chunk)
		}
		remaining = []rune(strings.TrimSpace(string(remaining[resume:])))
	}
	if chunk := strings.TrimSpace(string(remaining)); chunk != "" {
		chunks = append(chunks, chunk)
	}
	return chunks
}

func softCut(runes []rune, maxRunes int) (cut int, resume int) {
	for i := maxRunes - 1; i >= 0; i-- {
		if unicode.IsSpace(runes[i]) {
			return i, i + 1
		}
		if isSoftPunctuation(runes[i]) {
			return i + 1, i + 1
		}
	}
	return maxRunes, maxRunes
}

func isSoftPunctuation(r rune) bool {
	switch r {
	case '，', ',', '；', ';', '：', ':', '、':
		return true
	default:
		return false
	}
}
