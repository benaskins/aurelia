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

// NewEngine creates a diagnostic engine with the given LLM client and API tools.
func NewEngine(client talk.LLMClient, model string, apiClient APIClient) *Engine {
	return &Engine{
		client: client,
		model:  model,
		tools:  Tools(apiClient),
	}
}

// Diagnose runs a diagnostic conversation. If service is non-empty, the
// diagnosis focuses on that service. The onToken callback is called with
// each streamed token for real-time output.
func (e *Engine) Diagnose(ctx context.Context, service string, onToken func(string)) (*loop.Result, error) {
	userMessage := "Review all services and report any concerns."
	if service != "" {
		userMessage = fmt.Sprintf("Diagnose the service %q — what is its current state and are there any issues?", service)
	}

	req := &talk.Request{
		Model: e.model,
		Messages: []talk.Message{
			{Role: talk.RoleSystem, Content: systemPrompt},
			{Role: talk.RoleUser, Content: userMessage},
		},
		Stream:        true,
		MaxIterations: 10,
	}

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
