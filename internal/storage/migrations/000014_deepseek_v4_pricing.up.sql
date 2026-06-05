-- DeepSeek V4 official CNY pricing per https://api-docs.deepseek.com/zh-cn/quick_start/pricing
UPDATE pricing_rules
SET currency = 'CNY',
    input_price_per_million_tokens = 100000000,
    output_price_per_million_tokens = 200000000,
    cache_read_price_per_million_tokens = 2000000,
    cache_write_price_per_million_tokens = 100000000
WHERE provider_id = 'deepseek'
  AND model_alias IN ('deepseek-v4-flash', 'deepseek-chat', 'deepseek-reasoner');

UPDATE pricing_rules
SET currency = 'CNY',
    input_price_per_million_tokens = 300000000,
    output_price_per_million_tokens = 600000000,
    cache_read_price_per_million_tokens = 2500000,
    cache_write_price_per_million_tokens = 300000000
WHERE provider_id = 'deepseek'
  AND model_alias = 'deepseek-v4-pro';
