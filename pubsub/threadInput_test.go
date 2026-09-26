package pubsub

import (
	"bufio"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/hnw/slack-commander/cmd"
	"github.com/slack-go/slack"
	"github.com/slack-go/slack/slackevents"
	"github.com/slack-go/slack/socketmode"
)

func TestShouldIgnoreMessageEventPreservesBotAndReminderHandling(t *testing.T) {
	previousUserID := userID
	userID = "U-self"
	t.Cleanup(func() { userID = previousUserID })

	if shouldIgnoreMessageEvent(&slackevents.MessageEvent{SubType: slack.MsgSubTypeBotMessage, User: "U-other"}, Config{AcceptBotMessage: true}) {
		t.Fatal("accepted bot message was ignored")
	}
	if !shouldIgnoreMessageEvent(&slackevents.MessageEvent{SubType: slack.MsgSubTypeBotMessage, User: "U-other"}, Config{}) {
		t.Fatal("bot message ignored accept_bot_message=false was accepted")
	}
	if !shouldIgnoreMessageEvent(&slackevents.MessageEvent{SubType: slack.MsgSubTypeMessageChanged}, Config{}) {
		t.Fatal("message_changed was accepted")
	}
	if !shouldIgnoreMessageEvent(&slackevents.MessageEvent{SubType: slack.MsgSubTypeMessageDeleted}, Config{}) {
		t.Fatal("message_deleted was accepted")
	}
	if shouldIgnoreMessageEvent(&slackevents.MessageEvent{SubType: slack.MsgSubTypeMeMessage}, Config{}) {
		t.Fatal("non-edit subtype was newly ignored")
	}
	if shouldIgnoreMessageEvent(&slackevents.MessageEvent{User: "USLACKBOT", Text: "Reminder: todo"}, Config{AcceptReminder: true}) {
		t.Fatal("accepted reminder was ignored")
	}
}

func TestRootQueueFullDoesNotRegisterRoute(t *testing.T) {
	root := cmd.NewCommandConfig(&cmd.Definition{Keyword: "todo", Command: "todo-wrapper"}, nil)
	tests := []struct {
		name   string
		handle func(*socketmode.Client, chan *cmd.CommandInput, *cmd.ThreadRouteCache, []*cmd.CommandConfig)
		key    cmd.ThreadKey
	}{
		{
			name: "message", key: cmd.ThreadKey{ChannelID: "C", RootThreadTimestamp: "1"},
			handle: func(smc *socketmode.Client, queue chan *cmd.CommandInput, cache *cmd.ThreadRouteCache, commands []*cmd.CommandConfig) {
				onMessageEvent(smc, &slackevents.MessageEvent{Channel: "C", User: "U", TimeStamp: "1", Text: "todo"}, queue, Config{AllowedUserIDs: []string{"U"}, AllowedChannelIDs: []string{"C"}}, nil, commands, cache)
			},
		},
		{
			name: "app mention", key: cmd.ThreadKey{ChannelID: "C", RootThreadTimestamp: "2"},
			handle: func(smc *socketmode.Client, queue chan *cmd.CommandInput, cache *cmd.ThreadRouteCache, commands []*cmd.CommandConfig) {
				onAppMentionEvent(smc, &slackevents.AppMentionEvent{Channel: "C", User: "U", TimeStamp: "2", Text: "todo"}, queue, Config{AllowedUserIDs: []string{"U"}, AllowedChannelIDs: []string{"C"}}, nil, commands, cache)
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			queue := make(chan *cmd.CommandInput, 1)
			queue <- &cmd.CommandInput{}
			cache := cmd.NewThreadRouteCache(1)
			tt.handle(socketmode.New(slack.New("test")), queue, cache, []*cmd.CommandConfig{root})
			if _, ok := cache.Lookup(tt.key); ok {
				t.Fatal("queue-dropped root route was cached")
			}
		})
	}
}

