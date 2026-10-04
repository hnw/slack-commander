package cmd

import (
	"context"
	"io"
	"log"
)

// StdinReplyRunner は選択済みのstdin endpointへ返信を渡す。
type StdinReplyRunner struct{}

// NewStdinReplyRunner は状態を持たないstdin reply runnerを生成する。
func NewStdinReplyRunner() CommandRunner {
	return &StdinReplyRunner{}
}

// CommandContext は実行用のstdin reply Cmdを生成する。
func (*StdinReplyRunner) CommandContext(_ context.Context, _ string, _ ...string) Cmd {
	return &stdinReplyCmd{}
}

type stdinReplyCmd struct {
	stdin  io.Reader
	target *InteractiveStdin
}

func (c *stdinReplyCmd) SetStdinTarget(target *InteractiveStdin) { c.target = target }
func (c *stdinReplyCmd) SetStdin(stdin io.Reader)                { c.stdin = stdin }
func (*stdinReplyCmd) SetStdout(io.Writer)                       {}
func (*stdinReplyCmd) SetStderr(io.Writer)                       {}

func (c *stdinReplyCmd) Run() int {
	if c.target == nil || c.stdin == nil {
		return 127
	}
	body, err := io.ReadAll(c.stdin)
	if err != nil {
		log.Printf("[WARN] reading interactive stdin reply: %v", err)
		return 127
	}
	if err := c.target.TrySend(string(body)); err != nil {
		log.Printf("[WARN] dropping interactive stdin reply: %v", err)
	}
	return 0
}
