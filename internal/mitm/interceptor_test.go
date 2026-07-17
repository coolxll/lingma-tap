package mitm

import (
	"bytes"
	"errors"
	"io"
	"net/http"
	"testing"

	"github.com/coolxll/lingma-tap/internal/proto"
	"github.com/lqqyt2423/go-mitmproxy/proxy"
	uuid "github.com/satori/go.uuid"
)

func TestBodyCaptureSpoolsAndBoundsMemory(t *testing.T) {
	capture := &bodyCapture{}
	input := bytes.Repeat([]byte("x"), proto.BodyCaptureLimit+1024)
	capture.Write(input)
	body, total, truncated := capture.Finish()
	if total != int64(len(input)) {
		t.Fatalf("total=%d want=%d", total, len(input))
	}
	if !truncated || len(body) != proto.BodyCaptureLimit {
		t.Fatalf("truncated=%v body=%d", truncated, len(body))
	}
	if !bytes.Equal(body, input[:proto.BodyCaptureLimit]) {
		t.Fatal("captured prefix differs from input")
	}
}

func TestCaptureReaderFinalizesOnEOF(t *testing.T) {
	capture := &bodyCapture{}
	completed := false
	reader := &captureReader{
		reader: bytes.NewReader([]byte("hello")), capture: capture,
		onDone: func(ok bool) { completed = ok; _, _, _ = capture.Finish() },
	}
	if _, err := io.ReadAll(reader); err != nil {
		t.Fatal(err)
	}
	if !completed {
		t.Fatal("capture reader did not finalize as complete")
	}
}

func TestRequestErrorRemovesRequestOnlyFlow(t *testing.T) {
	var records []*proto.Record
	addon := NewMitmProxyAddon(func(rec *proto.Record) { records = append(records, rec) })
	flow := &proxy.Flow{Id: uuid.NewV4()}
	req, err := http.NewRequest(http.MethodPost, "https://example.test/v1/chat", nil)
	if err != nil {
		t.Fatal(err)
	}
	state := &flowState{session: flowKey(flow), reqHTTP: req}
	addon.flows[state.session] = state

	addon.RequestError(flow, errors.New("connection refused"))
	if _, ok := addon.flows[state.session]; ok {
		t.Fatal("request-only failed flow was not removed")
	}
	if len(records) == 0 || records[len(records)-1].Error != "connection refused" {
		t.Fatalf("request error was not recorded: %+v", records)
	}
}

func TestClientDisconnectFinalizesIncompleteFlow(t *testing.T) {
	var records []*proto.Record
	addon := NewMitmProxyAddon(func(rec *proto.Record) { records = append(records, rec) })
	flow := &proxy.Flow{Id: uuid.NewV4()}
	req, err := http.NewRequest(http.MethodPost, "https://example.test/v1/chat", nil)
	if err != nil {
		t.Fatal(err)
	}
	client := &proxy.ClientConn{}
	state := &flowState{session: flowKey(flow), reqHTTP: req, clientConn: client}
	addon.flows[state.session] = state

	addon.ClientDisconnected(client)
	if _, ok := addon.flows[state.session]; ok {
		t.Fatal("disconnected flow was not removed")
	}
	if len(records) == 0 || records[len(records)-1].BodyComplete || records[len(records)-1].Error != "downstream disconnected" {
		t.Fatalf("disconnect was not recorded as incomplete: %+v", records)
	}
}

func TestConnectFlowIsIgnored(t *testing.T) {
	addon := NewMitmProxyAddon(nil)
	flow := &proxy.Flow{Id: uuid.NewV4(), Request: &proxy.Request{Method: http.MethodConnect}}
	addon.Requestheaders(flow)
	addon.Request(flow)
	if got := addon.StreamRequestModifier(flow, bytes.NewReader([]byte("tunnel"))); got == nil {
		t.Fatal("CONNECT request modifier returned nil")
	}
	addon.Responseheaders(flow)
	addon.Response(flow)
	if got := addon.StreamResponseModifier(flow, bytes.NewReader([]byte("tunnel"))); got == nil {
		t.Fatal("CONNECT response modifier returned nil")
	}
	addon.SSEEnd(flow)
	addon.RequestError(flow, errors.New("ignored"))
	if len(addon.flows) != 0 {
		t.Fatalf("CONNECT handshake polluted flow map: %d", len(addon.flows))
	}
}

func TestSSEEndDoesNotMarkResponseComplete(t *testing.T) {
	addon := NewMitmProxyAddon(nil)
	flow := &proxy.Flow{Id: uuid.NewV4(), Request: &proxy.Request{Method: http.MethodPost}}
	state := &flowState{session: flowKey(flow), respCapture: &bodyCapture{}}
	addon.flows[state.session] = state
	addon.SSEEnd(flow)
	if state.respFinalized {
		t.Fatal("SSEEnd finalized a response before the stream reader reported EOF")
	}
}
