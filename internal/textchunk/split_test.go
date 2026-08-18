package textchunk

import (
	"reflect"
	"strings"
	"testing"
	"unicode/utf8"
)

func TestSplitKeepsNaturalSentences(t *testing.T) {
	got, err := Split("  你好世界。“现在开始！”  Go   is fun.\n最后一行  ", 50)
	if err != nil {
		t.Fatalf("Split() error = %v", err)
	}
	want := []string{"你好世界。", "“现在开始！”", "Go is fun.", "最后一行"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Split() = %#v, want %#v", got, want)
	}
}

func TestSplitOversizedSentenceAtSoftBoundaries(t *testing.T) {
	got, err := Split("第一段很长，第二段也长，第三段结束。", 8)
	if err != nil {
		t.Fatalf("Split() error = %v", err)
	}
	want := []string{"第一段很长，", "第二段也长，", "第三段结束。"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Split() = %#v, want %#v", got, want)
	}
}

func TestSplitOversizedSentenceAtWhitespace(t *testing.T) {
	got, err := Split("one two three four.", 9)
	if err != nil {
		t.Fatalf("Split() error = %v", err)
	}
	want := []string{"one two", "three", "four."}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Split() = %#v, want %#v", got, want)
	}
}

func TestSplitHandlesEnglishAndUnicodeEllipses(t *testing.T) {
	got, err := Split("Wait... Next. 再等等……然后出发。", 50)
	if err != nil {
		t.Fatalf("Split() error = %v", err)
	}
	want := []string{"Wait...", "Next.", "再等等……", "然后出发。"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Split() = %#v, want %#v", got, want)
	}
}

func TestSplitDoesNotBreakCommonAbbreviationsOrInitials(t *testing.T) {
	got, err := Split("Dr. Smith met J. R. Tolkien. They talked.", 80)
	if err != nil {
		t.Fatalf("Split() error = %v", err)
	}
	want := []string{"Dr. Smith met J. R. Tolkien.", "They talked."}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Split() = %#v, want %#v", got, want)
	}
}

func TestSplitDistinguishesInitialismsFromSentenceEndingLetters(t *testing.T) {
	got, err := Split("U.S. Navy arrived. Option A. Next.", 80)
	if err != nil {
		t.Fatalf("Split() error = %v", err)
	}
	want := []string{"U.S. Navy arrived.", "Option A.", "Next."}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Split() = %#v, want %#v", got, want)
	}
}

func TestSplitKeepsNumberedMarkdownMarkersWithTheirLines(t *testing.T) {
	got, err := Split("1. First item.\n2. Second item.", 80)
	if err != nil {
		t.Fatalf("Split() error = %v", err)
	}
	want := []string{"1. First item.", "2. Second item."}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Split() = %#v, want %#v", got, want)
	}
}

func TestSplitHardLimitIsRuneSafe(t *testing.T) {
	got, err := Split("🙂🙂🙂🙂🙂", 2)
	if err != nil {
		t.Fatalf("Split() error = %v", err)
	}
	want := []string{"🙂🙂", "🙂🙂", "🙂"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Split() = %#v, want %#v", got, want)
	}
	for _, chunk := range got {
		if n := utf8.RuneCountInString(chunk); n > 2 {
			t.Fatalf("chunk %q has %d runes, want at most 2", chunk, n)
		}
	}
}

func TestSplitRejectsInvalidInput(t *testing.T) {
	tests := []struct {
		name    string
		text    string
		limit   int
		wantErr string
	}{
		{name: "zero limit", text: "hello", limit: 0, wantErr: "max runes must be greater than zero"},
		{name: "negative limit", text: "hello", limit: -1, wantErr: "max runes must be greater than zero"},
		{name: "blank text", text: " \n\t", limit: 10, wantErr: "text is empty"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := Split(tt.text, tt.limit)
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("Split() error = %v, want containing %q", err, tt.wantErr)
			}
		})
	}
}
