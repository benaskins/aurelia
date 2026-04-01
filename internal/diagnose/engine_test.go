package diagnose

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	talk "github.com/benaskins/axon-talk"
)

// mockLLMClient simulates an LLM that calls list_services then responds.
type mockLLMClient struct {
	calls int
}

func (m *mockLLMClient) Chat(ctx context.Context, req *talk.Request, fn func(talk.Response) error) error {
	m.calls++

	// First call: LLM decides to use list_services tool
	if m.calls == 1 {
		return fn(talk.Response{
			ToolCalls: []talk.ToolCall{
				{ID: "call_1", Name: "list_services", Arguments: map[string]any{}},
			},
		})
	}

	// Second call: LLM produces final answer
	return fn(talk.Response{
		Content: "All services are healthy.",
		Done:    true,
	})
}

func TestEngineDiagnoseAllServices(t *testing.T) {
	t.Parallel()

	// Set up a mock aurelia API
	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1/services", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode([]map[string]any{
			{"name": "chat", "state": "running", "health": "healthy"},
		})
	})
	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)
	apiClient := &testAPIClient{server: server}

	llm := &mockLLMClient{}
	engine := NewEngine(llm, "test-model", apiClient)

	var tokens []string
	result, err := engine.Diagnose(context.Background(), "", func(token string) {
		tokens = append(tokens, token)
	})
	if err != nil {
		t.Fatalf("Diagnose error: %v", err)
	}

	if result.Content != "All services are healthy." {
		t.Errorf("Content = %q, want %q", result.Content, "All services are healthy.")
	}

	if llm.calls != 2 {
		t.Errorf("LLM calls = %d, want 2 (tool call + final response)", llm.calls)
	}
}

func TestEngineDiagnoseSpecificService(t *testing.T) {
	t.Parallel()

	// Track what messages the LLM receives
	var receivedMessages []talk.Message

	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1/services/chat", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{"name": "chat", "state": "failed"})
	})
	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)
	apiClient := &testAPIClient{server: server}

	llm := &captureLLMClient{
		response: talk.Response{Content: "chat is down due to a config error."},
		capture:  &receivedMessages,
	}
	engine := NewEngine(llm, "test-model", apiClient)

	result, err := engine.Diagnose(context.Background(), "chat", nil)
	if err != nil {
		t.Fatalf("Diagnose error: %v", err)
	}

	if result.Content != "chat is down due to a config error." {
		t.Errorf("Content = %q", result.Content)
	}

	// Verify the user message mentions the service name
	if len(receivedMessages) < 2 {
		t.Fatalf("expected at least 2 messages (system + user), got %d", len(receivedMessages))
	}
	userMsg := receivedMessages[1].Content
	if !strings.Contains(userMsg, "chat") {
		t.Errorf("user message should mention service name, got: %s", userMsg)
	}
}

// captureLLMClient records messages and returns a fixed response.
type captureLLMClient struct {
	response talk.Response
	capture  *[]talk.Message
}

func (c *captureLLMClient) Chat(ctx context.Context, req *talk.Request, fn func(talk.Response) error) error {
	*c.capture = req.Messages
	return fn(c.response)
}
