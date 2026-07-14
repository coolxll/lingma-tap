package bridge

import "encoding/json"

// responsesRequest represents an OpenAI Responses API request
type responsesRequest struct {
	Model              string              `json:"model"`
	Input              json.RawMessage     `json:"input"` // string | []item
	Instructions       string              `json:"instructions,omitempty"`
	Tools              []responsesTool     `json:"tools,omitempty"`
	ToolChoice         json.RawMessage     `json:"tool_choice,omitempty"`
	ParallelToolCalls  *bool               `json:"parallel_tool_calls,omitempty"`
	Stream             bool                `json:"stream"`
	Temperature        *float64            `json:"temperature,omitempty"`
	MaxOutputTokens    *int                `json:"max_output_tokens,omitempty"`
	Reasoning          *responsesReasoning `json:"reasoning,omitempty"`        // nested form
	ReasoningEffort    string              `json:"reasoning_effort,omitempty"` // flat form (legacy)
	PreviousResponseID string              `json:"previous_response_id,omitempty"`
	Background         bool                `json:"background,omitempty"`
	Store              *bool               `json:"store,omitempty"`
}

// responsesReasoning represents nested reasoning configuration
type responsesReasoning struct {
	Effort  string `json:"effort,omitempty"`
	Summary string `json:"summary,omitempty"` // "auto", "concise", "detailed"
}

// responsesResponseConfig carries request options that must be reflected in
// every response lifecycle envelope.
type responsesResponseConfig struct {
	MaxOutputTokens   *int
	ParallelToolCalls bool
	Store             bool
	Temperature       *float64
	ToolChoice        any
	Input             any
}

// responsesTool represents a tool definition in Responses API format
type responsesTool struct {
	Type        string         `json:"type"` // "function"
	Name        string         `json:"name"`
	Description string         `json:"description,omitempty"`
	Parameters  map[string]any `json:"parameters,omitempty"`
	Strict      *bool          `json:"strict,omitempty"`
	// Legacy schema compatibility: may receive {name, description, parameters} without type wrapper
}

// responsesResponse represents an OpenAI Responses API response
type responsesResponse struct {
	ID                 string                `json:"id"`
	Object             string                `json:"object"`
	CreatedAt          int64                 `json:"created_at"`
	Status             string                `json:"status"`
	CompletedAt        *int64                `json:"completed_at"`
	Error              *responsesError       `json:"error"`
	IncompleteDetails  any                   `json:"incomplete_details"`
	Instructions       *string               `json:"instructions"`
	MaxOutputTokens    *int                  `json:"max_output_tokens"`
	Model              string                `json:"model"`
	Output             []responsesOutputItem `json:"output"`
	ParallelToolCalls  bool                  `json:"parallel_tool_calls"`
	PreviousResponseID *string               `json:"previous_response_id"`
	Reasoning          *responsesReasoning   `json:"reasoning"`
	ReasoningEffort    *string               `json:"reasoning_effort"`
	Store              bool                  `json:"store"`
	Temperature        *float64              `json:"temperature"`
	Text               map[string]any        `json:"text"`
	ToolChoice         any                   `json:"tool_choice"`
	Tools              []responsesTool       `json:"tools"`
	TopP               float64               `json:"top_p"`
	Truncation         string                `json:"truncation"`
	Usage              *responsesUsage       `json:"usage"`
	User               *string               `json:"user"`
	Metadata           map[string]string     `json:"metadata"`
	Input              any                   `json:"input"`
}

// responsesOutputItem represents an output item in the response
type responsesOutputItem struct {
	Type      string             `json:"type"` // "message", "function_call", "reasoning"
	ID        string             `json:"id,omitempty"`
	Role      string             `json:"role,omitempty"`
	Status    string             `json:"status,omitempty"`
	Content   []responsesContent `json:"content,omitempty"`
	Name      string             `json:"name,omitempty"`
	CallID    string             `json:"call_id,omitempty"`
	Arguments string             `json:"arguments,omitempty"`
}

// responsesContent represents content within an output item
type responsesContent struct {
	Type string `json:"type"` // "output_text", "reasoning_text"
	Text string `json:"text"`
}

// responsesUsage represents token usage in Responses API format
type responsesUsage struct {
	InputTokens         int                    `json:"input_tokens"`
	OutputTokens        int                    `json:"output_tokens"`
	TotalTokens         int                    `json:"total_tokens"`
	InputTokensDetails  *responsesTokenDetails `json:"input_tokens_details,omitempty"`
	OutputTokensDetails *responsesTokenDetails `json:"output_tokens_details,omitempty"`
}

// responsesTokenDetails represents detailed token breakdown
type responsesTokenDetails struct {
	CachedTokens    int `json:"cached_tokens,omitempty"`
	ReasoningTokens int `json:"reasoning_tokens,omitempty"`
}

// responsesError represents an error in Responses API format
type responsesError struct {
	Type    string `json:"type"`
	Code    string `json:"code,omitempty"`
	Message string `json:"message"`
	Param   string `json:"param,omitempty"`
}

// ResponsesStateEntry represents a persisted response state for multi-turn conversations
type ResponsesStateEntry struct {
	ResponseID   string
	ParentID     string
	UIDDigest    string
	Status       string
	InputJSON    string
	OutputJSON   string
	ResponseJSON string
	Instructions string
	WarningsJSON string
	CreatedAt    string
	ExpiresAt    string
}
