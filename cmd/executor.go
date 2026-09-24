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
	ReplyInfo           interface{} // PubSubの返信に必要な構造体（PubSubの種類ごとにキャストして利用する）
	Text                string      // 起動コマンド平文
	CommandConfigs      []*CommandConfig
	ConversationContext ConversationContext
}

// ConversationContext identifies the Slack thread that receives command output.
type ConversationContext struct {
	ChannelID           string
	RootThreadTimestamp string
}

// CommandOutput はExecutorからの実行結果を引き渡してPubSubに書き出すための構造体
type CommandOutput struct {
	ReplyInfo           interface{}
	ReplyConfig         interface{}
	ConversationContext ConversationContext
	Text                string // コマンドからのテキスト出力（ImageData と排他）
	ImageData           []byte // sixel を変換した PNG バイト列（Text と排他）
	IsErrOut            bool
	Spawned             bool
	Finished            bool
	ExitCode            int
}

// Definition describes a command definition in the configuration.
type Definition struct {
	StdinIdleTimeout int  `toml:"stdin_idle_timeout"`
	TTY              bool `toml:"tty"`
	Timeout          int
	Keyword          string
	Command          string
	Runner           string
	Method           string
	URL              string
	Headers          map[string]string
	Body             string
}

// CommandConfig holds a Definition with reply configuration.
type CommandConfig struct {
	*Definition
	ReplyConfig interface{} //*pubsub.ReplyConfig
	Replies     []*CommandConfig
}

// NewCommandConfig builds a CommandConfig from a definition and reply config.
func NewCommandConfig(def *Definition, replyConfig interface{}) *CommandConfig {
	return &CommandConfig{
		Definition:  def,
		ReplyConfig: replyConfig,
	}
}

// Executor runs commands using the default runner factory.
func Executor(rq chan *CommandInput, wq chan *CommandOutput, cfgs []*CommandConfig) {
	ExecutorWithRunner(context.Background(), rq, wq, cfgs, nil)
}

// RunnerFactory returns a runner for the given command config.
type RunnerFactory func(cfg *CommandConfig) CommandRunner

// ExecutorWithRunner runs commands using runners provided by runnerFactory.
func ExecutorWithRunner(
	ctx context.Context,
	rq chan *CommandInput,
	wq chan *CommandOutput,
	cfgs []*CommandConfig,
	runnerFactory RunnerFactory,
) {
	ExecutorWithThreadInput(ctx, rq, wq, cfgs, runnerFactory, nil)
}

// ExecutorWithThreadInput は実行中の thread 入力先を listener と他の worker に公開する。
func ExecutorWithThreadInput(
	ctx context.Context,
	rq chan *CommandInput,
	wq chan *CommandOutput,
	cfgs []*CommandConfig,
	runnerFactory RunnerFactory,
	registry *ThreadInputRegistry,
) {
	ExecutorWithThreadInputAndLocks(ctx, rq, wq, cfgs, runnerFactory, registry, nil)
}

// ExecutorWithThreadInputAndLocks serializes new commands from the same Slack thread.
func ExecutorWithThreadInputAndLocks(
	ctx context.Context,
	rq chan *CommandInput,
	wq chan *CommandOutput,
	cfgs []*CommandConfig,
	runnerFactory RunnerFactory,
	registry *ThreadInputRegistry,
	threadLocks *ThreadLocks,
) {
	runnerFactory = normalizeRunnerFactory(runnerFactory)
	matchers := buildMatchers(cfgs, runnerFactory)

	if ctx == nil {
		ctx = context.Background()
	}
	for {
		select {
		case <-ctx.Done():
			return
		case input, ok := <-rq:
			if !ok {
				return
			}
			inputMatchers := matchers
			if input.CommandConfigs != nil {
				inputMatchers = buildMatchers(input.CommandConfigs, runnerFactory)
			}
			cmdMsg, stdinText := splitCommandInput(input.Text)
			cmds, parseErr := parseCommands(cmdMsg)

			executeCommandsWithThreadLock(
				ctx,
				cmds,
				parseErr,
				stdinText,
				input,
				inputMatchers,
				wq,
				registry,
				threadLocks,
			)

		}
	}
}

// MatchSingleCommand returns the configured command matching one complete input command.
// Chained or malformed inputs have no owner for thread reply routing.
func MatchSingleCommand(text string, cfgs []*CommandConfig) *CommandConfig {
	cmdMsg, _ := splitCommandInput(text)
	cmds, err := parseCommands(cmdMsg)
	if err != nil || len(cmds) != 1 {
		return nil
	}
	for _, cfg := range cfgs {
		matcher := newMatcher(cfg)
		if matcher == nil {
			continue
		}
		if args := matcher.build(cmds[0].args); len(args) > 0 {
			return cfg
		}
	}
	return nil
}

func executeCommandsWithThreadLock(
	ctx context.Context,
	cmds []*parsedCommand,
	parseErr error,
	stdinText string,
	input *CommandInput,
	matchers []*Matcher,
	wq chan *CommandOutput,
	registry *ThreadInputRegistry,
	threadLocks *ThreadLocks,
) {
	unlock := threadLocks.Lock(input.ConversationContext)
	defer unlock()
	_ = executeCommands(ctx, cmds, parseErr, stdinText, input, matchers, wq, registry)
}

func normalizeRunnerFactory(runnerFactory RunnerFactory) RunnerFactory {
	if runnerFactory != nil {
		return runnerFactory
	}
	return func(*CommandConfig) CommandRunner {
		return NewExecRunner()
	}
}

