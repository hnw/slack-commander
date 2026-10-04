package cmd

import (
	"bytes"
	"context"
	"errors"
	"reflect"
	"testing"
	"time"
)

type recordedOutputCall struct {
	method       string
	conversation ConversationID
	message      MessageID
	code         int
	text         string
	kind         SystemErrorKind
}

type recordingOutputStream struct {
	bytes.Buffer
	flushed bool
}

func (s *recordingOutputStream) Flush() error { s.flushed = true; return nil }

type recordingOutputHandler struct {
	calls          []recordedOutputCall
	stdout, stderr recordingOutputStream
}

func (h *recordingOutputHandler) Stdout(c ConversationID, m MessageID) OutputStream {
	h.calls = append(h.calls, recordedOutputCall{method: "stdout", conversation: c, message: m})
	return &h.stdout
}

func (h *recordingOutputHandler) Stderr(c ConversationID, m MessageID) OutputStream {
	h.calls = append(h.calls, recordedOutputCall{method: "stderr", conversation: c, message: m})
	return &h.stderr
}

func (h *recordingOutputHandler) Start(c ConversationID, m MessageID) {
	h.calls = append(h.calls, recordedOutputCall{method: "start", conversation: c, message: m})
}

func (h *recordingOutputHandler) Finish(c ConversationID, m MessageID, code int) {
	h.calls = append(h.calls, recordedOutputCall{method: "finish", conversation: c, message: m, code: code})
}

func (h *recordingOutputHandler) SystemError(c ConversationID, m MessageID, text string, kind SystemErrorKind) {
	h.calls = append(h.calls, recordedOutputCall{method: "system", conversation: c, message: m, text: text, kind: kind})
}

func outputHandlerInput(set *CommandSet, text string) *CommandInput {
	return &CommandInput{
		ConversationID: ConversationID{ChannelID: "C", RootTimestamp: "1"},
		MessageID:      MessageID{ChannelID: "C", Timestamp: "2"},
		ResolvedInput:  set.ResolveInput(text, []int{0, 1}),
	}
}

func assertOutputCalls(t *testing.T, handler *recordingOutputHandler, input *CommandInput, want []recordedOutputCall) {
	t.Helper()
	for i := range want {
		want[i].conversation = input.ConversationID
		want[i].message = input.MessageID
	}
	if !reflect.DeepEqual(handler.calls, want) {
		t.Fatalf("handler calls = %#v, want %#v", handler.calls, want)
	}
}

func TestExecutorOutputStreamsAndLifecycle(t *testing.T) {
	for _, tty := range []bool{false, true} {
		t.Run(map[bool]string{false: "plain", true: "tty"}[tty], func(t *testing.T) {
			handler := &recordingOutputHandler{}
			command := testRuntimeCommand(&testExecutionConfig{Keyword: "run", Command: "run", TTY: tty}, singleCmdRunner{command: &fakeCmd{stdoutText: "out\r\n", stderrText: "err", exitCode: 7}})
			command.output = handler
			input := outputHandlerInput(NewCommandSet([]*Command{command}), "run")
			NewExecutor().Execute(context.Background(), input, nil)
			assertOutputCalls(t, handler, input, []recordedOutputCall{{method: "start"}, {method: "stdout"}, {method: "stderr"}, {method: "finish", code: 7}})
			wantOut, wantErr := "out\r\n", "err"
			if tty {
				wantOut, wantErr = "out\nerr", ""
			}
			if handler.stdout.String() != wantOut || handler.stderr.String() != wantErr || !handler.stdout.flushed || (!tty && !handler.stderr.flushed) {
				t.Fatalf("streams: stdout=%q stderr=%q flushed=%v/%v", handler.stdout.String(), handler.stderr.String(), handler.stdout.flushed, handler.stderr.flushed)
			}
		})
	}
}

func TestExecutorOutputTimeout(t *testing.T) {
	for _, tty := range []bool{false, true} {
		t.Run(map[bool]string{false: "plain", true: "tty"}[tty], func(t *testing.T) {
			handler := &recordingOutputHandler{}
			command := testRuntimeCommand(&testExecutionConfig{Keyword: "run", Command: "run", Timeout: 10 * time.Millisecond, TTY: tty}, &blockingRunner{})
			command.output = handler
			input := outputHandlerInput(NewCommandSet([]*Command{command}), "run")
			NewExecutor().Execute(context.Background(), input, nil)
			assertOutputCalls(t, handler, input, []recordedOutputCall{{method: "start"}, {method: "stdout"}, {method: "stderr"}, {method: "finish", code: 143}})
			stream := &handler.stderr
			if tty {
				stream = &handler.stdout
			}
			if stream.String() != "Timeout exceeded (10ms)" || !stream.flushed {
				t.Fatalf("timeout stream = %q, flushed=%v", stream.String(), stream.flushed)
			}
		})
	}
}

