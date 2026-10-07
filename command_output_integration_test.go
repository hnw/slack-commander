package main

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"testing"
	"time"

	"github.com/slack-go/slack"
	"github.com/slack-go/slack/socketmode"

	"github.com/hnw/slack-commander/cmd"
	"github.com/hnw/slack-commander/pubsub"
)

func TestRuntimeReplyTreeHasCommandSpecificOutput(t *testing.T) {
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
	outputs := pubsub.NewSlackOutput(10)
	posts := captureSlackOutput(t, outputs)
	commands := buildCommandSet(cfg.commandConfigs, newRunnerFactory(), outputs.NewCommandOutput)
	var accepted *cmd.CommandInput
	dispatcher := newMainTestDispatcher(context.Background(), nil, nil, func(input *cmd.CommandInput) bool { accepted = input; return true })
	router := cmd.NewConversationRouter(&cmd.StdinStore{}, commands, nil, dispatcher, 1)
	for i, text := range []string{"root", "reply"} {
		input := &cmd.CommandInput{Text: text, ConversationID: cmd.ConversationID{ChannelID: "C", RootTimestamp: "1"}, MessageID: cmd.MessageID{ChannelID: "C", Timestamp: []string{"1", "2"}[i]}, AllowedCommandIndexes: []int{i}}
		if result, err := router.Accept(context.Background(), input); err != nil || result != cmd.AcceptRouted || accepted != input {
			t.Fatalf("Accept = %v, %v, accepted=%p", result, err, accepted)
		}
		cmd.NewExecutor().Execute(context.Background(), accepted, nil)
		post := receiveSlackPost(t, posts)
		want := cfg.commandConfigs[0].ReplyConfig
		if i == 1 {
			want = cfg.commandConfigs[0].Replies[0].ReplyConfig
		}
		assertSlackCommandPost(t, post, want, text+"\n")
	}
	dispatcher.Close()
	dispatcher.Wait()
}

func TestImplicitStdinReplyGetsOutputAtConstruction(t *testing.T) {
	broadcast := false
	root := &RawCommandConfig{RawMatcherConfig: cmd.RawMatcherConfig{Keyword: "root"}, RawRunnerConfig: cmd.RawRunnerConfig{Command: "/bin/cat"}, Interaction: cmd.InteractionStdin, ReplyConfig: pubsub.ReplyConfig{Username: "root", ReplyBroadcast: &broadcast}}
	cfg := validTestConfig(root)
	if err := resolveConfig(cfg); err != nil {
		t.Fatal(err)
	}
	outputs := pubsub.NewSlackOutput(1)
	posts := captureSlackOutput(t, outputs)
	constructed := 0
	var outputConfigs []pubsub.ReplyConfig
	var intervals []time.Duration
	newOutput := func(reply pubsub.ReplyConfig, interval time.Duration) cmd.CommandOutput {
		outputConfigs = append(outputConfigs, reply)
		intervals = append(intervals, interval)
		return outputs.NewCommandOutput(reply, interval)
	}
	factory := newRunnerFactory()
	_ = buildCommandSet(cfg.commandConfigs, func(config cmd.RunnerConfig) cmd.CommandRunner { constructed++; return factory(config) }, newOutput)
	if constructed != 2 {
		t.Fatalf("constructed %d commands, want root and implicit stdin reply", constructed)
	}
	if !reflect.DeepEqual(outputConfigs, []pubsub.ReplyConfig{root.ReplyConfig, root.ReplyConfig}) || !reflect.DeepEqual(intervals, []time.Duration{cfg.commandConfigs[0].OutputFlushInterval, cfg.commandConfigs[0].OutputFlushInterval}) {
		t.Fatal("command outputs lost inherited settings")
	}
	resolved := cfg.commandConfigs[0].Replies[0]
	if !reflect.DeepEqual(resolved.ReplyConfig, root.ReplyConfig) || resolved.OutputFlushInterval != cfg.commandConfigs[0].OutputFlushInterval {
		t.Fatal("implicit reply lost inherited output settings")
	}
	reply := buildCommand(resolved, factory, outputs.NewCommandOutput)
	dispatcher := cmd.NewCommandDispatcher(context.Background(), nil, nil, nil, nil)
	input := &cmd.CommandInput{ConversationID: cmd.ConversationID{ChannelID: "C", RootTimestamp: "1"}, MessageID: cmd.MessageID{Timestamp: "2"}, ResolvedInput: &cmd.ResolvedInput{Commands: []cmd.ResolvedCommand{{Command: reply}}, ParseErr: errors.New("parse failure")}}
	if dispatcher.Dispatch(input) != cmd.DispatchAccepted {
		t.Fatal("parse error rejected")
	}
	dispatcher.Close()
	dispatcher.Wait()
	post := receiveSlackPost(t, posts)
	var attachments []slack.Attachment
	if err := json.Unmarshal([]byte(post.Get("attachments")), &attachments); err != nil {
		t.Fatal(err)
	}
	if post.Get("username") != pubsub.NewSystemReplyConfig(&broadcast).Username || len(attachments) != 1 || attachments[0].Text != "parse failure" || attachments[0].Color != "#E01E5A" || post.Get("reply_broadcast") != "" {
		t.Fatalf("implicit reply system post = %#v, attachments = %#v", post, attachments)
	}
}

