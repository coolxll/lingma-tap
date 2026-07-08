package bridge

const DefaultAnthropicModel = "dashscope_qmodel"

// DefaultAnthropicModelMapping returns the built-in Claude-family keyword mapping.
// Keep this in one place so desktop, server, and tests do not drift.
func DefaultAnthropicModelMapping() map[string]string {
	return map[string]string{
		"sonnet": "gm51model",
		"haiku":  DefaultAnthropicModel,
		"opus":   "gm51model",
	}
}
