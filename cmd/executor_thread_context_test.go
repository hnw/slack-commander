package cmd

import (
	"context"
	"io"
	"sync"
	"testing"
	"time"
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
	return []*CommandConfig{NewCommandConfig(&Definition{Keyword: "date", Command: "date"}, nil)}
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
	rq := make(chan *CommandInput, 1)
	wq := make(chan *CommandOutput, 10)
	runner := &environmentRecordingRunner{}
	done := make(chan struct{})
	go func() {
		ExecutorWithRunner(
			context.Background(),
			rq,
			wq,
			dateConfig(),
			func(*CommandConfig) CommandRunner { return runner },
		)
		close(done)
	}()

	rq <- &CommandInput{
		Text: "date",
		ConversationContext: ConversationContext{
			ChannelID:           "C123",
			RootThreadTimestamp: "1700000000.000100",
		},
	}
	close(rq)
	<-done

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

func TestExecutorSerializesCommandsInSameThread(t *testing.T) {
	started := make(chan struct{}, 2)
	release := make(chan struct{}, 2)
	runner := &environmentRecordingRunner{started: started, release: release}
	rq := make(chan *CommandInput, 2)
	wq := make(chan *CommandOutput, 10)
	locks := &ThreadLocks{}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var workers sync.WaitGroup
	for range 2 {
		workers.Add(1)
		go func() {
			defer workers.Done()
			ExecutorWithThreadInputAndLocks(
				ctx,
				rq,
				wq,
				dateConfig(),
				func(*CommandConfig) CommandRunner { return runner },
				nil,
				locks,
			)
		}()
	}
	input := &CommandInput{
		Text:                "date",
		ConversationContext: ConversationContext{ChannelID: "C", RootThreadTimestamp: "1"},
	}
	rq <- input
	rq <- input

	awaitStart(t, started)
	select {
	case <-started:
		t.Fatal("second command started while first command was running")
	case <-time.After(100 * time.Millisecond):
	}
	release <- struct{}{}
	awaitStart(t, started)
	release <- struct{}{}
	close(rq)
	workers.Wait()
}

func TestExecutorRunsCommandsInDifferentThreadsConcurrently(t *testing.T) {
	started := make(chan struct{}, 2)
	release := make(chan struct{}, 2)
	runner := &environmentRecordingRunner{started: started, release: release}
	rq := make(chan *CommandInput, 2)
	wq := make(chan *CommandOutput, 10)
	locks := &ThreadLocks{}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var workers sync.WaitGroup
	for range 2 {
		workers.Add(1)
		go func() {
			defer workers.Done()
			ExecutorWithThreadInputAndLocks(
				ctx,
				rq,
				wq,
				dateConfig(),
				func(*CommandConfig) CommandRunner { return runner },
				nil,
				locks,
			)
		}()
	}
	rq <- &CommandInput{
		Text:                "date",
		ConversationContext: ConversationContext{ChannelID: "C", RootThreadTimestamp: "1"},
	}
	rq <- &CommandInput{
		Text:                "date",
		ConversationContext: ConversationContext{ChannelID: "C", RootThreadTimestamp: "2"},
	}

	awaitStart(t, started)
	awaitStart(t, started)
	release <- struct{}{}
	release <- struct{}{}
	close(rq)
	workers.Wait()
}

func awaitStart(t *testing.T, started <-chan struct{}) {
	t.Helper()
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("command did not start")
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
