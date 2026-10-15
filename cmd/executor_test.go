package cmd

import (
	"reflect"
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

func TestParseUnicodeChains(t *testing.T) {
	for _, tt := range []struct {
		line string
		want []*commandPart
	}{
		{"あ;echo hi", []*commandPart{newCommandPart("", []string{"あ"}), newCommandPart(";", []string{"echo", "hi"})}},
		{"あ && echo hi", []*commandPart{newCommandPart("", []string{"あ"}), newCommandPart("&&", []string{"echo", "hi"})}},
		{"echo あ ; echo い", []*commandPart{newCommandPart("", []string{"echo", "あ"}), newCommandPart(";", []string{"echo", "い"})}},
		{"echo あ || echo い && echo う", []*commandPart{newCommandPart("", []string{"echo", "あ"}), newCommandPart("||", []string{"echo", "い"}), newCommandPart("&&", []string{"echo", "う"})}},
	} {
		t.Run(tt.line, func(t *testing.T) {
			got, err := parse(tt.line)
			if err != nil {
				t.Fatalf("parse() error = %v", err)
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("parse() = %#v, want %#v", got, tt.want)
			}
		})
	}
}
