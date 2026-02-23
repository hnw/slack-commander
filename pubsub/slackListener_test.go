package pubsub

import (
	"testing"
	"time"

	"github.com/hnw/slack-commander/cmd"
)

func TestEnqueueCommand(t *testing.T) {
	ch := make(chan *cmd.CommandInput, 1)
	input := &cmd.CommandInput{Text: "date"}

	if ok := enqueueCommand(ch, input); !ok {
		t.Fatalf("expected enqueue success")
	}
}

func TestEnqueueCommandQueueFullDoesNotBlock(t *testing.T) {
	ch := make(chan *cmd.CommandInput, 1)
	ch <- &cmd.CommandInput{Text: "filled"}

	start := time.Now()
	ok := enqueueCommand(ch, &cmd.CommandInput{Text: "drop-me"})
	if ok {
		t.Fatalf("expected enqueue failure when queue is full")
	}
	if d := time.Since(start); d > 100*time.Millisecond {
		t.Fatalf("enqueue blocked too long: %v", d)
	}
}

func TestNormalizeSlackURLs(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{
			name:  "plain URL is unwrapped",
			input: "<https://example.com>",
			want:  "https://example.com",
		},
		{
			name:  "URL with display text returns display text",
			input: "<https://example.com|click here>",
			want:  "click here",
		},
		{
			name:  "URL with empty display text falls back to URL",
			input: "<https://example.com|>",
			want:  "https://example.com",
		},
		{
			name:  "no URL markup is left unchanged",
			input: "hello world",
			want:  "hello world",
		},
		{
			name:  "URL embedded in prose",
			input: "check out <https://example.com|this link> please",
			want:  "check out this link please",
		},
		{
			name:  "multiple URLs in one message",
			input: "<https://a.com> and <https://b.com|B site>",
			want:  "https://a.com and B site",
		},
		{
			name:  "mention pattern is not affected",
			input: "<@U12345>",
			want:  "<@U12345>",
		},
		{
			name:  "special token like <!channel> is not affected",
			input: "<!channel>",
			want:  "<!channel>",
		},
		{
			name:  "http URL is unwrapped",
			input: "<http://example.com>",
			want:  "http://example.com",
		},
		{
			name:  "URL with query string is unwrapped",
			input: "<https://example.com?foo=bar>",
			want:  "https://example.com?foo=bar",
		},
		{
			name:  "empty string is unchanged",
			input: "",
			want:  "",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := normalizeSlackURLs(tc.input)
			if got != tc.want {
				t.Errorf("normalizeSlackURLs(%q) = %q, want %q", tc.input, got, tc.want)
			}
		})
	}
}