func TestThreadReplyUsesOnlyRootCommandReplyRules(t *testing.T) {
	lookups := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		lookups++
		if r.URL.Path != "/conversations.replies" {
			t.Fatalf("path = %q", r.URL.Path)
		}
		_, _ = w.Write([]byte(`{"ok":true,"messages":[{"ts":"1","text":"todo <https://example.com|item>"}]}`))
	}))
	defer server.Close()
	smc := socketmode.New(slack.New("test", slack.OptionAPIURL(server.URL+"/")))
	root := cmd.NewCommandConfig(&cmd.Definition{Keyword: "todo *", Command: "todo-wrapper *"}, nil)
	root.Interaction = cmd.InteractionCommand
	reply := cmd.NewCommandConfig(&cmd.Definition{Keyword: "cancel", Command: "todo-wrapper --cancel"}, nil)
	root.Replies = []*cmd.CommandConfig{reply}
	queue := make(chan *cmd.CommandInput, 1)
	onMessageEvent(smc, &slackevents.MessageEvent{
		Channel: "C", User: "U", TimeStamp: "2", ThreadTimeStamp: "1", Text: "<@BOT> “cancel”",
	}, queue, Config{AllowedUserIDs: []string{"U"}, AllowedChannelIDs: []string{"C"}}, nil, []*cmd.CommandConfig{root}, nil)
	select {
	case input := <-queue:
		if input.Text != ` "cancel"` || len(input.CommandConfigs) != 1 || input.CommandConfigs[0] != reply {
			t.Fatalf("input = %+v", input)
		}
	case <-time.After(time.Second):
		t.Fatal("reply command was not queued")
	}
	if lookups != 1 {
		t.Fatalf("lookups = %d, want 1", lookups)
	}
}

func TestThreadReplyReconstructsReminderRootWithNormalTextRules(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"ok":true,"messages":[{"ts":"1","user":"USLACKBOT","text":"Reminder: todo <https://example.com|item>."}]}`))
	}))
	defer server.Close()
	smc := socketmode.New(slack.New("test", slack.OptionAPIURL(server.URL+"/")))
	root := cmd.NewCommandConfig(&cmd.Definition{Keyword: "todo *", Command: "todo-wrapper *"}, nil)
	root.Interaction = cmd.InteractionCommand
	reply := cmd.NewCommandConfig(&cmd.Definition{Keyword: "cancel", Command: "todo-wrapper --cancel"}, nil)
	root.Replies = []*cmd.CommandConfig{reply}
	configs := []*cmd.CommandConfig{root}
	normalText := normalizeCommandText(extractMessageText(&slackevents.MessageEvent{
		User: "USLACKBOT", Text: "Reminder: todo <https://example.com|item>.",
	}))
	reconstructedText := normalizeCommandText(rootMessageText(&slack.Message{Msg: slack.Msg{
		User: "USLACKBOT", Text: "Reminder: todo <https://example.com|item>.",
	}}))
	if got := cmd.MatchSingleCommand(normalText, configs); got != root {
		t.Fatalf("normal route = %v, want root", got)
	}
	if got := cmd.MatchSingleCommand(reconstructedText, configs); got != root {
		t.Fatalf("reconstructed route = %v, want root", got)
	}
	queue := make(chan *cmd.CommandInput, 1)
	routeThreadReply(smc, &cmd.CommandInput{Text: "cancel", ConversationContext: cmd.ConversationContext{ChannelID: "C", RootThreadTimestamp: "1"}}, queue, nil, configs, cmd.NewThreadRouteCache(1))
	select {
	case input := <-queue:
		if len(input.CommandConfigs) != 1 || input.CommandConfigs[0] != reply {
			t.Fatalf("input = %+v", input)
		}
	case <-time.After(time.Second):
		t.Fatal("reply command was not queued")
	}
}

func TestThreadReplyIgnoresCommandChainAndMessageEdits(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"ok":true,"messages":[{"ts":"1","text":"todo one && todo two"}]}`))
	}))
	defer server.Close()
	smc := socketmode.New(slack.New("test", slack.OptionAPIURL(server.URL+"/")))
	root := cmd.NewCommandConfig(&cmd.Definition{Keyword: "todo *", Command: "todo-wrapper *"}, nil)
	root.Interaction = cmd.InteractionCommand
	root.Replies = []*cmd.CommandConfig{cmd.NewCommandConfig(&cmd.Definition{Keyword: "cancel", Command: "todo-wrapper --cancel"}, nil)}
	queue := make(chan *cmd.CommandInput, 1)
	cfg := Config{AllowedUserIDs: []string{"U"}, AllowedChannelIDs: []string{"C"}}
	onMessageEvent(smc, &slackevents.MessageEvent{
		Channel: "C", User: "U", TimeStamp: "2", ThreadTimeStamp: "1", Text: "cancel",
	}, queue, cfg, nil, []*cmd.CommandConfig{root}, nil)
	onMessageEvent(smc, &slackevents.MessageEvent{
		Channel: "C", User: "U", TimeStamp: "3", Text: "todo item", SubType: slack.MsgSubTypeMessageChanged,
	}, queue, cfg, nil, []*cmd.CommandConfig{root}, nil)
	select {
	case input := <-queue:
		t.Fatalf("unexpected input = %+v", input)
	default:
	}
}