func captureSlackOutput(t *testing.T, output *pubsub.SlackOutput) <-chan url.Values {
	t.Helper()
	posts := make(chan url.Values, 10)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseForm(); err != nil {
			t.Error(err)
		}
		if r.URL.Path == "/chat.postMessage" {
			posts <- r.Form
		}
		_, _ = io.WriteString(w, `{"ok":true,"channel":"C","ts":"3"}`)
	}))
	smc := socketmode.New(slack.New("token", slack.OptionAPIURL(server.URL+"/")))
	done := make(chan struct{})
	go func() { output.Run(context.Background(), smc); close(done) }()
	t.Cleanup(func() { output.Close(); <-done; server.Close() })
	return posts
}

func receiveSlackPost(t *testing.T, posts <-chan url.Values) url.Values {
	t.Helper()
	select {
	case post := <-posts:
		return post
	case <-time.After(3 * time.Second):
		t.Fatal("Slack output was not posted")
		return nil
	}
}

func assertSlackCommandPost(t *testing.T, post url.Values, config pubsub.ReplyConfig, text string) {
	t.Helper()
	if post.Get("username") != config.Username || post.Get("icon_emoji") != config.IconEmoji || post.Get("channel") != "C" || post.Get("thread_ts") != "1" || post.Get("reply_broadcast") != "" {
		t.Fatalf("Slack post = %#v", post)
	}
	var attachments []slack.Attachment
	if err := json.Unmarshal([]byte(post.Get("attachments")), &attachments); err != nil {
		t.Fatal(err)
	}
	if len(attachments) != 1 {
		t.Fatalf("attachments = %#v", attachments)
	}
	assertSlackAttachmentText(t, attachments[0], config.OutputFormat, text)
}

func assertSlackAttachmentText(t *testing.T, attachment slack.Attachment, format string, text string) {
	t.Helper()
	if format == pubsub.OutputFormatMarkdown {
		if len(attachment.Blocks.BlockSet) != 1 {
			t.Fatalf("missing markdown block: %#v", attachment)
		}
		block, ok := attachment.Blocks.BlockSet[0].(*slack.MarkdownBlock)
		if !ok || block.Type != slack.MBTMarkdown || block.Text != text {
			t.Fatalf("markdown block = %#v", block)
		}
		return
	}
	if attachment.Text != "```"+text+"```" {
		t.Fatalf("attachment text = %q", attachment.Text)
	}
}
