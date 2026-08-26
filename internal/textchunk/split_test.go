package textchunk

import (
	"reflect"
	"strings"
	"testing"
	"unicode/utf8"
)

func TestSentencesReturnsNaturalSentenceUnits(t *testing.T) {
	got := Sentences("第一句。第二句！ Dr. Smith stayed.")
	want := []string{"第一句。", "第二句！", "Dr. Smith stayed."}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Sentences() = %#v, want %#v", got, want)
	}
}

func TestSentencesKeepsPunctuationFragmentsWithAdjacentText(t *testing.T) {
	tests := []struct {
		name string
		text string
		want []string
	}{
		{"function suffix", "再调用既有 queries/create-card!：", []string{"再调用既有 queries/create-card!："}},
		{"predicate suffix", "检查 valid?；", []string{"检查 valid?；"}},
		{"quoted punctuation", "他说：“完成！”，", []string{"他说：“完成！”，"}},
		{"spaced punctuation", "完成！ ：", []string{"完成！ ："}},
		{"leading ellipsis", "……稍后继续。下一句！", []string{"……稍后继续。", "下一句！"}},
		{"separate punctuation line", "完成！\n：\n继续。", []string{"完成！ ：", "继续。"}},
		{"preserve numbers and symbols", "完成！123。🙂", []string{"完成！", "123。", "🙂"}},
		{"normal sentence boundaries", "完成！继续？结束。", []string{"完成！", "继续？", "结束。"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := Sentences(tt.text); !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("Sentences(%q) = %#v, want %#v", tt.text, got, tt.want)
			}
		})
	}
}

func TestSplitKeepsNaturalParagraphs(t *testing.T) {
	got, err := Split("第一句。第二句。\n这一行仍属于同一自然段。\n\n  \n第三句。第四句。", 100)
	if err != nil {
		t.Fatalf("Split() error = %v", err)
	}
	want := []string{"第一句。第二句。 这一行仍属于同一自然段。", "第三句。第四句。"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Split() = %#v, want %#v", got, want)
	}
}

func TestSplitPacksSentencesWithinParagraphLimit(t *testing.T) {
	got, err := Split("第一句。第二句。第三句。第四句。", 9)
	if err != nil {
		t.Fatalf("Split() error = %v", err)
	}
	want := []string{"第一句。 第二句。", "第三句。 第四句。"}
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
	got, err := Split("Wait... Next. 再等等……然后出发。", 16)
	if err != nil {
		t.Fatalf("Split() error = %v", err)
	}
	want := []string{"Wait... Next.", "再等等…… 然后出发。"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Split() = %#v, want %#v", got, want)
	}
}

func TestSplitDoesNotBreakCommonAbbreviationsOrInitials(t *testing.T) {
	got, err := Split("Dr. Smith met J. R. Tolkien. They talked.", 28)
	if err != nil {
		t.Fatalf("Split() error = %v", err)
	}
	want := []string{"Dr. Smith met J. R. Tolkien.", "They talked."}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Split() = %#v, want %#v", got, want)
	}
}

func TestSplitDistinguishesInitialismsFromSentenceEndingLetters(t *testing.T) {
	got, err := Split("U.S. Navy arrived. Option A. Next.", 18)
	if err != nil {
		t.Fatalf("Split() error = %v", err)
	}
	want := []string{"U.S. Navy arrived.", "Option A. Next."}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Split() = %#v, want %#v", got, want)
	}
}

func TestSplitKeepsNumberedMarkdownMarkersWithTheirTextWhenParagraphIsOversized(t *testing.T) {
	got, err := Split("1. First item has several words.\n2. Second item.", 20)
	if err != nil {
		t.Fatalf("Split() error = %v", err)
	}
	want := []string{"1. First item has", "several words.", "2. Second item."}
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
