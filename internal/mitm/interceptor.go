package mitm

import (
	"fmt"
	"net/http"
	"strings"

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

// contains checks if a string contains a substring (case-insensitive).
func contains(s, substr string) bool {
	return strings.Contains(strings.ToLower(s), strings.ToLower(substr))
}
