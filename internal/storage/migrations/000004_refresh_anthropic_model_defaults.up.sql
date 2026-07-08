-- Refresh built-in Anthropic model defaults after older DashScope model keys were removed.
UPDATE settings
SET value = replace(replace(value, 'dashscope_qwen3_coder', 'gm51model'), 'dashscope_qwen_max_latest', 'gm51model')
WHERE key = 'anthropic_model_mapping'
  AND (value LIKE '%dashscope_qwen3_coder%' OR value LIKE '%dashscope_qwen_max_latest%');

UPDATE settings
SET value = 'dashscope_qmodel'
WHERE key = 'default_anthropic_model'
  AND value IN ('dashscope_qwen3_coder', 'dashscope_qwen_max_latest');
