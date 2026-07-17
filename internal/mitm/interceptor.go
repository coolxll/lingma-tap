package mitm

import (
	"bytes"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"sync"
	"unicode/utf8"

	"github.com/coolxll/lingma-tap/internal/proto"
	"github.com/lqqyt2423/go-mitmproxy/proxy"
)

const bodySpoolMemoryLimit = 1 * 1024 * 1024

// OnRecordFunc is called whenever a flow changes lifecycle state. Callers
// should treat records with the same (session,index) as upserts.
type OnRecordFunc func(rec *proto.Record)

type flowState struct {
	session       string
	reqHTTP       *http.Request
	respHTTP      *http.Response
	req           *proto.Record
	resp          *proto.Record
	reqCapture    *bodyCapture
	respCapture   *bodyCapture
	reqFinalized  bool
	respFinalized bool
	clientConn    *proxy.ClientConn
	serverConn    *proxy.ServerConn
}

// bodyCapture bounds memory while retaining the first BodyCaptureLimit bytes.
// Once the in-memory threshold is reached it spills to a temporary file so a
// set of concurrent uploads cannot multiply the memory footprint.
type bodyCapture struct {
	mu        sync.Mutex
	buf       bytes.Buffer
	file      *os.File
	total     int64
	truncated bool
	closed    bool
}

func (c *bodyCapture) Write(p []byte) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return
	}
	c.total += int64(len(p))
	remaining := proto.BodyCaptureLimit - c.capturedLocked()
	if remaining <= 0 {
		c.truncated = true
		return
	}
	if int64(len(p)) > remaining {
		p = p[:remaining]
		c.truncated = true
	}
	if c.file == nil && c.buf.Len()+len(p) > bodySpoolMemoryLimit {
		file, err := os.CreateTemp("", "lingma-tap-body-*")
		if err == nil {
			_, _ = file.Write(c.buf.Bytes())
			c.buf.Reset()
			c.file = file
		}
	}
	if c.file != nil {
		if _, err := c.file.Write(p); err != nil {
			c.truncated = true
		}
	} else {
		_, _ = c.buf.Write(p)
	}
}

func (c *bodyCapture) capturedLocked() int64 {
	if c.file != nil {
		pos, _ := c.file.Seek(0, io.SeekCurrent)
		return pos
	}
	return int64(c.buf.Len())
}

func (c *bodyCapture) Finish() ([]byte, int64, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return nil, c.total, c.truncated
	}
	c.closed = true
	var body []byte
	if c.file != nil {
		_, _ = c.file.Seek(0, io.SeekStart)
		body, _ = io.ReadAll(io.LimitReader(c.file, proto.BodyCaptureLimit))
		name := c.file.Name()
		_ = c.file.Close()
		_ = os.Remove(name)
	} else {
		body = append([]byte(nil), c.buf.Bytes()...)
	}
	return body, c.total, c.truncated
}

type captureReader struct {
	reader  io.Reader
	closer  io.Closer
	capture *bodyCapture
	onDone  func(complete bool)
	done    sync.Once
}

func (r *captureReader) Read(p []byte) (int, error) {
	n, err := r.reader.Read(p)
	if n > 0 {
		r.capture.Write(p[:n])
	}
	if err != nil {
		r.finish(err == io.EOF)
	}
	return n, err
}

func (r *captureReader) Close() error {
	if r.closer != nil {
		_ = r.closer.Close()
	}
	r.finish(false)
	return nil
}

func (r *captureReader) finish(complete bool) {
	r.done.Do(func() { r.onDone(complete) })
}

// MitmProxyAddon implements the go-mitmproxy Addon interface.
type MitmProxyAddon struct {
	proxy.BaseAddon
	onRecord OnRecordFunc
	mu       sync.Mutex
	flows    map[string]*flowState
}

func NewMitmProxyAddon(onRecord OnRecordFunc) *MitmProxyAddon {
	return &MitmProxyAddon{onRecord: onRecord, flows: make(map[string]*flowState)}
}

