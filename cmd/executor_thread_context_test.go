package cmd

import (
	"context"
	"io"
	"sync"
	"testing"
)

type environmentRecordingCmd struct {
	environment []string
	started     chan<- struct{}
	release     <-chan struct{}
}

func (c *environmentRecordingCmd) SetStdin(io.Reader)  {}
func (c *environmentRecordingCmd) SetStdout(io.Writer) {}
func (c *environmentRecordingCmd) SetStderr(io.Writer) {}
func (c *environmentRecordingCmd) SetEnv(environment []string) {
	c.environment = append([]string(nil), environment...)
}

func (c *environmentRecordingCmd) Run(int) int {
	if c.started != nil {
		c.started <- struct{}{}
	}
	if c.release != nil {
		<-c.release
	}
	return 0
}

func dateConfig() []*CommandConfig {
	return []*CommandConfig{NewCommandConfig(&ExecutionConfig{Keyword: "date", Command: "date"})}
}

type environmentRecordingRunner struct {
	mu       sync.Mutex
	commands []*environmentRecordingCmd
	started  chan<- struct{}
	release  <-chan struct{}
}

func (r *environmentRecordingRunner) CommandContext(context.Context, string, ...string) Cmd {
	command := &environmentRecordingCmd{started: r.started, release: r.release}
	r.mu.Lock()
	r.commands = append(r.commands, command)
	r.mu.Unlock()
	return command
}

func (r *environmentRecordingRunner) Commands() []*environmentRecordingCmd {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]*environmentRecordingCmd(nil), r.commands...)
}

func TestExecutorPassesSlackContextEnvironment(t *testing.T) {
	wq := make(chan *CommandOutput, 10)
	runner := &environmentRecordingRunner{}
	executor := NewExecutor(ExecutionConfigs(dateConfig()), func(*ExecutionConfig) CommandRunner { return runner }, wq)

	executor.Execute(context.Background(), &CommandInput{
		Text: "date",
		ConversationID: ConversationID{
			ChannelID:     "C123",
			RootTimestamp: "1700000000.000100",
		},
	}, nil)

	commands := runner.Commands()
	if len(commands) != 1 {
		t.Fatalf("commands = %d, want 1", len(commands))
	}
	if got, want := commands[0].environment, []string{
		"SLACK_CHANNEL_ID=C123",
		"SLACK_THREAD_TS=1700000000.000100",
	}; !equalStrings(got, want) {
		t.Fatalf("environment = %q, want %q", got, want)
	}
}

func TestExecutorPreservesConversationIDOutput(t *testing.T) {
	wq := make(chan *CommandOutput, 2)
	root := NewCommandConfig(&ExecutionConfig{Keyword: "date", Command: "date"})
	executor := NewExecutor(ExecutionConfigs([]*CommandConfig{root}), func(*ExecutionConfig) CommandRunner { return &environmentRecordingRunner{} }, wq)
	executor.Execute(context.Background(), &CommandInput{Text: "date", ConversationID: ConversationID{ChannelID: "C", RootTimestamp: "1"}}, nil)
	output := <-wq
	if output.ConversationID != (ConversationID{ChannelID: "C", RootTimestamp: "1"}) {
		t.Fatalf("context=%+v", output.ConversationID)
	}
}

func equalStrings(got, want []string) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range got {
		if got[i] != want[i] {
			return false
		}
	}
	return true
}
