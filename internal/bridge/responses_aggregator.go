package bridge

import (
	"sort"
	"strings"
	"time"
)

// responsesAggregator maintains shared state for building Responses API output.
// Used by both streaming and non-streaming paths to ensure semantic equivalence.
type responsesAggregator struct {
	responseID         string
	modelKey           string
	startTime          time.Time
	instructions       string
	previousResponseID string
	tools              []responsesTool
	reasoning          *responsesReasoning
	responseConfig     responsesResponseConfig

	// Output items in insertion order
	items           []*responsesOutputItemState
	sequenceNum     int // monotonically increasing
	nextOutputIndex int // assigned to each new block on creation

	// Active blocks
	activeText   *textBlockState
	activeReason *reasoningBlockState
	toolCalls    map[int]*toolCallAggState

	// Accumulated content
	usage         *Usage
	fullContent   strings.Builder
	fullReasoning strings.Builder

	// Metadata
	firstTokenTime     time.Time
	firstTokenRecorded bool
	upstreamErrored    bool
}

// textBlockState tracks an active text output block
type textBlockState struct {
	itemID      string
	itemIndex   int
	startOffset int // offset in fullContent where this block starts
}

// reasoningBlockState tracks an active reasoning output block
type reasoningBlockState struct {
	itemID      string
	itemIndex   int
	startOffset int // offset in fullReasoning where this block starts
}

// toolCallAggState tracks an active tool call being aggregated.
// id is the item ID (fc_xxx), callID is the tool call ID (call_xxx).
type toolCallAggState struct {
	id          string // item ID: fc_xxx
	callID      string // tool call ID: call_xxx
	name        string
	args        strings.Builder
	outputIndex int
	idLocked    bool // once set, callID cannot be overwritten
}

// responsesOutputItemState tracks a completed output item
type responsesOutputItemState struct {
	itemType  string
	id        string
	role      string
	content   []responsesContent
	name      string
	callID    string
	arguments string
	index     int
}

// newResponsesAggregator creates a new aggregator for a response
func newResponsesAggregator(responseID, modelKey string, startTime time.Time, instructions, previousResponseID string, tools []responsesTool, reasoning *responsesReasoning, responseConfig responsesResponseConfig) *responsesAggregator {
	return &responsesAggregator{
		responseID:         responseID,
		modelKey:           modelKey,
		startTime:          startTime,
		instructions:       instructions,
		previousResponseID: previousResponseID,
		tools:              tools,
		reasoning:          reasoning,
		responseConfig:     responseConfig,
		toolCalls:          make(map[int]*toolCallAggState),
	}
}

// nextSequence returns the next sequence number and increments the counter
func (a *responsesAggregator) nextSequence() int {
	seq := a.sequenceNum
	a.sequenceNum++
	return seq
}

// recordFirstToken records the time of the first token if not already recorded
func (a *responsesAggregator) recordFirstToken() {
	if !a.firstTokenRecorded {
		a.firstTokenTime = time.Now()
		a.firstTokenRecorded = true
	}
}

// addTextDelta adds text content, creating a text block if needed.
// Returns (itemID, itemIndex, isNewBlock) for streaming emission.
func (a *responsesAggregator) addTextDelta(content string) (string, int, bool) {
	a.recordFirstToken()

	isNew := false
	if a.activeText == nil {
		a.activeText = &textBlockState{
			itemID:      "msg_" + newUUID()[:24],
			itemIndex:   a.nextOutputIndex,
			startOffset: a.fullContent.Len(),
		}
		a.nextOutputIndex++
		isNew = true
	}

	a.fullContent.WriteString(content)
	return a.activeText.itemID, a.activeText.itemIndex, isNew
}

// addReasoningDelta adds reasoning content, creating a reasoning block if needed.
// Returns (itemID, itemIndex, isNewBlock) for streaming emission.
func (a *responsesAggregator) addReasoningDelta(content string) (string, int, bool) {
	a.recordFirstToken()

	isNew := false
	if a.activeReason == nil {
		a.activeReason = &reasoningBlockState{
			itemID:      "reason_" + newUUID()[:24],
			itemIndex:   a.nextOutputIndex,
			startOffset: a.fullReasoning.Len(),
		}
		a.nextOutputIndex++
		isNew = true
	}

	a.fullReasoning.WriteString(content)
	return a.activeReason.itemID, a.activeReason.itemIndex, isNew
}

