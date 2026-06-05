package workspace

import (
	"strings"
	"testing"
)

func TestValidateID(t *testing.T) {
	cases := []struct {
		id   string
		want bool
	}{
		{"foo", true},
		{"my-workspace_123", true},
		{strings.Repeat("a", 64), true},
		{strings.Repeat("a", 65), false},
		{"", false},
		{"../etc/passwd", false},
		{"foo/bar", false},
		{"foo bar", false},
		{"foo;rm -rf /", false},
		{".hidden", false},
	}
	for _, tc := range cases {
		got := ValidateID(tc.id) == nil
		if got != tc.want {
			t.Errorf("ValidateID(%q) ok=%v, want %v", tc.id, got, tc.want)
		}
	}
}
