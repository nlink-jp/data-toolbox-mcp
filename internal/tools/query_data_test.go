package tools

import "testing"

func TestContainsLimit(t *testing.T) {
	cases := []struct {
		sql  string
		want bool
	}{
		{"SELECT * FROM t", false},
		{"SELECT * FROM t LIMIT 10", true},
		{"select * from t limit 10", true},
		{"SELECT * FROM t -- LIMIT 10", true}, // crude: appears anywhere as word
		{"SELECT col_limit FROM t", false},    // not a standalone LIMIT token
		{"SELECT limitless FROM t", false},
		{"WITH x AS (SELECT * FROM t LIMIT 5) SELECT * FROM x", true},
	}
	for _, tc := range cases {
		if got := containsLimit(tc.sql); got != tc.want {
			t.Errorf("containsLimit(%q) = %v, want %v", tc.sql, got, tc.want)
		}
	}
}
