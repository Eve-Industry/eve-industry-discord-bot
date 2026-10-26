package feedback

import (
	"strings"
	"testing"
	"unicode/utf8"
)

func TestPostMessageSkipsBlankFields(t *testing.T) {
	msg := postMessage(42, bugColor, []field{
		{"What happened", "It crashed"},
		{"Steps to reproduce", "   "},
		{"Version and setup", "v1.2"},
	})
	desc := msg.Embeds[0].Description
	if strings.Contains(desc, "Steps to reproduce") {
		t.Errorf("blank field included: %q", desc)
	}
	want := "**What happened**\nIt crashed\n\n**Version and setup**\nv1.2"
	if desc != want {
		t.Errorf("description = %q, want %q", desc, want)
	}
	if msg.Content != "Submitted by <@42>" {
		t.Errorf("content = %q", msg.Content)
	}
}

func TestPostTitle(t *testing.T) {
	if got := postTitle("  Export   to\nCSV "); got != "Export to CSV" {
		t.Errorf("postTitle = %q", got)
	}
	if got := postTitle("   "); got != "Untitled" {
		t.Errorf("postTitle(blank) = %q", got)
	}
	if n := utf8.RuneCountInString(postTitle(strings.Repeat("ab ", 60))); n > maxPostTitle {
		t.Errorf("title has %d characters, want at most %d", n, maxPostTitle)
	}
}
