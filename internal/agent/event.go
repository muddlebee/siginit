package agent

// EventKind classifies an agent event for the TUI to render.
type EventKind int

const (
	EventThinking   EventKind = iota // LLM is reasoning
	EventToolCall                    // about to execute a tool
	EventToolResult                  // tool returned
	EventFinal                       // agent produced a final text answer
	EventError                       // terminal error
	EventPermission                  // permission gate triggered
)

// Event is emitted by the agent loop on a channel consumed by the TUI.
type Event struct {
	Kind    EventKind
	Tool    string // EventToolCall / EventToolResult
	Args    string // EventToolCall — raw JSON args
	Result  string // EventToolResult / EventFinal / EventError
	Message string // human-readable status line
}
