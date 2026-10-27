package domain

import (
	"strings"
	"testing"
)

func TestValidateChatText(t *testing.T) {
	for _, text := range []string{"hello\nworld", strings.Repeat("я", 500), strings.Repeat("🙂", 250)} {
		if err := ValidateChatText(text); err != nil {
			t.Fatalf("valid text rejected: %v", err)
		}
	}
	for _, text := range []string{"", " \n\t", "hello\x00", string([]byte{0xff}), strings.Repeat("я", 501), strings.Repeat("a", 1001)} {
		if err := ValidateChatText(text); err == nil {
			t.Fatalf("invalid text accepted (%d bytes)", len(text))
		}
	}
}
func TestChatFilesOnlyStillValidateTextAndIDs(t *testing.T) {
	for _, text := range []string{"", " \n\t"} {
		if err := ValidateChatContent(text, []int64{1}); err != nil {
			t.Fatal(err)
		}
	}
	for _, text := range []string{"\x00", string([]byte{255}), strings.Repeat(" ", 1001)} {
		if err := ValidateChatContent(text, []int64{1}); err == nil {
			t.Fatalf("accepted invalid text %q", text)
		}
	}
}