func flowKey(f *proxy.Flow) string {
	return f.Id.String()
}

func (a *MitmProxyAddon) getFlow(f *proxy.Flow) *flowState {
	key := flowKey(f)
	a.mu.Lock()
	defer a.mu.Unlock()
	if state := a.flows[key]; state != nil {
		if f.ConnContext != nil {
			state.clientConn = f.ConnContext.ClientConn
			state.serverConn = f.ConnContext.ServerConn
		}
		return state
	}
	state := &flowState{session: key}
	if f.ConnContext != nil {
		state.clientConn = f.ConnContext.ClientConn
		state.serverConn = f.ConnContext.ServerConn
	}
	a.flows[key] = state
	return state
}

func isConnectFlow(f *proxy.Flow) bool {
	return f != nil && f.Request != nil && strings.EqualFold(f.Request.Method, http.MethodConnect)
}

func (a *MitmProxyAddon) emit(rec *proto.Record) {
	if rec != nil && a.onRecord != nil {
		a.onRecord(rec)
	}
}

// Requestheaders makes a request visible before its body or response exists.
func (a *MitmProxyAddon) Requestheaders(f *proxy.Flow) {
	if isConnectFlow(f) {
		return
	}
	if f.Request == nil || f.Request.Raw() == nil {
		return
	}
	state := a.getFlow(f)
	req := f.Request.Raw()
	rec := proto.ParseRequest(req, nil)
	rec.Session = state.session
	rec.Index = 0
	rec.BodyPhase = "headers"
	rec.DeclaredSize = req.ContentLength
	if rec.DeclaredSize < 0 {
		rec.DeclaredSize = 0
	}
	a.mu.Lock()
	state.reqHTTP = req
	state.req = rec
	a.mu.Unlock()
	a.emit(rec)
}

// Request handles bodies that go-mitmproxy buffered below its streaming limit.
func (a *MitmProxyAddon) Request(f *proxy.Flow) {
	if isConnectFlow(f) {
		return
	}
	state := a.getFlow(f)
	if f.Request == nil {
		return
	}
	a.finishRequest(state, f.Request.Body, true, "complete")
}

func (a *MitmProxyAddon) StreamRequestModifier(f *proxy.Flow, in io.Reader) io.Reader {
	if isConnectFlow(f) {
		return in
	}
	state := a.getFlow(f)
	a.mu.Lock()
	done := state.reqFinalized
	a.mu.Unlock()
	if done {
		return in
	}
	capture := &bodyCapture{}
	a.mu.Lock()
	state.reqCapture = capture
	a.mu.Unlock()
	var closer io.Closer
	if c, ok := in.(io.Closer); ok {
		closer = c
	}
	return &captureReader{reader: in, closer: closer, capture: capture, onDone: func(complete bool) {
		body, total, truncated := capture.Finish()
		phase := "complete"
		if !complete {
			phase = "error"
		}
		a.finishRequestBytes(state, body, total, truncated, complete, phase)
	}}
}

func (a *MitmProxyAddon) finishRequest(state *flowState, body []byte, complete bool, phase string) {
	if body == nil {
		body = []byte{}
	}
	a.finishRequestBytes(state, body, int64(len(body)), false, complete, phase)
}

func (a *MitmProxyAddon) finishRequestBytes(state *flowState, body []byte, total int64, truncated bool, complete bool, phase string) {
	a.mu.Lock()
	if state.reqFinalized || state.reqHTTP == nil {
		a.mu.Unlock()
		return
	}
	state.reqFinalized = true
	rec := proto.ParseRequest(state.reqHTTP, body)
	rec.Session = state.session
	rec.Index = 0
	rec.ReqSize = total
	rec.ReqBodyBlob = append([]byte(nil), body...)
	rec.BodyPhase = phase
	rec.BodyComplete = complete
	rec.BodyTruncated = truncated
	rec.CapturedSize = int64(len(body))
	rec.DeclaredSize = state.reqHTTP.ContentLength
	if rec.DeclaredSize < 0 {
		rec.DeclaredSize = 0
	}
	rec.BodyEncoding = bodyEncoding(body)
	state.req = rec
	a.mu.Unlock()
	a.emit(rec)
}

