// Package main is the entry point for slack-commander.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"sync"
	"syscall"

	"github.com/slack-go/slack"
	"github.com/slack-go/slack/socketmode"

	"github.com/hnw/slack-commander/cmd"
	"github.com/hnw/slack-commander/pubsub"
)

var version = "dev"

func main() {
	os.Exit(run(os.Args[1:]))
}

func run(args []string) int {
	flags := flag.NewFlagSet("slack-commander", flag.ContinueOnError)
	flags.SetOutput(os.Stderr)
	quiet := flags.Bool("q", false, "Quiet mode")
	configFile := flags.String("config-file", "config.toml", "Specify configuration file")
	verbose := flags.Bool("v", false, "Verbose mode")
	debug := flags.Bool("debug", false, "Debug mode") // slack-go/slackのdebug mode
	checkConfig := flags.Bool("check-config", false, "Validate the configuration file and exit")
	showVersion := flags.Bool("version", false, "Print version and exit")
	err := flags.Parse(args)
	if errors.Is(err, flag.ErrHelp) {
		return 0
	}
	if err != nil {
		return 2
	}
	if *showVersion {
		fmt.Printf("slack-commander %s\n", version)
		return 0
	}

	cfg, err := loadConfig(*configFile)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	if *checkConfig {
		return 0
	}

	var level slog.LevelVar
	level.Set(slog.LevelWarn)
	if *verbose {
		level.Set(slog.LevelInfo)
	}
	if *debug {
		level.Set(slog.LevelDebug)
	}
	if *quiet {
		level.Set(slog.LevelError)
	}

	handler := slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{
		Level: &level,
	})
	logger := slog.New(handler)
	stdLogger := slog.NewLogLogger(handler, slog.LevelDebug)

	stdinStore := &cmd.StdinStore{}
	runnerFactory := newRunnerFactory()

	api := slack.New(
		cfg.SlackBotToken,
		slack.OptionDebug(*debug),
		slack.OptionLog(stdLogger),
		slack.OptionAppLevelToken(cfg.SlackAppToken),
	)
	smc := socketmode.New(
		api,
		socketmode.OptionDebug(*debug),
		socketmode.OptionLog(stdLogger),
	)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	// チャンネルの容量を大きめに取る。本来cfg.NumWorkersで問題ないはずだが、
	// ack返せない問題への暫定対処。
	commandQueue := make(chan *cmd.CommandInput, 50)
	outputQueue := make(chan *pubsub.CommandOutput, cfg.NumWorkers)
	commands := buildCommandSet(cfg.commandConfigs, runnerFactory, outputQueue)
	conversationLocks := &cmd.ConversationLocks{}
	executor := cmd.NewExecutor()
	dispatcher := cmd.NewCommandDispatcher(ctx, executor, stdinStore, conversationLocks, func(input *cmd.CommandInput) bool {
		select {
		case commandQueue <- input:
			return true
		default:
			return false
		}
	})
	router := cmd.NewConversationRouter(
		stdinStore,
		commands,
		pubsub.SlackRootInputResolver(smc, cfg.PubSubConfig),
		dispatcher,
		4096,
	)
	var executorWG sync.WaitGroup
	startWorkers(ctx, cfg.NumWorkers, commandQueue, stdinStore, conversationLocks, executor, &executorWG)
	var writerWG sync.WaitGroup
	writerWG.Add(1)
	go func() {
		defer writerWG.Done()
		pubsub.SlackWriter(ctx, smc, outputQueue)
	}()
	var listenerWG sync.WaitGroup
	var listenerErr error
	listenerWG.Add(1)
	go func() {
		defer listenerWG.Done()
		listenerErr = pubsub.SlackListener(
			ctx,
			smc,
			cfg.PubSubConfig,
			router,
		)
		if listenerErr != nil {
			stop()
		}
	}()

	exitCode := 0
	if err := smc.RunContext(ctx); err != nil && !errors.Is(err, context.Canceled) {
		logger.Error("Socket Mode error", "error", err)
		exitCode = 1
	}
	stop()
	listenerWG.Wait()
	if listenerErr != nil {
		logger.Error("Slack listener error", "error", listenerErr)
		exitCode = 1
	}
	dispatcher.Close()
	close(commandQueue)
	executorWG.Wait()
	dispatcher.Wait()
	close(outputQueue)
	writerWG.Wait()
	return exitCode
}

func newRunnerFactory() cmd.RunnerFactory {
	execRunner := cmd.NewExecRunner()
	composeRunner := cmd.NewComposeRunner("")
	return func(config cmd.RunnerConfig) cmd.CommandRunner {
		if config.Runner == cmd.RunnerStdinReply {
			return cmd.NewStdinReplyRunner()
		}
		if config.Runner == cmd.RunnerCompose {
			return composeRunner
		}
		if config.Runner == cmd.RunnerHTTP {
			return cmd.NewHTTPRunner(config)
		}
		return execRunner
	}
}

func startWorkers(
	ctx context.Context,
	workers int,
	inputs <-chan *cmd.CommandInput,
	stdinStore *cmd.StdinStore,
	conversationLocks *cmd.ConversationLocks,
	executor *cmd.Executor,
	wg *sync.WaitGroup,
) {
	for range workers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-ctx.Done():
					return
				case input, ok := <-inputs:
					if !ok {
						return
					}
					func() {
						unlock := conversationLocks.Lock(input.ConversationID)
						defer unlock()
						executor.Execute(ctx, input, stdinStore.Lifecycle(input.ConversationID))
					}()
				}
			}
		}()
	}
}
