package bridge

import (
	"context"
	"errors"
	"io"
	"testing"
	"time"

	"github.com/coolxll/lingma-tap/internal/proto"
)

func TestRecordContextError_ClientCanceled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	log := &proto.GatewayLog{}
	var recorded *proto.GatewayLog
	if !recordContextError(ctx, log, time.Now(), context.Canceled, func(g *proto.GatewayLog) {
		recorded = g
	}) {
		t.Fatal("expected context cancellation to be handled")
	}
	if recorded == nil {
		t.Fatal("expected recorder to be called")
	}
	if log.Status != statusClientClosedRequest {
		t.Fatalf("status = %d, want %d", log.Status, statusClientClosedRequest)
	}
	if log.Error != "client canceled request" {
		t.Fatalf("error = %q", log.Error)
	}
}

func TestRecordContextError_DeadlineExceeded(t *testing.T) {
	log := &proto.GatewayLog{}
	if !recordContextError(context.Background(), log, time.Now(), context.DeadlineExceeded, nil) {
		t.Fatal("expected deadline exceeded to be handled")
	}
	if log.Status != 504 {
		t.Fatalf("status = %d, want 504", log.Status)
	}
}

func TestRecordContextError_OtherError(t *testing.T) {
	log := &proto.GatewayLog{}
	if recordContextError(context.Background(), log, time.Now(), errors.New("boom"), nil) {
		t.Fatal("did not expect unrelated error to be handled")
	}
}

func TestNormalizeLingmaUpstreamError(t *testing.T) {
	if got := normalizeLingmaUpstreamError(io.ErrUnexpectedEOF); got != "lingma upstream connection closed before [DONE]" {
		t.Fatalf("normalizeLingmaUpstreamError = %q", got)
	}
	if got := statusForLingmaUpstreamError(io.ErrUnexpectedEOF); got != 502 {
		t.Fatalf("statusForLingmaUpstreamError = %d, want 502", got)
	}
}