// Responseheaders creates a response record before its potentially large body.
func (a *MitmProxyAddon) Responseheaders(f *proxy.Flow) {
	if isConnectFlow(f) {
		return
	}
	state := a.getFlow(f)
	if f.Response == nil {
		return
	}
	a.mu.Lock()
	reqHTTP := state.reqHTTP
	a.mu.Unlock()
	if reqHTTP == nil {
		return
	}
	resp := &http.Response{
		StatusCode: f.Response.StatusCode,
		Status:     fmt.Sprintf("%d %s", f.Response.StatusCode, http.StatusText(f.Response.StatusCode)),
		Header:     f.Response.Header,
		Request:    reqHTTP,
	}
	rec := proto.ParseResponse(resp, nil, state.session, 1)
	rec.BodyPhase = "headers"
	rec.DeclaredSize = resp.ContentLength
	if rec.DeclaredSize < 0 {
		rec.DeclaredSize = 0
	}
	a.mu.Lock()
	state.respHTTP = resp
	state.resp = rec
	a.mu.Unlock()
	a.emit(rec)
}

// Response handles responses buffered below go-mitmproxy's streaming limit.
func (a *MitmProxyAddon) Response(f *proxy.Flow) {
	if isConnectFlow(f) {
		return
	}
	state := a.getFlow(f)
	if f.Response == nil {
		return
	}
	a.finishResponse(state, f.Response.Body, true, "complete")
}

func (a *MitmProxyAddon) StreamResponseModifier(f *proxy.Flow, in io.Reader) io.Reader {
	if isConnectFlow(f) {
		return in
	}
	state := a.getFlow(f)
	a.mu.Lock()
	done := state.respFinalized
	a.mu.Unlock()
	if done {
		return in
	}
	capture := &bodyCapture{}
	a.mu.Lock()
	state.respCapture = capture
	a.mu.Unlock()
	var closer io.Closer
	if c, ok := in.(io.Closer); ok {
		closer = c
	}
	return &captureReader{reader: in, closer: closer, capture: capture, onDone: func(complete bool) {
		body, total, truncated := capture.Finish()
		phase := "complete"
		if !complete {
			phase = "error"
		}
		a.finishResponseBytes(state, body, total, truncated, complete, phase)
	}}
}

func (a *MitmProxyAddon) finishResponse(state *flowState, body []byte, complete bool, phase string) {
	if body == nil {
		body = []byte{}
	}
	a.finishResponseBytes(state, body, int64(len(body)), false, complete, phase)
}

func (a *MitmProxyAddon) finishResponseBytes(state *flowState, body []byte, total int64, truncated bool, complete bool, phase string) {
	a.mu.Lock()
	if state.respFinalized || state.respHTTP == nil {
		a.mu.Unlock()
		return
	}
	state.respFinalized = true
	rec := proto.ParseResponse(state.respHTTP, body, state.session, 1)
	rec.RespSize = total
	rec.RespBodyBlob = append([]byte(nil), body...)
	rec.BodyPhase = phase
	rec.BodyComplete = complete
	rec.BodyTruncated = truncated
	rec.CapturedSize = int64(len(body))
	rec.DeclaredSize = state.respHTTP.ContentLength
	if rec.DeclaredSize < 0 {
		rec.DeclaredSize = 0
	}
	rec.BodyEncoding = bodyEncoding(body)
	state.resp = rec
	a.mu.Unlock()
	a.emit(rec)
	a.cleanupIfDone(state)
}

// SSEEnd is only a notification. The stream wrapper owns finalization so an
// SSE read error is persisted as incomplete instead of being mistaken for EOF.
func (a *MitmProxyAddon) SSEEnd(f *proxy.Flow) {
	if isConnectFlow(f) {
		return
	}
}

