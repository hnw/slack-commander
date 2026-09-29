package cmd

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"strings"
	"time"

	"github.com/mattn/go-shellwords"
)

// CommandInput はPubSubからの情報をExecutorに引き渡す構造体
type CommandInput struct {
	ConversationID   ConversationID
	MessageID        MessageID
	Text             string // 起動コマンド平文
	ExecutionConfigs []*ExecutionConfig
}

// ConversationID identifies the Slack thread that receives command output.
type ConversationID struct {
	ChannelID     string
	RootTimestamp string
}

// MessageID identifies the Slack message that triggered command execution.
type MessageID struct {
	ChannelID string
	Timestamp string
}

// CommandOutput はExecutorからの実行結果を引き渡してPubSubに書き出すための構造体
type CommandOutput struct {
	ReplyConfig    interface{}
	ConversationID ConversationID
	MessageID      MessageID
	Text           string // コマンドからのテキスト出力（ImageData と排他）
	ImageData      []byte // sixel を変換した PNG バイト列（Text と排他）
	IsErrOut       bool
	Spawned        bool
	Finished       bool
	ExitCode       int
}

// ExecutionConfig is the complete resolved configuration needed to execute a command.
type ExecutionConfig struct {
	StdinIdleTimeout    int  `toml:"stdin_idle_timeout"`
	TTY                 bool `toml:"tty"`
	Timeout             int
	OutputFlushInterval time.Duration `toml:"-"`
	AllowInChain        bool          `toml:"-"`
	InteractiveStdin    bool          `toml:"-"`
	InputBodyMode       InputBodyMode `toml:"-"`
	Keyword             string
	Command             string
	Runner              string
	Method              string
	URL                 string
	Headers             map[string]string
	Body                string
	// ReplyConfig and SystemReplyConfig are opaque output metadata.
	ReplyConfig       interface{} `toml:"-"`
	SystemReplyConfig interface{} `toml:"-"`
}

// RunnerFactory returns a runner for the given execution definition.
type RunnerFactory func(config *ExecutionConfig) CommandRunner

// Executor executes individual command inputs.
type Executor struct {
	matchers      []*Matcher
	runnerFactory RunnerFactory
	outputQueue   chan *CommandOutput
}

// NewExecutor creates an executor with initialized matchers and runners.
func NewExecutor(configs []*ExecutionConfig, runnerFactory RunnerFactory, outputQueue chan *CommandOutput) *Executor {
	runnerFactory = normalizeRunnerFactory(runnerFactory)
	return &Executor{
		matchers:      buildMatchers(configs, runnerFactory),
		runnerFactory: runnerFactory,
		outputQueue:   outputQueue,
	}
}

// Execute processes one command input without queue or conversation ownership.
func (e *Executor) Execute(
	ctx context.Context,
	input *CommandInput,
	lifecycle StdinLifecycle,
) {
	if ctx == nil {
		ctx = context.Background()
	}
	inputMatchers := e.matchers
	if input.ExecutionConfigs != nil {
		inputMatchers = buildMatchers(input.ExecutionConfigs, e.runnerFactory)
	}

	cmdMsg, stdinText := splitCommandInput(input.Text)
	cmds, parseErr := parseCommands(cmdMsg)

	if len(cmds) > 1 && !chainUsesOnlyAllowedCommands(cmds, inputMatchers) {
		return
	}

	rawBody := ""
	initialStdin := stdinText
	inputLifecycle := lifecycle

	if len(cmds) == 0 {
		return
	}
	matcher, _ := findMatchedMatcher(cmds[0], inputMatchers)
	if matcher != nil && matcher.config.InputBodyMode == InputBodyArgument {
		if stdinText != "" {
			rawBody = "\n" + stdinText
		}
		initialStdin = ""
	}

	if matcher == nil || !matcher.config.InteractiveStdin {
		inputLifecycle = nil
	}

	_ = executeCommands(
		ctx,
		cmds,
		parseErr,
		initialStdin,
		rawBody,
		input,
		inputMatchers,
		e.outputQueue,
		inputLifecycle,
	)
}

func chainUsesOnlyAllowedCommands(cmds []*parsedCommand, matchers []*Matcher) bool {
	for _, command := range cmds {
		matcher, _ := findMatchedMatcher(command, matchers)
		if matcher == nil {
			continue
		}
		if !matcher.config.AllowInChain {
			return false
		}
	}
	return true
}

func normalizeRunnerFactory(runnerFactory RunnerFactory) RunnerFactory {
	if runnerFactory != nil {
		return runnerFactory
	}
	return func(*ExecutionConfig) CommandRunner {
		return NewExecRunner()
	}
}

