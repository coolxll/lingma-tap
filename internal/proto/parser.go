package proto

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/coolxll/lingma-tap/internal/encoding"
)

// ParseRequest parses an HTTP request into a Record (C2S direction).
func ParseRequest(req *http.Request, body []byte) *Record {
	rec := &Record{
		Ts:        time.Now().Format(time.RFC3339Nano),
		Direction: "C2S",
		Method:    req.Method,
		URL:       req.URL.String(),
		Host:      req.Host,
		Path:      req.URL.Path,
	}

	// Parse headers
	rec.ReqHeaders = make(map[string]string)
	for k, v := range req.Header {
		rec.ReqHeaders[k] = strings.Join(v, ", ")
	}
	rec.ReqMime = req.Header.Get("Content-Type")
	rec.ReqSize = int64(len(body))

	// Check for Encode=1
	query := req.URL.Query()
	rec.IsEncoded = query.Get("Encode") == "1"
	rec.EndpointType = ClassifyEndpoint(req.URL.Path)

	// Store raw body
	if len(body) > 0 {
		rec.ReqBodyRaw = string(body)
	}

	// Decode if encoded
	if rec.IsEncoded && len(body) > 0 {
		decoded, err := encoding.Decode(string(body))
		if err == nil {
			rec.ReqBody = string(decoded)
		} else {
			rec.ReqBody = fmt.Sprintf("[decode error: %v]", err)
		}
	} else if len(body) > 0 {
		rec.ReqBody = string(body)
	}

	return rec
}

// ParseResponse parses an HTTP response into a Record (S2C direction).
func ParseResponse(resp *http.Response, body []byte, session string, index int) *Record {
	rec := &Record{
		Ts:         time.Now().Format(time.RFC3339Nano),
		Session:    session,
		Index:      index,
		Direction:  "S2C",
		Status:     resp.StatusCode,
		StatusText: resp.Status,
		Host:       resp.Request.Host,
		Path:       resp.Request.URL.Path,
		Method:     resp.Request.Method,
		URL:        resp.Request.URL.String(),
	}

	// Parse headers
	rec.RespHeaders = make(map[string]string)
	for k, v := range resp.Header {
		rec.RespHeaders[k] = strings.Join(v, ", ")
	}
	rec.RespMime = resp.Header.Get("Content-Type")
	rec.RespSize = int64(len(body))

	// Store response body
	if len(body) > 0 {
		rec.RespBody = string(body)
	}

	// Parse SSE if applicable
	if strings.Contains(rec.RespMime, "text/event-stream") && len(body) > 0 {
		rec.IsSSE = true
		rec.SSEEvents = ParseSSEEvents(string(body))
	}

	// Classify endpoint
	rec.EndpointType = ClassifyEndpoint(resp.Request.URL.Path)
	rec.IsEncoded = resp.Request.URL.Query().Get("Encode") == "1"

	return rec
}

// GenerateSessionID creates a random 12-character hex session ID.
func GenerateSessionID() string {
	b := make([]byte, 6)
	rand.Read(b)
	return hex.EncodeToString(b)
}

// ReadBody reads and returns the full body from an HTTP request or response.
func ReadBody(r io.ReadCloser) ([]byte, error) {
	defer r.Close()
	return io.ReadAll(r)
}

// ExtractHeaders converts http.Header to a flat string map.
func ExtractHeaders(h http.Header) map[string]string {
	m := make(map[string]string, len(h))
	for k, v := range h {
		m[k] = strings.Join(v, ", ")
	}
	return m
}

// HasEncodeParam checks if the URL contains Encode=1.
func HasEncodeParam(rawURL string) bool {
	u, err := url.Parse(rawURL)
	if err != nil {
		return false
	}
	return u.Query().Get("Encode") == "1"
}