func buildMatchers(cfgs []*CommandConfig, runnerFactory RunnerFactory) []*Matcher {
	matchers := make([]*Matcher, 0, len(cfgs))
	for _, cfg := range cfgs {
		matcher := newMatcher(cfg)
		if matcher == nil {
			continue
		}
		runner := runnerFactory(cfg)
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
	input *CommandInput,
	matchers []*Matcher,
	wq chan *CommandOutput,
	registry *ThreadInputRegistry,
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
				ReplyInfo:           input.ReplyInfo,
				ConversationContext: input.ConversationContext,
				Spawned:             true,
			}
			// 関数を抜ける時に必ず終了通知を送る
			defer func() {
				wq <- &CommandOutput{
					ReplyInfo:           input.ReplyInfo,
					ConversationContext: input.ConversationContext,
					Finished:            true,
					ExitCode:            ret,
				}
			}()
		}
		if parseErr != nil {
			// parse errorありで1つ目のコマンドがキーワードマッチした場合
			// エラー表示して処理全体を終了
			ret = writeParseError(wq, input, parseErr)
			return ret
		}
		ret = runMatchedCommand(ctx, m, args, stdinText, input, wq, registry)
	}
	return ret
}

func shouldSkipCommand(cmd *parsedCommand, ret int) bool {
	if ret == 0 && cmd.skipIfSucceeded {
		return true
	}
	return ret != 0 && cmd.skipIfFailed
}

func writeParseError(wq chan *CommandOutput, input *CommandInput, parseErr error) int {
	syserr := newErrWriter(wq, input.ReplyInfo, nil, input.ConversationContext)
	_, _ = fmt.Fprintf(syserr, "%v", parseErr)
	_ = syserr.Flush()
	return 2
}

func writeCommandNotFound(wq chan *CommandOutput, input *CommandInput, cmd *parsedCommand) int {
	syserr := newErrWriter(wq, input.ReplyInfo, nil, input.ConversationContext)
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
	registry *ThreadInputRegistry,
) int {
	var cmdCtx context.Context
	var cancel context.CancelFunc
	if m.cfg.Timeout > 0 {
		cmdCtx, cancel = context.WithTimeout(
			ctx,
			time.Duration(m.cfg.Timeout)*time.Second,
		)
	} else {
		cmdCtx, cancel = context.WithCancel(ctx)
	}
	defer cancel()

	execCmd := m.runner.CommandContext(cmdCtx, args[0], args[1:]...)
	setSlackContextEnvironment(execCmd, input.ConversationContext)
	stdout := newStdWriter(wq, input.ReplyInfo, m.cfg.ReplyConfig, input.ConversationContext)
	stderr := newErrWriter(wq, input.ReplyInfo, m.cfg.ReplyConfig, input.ConversationContext)
	if m.cfg.TTY {
		terminal := newTTYOutputNormalizer(stdout)
		if cmd, ok := execCmd.(interface{ SetTTY() }); ok {
			cmd.SetTTY()
		}
		execCmd.SetStdout(terminal)
		execCmd.SetStderr(terminal)
		ret := runWithInputWithLineEnding(
			execCmd,
			m.cfg.Timeout,
			0,
			stdinText,
			input.ConversationContext,
			registry,
			"\r",
		)
		_ = terminal.Flush()
		return ret
	}
	execCmd.SetStdout(stdout)
	execCmd.SetStderr(stderr)
	ret := runWithInput(execCmd, m.cfg.Timeout, time.Duration(m.cfg.StdinIdleTimeout)*time.Second,
		stdinText, input.ConversationContext, registry)
	_ = stdout.Flush()
	_ = stderr.Flush()

	return ret
}

func runWithInput(
	command Cmd,
	timeout int,
	idle time.Duration,
	initial string,
	conversation ConversationContext,
	registry *ThreadInputRegistry,
) int {
	return runWithInputWithLineEnding(command, timeout, idle, initial, conversation, registry, "\n")
}

func runWithInputWithLineEnding(
	command Cmd,
	timeout int,
	idle time.Duration,
	initial string,
	conversation ConversationContext,
	registry *ThreadInputRegistry,
	lineEnding string,
) int {
	runner, ok := command.(interface {
		RunWithStdin(int, func(io.WriteCloser)) int
	})
	if !ok {
		command.SetStdin(strings.NewReader(initial))
		return command.Run(timeout)
	}
	if registry == nil || conversation.ChannelID == "" || conversation.RootThreadTimestamp == "" {
		session := newStdinSession(initial, nil)
		defer session.Close()
		return runner.RunWithStdin(timeout, session.Start)
	}
	//nolint:staticcheck // ConversationContext に項目が増えても thread 識別子の2項目だけを使う。
	key := ThreadKey{
		ChannelID:           conversation.ChannelID,
		RootThreadTimestamp: conversation.RootThreadTimestamp,
	}
	onError := func(err error) {
		log.Printf(
			"[WARN] live stdin write failed channel=%s thread=%s: %v",
			key.ChannelID,
			key.RootThreadTimestamp,
			err,
		)
	}
	endpoint := newInteractiveStdinSessionWithLineEnding(initial, idle, onError, lineEnding)
	endpoint.onClose = func() { registry.Unregister(key, endpoint) }
	defer endpoint.Close()
	return runner.RunWithStdin(timeout, func(stdin io.WriteCloser) {
		endpoint.Start(stdin)
		registry.Register(key, endpoint)
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