func buildMatchers(configs []*ExecutionConfig, runnerFactory RunnerFactory) []*Matcher {
	matchers := make([]*Matcher, 0, len(configs))
	for _, config := range configs {
		matcher := newMatcher(config)
		if matcher == nil {
			continue
		}
		runner := runnerFactory(config)
		if runner == nil {
			runner = NewExecRunner()
		}
		matcher.runner = runner
		matchers = append(matchers, matcher)
	}
	return matchers
}

func splitCommandInput(text string) (string, string) {
	msgArr := strings.SplitN(text, "\n", 2)
	cmdMsg := msgArr[0]
	stdinText := ""
	if len(msgArr) >= 2 {
		// メッセージが複数行だった場合、1行目をコマンド、2行目以降を標準入力として扱う
		stdinText = msgArr[1]
	}
	return cmdMsg, stdinText
}

func parseCommands(cmdMsg string) ([]*parsedCommand, error) {
	cmds, err := parse(cmdMsg)
	// パースに完全に失敗した場合のフォールバック（クォーテーション忘れ等）
	if len(cmds) == 0 {
		fields := strings.Fields(cmdMsg)
		if len(fields) > 0 {
			cmds = append(cmds, newParsedCommand("", fields))
		}
	}
	return cmds, err
}

func executeCommands(
	ctx context.Context,
	cmds []*parsedCommand,
	parseErr error,
	stdinText string,
	rawBody string,
	input *CommandInput,
	matchers []*Matcher,
	wq chan *CommandOutput,
	lifecycle StdinLifecycle,
) int {
	ret := 0
	for i, cmd := range cmds {
		if shouldSkipCommand(cmd, ret) {
			continue
		}
		ret = -1
		m, args := findMatchedMatcher(cmd, matchers)
		if m == nil {
			if i == 0 {
				// キーワードにマッチしなかったらparse errorがあっても表示せず終了
				return 0
			}
			ret = writeCommandNotFound(wq, input, cmd)
			continue
		}
		if i == 0 {
			// コマンド実行開始を通知
			wq <- &CommandOutput{
				ConversationID: input.ConversationID,
				MessageID:      input.MessageID,
				Spawned:        true,
			}
			// 関数を抜ける時に必ず終了通知を送る
			defer func() {
				wq <- &CommandOutput{
					ConversationID: input.ConversationID,
					MessageID:      input.MessageID,
					Finished:       true,
					ExitCode:       ret,
				}
			}()
		}
		if parseErr != nil {
			// parse errorありで1つ目のコマンドがキーワードマッチした場合
			// エラー表示して処理全体を終了
			ret = writeParseError(wq, input, parseErr, m)
			return ret
		}
		if rawBody != "" && m.hasTrailingWildcard() {
			args = append(args, rawBody)
		}
		ret = runMatchedCommand(ctx, m, args, stdinText, input, wq, lifecycle)
	}
	return ret
}

func shouldSkipCommand(cmd *parsedCommand, ret int) bool {
	if ret == 0 && cmd.skipIfSucceeded {
		return true
	}
	return ret != 0 && cmd.skipIfFailed
}

func writeParseError(wq chan *CommandOutput, input *CommandInput, parseErr error, m *Matcher) int {
	syserr := newErrWriter(wq, m.config.SystemReplyConfig, input.ConversationID, input.MessageID, m.config.OutputFlushInterval)
	_, _ = fmt.Fprintf(syserr, "%v", parseErr)
	_ = syserr.Flush()
	return 2
}

func writeCommandNotFound(wq chan *CommandOutput, input *CommandInput, cmd *parsedCommand) int {
	syserr := newErrWriter(wq, nil, input.ConversationID, input.MessageID, DefaultOutputFlushInterval)
	_, _ = fmt.Fprintf(syserr, "コマンドが見つかりませんでした: %v", strings.Join(cmd.args, " "))
	_ = syserr.Flush()
	return 127
}

