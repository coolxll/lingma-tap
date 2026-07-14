package storage

import (
	"database/sql"
	"time"

	"github.com/coolxll/lingma-tap/internal/bridge"
)

// responsesStateStore implements bridge.ResponsesStateStore using SQLite.
type responsesStateStore struct {
	db *DB
}

// NewResponsesStateStore creates a new ResponsesStateStore backed by the given DB.
func NewResponsesStateStore(db *DB) bridge.ResponsesStateStore {
	return &responsesStateStore{db: db}
}

// SaveResponse persists a response state entry with 24-hour expiration.
func (s *responsesStateStore) SaveResponse(entry *bridge.ResponsesStateEntry) error {
	s.db.writeMu.Lock()
	defer s.db.writeMu.Unlock()

	// Lazy cleanup of expired entries
	s.clearExpiredLocked()

	// Set expiration to 24 hours from creation
	createdAt, err := time.Parse(time.RFC3339Nano, entry.CreatedAt)
	if err != nil {
		createdAt = time.Now()
	}
	expiresAt := createdAt.Add(24 * time.Hour)

	_, err = s.db.db.Exec(`
		INSERT OR REPLACE INTO responses_state
		(response_id, parent_id, uid_digest, status, input_json, output_json, response_json, instructions, warnings_json, created_at, expires_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		entry.ResponseID,
		entry.ParentID,
		entry.UIDDigest,
		entry.Status,
		entry.InputJSON,
		entry.OutputJSON,
		entry.ResponseJSON,
		entry.Instructions,
		entry.WarningsJSON,
		entry.CreatedAt,
		expiresAt.Format(time.RFC3339Nano),
	)
	return err
}

// GetResponse retrieves a response by ID with UID validation and expiration check.
func (s *responsesStateStore) GetResponse(responseID, uidDigest string) (*bridge.ResponsesStateEntry, error) {
	// Lazy cleanup
	s.db.writeMu.Lock()
	s.clearExpiredLocked()
	s.db.writeMu.Unlock()

	var entry bridge.ResponsesStateEntry
	err := s.db.db.Get(&entry, `
		SELECT response_id, parent_id, uid_digest, status, input_json, output_json, response_json, instructions, warnings_json, created_at, expires_at
		FROM responses_state
		WHERE response_id = ?`,
		responseID,
	)
	if err == sql.ErrNoRows {
		return nil, bridge.ErrResponseNotFound
	}
	if err != nil {
		return nil, err
	}

	// Validate UID isolation
	if entry.UIDDigest != uidDigest {
		return nil, bridge.ErrResponseUIDMismatch
	}

	// Check expiration
	expiresAt, err := time.Parse(time.RFC3339Nano, entry.ExpiresAt)
	if err != nil {
		return nil, bridge.ErrResponseExpired
	}
	if time.Now().After(expiresAt) {
		return nil, bridge.ErrResponseExpired
	}

	// Check status
	if entry.Status != "completed" {
		return nil, bridge.ErrResponseNotCompleted
	}

	return &entry, nil
}

// WalkChain traverses the parent chain from a response ID upward.
// Returns entries in chronological order (oldest first).
// Returns ErrResponseNotFound if the initial responseID doesn't exist.
func (s *responsesStateStore) WalkChain(responseID, uidDigest string) ([]bridge.ResponsesStateEntry, error) {
	// Lazy cleanup
	s.db.writeMu.Lock()
	s.clearExpiredLocked()
	s.db.writeMu.Unlock()

	var chain []bridge.ResponsesStateEntry
	currentID := responseID
	visited := make(map[string]bool)
	isFirst := true

	for currentID != "" {
		// Prevent infinite loops - cycle detection
		if visited[currentID] {
			return nil, bridge.ErrChainIncomplete
		}
		visited[currentID] = true

		var entry bridge.ResponsesStateEntry
		err := s.db.db.Get(&entry, `
			SELECT response_id, parent_id, uid_digest, status, input_json, output_json, response_json, instructions, warnings_json, created_at, expires_at
			FROM responses_state
			WHERE response_id = ?`,
			currentID,
		)
		if err == sql.ErrNoRows {
			// If the first response doesn't exist, return error
			if isFirst {
				return nil, bridge.ErrResponseNotFound
			}
			// Intermediate parent is missing - chain is incomplete
			return nil, bridge.ErrChainIncomplete
		}
		if err != nil {
			return nil, err
		}
		isFirst = false

		// Validate UID isolation
		if entry.UIDDigest != uidDigest {
			return nil, bridge.ErrResponseUIDMismatch
		}

		// Check expiration
		expiresAt, err := time.Parse(time.RFC3339Nano, entry.ExpiresAt)
		if err != nil {
			return nil, bridge.ErrResponseExpired
		}
		if time.Now().After(expiresAt) {
			return nil, bridge.ErrResponseExpired
		}

		// Check status
		if entry.Status != "completed" {
			return nil, bridge.ErrResponseNotCompleted
		}

		chain = append(chain, entry)
		currentID = entry.ParentID
	}

	// Reverse to chronological order (oldest first)
	for i, j := 0, len(chain)-1; i < j; i, j = i+1, j-1 {
		chain[i], chain[j] = chain[j], chain[i]
	}

	return chain, nil
}

// ClearExpired removes expired response state entries.
func (s *responsesStateStore) ClearExpired() error {
	s.db.writeMu.Lock()
	defer s.db.writeMu.Unlock()
	return s.clearExpiredLocked()
}

// clearExpiredLocked removes expired entries (must be called with writeMu held).
func (s *responsesStateStore) clearExpiredLocked() error {
	now := time.Now().Format(time.RFC3339Nano)
	_, err := s.db.db.Exec("DELETE FROM responses_state WHERE expires_at < ?", now)
	return err
}

// ClearAll removes all response state entries.
func (s *responsesStateStore) ClearAll() error {
	s.db.writeMu.Lock()
	defer s.db.writeMu.Unlock()
	_, err := s.db.db.Exec("DELETE FROM responses_state")
	return err
}
