package core_test

import (
	"testing"
	"unicode/utf8"

	"github.com/go-via/viashowcase/internal/core"
	"github.com/stretchr/testify/assert"
)

// Surrounding whitespace is stripped so it never reaches the projection or the
// big screen.
func TestNormalizeTextTrimsWhitespace(t *testing.T) {
	t.Parallel()
	assert.Equal(t, "hi", core.NormalizeText("  hi \n", 0))
}

// Input within the limit is returned unchanged (after trim).
func TestNormalizeTextUnderLimitIsUnchanged(t *testing.T) {
	t.Parallel()
	assert.Equal(t, "hello", core.NormalizeText("hello", 10))
	assert.Equal(t, "hello", core.NormalizeText("hello", 5)) // exactly at the limit
}

// Over-long input is capped to maxRunes so a participant can't flood the board.
func TestNormalizeTextOverLimitIsTruncated(t *testing.T) {
	t.Parallel()
	assert.Equal(t, "hello", core.NormalizeText("hello world", 5))
}

// Truncation counts runes, not bytes, so multibyte text is never cut mid-rune
// (the old byte-slice bug produced invalid UTF-8). The result stays valid UTF-8.
func TestNormalizeTextTruncatesMultibyteRuneSafely(t *testing.T) {
	t.Parallel()
	got := core.NormalizeText("日本語のテキスト", 3) // each rune is 3 bytes
	assert.Equal(t, "日本語", got)
	assert.True(t, utf8.ValidString(got), "result must be valid UTF-8")
	assert.Equal(t, 3, utf8.RuneCountInString(got))
}

// Emoji (4-byte runes, some grapheme-composed) are also cut on rune boundaries.
func TestNormalizeTextTruncatesEmojiSafely(t *testing.T) {
	t.Parallel()
	got := core.NormalizeText("😀😁😂🤣", 2)
	assert.True(t, utf8.ValidString(got))
	assert.Equal(t, 2, utf8.RuneCountInString(got))
}

// Trim happens BEFORE counting, so padding spaces don't eat into the budget.
func TestNormalizeTextTrimsBeforeCounting(t *testing.T) {
	t.Parallel()
	assert.Equal(t, "abcde", core.NormalizeText("   abcde   ", 5))
}

// Empty and whitespace-only input normalize to the empty string, so a blank
// submission can be rejected uniformly by callers.
func TestNormalizeTextEmptyAndBlankBecomeEmpty(t *testing.T) {
	t.Parallel()
	assert.Equal(t, "", core.NormalizeText("", 40))
	assert.Equal(t, "", core.NormalizeText("    \t\n", 40))
}

// maxRunes <= 0 means "no cap" — only trimming is applied.
func TestNormalizeTextNonPositiveLimitMeansNoCap(t *testing.T) {
	t.Parallel()
	long := "this is a fairly long string that should survive intact"
	assert.Equal(t, long, core.NormalizeText("  "+long+"  ", 0))
	assert.Equal(t, long, core.NormalizeText(long, -1))
}
