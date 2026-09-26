package cmd

import "fmt"

// Interaction describes how a root command owns a Slack thread.
type Interaction string

const (
	// InteractionOneshot runs a command without accepting subsequent thread input.
	InteractionOneshot Interaction = "oneshot"
	// InteractionStdin forwards subsequent thread messages to the command's standard input.
	InteractionStdin Interaction = "stdin"
	// InteractionCommand routes thread messages to configured reply commands.
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
