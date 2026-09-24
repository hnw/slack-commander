package cmd

import "fmt"

// Interaction describes how a root command owns a Slack thread.
type Interaction string

const (
	InteractionOneshot Interaction = "oneshot"
	InteractionStdin   Interaction = "stdin"
	InteractionCommand Interaction = "command"
)

// Normalize returns the configured interaction, defaulting omitted values to oneshot.
func (i Interaction) Normalize() (Interaction, error) {
	switch i {
	case "", InteractionOneshot:
		return InteractionOneshot, nil
	case InteractionStdin, InteractionCommand:
		return i, nil
	default:
		return "", fmt.Errorf("unknown interaction %q", i)
	}
}
