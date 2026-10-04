package main

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/hnw/slack-commander/cmd"
	"github.com/hnw/slack-commander/pubsub"
)

func TestRuntimeReplyTreeHasCommandSpecificOutputHandlers(t *testing.T) {
	broadcast := false
	root := &RawCommandConfig{
		RawMatcherConfig: cmd.RawMatcherConfig{Keyword: "root"},
		RawRunnerConfig:  cmd.RawRunnerConfig{Command: "/bin/echo root"},
		Interaction:      cmd.InteractionCommand,
		ReplyConfig:      pubsub.ReplyConfig{Username: "root", IconEmoji: ":robot_face:", ReplyBroadcast: &broadcast, OutputFormat: pubsub.OutputFormatMonospaced},
		Replies: []*RawCommandConfig{
			{RawMatcherConfig: cmd.RawMatcherConfig{Keyword: "reply"}, RawRunnerConfig: cmd.RawRunnerConfig{Command: "/bin/echo reply"}, ReplyConfig: pubsub.ReplyConfig{Username: "reply", IconEmoji: ":ghost:", OutputFormat: pubsub.OutputFormatMarkdown}},
		},
	}
	cfg := validTestConfig(root)
	zero := Duration(0)
	cfg.OutputFlushInterval = &zero
	if err := resolveConfig(cfg); err != nil {
		t.Fatal(err)
	}
	outputs := make(chan *pubsub.CommandOutput, 10)
	commands := buildCommandSet(cfg.commandConfigs, newRunnerFactory(), outputs)
	var accepted *cmd.CommandInput
	dispatcher := newMainTestDispatcher(context.Background(), nil, nil, func(input *cmd.CommandInput) bool { accepted = input; return true })
	router := cmd.NewConversationRouter(&cmd.StdinStore{}, commands, nil, dispatcher, 1)
	for i, text := range []string{"root", "reply"} {
		input := &cmd.CommandInput{Text: text, ConversationID: cmd.ConversationID{ChannelID: "C", RootTimestamp: "1"}, MessageID: cmd.MessageID{ChannelID: "C", Timestamp: []string{"1", "2"}[i]}, AllowedCommandIndexes: []int{i}}
		if result, err := router.Accept(input); err != nil || result != cmd.AcceptRouted || accepted != input {
			t.Fatalf("Accept = %v, %v, accepted=%p", result, err, accepted)
		}
		cmd.NewExecutor().Execute(context.Background(), accepted, nil)
		start, output, finish := <-outputs, <-outputs, <-outputs
		want := cfg.commandConfigs[0].ReplyConfig
		if i == 1 {
			want = cfg.commandConfigs[0].Replies[0].ReplyConfig
		}
		if !start.Spawned || !finish.Finished || finish.ExitCode != 0 || output.Text != text+"\n" || !reflect.DeepEqual(output.ReplyConfig, &want) {
			t.Fatalf("runtime output: start=%#v text=%#v finish=%#v", start, output, finish)
		}
	}
	dispatcher.Close()
	dispatcher.Wait()
}

func TestImplicitStdinReplyGetsOutputHandlerAtConstruction(t *testing.T) {
	broadcast := false
	root := &RawCommandConfig{RawMatcherConfig: cmd.RawMatcherConfig{Keyword: "root"}, RawRunnerConfig: cmd.RawRunnerConfig{Command: "/bin/cat"}, Interaction: cmd.InteractionStdin, ReplyConfig: pubsub.ReplyConfig{Username: "root", ReplyBroadcast: &broadcast}}
	cfg := validTestConfig(root)
	if err := resolveConfig(cfg); err != nil {
		t.Fatal(err)
	}
	outputs := make(chan *pubsub.CommandOutput, 1)
	constructed := 0
	factory := newRunnerFactory()
	_ = buildCommandSet(cfg.commandConfigs, func(config cmd.RunnerConfig) cmd.CommandRunner { constructed++; return factory(config) }, outputs)
	if constructed != 2 {
		t.Fatalf("constructed %d commands, want root and implicit stdin reply", constructed)
	}
	resolved := cfg.commandConfigs[0].Replies[0]
	if !reflect.DeepEqual(resolved.ReplyConfig, root.ReplyConfig) || resolved.OutputFlushInterval != cfg.commandConfigs[0].OutputFlushInterval {
		t.Fatal("implicit reply lost inherited output settings")
	}
	reply := buildCommand(resolved, factory, outputs)
	dispatcher := cmd.NewCommandDispatcher(context.Background(), nil, nil, nil, nil)
	input := &cmd.CommandInput{ConversationID: cmd.ConversationID{ChannelID: "C", RootTimestamp: "1"}, MessageID: cmd.MessageID{Timestamp: "2"}, ResolvedInput: &cmd.ResolvedInput{Commands: []cmd.ResolvedCommand{{Command: reply}}, ParseErr: errors.New("parse failure")}}
	if dispatcher.Dispatch(input) != cmd.DispatchAccepted {
		t.Fatal("parse error rejected")
	}
	dispatcher.Close()
	dispatcher.Wait()
	output := <-outputs
	if output.Text != "parse failure" || !reflect.DeepEqual(output.ReplyConfig, pubsub.NewSystemReplyConfig(&broadcast)) || output.ExitCode != 2 || !output.IsErrOut {
		t.Fatalf("implicit reply system output = %#v", output)
	}
}
