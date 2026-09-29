package cmd

import "strings"

const (
	slackChannelIDEnvironment = "SLACK_CHANNEL_ID"
	slackThreadTSEnvironment  = "SLACK_THREAD_TS"
)

type environmentSetter interface {
	SetEnv([]string)
}

func setSlackContextEnvironment(command Cmd, conversation ConversationID) {
	if conversation.ChannelID == "" || conversation.RootTimestamp == "" {
		return
	}
	setter, ok := command.(environmentSetter)
	if !ok {
		return
	}
	setter.SetEnv([]string{
		slackChannelIDEnvironment + "=" + conversation.ChannelID,
		slackThreadTSEnvironment + "=" + conversation.RootTimestamp,
	})
}

func mergeEnvironment(base, overrides []string) []string {
	overrideKeys := make(map[string]struct{}, len(overrides))
	for _, value := range overrides {
		overrideKeys[environmentKey(value)] = struct{}{}
	}
	merged := make([]string, 0, len(base)+len(overrides))
	for _, value := range base {
		if _, overridden := overrideKeys[environmentKey(value)]; !overridden {
			merged = append(merged, value)
		}
	}
	return append(merged, overrides...)
}

func environmentKey(value string) string {
	key, _, _ := strings.Cut(value, "=")
	return key
}
