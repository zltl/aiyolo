ALTER TABLE usage_ledger ALTER COLUMN currency SET DEFAULT 'USD';
ALTER TABLE pricing_rules ALTER COLUMN currency SET DEFAULT 'USD';

UPDATE pricing_rules
SET currency = 'USD',
    input_price_per_million_tokens = input_price_per_million_tokens / 8,
    output_price_per_million_tokens = output_price_per_million_tokens / 8,
    cache_read_price_per_million_tokens = cache_read_price_per_million_tokens / 8,
    cache_write_price_per_million_tokens = cache_write_price_per_million_tokens / 8
WHERE upper(currency) = 'CNY' AND provider_id <> 'deepseek';

UPDATE usage_ledger
SET currency = 'USD',
    cost_micro_cents = cost_micro_cents / 8
WHERE upper(currency) = 'CNY' AND provider_id <> 'deepseek';

UPDATE api_keys
SET daily_budget_cents = daily_budget_cents / 8,
    monthly_budget_cents = monthly_budget_cents / 8
WHERE daily_budget_cents <> 0 OR monthly_budget_cents <> 0;