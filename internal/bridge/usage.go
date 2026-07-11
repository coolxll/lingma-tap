package bridge

import (
	"context"
	"errors"
	"io"
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
	gLog.Error = normalizeLingmaUpstreamError(err)
	gLog.Status = statusForLingmaUpstreamError(err)
	gLog.Latency = time.Since(startTime).Milliseconds()
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

func gatewayLogHasCompletedResult(gLog *proto.GatewayLog) bool {
	if gLog == nil {
		return false
	}
	if gLog.Status >= http.StatusOK && gLog.Status < http.StatusMultipleChoices {
		return true
	}
	return strings.TrimSpace(gLog.FinishReason) != ""
}

func recordContextError(ctx context.Context, gLog *proto.GatewayLog, startTime time.Time, err error, recorder func(*proto.GatewayLog)) bool {
	if gLog == nil || err == nil {
		return false
	}
	switch {
	case isContextCanceled(ctx, err):
		if gatewayLogHasCompletedResult(gLog) {
			gLog.Error = ""
			gLog.Status = http.StatusOK
		} else {
			gLog.Error = "client canceled request"
			gLog.Status = statusClientClosedRequest
		}
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

func statusForLingmaUpstreamError(err error) int {
	var upstreamErr lingmaUpstreamEventError
	if errors.As(err, &upstreamErr) {
		return http.StatusBadGateway
	}
	if isLingmaUpstreamEOF(err) {
		return http.StatusBadGateway
	}
	return http.StatusInternalServerError
}

func normalizeLingmaUpstreamError(err error) string {
	var upstreamErr lingmaUpstreamEventError
	if errors.As(err, &upstreamErr) {
		return upstreamErr.Error()
	}
	if isLingmaUpstreamEOF(err) {
		return "lingma upstream connection closed before [DONE]"
	}
	if err == nil {
		return ""
	}
	return err.Error()
}

func isLingmaUpstreamEOF(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
		return true
	}
	return strings.Contains(strings.ToLower(err.Error()), "unexpected eof")
}
