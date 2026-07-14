package bridge

import (
	"encoding/json"
	"errors"
)

const (
	// DefaultMaxContextBytes is the default maximum context size (512 KiB)
	DefaultMaxContextBytes = 512 * 1024
)

// ErrCurrentRoundTooLarge is returned when the current turn exceeds the context limit
var ErrCurrentRoundTooLarge = errors.New("current round exceeds context limit")

// responsesContextTrimmer handles context trimming for long conversations
type responsesContextTrimmer struct {
	maxBytes int
}

// newResponsesContextTrimmer creates a new context trimmer
func newResponsesContextTrimmer(maxBytes int) *responsesContextTrimmer {
	if maxBytes <= 0 {
		maxBytes = DefaultMaxContextBytes
	}
	return &responsesContextTrimmer{maxBytes: maxBytes}
}

// trimContext trims the conversation history to fit within the byte limit.
// It preserves the current turn (last user message and all subsequent messages).
// Returns the trimmed messages, whether trimming occurred, and an error if the current round exceeds the limit.
func (t *responsesContextTrimmer) trimContext(messages []map[string]any) ([]map[string]any, bool, error) {
	return t.trimContextAt(messages, -1)
}

// trimContextAt is the boundary-aware variant used by the Responses handler.
// currentTurnStart is the first message belonging to the current request; -1
// retains the legacy heuristic of finding the last user message.
func (t *responsesContextTrimmer) trimContextAt(messages []map[string]any, currentTurnStart int) ([]map[string]any, bool, error) {
	// Calculate current size
	currentSize := t.calculateSize(messages)
	if currentSize <= t.maxBytes {
		return messages, false, nil
	}

	// Find the last user message when the caller did not provide an exact
	// current-turn boundary.
	if currentTurnStart < 0 {
		for i := len(messages) - 1; i >= 0; i-- {
			if role, ok := messages[i]["role"].(string); ok && role == "user" {
				currentTurnStart = i
				break
			}
		}
	}

	// If no user message found, treat entire message list as current turn
	if currentTurnStart == -1 {
		currentTurnStart = 0
	}

	// Preserve current turn
	currentTurn := messages[currentTurnStart:]
	currentTurnSize := t.calculateSize(currentTurn)

	// If current turn alone exceeds limit, return error
	if currentTurnSize > t.maxBytes {
		return nil, false, ErrCurrentRoundTooLarge
	}

	// Calculate available space for history
	availableBytes := t.maxBytes - currentTurnSize

	// Build history from the end backwards, preserving tool call/result pairs
	var history []map[string]any
	historySize := 0
	consumedIndices := make(map[int]bool) // Track indices already added to history

	for i := currentTurnStart - 1; i >= 0; i-- {
		// Skip indices already consumed by tool call/result pairing
		if consumedIndices[i] {
			continue
		}

		msg := messages[i]
		msgSize := t.calculateSize([]map[string]any{msg})

		// Check if adding this message would exceed the limit
		if historySize+msgSize > availableBytes {
			break
		}

		// If this is a tool result, check if we have the corresponding tool call
		if role, ok := msg["role"].(string); ok && role == "tool" {
			// Look for the corresponding assistant message with tool_calls
			foundCall := false
			callFoundButTooLarge := false
			for j := i - 1; j >= 0; j-- {
				if role, ok := messages[j]["role"].(string); ok && role == "assistant" {
					if toolCalls, hasCalls := messages[j]["tool_calls"]; hasCalls {
						// Check if this specific tool call ID matches
						toolCallID, _ := msg["tool_call_id"].(string)
						matches := false
						if tcSlice, ok := toolCalls.([]map[string]any); ok {
							for _, tc := range tcSlice {
								if id, ok := tc["id"].(string); ok && id == toolCallID {
									matches = true
									break
								}
							}
						}
						if !matches {
							continue
						}
						// Include both the call and result
						callSize := t.calculateSize([]map[string]any{messages[j]})
						// Only count callSize if we haven't already added this assistant message
						additionalSize := msgSize
						if !consumedIndices[j] {
							additionalSize += callSize
						}
						if historySize+additionalSize <= availableBytes {
							// Append in reverse order (will reverse at end)
							history = append(history, msg)
							if !consumedIndices[j] {
								history = append(history, messages[j])
								consumedIndices[j] = true
							}
							historySize += additionalSize
							consumedIndices[i] = true // Mark tool result as consumed
							foundCall = true
						} else {
							// Call found but doesn't fit — skip both the result and the call
							callFoundButTooLarge = true
						}
						break
					}
				}
			}
			if !foundCall && !callFoundButTooLarge {
				// Truly orphan tool result (no matching call exists) — skip to avoid invalid sequence
				consumedIndices[i] = true
			}
			// If callFoundButTooLarge, skip the tool result to avoid invalid state
		} else {
			// Regular message, append it (will reverse at end)
			history = append(history, msg)
			historySize += msgSize
			consumedIndices[i] = true
		}
	}

	// Reverse history to restore chronological order
	for i, j := 0, len(history)-1; i < j; i, j = i+1, j-1 {
		history[i], history[j] = history[j], history[i]
	}

	// Combine history and current turn
	result := append(history, currentTurn...)
	return result, len(result) < len(messages), nil
}

// calculateSize calculates the JSON size of messages in bytes
func (t *responsesContextTrimmer) calculateSize(messages []map[string]any) int {
	data, err := json.Marshal(messages)
	if err != nil {
		// Return a large value to be conservative and trigger trimming
		return 1 << 30 // 1 GiB
	}
	return len(data)
}
