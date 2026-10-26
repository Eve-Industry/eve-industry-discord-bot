package tickets

import (
	"strings"
	"testing"
	"unicode/utf8"
)

func TestThreadName(t *testing.T) {
	tests := []struct {
		user, topic, want string
	}{
		{"Alice", "Account problem", "ticket-alice-account-problem"},
		{"bob_99", "  Can't log in!! ", "ticket-bob-99-cant-log-in"},
		{"Ørjan", "Spørsmål", "ticket-ørjan-spørsmål"},
	}
	for _, tt := range tests {
		if got := threadName(tt.user, tt.topic); got != tt.want {
			t.Errorf("threadName(%q, %q) = %q, want %q", tt.user, tt.topic, got, tt.want)
		}
	}
}

func TestThreadNameLength(t *testing.T) {
	got := threadName("someone", strings.Repeat("word ", 50))
	if n := utf8.RuneCountInString(got); n > maxThreadName {
		t.Errorf("name has %d characters, want at most %d", n, maxThreadName)
	}
	if strings.HasSuffix(got, "-") {
		t.Errorf("name %q ends with a dash", got)
	}
}
