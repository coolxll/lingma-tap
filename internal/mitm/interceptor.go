package mitm

import (
	"fmt"
	"net/http"

	"github.com/coolxll/lingma-tap/internal/proto"
	"github.com/lqqyt2423/go-mitmproxy/proxy"
)

// OnRecordFunc is called when a traffic record is parsed.
type OnRecordFunc func(rec *proto.Record)

// MitmProxyAddon implements the go-mitmproxy Addon interface.
type MitmProxyAddon struct {
	proxy.BaseAddon
	onRecord OnRecordFunc
}

func NewMitmProxyAddon(onRecord OnRecordFunc) *MitmProxyAddon {
	return &MitmProxyAddon{
		onRecord: onRecord,
	}
}

// Response is called when a response is received from the server.
// At this point, both Request and Response are available in the flow.
func (a *MitmProxyAddon) Response(f *proxy.Flow) {
	// Generate a unique session ID for this flow.
	sessionID := proto.GenerateSessionID()

	// 1. Record Request
	req := f.Request.Raw()
	reqBody := f.Request.Body

	rec := proto.ParseRequest(req, reqBody)
	rec.Session = sessionID
	rec.Index = 0
	if rec.Host == "" {
		rec.Host = f.Request.URL.Host
	}

	if a.onRecord != nil {
		a.onRecord(rec)
	}

	// 2. Record Response
	// Reconstruct http.Response from go-mitmproxy Response
	resp := &http.Response{
		StatusCode: f.Response.StatusCode,
		Status:     fmt.Sprintf("%d %s", f.Response.StatusCode, http.StatusText(f.Response.StatusCode)),
		Header:     f.Response.Header,
		Request:    req,
	}
	respBody := f.Response.Body

	respRec := proto.ParseResponse(resp, respBody, sessionID, 1)

	if a.onRecord != nil {
		a.onRecord(respRec)
	}
}

// SSEEnd is called when an SSE stream finishes (normal close or context cancel).
// For SSE connections, Response() is never called because the body never fully
// arrives. SSEEnd gives us request + response headers + all received SSE events.
func (a *MitmProxyAddon) SSEEnd(f *proxy.Flow) {
	if a.onRecord == nil {
		return
	}
	if f.Request == nil {
		return
	}

	sessionID := proto.GenerateSessionID()

	// 1. Record the request
	req := f.Request.Raw()
	if req == nil {
		return
	}
	reqBody := f.Request.Body
	rec := proto.ParseRequest(req, reqBody)
	rec.Session = sessionID
	rec.Index = 0
	if rec.Host == "" && f.Request.URL != nil {
		rec.Host = f.Request.URL.Host
	}
	a.onRecord(rec)

	// 2. Record the SSE response
	var statusCode int
	var respHeader http.Header
	if f.Response != nil {
		statusCode = f.Response.StatusCode
		respHeader = f.Response.Header
	}
	resp := &http.Response{
		StatusCode: statusCode,
		Status:     fmt.Sprintf("%d %s", statusCode, http.StatusText(statusCode)),
		Header:     respHeader,
		Request:    req,
	}

	// Combine all SSE event data into a single body for storage.
	var body []byte
	if f.SSE != nil && len(f.SSE.Events) > 0 {
		for _, evt := range f.SSE.Events {
			body = append(body, []byte(evt.Raw)...)
			body = append(body, '\n')
		}
	}

	respRec := proto.ParseResponse(resp, body, sessionID, 1)
	a.onRecord(respRec)
}

// RequestError is called when an error occurs during the request (e.g., context canceled, connection error).
func (a *MitmProxyAddon) RequestError(f *proxy.Flow, err error) {
	if a.onRecord == nil {
		return
	}

	sessionID := proto.GenerateSessionID()

	// Connection-level error before any request was attached to the flow.
	if f.Request == nil {
		a.onRecord(&proto.Record{
			Session: sessionID,
			Error:   err.Error(),
		})
		return
	}

	req := f.Request.Raw()
	if req == nil {
		// Request URL parsed but raw request never built (e.g. handshake failure).
		host := ""
		if f.Request.URL != nil {
			host = f.Request.URL.Host
		}
		a.onRecord(&proto.Record{
			Session: sessionID,
			Host:    host,
			Error:   err.Error(),
		})
		return
	}

	reqBody := f.Request.Body
	rec := proto.ParseRequest(req, reqBody)
	rec.Session = sessionID
	rec.Index = 0
	if rec.Host == "" && f.Request.URL != nil {
		rec.Host = f.Request.URL.Host
	}
	rec.Error = err.Error()

	a.onRecord(rec)
}
