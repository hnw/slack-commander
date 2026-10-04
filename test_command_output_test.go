package main

import (
	"time"

	"github.com/hnw/slack-commander/cmd"
	"github.com/hnw/slack-commander/pubsub"
)

type observedCommandOutput struct {
	ConversationID cmd.ConversationID
	MessageID      cmd.MessageID
	Text           string
	IsErrOut       bool
	Spawned        bool
	Finished       bool
	ExitCode       int
}

type testCommandOutput struct{ events chan *observedCommandOutput }

func testOutputFactory(events chan *observedCommandOutput) func(pubsub.ReplyConfig, time.Duration) cmd.CommandOutput {
	return func(pubsub.ReplyConfig, time.Duration) cmd.CommandOutput { return &testCommandOutput{events: events} }
}

type testCommandStream struct{ emit func(string) }

func (s *testCommandStream) Write(data []byte) (int, error) {
	s.emit(string(data))
	return len(data), nil
}
func (*testCommandStream) Flush() error { return nil }

func (o *testCommandOutput) Stdout(c cmd.ConversationID, m cmd.MessageID) cmd.OutputStream {
	return o.stream(c, m, false)
}

func (o *testCommandOutput) Stderr(c cmd.ConversationID, m cmd.MessageID) cmd.OutputStream {
	return o.stream(c, m, true)
}

func (o *testCommandOutput) stream(c cmd.ConversationID, m cmd.MessageID, stderr bool) cmd.OutputStream {
	return &testCommandStream{emit: func(text string) {
		o.events <- &observedCommandOutput{ConversationID: c, MessageID: m, Text: text, IsErrOut: stderr}
	}}
}

func (o *testCommandOutput) Start(c cmd.ConversationID, m cmd.MessageID) {
	o.events <- &observedCommandOutput{ConversationID: c, MessageID: m, Spawned: true}
}

func (o *testCommandOutput) Finish(c cmd.ConversationID, m cmd.MessageID, code int) {
	o.events <- &observedCommandOutput{ConversationID: c, MessageID: m, Finished: true, ExitCode: code}
}

func (o *testCommandOutput) SystemError(c cmd.ConversationID, m cmd.MessageID, text string, kind cmd.SystemErrorKind) {
	code := 0
	if kind == cmd.SystemErrorParse {
		code = 2
	}
	o.events <- &observedCommandOutput{ConversationID: c, MessageID: m, Text: text, IsErrOut: true, ExitCode: code}
}
