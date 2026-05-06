CREATE TABLE IF NOT EXISTS settings (
    key   TEXT PRIMARY KEY,
    value TEXT NOT NULL
);

-- Insert default Anthropic model mapping
INSERT OR IGNORE INTO settings (key, value) VALUES ('anthropic_model_mapping', '{"sonnet": "dashscope_qwen3_coder", "haiku": "dashscope_qmodel", "opus": "dashscope_qwen_max_latest"}');

-- Insert default fallback model
INSERT OR IGNORE INTO settings (key, value) VALUES ('default_anthropic_model', 'dashscope_qmodel');
