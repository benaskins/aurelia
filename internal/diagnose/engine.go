package diagnose

import (
	"context"
	"fmt"

	loop "github.com/benaskins/axon-loop"
	talk "github.com/benaskins/axon-talk"
	tool "github.com/benaskins/axon-tool"
)

const systemPrompt = `You are aurelia's diagnostic agent. Aurelia is a process supervisor managing services on this node (macOS, Apple Silicon).

You have tools to inspect the services aurelia manages. Use them to gather evidence before drawing conclusions. Always check service state and logs before diagnosing.

When asked about a specific service, focus there but check dependencies and related services if relevant. When asked for a general review, survey all services and flag anything concerning.

You can propose actions (restart, start, stop) when you've identified a clear issue. The operator will approve or reject each action. If rejected, continue investigating or suggest alternatives.

Key things to look for:
- Services in failed or unhealthy state
- High restart counts (suggests crash loops)
- Services with recent errors in logs
- GPU/VRAM pressure if ML services are running
- Dependency chains — if a required service is down, dependents will fail too

Be concise. Lead with the diagnosis, then supporting evidence.`

// Engine runs LLM-powered diagnostic conversations against aurelia's API.
type Engine struct {
	client talk.LLMClient
	model  string
	tools  map[string]tool.ToolDef
}

// NewEngine creates a diagnostic engine with read-only tools (no actions).
func NewEngine(client talk.LLMClient, model string, apiClient APIClient) *Engine {
	return &Engine{
		client: client,
		model:  model,
		tools:  ReadTools(apiClient),
	}
}

// NewEngineWithActions creates a diagnostic engine with both read and action tools.
// The confirm callback is called before executing any action tool.
func NewEngineWithActions(client talk.LLMClient, model string, apiClient APIClient, confirm ConfirmFunc) *Engine {
	return &Engine{
		client: client,
		model:  model,
		tools:  AllTools(apiClient, confirm),
	}
}

// Diagnose runs a diagnostic conversation. If service is non-empty, the
// diagnosis focuses on that service. The onToken callback is called with
// each streamed token for real-time output.
func (e *Engine) Diagnose(ctx context.Context, service string, onToken func(string)) (*loop.Result, error) {
	req := e.buildRequest(service)
	cfg := loop.RunConfig{
		Client:  e.client,
		Request: req,
		Tools:   e.tools,
		ToolCtx: &tool.ToolContext{Ctx: ctx},
		Callbacks: loop.Callbacks{
			OnToken: onToken,
		},
	}
	return loop.Run(ctx, cfg)
}

// Stream runs a diagnostic conversation and returns a channel of events
// for use with a TUI.
func (e *Engine) Stream(ctx context.Context, service string, messages []talk.Message) <-chan loop.Event {
	req := e.buildRequestWithMessages(service, messages)
	cfg := loop.RunConfig{
		Client:  e.client,
		Request: req,
		Tools:   e.tools,
		ToolCtx: &tool.ToolContext{Ctx: ctx},
	}
	return loop.Stream(ctx, cfg)
}

// Tools returns the engine's tool map (for TUI to pass to axon-loop).
func (e *Engine) Tools() map[string]tool.ToolDef {
	return e.tools
}

// Model returns the configured model name.
func (e *Engine) Model() string {
	return e.model
}

// Client returns the LLM client.
func (e *Engine) Client() talk.LLMClient {
	return e.client
}

func (e *Engine) buildRequest(service string) *talk.Request {
	messages := []talk.Message{
		{Role: talk.RoleSystem, Content: systemPrompt},
		{Role: talk.RoleUser, Content: userMessage(service)},
	}
	return e.buildRequestWithMessages(service, messages)
}

func (e *Engine) buildRequestWithMessages(service string, messages []talk.Message) *talk.Request {
	toolDefs := make([]tool.ToolDef, 0, len(e.tools))
	for _, t := range e.tools {
		toolDefs = append(toolDefs, t)
	}

	return &talk.Request{
		Model:         e.model,
		Messages:      messages,
		Tools:         toolDefs,
		Stream:        true,
		MaxIterations: 10,
	}
}

func userMessage(service string) string {
	if service != "" {
		return fmt.Sprintf("Diagnose the service %q — what is its current state and are there any issues?", service)
	}
	return "Review all services and report any concerns."
}