func TestThreadReplyRouteCacheAvoidsLookupForPositiveAndNegativeResults(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) {
		t.Fatal("cached route unexpectedly called Slack")
	}))
	defer server.Close()
	smc := socketmode.New(slack.New("test", slack.OptionAPIURL(server.URL+"/")))
	root := cmd.NewCommandConfig(&cmd.Definition{Keyword: "todo", Command: "todo-wrapper"}, nil)
	root.Interaction = cmd.InteractionCommand
	reply := cmd.NewCommandConfig(&cmd.Definition{Keyword: "cancel", Command: "todo-wrapper --cancel"}, nil)
	root.Replies = []*cmd.CommandConfig{reply}
	cache := cmd.NewThreadRouteCache(2)
	positive := cmd.ConversationContext{ChannelID: "C", RootThreadTimestamp: "1"}
	negative := cmd.ConversationContext{ChannelID: "C", RootThreadTimestamp: "2"}
	cache.Store(positive.ThreadKey(), root)
	cache.Store(negative.ThreadKey(), nil)
	queue := make(chan *cmd.CommandInput, 1)
	routeThreadReply(smc, &cmd.CommandInput{Text: "cancel", ConversationContext: positive}, queue, nil, []*cmd.CommandConfig{root}, cache)
	routeThreadReply(smc, &cmd.CommandInput{Text: "cancel", ConversationContext: negative}, queue, nil, []*cmd.CommandConfig{root}, cache)
	if input := <-queue; len(input.CommandConfigs) != 1 || input.CommandConfigs[0] != reply {
		t.Fatalf("input = %+v", input)
	}
	select {
	case input := <-queue:
		t.Fatalf("negative route queued input = %+v", input)
	default:
	}
}

func TestThreadReplyLookupErrorIsNotCached(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()
	smc := socketmode.New(slack.New("test", slack.OptionAPIURL(server.URL+"/")))
	cache := cmd.NewThreadRouteCache(1)
	context := cmd.ConversationContext{ChannelID: "C", RootThreadTimestamp: "1"}
	routeThreadReply(smc, &cmd.CommandInput{Text: "cancel", ConversationContext: context}, make(chan *cmd.CommandInput, 1), nil, nil, cache)
	if _, ok := cache.Lookup(context.ThreadKey()); ok {
		t.Fatal("Slack lookup error was cached")
	}
}

func TestThreadInputRoutesRawTextWithoutFallback(t *testing.T) {
	for _, mention := range []bool{false, true} {
		var registry cmd.ThreadInputRegistry
		r, w := io.Pipe()
		endpoint := cmd.NewInteractiveStdin(w, "initial", func(err error) { t.Error(err) })
		key := cmd.ThreadKey{ChannelID: "C", RootThreadTimestamp: "1"}
		registry.Register(key, endpoint)
		queue := make(chan *cmd.CommandInput, 10)
		root := cmd.NewCommandConfig(&cmd.Definition{Keyword: "agent", Command: "cat"}, nil)
		root.Interaction = cmd.InteractionStdin
		commands := []*cmd.CommandConfig{root}
		routeCache := cmd.NewThreadRouteCache(1)
		routeCache.Store(key, root)
		smc := socketmode.New(slack.New("test"))
		cfg := Config{AllowedUserIDs: []string{"U"}, AllowedChannelIDs: []string{"C"}}
		text := "<@BOT> “hello” &amp; <https://example.com|label>"
		send := func(text, user string) {
			if mention {
				onAppMentionEvent(
					smc,
					&slackevents.AppMentionEvent{
						Channel:         "C",
						User:            user,
						ThreadTimeStamp: "1",
						Text:            text,
					},
					queue,
					cfg,
					&registry, commands, routeCache,
				)
			} else {
				onMessageEvent(
					smc,
					&slackevents.MessageEvent{
						Channel:         "C",
						User:            user,
						ThreadTimeStamp: "1",
						Text:            text,
					},
					queue,
					cfg,
					&registry, commands, routeCache,
				)
			}
		}
		send("unauthorized", "other")
		send(text, "U")
		send("dropped", "U")
		reader := bufio.NewReader(r)
		for _, want := range []string{"initial\n", text + "\n"} {
			got, err := reader.ReadString('\n')
			if err != nil || got != want {
				t.Fatalf("got=%q err=%v", got, err)
			}
		}
		endpoint.Close()
		send("closed", "U")
		_ = r.Close()
		if len(queue) != 0 {
			t.Fatal("live reply entered command queue")
		}
	}
}