func runMatchedCommand(
	ctx context.Context,
	m *Matcher,
	args []string,
	stdinText string,
	input *CommandInput,
	wq chan *CommandOutput,
	lifecycle StdinLifecycle,
) int {
	var cmdCtx context.Context
	var cancel context.CancelFunc
	if m.config.Timeout > 0 {
		cmdCtx, cancel = context.WithTimeout(
			ctx,
			time.Duration(m.config.Timeout)*time.Second,
		)
	} else {
		cmdCtx, cancel = context.WithCancel(ctx)
	}
	defer cancel()

	execCmd := m.runner.CommandContext(cmdCtx, args[0], args[1:]...)
	setSlackContextEnvironment(execCmd, input.ConversationID)
	stdout := newStdWriter(wq, m.config.ReplyConfig, input.ConversationID, input.MessageID, m.config.OutputFlushInterval)
	stderr := newErrWriter(wq, m.config.ReplyConfig, input.ConversationID, input.MessageID, m.config.OutputFlushInterval)
	if m.config.TTY {
		terminal := newTTYOutputNormalizer(stdout)
		if cmd, ok := execCmd.(interface{ SetTTY() }); ok {
			cmd.SetTTY()
		}
		execCmd.SetStdout(terminal)
		execCmd.SetStderr(terminal)
		ret := runWithLifecycleInputWithLineEnding(
			execCmd,
			m.config.Timeout,
			0,
			stdinText,
			input.ConversationID,
			lifecycle,
			"\r",
		)
		_ = terminal.Flush()
		return ret
	}
	execCmd.SetStdout(stdout)
	execCmd.SetStderr(stderr)
	ret := runWithLifecycleInput(execCmd, m.config.Timeout, time.Duration(m.config.StdinIdleTimeout)*time.Second, stdinText, input.ConversationID, lifecycle)
	_ = stdout.Flush()
	_ = stderr.Flush()

	return ret
}

func runWithLifecycleInput(
	command Cmd,
	timeout int,
	idle time.Duration,
	initial string,
	conversation ConversationID,
	lifecycle StdinLifecycle,
) int {
	return runWithLifecycleInputWithLineEnding(command, timeout, idle, initial, conversation, lifecycle, "\n")
}

func runWithLifecycleInputWithLineEnding(
	command Cmd,
	timeout int,
	idle time.Duration,
	initial string,
	conversation ConversationID,
	lifecycle StdinLifecycle,
	lineEnding string,
) int {
	runner, ok := command.(interface {
		RunWithStdin(int, func(io.WriteCloser)) int
	})
	if !ok {
		command.SetStdin(strings.NewReader(initial))
		return command.Run(timeout)
	}
	if lifecycle == nil {
		session := newStdinSession(initial, nil)
		defer session.Close()
		return runner.RunWithStdin(timeout, session.Start)
	}
	onError := func(err error) {
		log.Printf("[WARN] live stdin write failed channel=%s thread=%s: %v", conversation.ChannelID, conversation.RootTimestamp, err)
	}
	endpoint := newInteractiveStdinSessionWithLineEnding(initial, idle, onError, lineEnding)
	endpoint.onClose = func() { lifecycle.StdinClosed(endpoint) }
	defer endpoint.Close()
	return runner.RunWithStdin(timeout, func(stdin io.WriteCloser) {
		endpoint.Start(stdin)
		lifecycle.StdinReady(endpoint)
	})
}

type parsedCommand struct {
	skipIfSucceeded bool
	skipIfFailed    bool
	args            []string
}

func newParsedCommand(op string, args []string) *parsedCommand {
	skipIfSucceeded := false
	skipIfFailed := false
	switch op {
	case "&&":
		skipIfFailed = true
	case "||":
		skipIfSucceeded = true
	}
	return &parsedCommand{
		skipIfSucceeded: skipIfSucceeded,
		skipIfFailed:    skipIfFailed,
		args:            args,
	}
}

func parse(line string) ([]*parsedCommand, error) {
	parser := shellwords.NewParser()
	prevOperator := "" // 「;」相当
	cmds := make([]*parsedCommand, 0)

	for {
		args, err := parser.Parse(line)
		if len(args) == 0 {
			if prevOperator == "" {
				end := 2
				if len(line) < 2 {
					end = len(line)
				}
				prevOperator = string([]rune(line)[0:end])
			}
			err = errors.New("Parse error near `" + prevOperator + "'")
		}
		if err != nil {
			return cmds, err
		}
		cmds = append(cmds, newParsedCommand(prevOperator, args))
		if parser.Position < 0 {
			// 文字列末尾までparseした
			return cmds, nil
		}
		i := parser.Position
		token := line[i:]
		operators := []string{";", "&&", "||"}
		prevOperator = ""
		for _, op := range operators {
			if strings.HasPrefix(token, op) {
				i += len(op)
				prevOperator = op
				break
			}
		}
		// 次のイテレーションでオペレータの次の文字列からparse開始
		// 未対応のオペレータだった場合は次のイテレーションでparse error
		line = string(line[i:])
	}
}

// findMatchedMatcher はマッチャーと構築された引数を返します。
// マッチしない場合は nil, nil を返します。
func findMatchedMatcher(cmd *parsedCommand, matchers []*Matcher) (*Matcher, []string) {
	for _, m := range matchers {
		if args := m.build(cmd.args); len(args) > 0 {
			return m, args
		}
	}
	return nil, nil
}
