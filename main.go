// Package main is the entry point for slack-commander.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"sync"
	"syscall"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"

	"github.com/slack-go/slack"
	"github.com/slack-go/slack/socketmode"

	"github.com/hnw/slack-commander/cmd"
	"github.com/hnw/slack-commander/pubsub"
)

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
	err := flags.Parse(args)
	if errors.Is(err, flag.ErrHelp) {
		return 0
	}
	if err != nil {
		return 2
	}

	cfg, err := loadConfig(*configFile)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	if *checkConfig {
		return 0
	}

	zapCfg := zap.NewDevelopmentConfig()
	zapCfg.DisableStacktrace = true
	zapCfg.EncoderConfig.EncodeTime = zapcore.RFC3339NanoTimeEncoder
	zapCfg.EncoderConfig.EncodeDuration = zapcore.SecondsDurationEncoder
	zapCfg.Level.SetLevel(zapcore.WarnLevel)
	if *verbose {
		zapCfg.Level.SetLevel(zapcore.InfoLevel)
	}
	if *debug {
		zapCfg.Level.SetLevel(zapcore.DebugLevel)
	}
	if *quiet {
		zapCfg.Level.SetLevel(zapcore.ErrorLevel)
	}

	logger, err := zapCfg.Build()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	defer func() {
		_ = logger.Sync()
	}()
	sugar := logger.Sugar()
	stdLogger, err := zap.NewStdLogAt(logger, zapcore.DebugLevel)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}

	cmdConfig := commandConfigs(cfg.Commands)

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
	outputQueue := make(chan *cmd.CommandOutput, cfg.NumWorkers)
	var threadInputs cmd.ThreadInputRegistry
	threadRoutes := cmd.NewThreadRouteCache(4096)
	var threadLocks cmd.ThreadLocks
	var composeRunnerOnce sync.Once
	var composeRunner cmd.CommandRunner
	runnerFactory := func(cfg *cmd.CommandConfig) cmd.CommandRunner {
		if cfg.Runner == cmd.RunnerCompose {
			composeRunnerOnce.Do(func() {
				composeRunner = cmd.NewComposeRunner("")
			})
			return composeRunner
		}
		if cfg.Runner == cmd.RunnerHTTP {
			return cmd.NewHTTPRunner(cfg)
		}
		return cmd.NewExecRunner()
	}
	var executorWG sync.WaitGroup
	for i := 0; i < cfg.NumWorkers; i++ {
		executorWG.Add(1)
		go func() {
			defer executorWG.Done()
			cmd.ExecutorWithThreadInputAndLocks(
				ctx,
				commandQueue,
				outputQueue,
				cmdConfig,
				runnerFactory,
				&threadInputs,
				&threadLocks,
			)
		}()
	}
	var writerWG sync.WaitGroup
	writerWG.Add(1)
	go func() {
		defer writerWG.Done()
		pubsub.SlackWriter(ctx, smc, outputQueue)
	}()
	var listenerWG sync.WaitGroup
	listenerWG.Add(1)
	go func() {
		defer listenerWG.Done()
		pubsub.SlackListener(
			ctx,
			smc,
			commandQueue,
			cfg.PubSubConfig,
			&threadInputs,
			cmdConfig,
			threadRoutes,
		)
	}()

	exitCode := 0
	if err := smc.RunContext(ctx); err != nil && !errors.Is(err, context.Canceled) {
		sugar.Errorf("Socket Mode error: %v", err)
		exitCode = 1
	}
	stop()
	listenerWG.Wait()
	close(commandQueue)
	executorWG.Wait()
	close(outputQueue)
	writerWG.Wait()
	return exitCode
}
