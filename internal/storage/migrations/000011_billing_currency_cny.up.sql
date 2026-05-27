ALTER TABLE usage_ledger ALTER COLUMN currency SET DEFAULT 'CNY';
ALTER TABLE pricing_rules ALTER COLUMN currency SET DEFAULT 'CNY';

UPDATE pricing_rules
SET currency = 'CNY',
    input_price_per_million_tokens = input_price_per_million_tokens * 8,
    output_price_per_million_tokens = output_price_per_million_tokens * 8,
    cache_read_price_per_million_tokens = cache_read_price_per_million_tokens * 8,
    cache_write_price_per_million_tokens = cache_write_price_per_million_tokens * 8
WHERE upper(currency) = 'USD';

UPDATE usage_ledger
SET currency = 'CNY',
    cost_micro_cents = cost_micro_cents * 8
WHERE upper(currency) = 'USD';

UPDATE api_keys
SET daily_budget_cents = daily_budget_cents * 8,
    monthly_budget_cents = monthly_budget_cents * 8
WHERE daily_budget_cents <> 0 OR monthly_budget_cents <> 0;