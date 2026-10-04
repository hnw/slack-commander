package cmd

import "bytes"

type observedOutput struct {
	ConversationID ConversationID
	MessageID      MessageID
	Text           string
	IsErrOut       bool
	Spawned        bool
	Finished       bool
	ExitCode       int
}

type testOutputHandler struct{ events chan *observedOutput }

func newTestCommand(config CommandConfig, runner CommandRunner, replies *CommandSet) *Command {
	return NewCommand(config, runner, replies, &testOutputHandler{})
}

func observeCommandSet(set *CommandSet, events chan *observedOutput) {
	if set == nil {
		return
	}
	for _, command := range set.commands {
		if command == nil {
			continue
		}
		command.output = &testOutputHandler{events: events}
		observeCommandSet(command.replies, events)
	}
}

func (h *testOutputHandler) emit(event *observedOutput) {
	if h.events != nil {
		h.events <- event
	}
}

type testOutputStream struct {
	bytes.Buffer
	onFlush func(string)
}

func (s *testOutputStream) Flush() error {
	if s.Len() > 0 {
		s.onFlush(s.String())
		s.Reset()
	}
	return nil
}

func (h *testOutputHandler) stream(c ConversationID, m MessageID, stderr bool) OutputStream {
	return &testOutputStream{onFlush: func(text string) {
		h.emit(&observedOutput{ConversationID: c, MessageID: m, Text: text, IsErrOut: stderr})
	}}
}

func (h *testOutputHandler) Stdout(c ConversationID, m MessageID) OutputStream {
	return h.stream(c, m, false)
}

func (h *testOutputHandler) Stderr(c ConversationID, m MessageID) OutputStream {
	return h.stream(c, m, true)
}

func (h *testOutputHandler) Start(c ConversationID, m MessageID) {
	h.emit(&observedOutput{ConversationID: c, MessageID: m, Spawned: true})
}

func (h *testOutputHandler) Finish(c ConversationID, m MessageID, code int) {
	h.emit(&observedOutput{ConversationID: c, MessageID: m, Finished: true, ExitCode: code})
}

func (h *testOutputHandler) SystemError(c ConversationID, m MessageID, text string, kind SystemErrorKind) {
	code := 0
	if kind == SystemErrorParse {
		code = 2
	}
	h.emit(&observedOutput{ConversationID: c, MessageID: m, Text: text, IsErrOut: true, ExitCode: code})
}