func TestExecutorChainOutputOwnership(t *testing.T) {
	owner, second := &recordingOutputHandler{}, &recordingOutputHandler{}
	firstCommand := testRuntimeCommand(&testExecutionConfig{Index: 0, Keyword: "first", Command: "first", AllowInChain: true}, singleCmdRunner{command: &fakeCmd{stdoutText: "first"}})
	secondCommand := testRuntimeCommand(&testExecutionConfig{Index: 1, Keyword: "second", Command: "second", AllowInChain: true}, singleCmdRunner{command: &fakeCmd{stderrText: "second", exitCode: 3}})
	firstCommand.output, secondCommand.output = owner, second
	input := outputHandlerInput(NewCommandSet([]*Command{firstCommand, secondCommand}), "first ; unknown ; second")
	NewExecutor().Execute(context.Background(), input, nil)
	assertOutputCalls(t, owner, input, []recordedOutputCall{{method: "start"}, {method: "stdout"}, {method: "stderr"}, {method: "system", text: "コマンドが見つかりませんでした: unknown", kind: SystemErrorCommandNotFound}, {method: "finish", code: 3}})
	assertOutputCalls(t, second, input, []recordedOutputCall{{method: "stdout"}, {method: "stderr"}})
	if owner.stdout.String() != "first" || second.stderr.String() != "second" {
		t.Fatal("chain streams used the wrong handler")
	}
}

func TestExecutorMissingCommandOutputAndExitCode(t *testing.T) {
	for _, tc := range []struct {
		text    string
		code    int
		missing bool
	}{
		{text: "first ; unknown", code: 127, missing: true},
		{text: "first && unknown", code: 127, missing: true},
		{text: "first || unknown", code: 0},
	} {
		t.Run(tc.text, func(t *testing.T) {
			handler := &recordingOutputHandler{}
			command := testRuntimeCommand(&testExecutionConfig{Keyword: "first", Command: "first", AllowInChain: true}, &fakeRunner{})
			command.output = handler
			input := outputHandlerInput(NewCommandSet([]*Command{command}), tc.text)
			NewExecutor().Execute(context.Background(), input, nil)
			want := []recordedOutputCall{{method: "start"}, {method: "stdout"}, {method: "stderr"}}
			if tc.missing {
				want = append(want, recordedOutputCall{method: "system", text: "コマンドが見つかりませんでした: unknown", kind: SystemErrorCommandNotFound})
			}
			want = append(want, recordedOutputCall{method: "finish", code: tc.code})
			assertOutputCalls(t, handler, input, want)
		})
	}
}

func TestDispatcherParseErrorUsesOutput(t *testing.T) {
	handler := &recordingOutputHandler{}
	command := testRuntimeCommand(&testExecutionConfig{Keyword: "run *", Command: "run *"}, &fakeRunner{})
	command.output = handler
	input := outputHandlerInput(NewCommandSet([]*Command{command}), "run '")
	if input.ResolvedInput.ParseErr == nil {
		t.Fatal("expected parse error")
	}
	dispatcher := NewCommandDispatcher(context.Background(), NewExecutor(), &StdinStore{}, &ConversationLocks{}, func(*CommandInput) bool { t.Fatal("parse error queued"); return false })
	if dispatcher.Dispatch(input) != DispatchAccepted {
		t.Fatal("parse error rejected")
	}
	snapshot := *input
	parseText := input.ResolvedInput.ParseErr.Error()
	input.ConversationID = ConversationID{ChannelID: "changed"}
	input.MessageID = MessageID{Timestamp: "changed"}
	input.ResolvedInput.Commands[0].Command = nil
	input.ResolvedInput.ParseErr = errors.New("unmatched")
	dispatcher.Close()
	dispatcher.Wait()
	assertOutputCalls(t, handler, &snapshot, []recordedOutputCall{{method: "system", text: parseText}})
	if NewCommandDispatcher(context.Background(), nil, nil, nil, nil).Dispatch(input) != DispatchIgnored {
		t.Fatal("unmatched parse error accepted")
	}
}
