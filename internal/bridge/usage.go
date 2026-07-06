package bridge

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/coolxll/lingma-tap/internal/proto"
)

const statusClientClosedRequest = 499

func applyUsageToGatewayLog(gLog *proto.GatewayLog, usage *Usage) {
	if gLog == nil || usage == nil {
		return
	}
	usage.Consolidate()
	gLog.InputTokens = usage.PromptTokens
	gLog.OutputTokens = usage.CompletionTokens
	gLog.CachedTokens = usage.CachedTokens
	gLog.ReasoningTokens = usage.ReasoningTokens
	gLog.TotalTokens = usage.TotalTokens
}

func applyFinishEvent(gLog *proto.GatewayLog, event SSEEvent) {
	if gLog == nil {
		return
	}
	if event.Usage != nil {
		applyUsageToGatewayLog(gLog, event.Usage)
	}
	if event.FirstTokenDuration > 0 {
		gLog.TTFT = int64(event.FirstTokenDuration)
	}
}

func recordStreamError(ctx context.Context, gLog *proto.GatewayLog, startTime time.Time, err error, recorder func(*proto.GatewayLog)) bool {
	if recordContextError(ctx, gLog, startTime, err, recorder) {
		return true
	}
	gLog.Error = err.Error()
	gLog.Status = 500
	if recorder != nil {
		recorder(gLog)
	}
	return false
}

func isContextCanceled(ctx context.Context, err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, context.Canceled) {
		return true
	}
	if ctx != nil && errors.Is(ctx.Err(), context.Canceled) {
		return true
	}
	return strings.Contains(strings.ToLower(err.Error()), "context canceled")
}

func isContextDeadlineExceeded(ctx context.Context, err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return true
	}
	if ctx != nil && errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return true
	}
	return strings.Contains(strings.ToLower(err.Error()), "context deadline exceeded")
}

func recordContextError(ctx context.Context, gLog *proto.GatewayLog, startTime time.Time, err error, recorder func(*proto.GatewayLog)) bool {
	if gLog == nil || err == nil {
		return false
	}
	switch {
	case isContextCanceled(ctx, err):
		gLog.Error = "client canceled request"
		gLog.Status = statusClientClosedRequest
	case isContextDeadlineExceeded(ctx, err):
		gLog.Error = "request deadline exceeded"
		gLog.Status = http.StatusGatewayTimeout
	default:
		return false
	}
	gLog.Latency = time.Since(startTime).Milliseconds()
	if recorder != nil {
		recorder(gLog)
	}
	return true
}
