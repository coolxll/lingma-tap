package bridge

import (
	"testing"
	"time"
)

func TestResponsesAggregatorBuildResponsePreservesOutputIndexOrder(t *testing.T) {
	agg := newResponsesAggregator("resp_test", "model", time.Unix(1, 0), "", "", nil, nil, responsesResponseConfig{ParallelToolCalls: true, Store: true, ToolChoice: "auto"})

	// Reasoning starts first (index 0), then visible text (index 1). The
	// finalizer closes text before reasoning, so buildResponse must explicitly
	// restore output_index order.
	agg.addReasoningDelta("think")
	agg.addTextDelta("answer")

	resp := agg.buildResponse("completed")
	if len(resp.Output) != 2 {
		t.Fatalf("expected 2 output items, got %d", len(resp.Output))
	}
	if resp.Output[0].Type != "reasoning" || resp.Output[1].Type != "message" {
		t.Fatalf("output order = [%s, %s], want [reasoning, message]", resp.Output[0].Type, resp.Output[1].Type)
	}
}

func TestResponsesAggregatorResponseEnvelopeIncludesLifecycleFields(t *testing.T) {
	agg := newResponsesAggregator("resp_test", "model", time.Unix(10, 0), "Be helpful", "resp_parent", nil, nil, responsesResponseConfig{ParallelToolCalls: true, Store: true, ToolChoice: "auto"})

	inProgress := agg.newResponse("in_progress", nil)
	if inProgress.CompletedAt != nil {
		t.Fatal("in-progress response should not have completed_at")
	}
	if inProgress.Instructions == nil || *inProgress.Instructions != "Be helpful" {
		t.Fatalf("instructions = %v, want request instructions", inProgress.Instructions)
	}
	if inProgress.PreviousResponseID == nil || *inProgress.PreviousResponseID != "resp_parent" {
		t.Fatalf("previous_response_id = %v, want resp_parent", inProgress.PreviousResponseID)
	}
	if inProgress.Text["format"] == nil || inProgress.ToolChoice != "auto" || inProgress.Truncation != "disabled" {
		t.Fatalf("missing default response envelope fields: %#v", inProgress)
	}

	completed := agg.newResponse("completed", nil)
	if completed.CompletedAt == nil {
		t.Fatal("completed response should have completed_at")
	}
	if completed.Metadata == nil || completed.Tools == nil || completed.Input == nil {
		t.Fatal("completed response should contain non-nil collection fields")
	}
}

func TestResponsesAggregatorResponseEnvelopeReflectsRequestOptions(t *testing.T) {
	maxOutputTokens := 512
	temperature := 0.25
	agg := newResponsesAggregator("resp_test", "model", time.Unix(10, 0), "", "", nil, nil, responsesResponseConfig{
		MaxOutputTokens:   &maxOutputTokens,
		ParallelToolCalls: false,
		Store:             false,
		Temperature:       &temperature,
		ToolChoice:        map[string]any{"type": "function"},
	})

	resp := agg.newResponse("completed", nil)
	if resp.MaxOutputTokens == nil || *resp.MaxOutputTokens != maxOutputTokens {
		t.Fatalf("max_output_tokens = %v, want %d", resp.MaxOutputTokens, maxOutputTokens)
	}
	if resp.ParallelToolCalls || resp.Store {
		t.Fatalf("request boolean options were not preserved: parallel=%v store=%v", resp.ParallelToolCalls, resp.Store)
	}
	if resp.Temperature == nil || *resp.Temperature != temperature {
		t.Fatalf("temperature = %v, want %v", resp.Temperature, temperature)
	}
	if choice, ok := resp.ToolChoice.(map[string]any); !ok || choice["type"] != "function" {
		t.Fatalf("tool_choice = %#v, want function choice", resp.ToolChoice)
	}
}
