package cmd

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestConversationRouterPropagatesResolverContextAndRetriesAfterCancellation(t *testing.T) {
	for _, tc := range []struct {
		name     string
		deadline time.Duration
		wantErr  error
	}{
		{"canceled", time.Hour, context.Canceled},
		{"deadline exceeded", -time.Second, context.DeadlineExceeded},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithDeadline(context.Background(), time.Now().Add(tc.deadline))
			defer cancel()
			if tc.wantErr == context.Canceled {
				cancel()
			}
			conversation := ConversationID{ChannelID: "C", RootTimestamp: "1"}
			calls, queued := 0, 0
			router := newTestConversationRouter([]*testCommandConfig{routerRoot(InteractionCommand)}, func(resolveCtx context.Context, got ConversationID) (RootCommandInput, error) {
				calls++
				if got != conversation {
					t.Errorf("conversation = %+v, want %+v", got, conversation)
				}
				if calls == 1 && resolveCtx != ctx {
					t.Error("resolver did not receive the caller's context")
				}
				if err := resolveCtx.Err(); err != nil {
					return RootCommandInput{}, err
				}
				return RootCommandInput{Text: "run", AllowedCommandIndexes: []int{0}}, nil
			}, func(*CommandInput) bool {
				queued++
				return true
			}, 1)
			input := &CommandInput{Text: "stop", ConversationID: conversation, MessageID: MessageID{Timestamp: "2"}, AllowedCommandIndexes: []int{1}}
			if result, err := router.Accept(ctx, input); result != AcceptIgnored || !errors.Is(err, tc.wantErr) {
				t.Fatalf("canceled Accept() = %v, %v; want ignored, %v", result, err, tc.wantErr)
			}
			if queued != 0 {
				t.Fatal("canceled reply was queued")
			}
			if _, found := router.routes.lookup(conversation); found {
				t.Fatal("canceled lookup was cached")
			}
			if result, err := router.Accept(context.Background(), input); result != AcceptRouted || err != nil {
				t.Fatalf("retry Accept() = %v, %v; want routed", result, err)
			}
			if calls != 2 || queued != 1 {
				t.Fatalf("resolver calls = %d, queued = %d; want 2, 1", calls, queued)
			}
		})
	}
}
