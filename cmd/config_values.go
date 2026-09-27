package cmd

const (
	// InteractionOneshot runs a command without accepting subsequent thread input.
	InteractionOneshot = "oneshot"
	// InteractionStdin forwards subsequent thread messages to the command's standard input.
	InteractionStdin = "stdin"
	// InteractionCommand routes thread messages to configured reply commands.
	InteractionCommand = "command"
)

const (
	// RunnerExec runs commands directly with os/exec.
	RunnerExec = "exec"
	// RunnerCompose runs commands through Docker Compose.
	RunnerCompose = "compose"
	// RunnerHTTP sends commands as HTTP requests.
	RunnerHTTP = "http"
)
