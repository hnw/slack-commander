package pubsub

import (
	"reflect"
	"testing"
	"time"

	"github.com/hnw/slack-commander/cmd"
)

func TestSlackOutputHandlerStreamsAndLifecycle(t *testing.T) {
	queue := make(chan *CommandOutput, 10)
	broadcast := false
	reply := ReplyConfig{Username: "command", IconEmoji: ":robot_face:", OutputFormat: OutputFormatMarkdown, ReplyBroadcast: &broadcast}
	handler := NewSlackOutputHandler(queue, reply, time.Hour)
	c := cmd.ConversationID{ChannelID: "C", RootTimestamp: "1"}
	m := cmd.MessageID{ChannelID: "C", Timestamp: "2"}
	handler.Start(c, m)
	stdout, stderr := handler.Stdout(c, m), handler.Stderr(c, m)
	_, _ = stdout.Write([]byte("out"))
	_, _ = stderr.Write([]byte("err"))
	if len(queue) != 1 {
		t.Fatal("output bypassed flush interval")
	}
	if err := stdout.Flush(); err != nil {
		t.Fatal(err)
	}
	if err := stderr.Flush(); err != nil {
		t.Fatal(err)
	}
	handler.Finish(c, m, 7)
	want := []*CommandOutput{
		{ConversationID: c, MessageID: m, Spawned: true},
		{ConversationID: c, MessageID: m, ReplyConfig: &reply, Text: "out"},
		{ConversationID: c, MessageID: m, ReplyConfig: &reply, Text: "err", IsErrOut: true},
		{ConversationID: c, MessageID: m, Finished: true, ExitCode: 7},
	}
	for _, expected := range want {
		if got := <-queue; !reflect.DeepEqual(got, expected) {
			t.Fatalf("output = %#v, want %#v", got, expected)
		}
	}
}

func TestSlackOutputHandlerSystemErrors(t *testing.T) {
	broadcast := false
	reply := ReplyConfig{Username: "custom", IconEmoji: ":robot_face:", OutputFormat: OutputFormatMarkdown, ReplyBroadcast: &broadcast}
	c, m := cmd.ConversationID{ChannelID: "C", RootTimestamp: "1"}, cmd.MessageID{ChannelID: "C", Timestamp: "2"}
	for _, tc := range []struct {
		name   string
		kind   cmd.SystemErrorKind
		config *ReplyConfig
		code   int
	}{
		{name: "parse", kind: cmd.SystemErrorParse, config: NewSystemReplyConfig(&broadcast), code: 2},
		{name: "not found", kind: cmd.SystemErrorCommandNotFound},
	} {
		t.Run(tc.name, func(t *testing.T) {
			queue := make(chan *CommandOutput, 1)
			handler := NewSlackOutputHandler(queue, reply, time.Hour)
			handler.SystemError(c, m, "error", tc.kind)
			got := <-queue
			want := &CommandOutput{ConversationID: c, MessageID: m, Text: "error", IsErrOut: true, ReplyConfig: tc.config, ExitCode: tc.code}
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("error output = %#v, want %#v", got, want)
			}
			if tc.kind == cmd.SystemErrorCommandNotFound && !reflect.DeepEqual(getConfig(got), NewSystemReplyConfig(nil)) {
				t.Fatal("not found used command-specific settings")
			}
		})
	}
}