func TestThreadReplyInteractionRouting(t *testing.T) {
	newRoot := func(interaction cmd.Interaction) (*cmd.CommandConfig, *cmd.CommandConfig) {
		root := cmd.NewCommandConfig(&cmd.Definition{Keyword: "todo", Command: "todo-wrapper"}, nil)
		root.Interaction = interaction
		reply := cmd.NewCommandConfig(&cmd.Definition{Keyword: "cancel", Command: "todo-wrapper --cancel"}, nil)
		root.Replies = []*cmd.CommandConfig{reply}
		return root, reply
	}
	context := cmd.ConversationContext{ChannelID: "C", RootThreadTimestamp: "1"}
	cfg := Config{AllowedUserIDs: []string{"U"}, AllowedChannelIDs: []string{"C"}}
	smc := socketmode.New(slack.New("test"))

	t.Run("oneshot drops replies", func(t *testing.T) {
		root, _ := newRoot(cmd.InteractionOneshot)
		queue := make(chan *cmd.CommandInput, 1)
		cache := cmd.NewThreadRouteCache(1)
		cache.Store(context.ThreadKey(), root)
		onMessageEvent(smc, &slackevents.MessageEvent{Channel: "C", User: "U", ThreadTimeStamp: "1", Text: "cancel"}, queue, cfg, nil, []*cmd.CommandConfig{root}, cache)
		if len(queue) != 0 {
			t.Fatal("oneshot reply entered command queue")
		}
	})

	t.Run("stdin drops replies without endpoint", func(t *testing.T) {
		root, _ := newRoot(cmd.InteractionStdin)
		queue := make(chan *cmd.CommandInput, 1)
		cache := cmd.NewThreadRouteCache(1)
		cache.Store(context.ThreadKey(), root)
		onMessageEvent(smc, &slackevents.MessageEvent{Channel: "C", User: "U", ThreadTimeStamp: "1", Text: "cancel"}, queue, cfg, nil, []*cmd.CommandConfig{root}, cache)
		if len(queue) != 0 {
			t.Fatal("stdin reply without endpoint entered command queue")
		}
	})

	t.Run("command queues reply despite live stdin endpoint", func(t *testing.T) {
		root, reply := newRoot(cmd.InteractionCommand)
		queue := make(chan *cmd.CommandInput, 1)
		cache := cmd.NewThreadRouteCache(1)
		cache.Store(context.ThreadKey(), root)
		var registry cmd.ThreadInputRegistry
		_, writer := io.Pipe()
		endpoint := cmd.NewInteractiveStdin(writer, "", func(error) {})
		registry.Register(context.ThreadKey(), endpoint)
		t.Cleanup(endpoint.Close)
		t.Cleanup(func() { _ = writer.Close() })
		onMessageEvent(smc, &slackevents.MessageEvent{Channel: "C", User: "U", ThreadTimeStamp: "1", Text: "cancel\n“raw”\n"}, queue, cfg, &registry, []*cmd.CommandConfig{root}, cache)
		select {
		case input := <-queue:
			if input.CommandConfigs[0] != reply || input.Interaction != cmd.InteractionCommand || input.Text != "cancel\n“raw”\n" {
				t.Fatalf("input = %#v", input)
			}
		default:
			t.Fatal("command reply was not queued")
		}
	})
}

func TestRootCommandInteractionPreservesRawBody(t *testing.T) {
	root := cmd.NewCommandConfig(&cmd.Definition{Keyword: "todo", Command: "todo-wrapper"}, nil)
	root.Interaction = cmd.InteractionCommand
	queue := make(chan *cmd.CommandInput, 1)
	onMessageEvent(socketmode.New(slack.New("test")), &slackevents.MessageEvent{Channel: "C", User: "U", TimeStamp: "1", Text: "todo\n“raw”\n"}, queue, Config{AllowedUserIDs: []string{"U"}, AllowedChannelIDs: []string{"C"}}, nil, []*cmd.CommandConfig{root}, nil)
	if input := <-queue; input.Text != "todo\n“raw”\n" {
		t.Fatalf("input text = %q", input.Text)
	}
}

func TestAbsentThreadInputDoesNotUseGlobalThreadRouting(t *testing.T) {
	smc := socketmode.New(slack.New("test"))
	cfg := Config{
		AllowedUserIDs:    []string{"U"},
		AllowedChannelIDs: []string{"C"},
	}
	var registry cmd.ThreadInputRegistry
	queue := make(chan *cmd.CommandInput, 1)
	event := &slackevents.MessageEvent{
		Channel:         "C",
		User:            "U",
		TimeStamp:       "2",
		ThreadTimeStamp: "1",
		Text:            "reply",
	}
	onMessageEvent(smc, event, queue, cfg, &registry, nil, nil)
	select {
	case input := <-queue:
		t.Fatalf("unexpected global input=%+v", input)
	default:
	}
}