// addToolCall adds or updates a tool call.
// Returns (itemID, callID, outputIndex, isNewCall) for streaming emission.
// The first-generated callID is locked and cannot be overwritten by upstream.
func (a *responsesAggregator) addToolCall(index int, upstreamID, name, args string) (string, string, int, bool) {
	a.recordFirstToken()
	name = a.restoreToolName(name)

	state, exists := a.toolCalls[index]
	isNew := false
	if !exists {
		// New tool call: generate item ID (fc_xxx) and call ID (call_xxx)
		itemID := "fc_" + newUUID()[:24]
		callID := upstreamID
		// The generated ID is part of the public Responses event stream. Lock it
		// immediately so a late upstream ID cannot change the tool result key.
		idLocked := true
		if callID == "" {
			callID = "call_" + newUUID()[:24]
		}
		state = &toolCallAggState{
			id:          itemID,
			callID:      callID,
			name:        name,
			outputIndex: a.nextOutputIndex,
			idLocked:    idLocked,
		}
		a.nextOutputIndex++
		a.toolCalls[index] = state
		isNew = true
	} else {
		// Update existing - but respect callID lock
		if !state.idLocked && upstreamID != "" {
			state.callID = upstreamID
			state.idLocked = true
		}
		if name != "" && state.name == "" {
			state.name = name
		}
	}

	state.args.WriteString(args)
	return state.id, state.callID, state.outputIndex, isNew
}

// restoreToolName maps the provider-safe namespace form back to the name the
// Responses client supplied. This keeps tool names stable across both sides of
// the adapter while still allowing the Chat Completions backend to use names
// without dots.
func (a *responsesAggregator) restoreToolName(name string) string {
	for _, tool := range a.tools {
		if tool.Name != "" && strings.ReplaceAll(tool.Name, ".", "__") == name {
			return tool.Name
		}
	}
	return name
}

// finishTextBlock closes the active text block and adds it to items.
// Returns the item for streaming emission, or nil if no active block.
func (a *responsesAggregator) finishTextBlock() *responsesOutputItemState {
	if a.activeText == nil {
		return nil
	}

	// Extract only the content for this specific block
	blockContent := a.fullContent.String()[a.activeText.startOffset:]

	item := &responsesOutputItemState{
		itemType: "message",
		id:       a.activeText.itemID,
		role:     "assistant",
		content: []responsesContent{
			{Type: "output_text", Text: blockContent},
		},
		index: a.activeText.itemIndex,
	}
	a.items = append(a.items, item)
	a.activeText = nil
	return item
}

// finishReasoningBlock closes the active reasoning block and adds it to items.
// Returns the item for streaming emission, or nil if no active block.
func (a *responsesAggregator) finishReasoningBlock() *responsesOutputItemState {
	if a.activeReason == nil {
		return nil
	}

	// Extract only the content for this specific block
	blockContent := a.fullReasoning.String()[a.activeReason.startOffset:]

	item := &responsesOutputItemState{
		itemType: "reasoning",
		id:       a.activeReason.itemID,
		content: []responsesContent{
			{Type: "reasoning_text", Text: blockContent},
		},
		index: a.activeReason.itemIndex,
	}
	a.items = append(a.items, item)
	a.activeReason = nil
	return item
}

// finishToolCall closes a tool call and adds it to items.
// Returns the item for streaming emission.
func (a *responsesAggregator) finishToolCall(index int) *responsesOutputItemState {
	state, exists := a.toolCalls[index]
	if !exists {
		return nil
	}

	item := &responsesOutputItemState{
		itemType:  "function_call",
		id:        state.id,
		name:      state.name,
		callID:    state.callID,
		arguments: state.args.String(),
		index:     state.outputIndex,
	}
	a.items = append(a.items, item)
	delete(a.toolCalls, index)
	return item
}

// setUsage sets the usage from upstream finish event.
func (a *responsesAggregator) setUsage(usage *Usage) {
	if usage == nil {
		return
	}
	a.usage = usage
}

// buildUsage converts internal Usage to Responses API format.
// After Consolidate(), PromptTokens == InputTokens and CompletionTokens == OutputTokens,
// so we use only one set to avoid double-counting.
func (a *responsesAggregator) buildUsage() *responsesUsage {
	if a.usage == nil {
		return nil
	}

	resp := &responsesUsage{
		InputTokens:  a.usage.PromptTokens,
		OutputTokens: a.usage.CompletionTokens,
		TotalTokens:  a.usage.TotalTokens,
	}

	// Calculate total if not provided
	if resp.TotalTokens == 0 {
		resp.TotalTokens = resp.InputTokens + resp.OutputTokens
	}

	// Add token details if present
	if a.usage.CachedTokens > 0 {
		resp.InputTokensDetails = &responsesTokenDetails{
			CachedTokens: a.usage.CachedTokens,
		}
	}
	if a.usage.ReasoningTokens > 0 {
		resp.OutputTokensDetails = &responsesTokenDetails{
			ReasoningTokens: a.usage.ReasoningTokens,
		}
	}

	return resp
}

