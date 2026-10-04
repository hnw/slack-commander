package pubsub

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/hnw/slack-commander/cmd"
	"github.com/slack-go/slack"
	"github.com/slack-go/slack/socketmode"
)

func TestSlackOutputDrainsConcurrentProducersAfterCancellation(t *testing.T) {
	const producers = 4
	posts := make(chan string, producers)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseForm(); err != nil {
			t.Error(err)
		}
		if r.URL.Path == "/chat.postMessage" {
			posts <- r.Form.Get("username")
		}
		_, _ = io.WriteString(w, `{"ok":true,"channel":"C","ts":"1"}`)
	}))
	defer server.Close()
	output := NewSlackOutput(1)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	smc := socketmode.New(slack.New("token", slack.OptionAPIURL(server.URL+"/")))
	done := make(chan struct{})
	go func() { output.Run(ctx, smc); close(done) }()
	var workers sync.WaitGroup
	for i := range producers {
		workers.Add(1)
		go func() {
			defer workers.Done()
			command := output.NewCommandOutput(ReplyConfig{Username: fmt.Sprintf("producer-%d", i)}, time.Hour)
			c := cmd.ConversationID{ChannelID: "C", RootTimestamp: "1"}
			m := cmd.MessageID{ChannelID: "C", Timestamp: "1"}
			command.Start(c, m)
			stream := command.Stdout(c, m)
			_, _ = io.WriteString(stream, "buffered output")
			if err := stream.Flush(); err != nil {
				t.Error(err)
			}
			command.Finish(c, m, 0)
		}()
	}
	producersDone := make(chan struct{})
	go func() { workers.Wait(); close(producersDone) }()
	select {
	case <-producersDone:
	case <-time.After(3 * time.Second):
		t.Fatal("producers could not send after cancellation")
	}
	select {
	case <-done:
		t.Fatal("consumer exited before producers closed the pipeline")
	default:
	}
	output.Close()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("consumer did not drain the pipeline")
	}
	seen := make(map[string]bool)
	for len(posts) > 0 {
		seen[<-posts] = true
	}
	if len(seen) != producers {
		t.Fatalf("posted producers = %v, want %d", seen, producers)
	}
}