func (a *MitmProxyAddon) RequestError(f *proxy.Flow, err error) {
	if isConnectFlow(f) {
		return
	}
	state := a.getFlow(f)
	if f.Request != nil && f.Request.Raw() != nil {
		a.mu.Lock()
		if state.reqHTTP == nil {
			state.reqHTTP = f.Request.Raw()
		}
		a.mu.Unlock()
	}
	a.mu.Lock()
	reqHTTP := state.reqHTTP
	a.mu.Unlock()
	message := "request failed"
	if err != nil {
		message = err.Error()
	}
	if reqHTTP == nil {
		a.emit(&proto.Record{Session: state.session, Error: message, BodyPhase: "error"})
		a.removeFlow(state)
		return
	}
	a.finalizeError(state, message)
}

func (a *MitmProxyAddon) finalizeError(state *flowState, message string) {
	a.mu.Lock()
	reqDone := state.reqFinalized
	respDone := state.respFinalized
	reqCapture := state.reqCapture
	respCapture := state.respCapture
	respHTTP := state.respHTTP
	a.mu.Unlock()
	if !reqDone {
		if reqCapture != nil {
			body, total, truncated := reqCapture.Finish()
			a.finishRequestBytes(state, body, total, truncated, false, "error")
		} else {
			a.finishRequest(state, nil, false, "error")
		}
	}
	if !respDone && respHTTP != nil {
		if respCapture != nil {
			body, total, truncated := respCapture.Finish()
			a.finishResponseBytes(state, body, total, truncated, false, "error")
		} else {
			a.finishResponse(state, nil, false, "error")
		}
	}
	// Attach the transport error to the latest record without creating a second
	// flow. The storage layer upserts by (session,index).
	a.mu.Lock()
	var rec *proto.Record
	if state.resp != nil {
		rec = state.resp
	} else {
		rec = state.req
	}
	if rec != nil {
		rec.Error = message
		rec.BodyPhase = "error"
		rec.BodyComplete = false
	}
	a.mu.Unlock()
	a.emit(rec)
	a.cleanupIfDone(state)
}

func (a *MitmProxyAddon) finalizeDisconnected(state *flowState, message string) {
	a.finalizeError(state, message)
}

func (a *MitmProxyAddon) ClientDisconnected(client *proxy.ClientConn) {
	a.mu.Lock()
	states := make([]*flowState, 0)
	for _, state := range a.flows {
		if state.clientConn == client {
			states = append(states, state)
		}
	}
	a.mu.Unlock()
	for _, state := range states {
		a.finalizeDisconnected(state, "downstream disconnected")
	}
}

func (a *MitmProxyAddon) ServerConnected(conn *proxy.ConnContext) {
	if conn == nil {
		return
	}
	a.mu.Lock()
	for _, state := range a.flows {
		if state.clientConn == conn.ClientConn {
			state.serverConn = conn.ServerConn
		}
	}
	a.mu.Unlock()
}

func (a *MitmProxyAddon) ServerDisconnected(conn *proxy.ConnContext) {
	if conn == nil {
		return
	}
	a.mu.Lock()
	states := make([]*flowState, 0)
	for _, state := range a.flows {
		if state.serverConn != nil && state.serverConn == conn.ServerConn {
			states = append(states, state)
		}
	}
	a.mu.Unlock()
	for _, state := range states {
		a.finalizeDisconnected(state, "upstream disconnected")
	}
}

func (a *MitmProxyAddon) removeFlow(state *flowState) {
	a.mu.Lock()
	defer a.mu.Unlock()
	delete(a.flows, state.session)
}

func (a *MitmProxyAddon) cleanupIfDone(state *flowState) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if state.reqFinalized && (state.respFinalized || state.respHTTP == nil) {
		delete(a.flows, state.session)
	}
}

func bodyEncoding(body []byte) string {
	if len(body) == 0 {
		return "empty"
	}
	if utf8.Valid(body) {
		return "text"
	}
	return "binary"
}