// buildResponse generates the complete response object from aggregated state.
func (a *responsesAggregator) buildResponse(status string) *responsesResponse {
	// Finish any active blocks
	if a.activeText != nil {
		a.finishTextBlock()
	}
	if a.activeReason != nil {
		a.finishReasoningBlock()
	}
	// Finish any remaining tool calls in deterministic order
	indices := make([]int, 0, len(a.toolCalls))
	for idx := range a.toolCalls {
		indices = append(indices, idx)
	}
	sort.Ints(indices)
	for _, idx := range indices {
		a.finishToolCall(idx)
	}

	// Build output array in the same order as the output_index values emitted on
	// the stream. Blocks are finalized by type above, so a.items is not
	// necessarily insertion-ordered at this point.
	orderedItems := append([]*responsesOutputItemState(nil), a.items...)
	sort.SliceStable(orderedItems, func(i, j int) bool {
		return orderedItems[i].index < orderedItems[j].index
	})
	output := make([]responsesOutputItem, 0, len(a.items))
	for _, item := range orderedItems {
		out := responsesOutputItem{
			Type:   item.itemType,
			ID:     item.id,
			Status: "completed",
		}
		if item.role != "" {
			out.Role = item.role
		}
		if len(item.content) > 0 {
			out.Content = item.content
		}
		if item.name != "" {
			out.Name = item.name
		}
		if item.callID != "" {
			out.CallID = item.callID
		}
		if item.arguments != "" {
			out.Arguments = item.arguments
		}
		output = append(output, out)
	}

	resp := a.newResponse(status, output)
	resp.Usage = a.buildUsage()
	return resp
}

// newResponse builds the common Responses response envelope. It is also used
// for response.created/in_progress so lifecycle events expose the same field
// set, including explicit null/default values expected by strict clients.
func (a *responsesAggregator) newResponse(status string, output []responsesOutputItem) *responsesResponse {
	if output == nil {
		output = []responsesOutputItem{}
	}
	tools := a.tools
	if tools == nil {
		tools = []responsesTool{}
	}

	var completedAt *int64
	if status == "completed" {
		completed := time.Now().Unix()
		completedAt = &completed
	}

	var instructions *string
	if a.instructions != "" {
		instructions = &a.instructions
	}
	var previousResponseID *string
	if a.previousResponseID != "" {
		previousResponseID = &a.previousResponseID
	}
	toolChoice := a.responseConfig.ToolChoice
	if toolChoice == nil {
		toolChoice = "auto"
	}
	var reasoningEffort *string
	if a.reasoning != nil && a.reasoning.Effort != "" {
		reasoningEffort = &a.reasoning.Effort
	}
	input := a.responseConfig.Input
	if input == nil {
		input = []any{}
	}

	return &responsesResponse{
		ID:                 a.responseID,
		Object:             "response",
		CreatedAt:          a.startTime.Unix(),
		Status:             status,
		CompletedAt:        completedAt,
		Error:              nil,
		IncompleteDetails:  nil,
		Instructions:       instructions,
		MaxOutputTokens:    a.responseConfig.MaxOutputTokens,
		Model:              a.modelKey,
		Output:             output,
		ParallelToolCalls:  a.responseConfig.ParallelToolCalls,
		PreviousResponseID: previousResponseID,
		Reasoning:          a.reasoning,
		ReasoningEffort:    reasoningEffort,
		Store:              a.responseConfig.Store,
		Temperature:        a.responseConfig.Temperature,
		Text: map[string]any{
			"format": map[string]any{"type": "text"},
		},
		ToolChoice: toolChoice,
		Tools:      tools,
		TopP:       1,
		Truncation: "disabled",
		Usage:      nil,
		User:       nil,
		Metadata:   map[string]string{},
		Input:      input,
	}
}

// buildOutputMessages converts aggregated output to Responses API format for state persistence.
// It finalizes any blocks that are still active. Streaming callers should invoke
// emitDone before this method so terminal SSE events are emitted first.
func (a *responsesAggregator) buildOutputMessages() []map[string]any {
	// Flush active blocks into items first
	if a.activeText != nil {
		a.finishTextBlock()
	}
	if a.activeReason != nil {
		a.finishReasoningBlock()
	}

	// Finish all tool calls in deterministic order to preserve correct output ordering
	if len(a.toolCalls) > 0 {
		indices := make([]int, 0, len(a.toolCalls))
		for idx := range a.toolCalls {
			indices = append(indices, idx)
		}
		sort.Ints(indices)

		for _, idx := range indices {
			a.finishToolCall(idx)
		}
	}

	var messages []map[string]any

	// Iterate through items in output_index order. Finalization order above is
	// type-based, not necessarily the order in which output blocks started.
	orderedItems := append([]*responsesOutputItemState(nil), a.items...)
	sort.SliceStable(orderedItems, func(i, j int) bool {
		return orderedItems[i].index < orderedItems[j].index
	})
	for _, item := range orderedItems {
		switch item.itemType {
		case "reasoning":
			if len(item.content) > 0 {
				messages = append(messages, map[string]any{
					"type":    "reasoning",
					"content": item.content[0].Text,
				})
			}
		case "message":
			if len(item.content) > 0 {
				messages = append(messages, map[string]any{
					"type":    "message",
					"role":    "assistant",
					"content": item.content[0].Text,
				})
			}
		case "function_call":
			messages = append(messages, map[string]any{
				"type":      "function_call",
				"call_id":   item.callID,
				"name":      item.name,
				"arguments": item.arguments,
			})
		}
	}

	return messages
}
