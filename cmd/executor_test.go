package cmd

import (
	"testing"
)

func TestParse(t *testing.T) {
	failurePatterns := []string{
		"",
		">",
		";x",
		"&x",
		"|x",
		"x>",
		"x;",
		"x&",
		"x|",
		"x&y",
		"x|x",
		"x&;x",
		"x|;x",
		"tr -cd '[:graph:]' < /dev/urandom",
		"ls > /dev/null",
		"ls 2>&1",
		"(ls)",
		"あい&うえお",
	}
	for _, s := range failurePatterns {
		_, err := parse(s)
		if err == nil {
			t.Errorf(`Unexpected success for checkSyntax("%v")`, s)
		} else {
			t.Logf(`expected failure for checkSyntax("%v"): %v`, s, err)
		}
	}
}
