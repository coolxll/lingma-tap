package bridge

import (
	"errors"
)

// ResponsesStateStore defines the interface for persisting response state
// to support multi-turn conversations via previous_response_id.
type ResponsesStateStore interface {
	// SaveResponse persists a completed response state entry.
	SaveResponse(entry *ResponsesStateEntry) error

	// GetResponse retrieves a response by ID, validating UID isolation and expiration.
	// Returns ErrResponseNotFound, ErrResponseExpired, ErrResponseNotCompleted, or ErrResponseUIDMismatch on error.
	GetResponse(responseID, uidDigest string) (*ResponsesStateEntry, error)

	// WalkChain traverses the parent chain from a response ID upward, rebuilding conversation history.
	// Returns entries in chronological order (oldest first).
	WalkChain(responseID, uidDigest string) ([]ResponsesStateEntry, error)

	// ClearExpired removes expired response state entries (lazy cleanup).
	ClearExpired() error

	// ClearAll removes all response state entries (for ClearTraffic cascade).
	ClearAll() error
}

// Error types for ResponsesStateStore operations
var (
	ErrResponseNotFound     = errors.New("response not found")
	ErrResponseExpired      = errors.New("response expired")
	ErrResponseNotCompleted = errors.New("response not completed")
	ErrResponseUIDMismatch  = errors.New("response UID mismatch")
	ErrChainIncomplete      = errors.New("conversation chain incomplete")
)
