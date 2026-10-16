package main

import (
	"time"

	"github.com/hnw/slack-commander/internal/command"
	"github.com/hnw/slack-commander/internal/messaging"
)

type observedCommandOutput struct {
	ConversationID command.ConversationID
	MessageID      command.MessageID
	Text           string
	IsErrOut       bool
	Spawned        bool
	Finished       bool
	ExitCode       int
}

type testCommandOutput struct{ events chan *observedCommandOutput }

func testOutputFactory(events chan *observedCommandOutput) func(messaging.ReplyConfig, time.Duration) command.CommandOutput {
	return func(messaging.ReplyConfig, time.Duration) command.CommandOutput {
		return &testCommandOutput{events: events}
	}
}

type testCommandStream struct{ emit func(string) }

func (s *testCommandStream) Write(data []byte) (int, error) {
	s.emit(string(data))
	return len(data), nil
}
func (*testCommandStream) Flush() error { return nil }

func (o *testCommandOutput) Stdout(c command.ConversationID, m command.MessageID) command.OutputStream {
	return o.stream(c, m, false)
}

func (o *testCommandOutput) Stderr(c command.ConversationID, m command.MessageID) command.OutputStream {
	return o.stream(c, m, true)
}

func (o *testCommandOutput) stream(c command.ConversationID, m command.MessageID, stderr bool) command.OutputStream {
	return &testCommandStream{emit: func(text string) {
		o.events <- &observedCommandOutput{ConversationID: c, MessageID: m, Text: text, IsErrOut: stderr}
	}}
}

func (o *testCommandOutput) Start(c command.ConversationID, m command.MessageID) {
	o.events <- &observedCommandOutput{ConversationID: c, MessageID: m, Spawned: true}
}

func (o *testCommandOutput) Finish(c command.ConversationID, m command.MessageID, code int) {
	o.events <- &observedCommandOutput{ConversationID: c, MessageID: m, Finished: true, ExitCode: code}
}

func (o *testCommandOutput) SystemError(c command.ConversationID, m command.MessageID, text string, kind command.SystemErrorKind) {
	code := 0
	if kind == command.SystemErrorParse {
		code = 2
	}
	o.events <- &observedCommandOutput{ConversationID: c, MessageID: m, Text: text, IsErrOut: true, ExitCode: code}
}
