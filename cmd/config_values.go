package cmd

const (
	// InteractionOneshot runs a command without accepting subsequent thread input.
	InteractionOneshot = "oneshot"
	// InteractionStdin forwards subsequent thread messages to the command's standard input.
	InteractionStdin = "stdin"
	// InteractionCommand routes thread messages to configured reply commands.
	InteractionCommand = "command"
)

// InputBodyMode controls how text after the first input line is consumed.
type InputBodyMode int

const (
	// InputBodyStdin passes the input body to standard input.
	InputBodyStdin InputBodyMode = iota
	// InputBodyArgument appends the input body to a trailing wildcard argument.
	InputBodyArgument
)

const (
	// RunnerExec runs commands directly with os/exec.
	RunnerExec = "exec"
	// RunnerCompose runs commands through Docker Compose.
	RunnerCompose = "compose"
	// RunnerHTTP sends commands as HTTP requests.
	RunnerHTTP = "http"
)
