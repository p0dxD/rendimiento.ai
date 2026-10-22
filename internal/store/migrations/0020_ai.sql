-- The AI gateway (internal/ia): each organization's choice of provider and
-- its monthly cap, and every call measured.
CREATE TABLE IF NOT EXISTS ai_settings (
    org_id      BIGINT PRIMARY KEY REFERENCES orgs(id),
    provider    TEXT NOT NULL DEFAULT 'local' CHECK (provider IN ('local', 'own', 'platform')),
    kind        TEXT NOT NULL DEFAULT '',   -- with provider 'own': anthropic or openai
    base_url    TEXT NOT NULL DEFAULT '',   -- openai: the API's address
    model       TEXT NOT NULL DEFAULT '',   -- '' for the provider's default
    cap_micros  BIGINT NOT NULL DEFAULT 1000000,  -- the organization's monthly cap (USD millionths)
    allowance_micros BIGINT NOT NULL DEFAULT 0,   -- what the operator lets it spend of the platform's key
    updated_by  TEXT NOT NULL DEFAULT '',
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS ai_usage (
    id            BIGSERIAL PRIMARY KEY,
    org_id        BIGINT NOT NULL REFERENCES orgs(id),
    at            TIMESTAMPTZ NOT NULL DEFAULT now(),
    source        TEXT NOT NULL,   -- who asked: a feature, or an agent key's name
    provider      TEXT NOT NULL,   -- local, own or platform: who pays
    model         TEXT NOT NULL,
    input_tokens  BIGINT NOT NULL DEFAULT 0,
    output_tokens BIGINT NOT NULL DEFAULT 0,
    cost_micros   BIGINT NOT NULL DEFAULT 0,
    ok            BOOLEAN NOT NULL,
    note          TEXT NOT NULL DEFAULT ''   -- an error, or why it fell back
);
CREATE INDEX IF NOT EXISTS ai_usage_org_at ON ai_usage (org_id, at DESC);
CREATE INDEX IF NOT EXISTS ai_usage_provider_at ON ai_usage (provider, at);
