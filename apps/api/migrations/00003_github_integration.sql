-- +goose Up
-- GitHub integration: organizations, installations, repositories, and the
-- webhook delivery dedup ledger. Spec: docs/architecture/data-model.md,
-- docs/architecture/github-app.md. Tasks gain their organization and
-- repository foreign keys here (nullable since milestone 2).

CREATE TABLE organizations (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    name TEXT NOT NULL CHECK (char_length(name) BETWEEN 1 AND 255),
    slug TEXT NOT NULL UNIQUE CHECK (slug ~ '^[a-z0-9][a-z0-9-]{0,99}$'),
    github_account_id BIGINT NOT NULL UNIQUE CHECK (github_account_id > 0),
    github_account_login TEXT NOT NULL
        CHECK (char_length(github_account_login) BETWEEN 1 AND 255),
    github_account_type TEXT NOT NULL
        CHECK (github_account_type IN ('User', 'Organization')),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE github_installations (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    organization_id UUID NOT NULL REFERENCES organizations (id),
    github_installation_id BIGINT NOT NULL UNIQUE
        CHECK (github_installation_id > 0),
    account_login TEXT NOT NULL
        CHECK (char_length(account_login) BETWEEN 1 AND 255),
    account_type TEXT NOT NULL CHECK (account_type IN ('User', 'Organization')),
    permissions_json JSONB NOT NULL DEFAULT '{}',
    events_json JSONB NOT NULL DEFAULT '[]',
    suspended_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE repositories (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    organization_id UUID NOT NULL REFERENCES organizations (id),
    github_repository_id BIGINT NOT NULL UNIQUE
        CHECK (github_repository_id > 0),
    owner TEXT NOT NULL CHECK (char_length(owner) BETWEEN 1 AND 255),
    name TEXT NOT NULL CHECK (char_length(name) BETWEEN 1 AND 255),
    full_name TEXT NOT NULL CHECK (char_length(full_name) BETWEEN 3 AND 511),
    default_branch TEXT NOT NULL DEFAULT 'main'
        CHECK (char_length(default_branch) BETWEEN 1 AND 255),
    clone_url TEXT NOT NULL CHECK (char_length(clone_url) BETWEEN 1 AND 1000),
    is_private BOOLEAN NOT NULL DEFAULT false,
    is_enabled BOOLEAN NOT NULL DEFAULT true,
    settings_json JSONB NOT NULL DEFAULT '{}',
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX repositories_organization_idx ON repositories (organization_id);

-- Delivery ledger: the UNIQUE github_delivery_id is the replay guard. Only
-- signature-valid deliveries are recorded (a forged request must not be able
-- to occupy a delivery id); invalid signatures are counted in metrics.
CREATE TABLE github_webhook_deliveries (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    github_delivery_id TEXT NOT NULL UNIQUE
        CHECK (char_length(github_delivery_id) BETWEEN 1 AND 100),
    event_type TEXT NOT NULL CHECK (char_length(event_type) BETWEEN 1 AND 100),
    action TEXT CHECK (char_length(action) BETWEEN 1 AND 100),
    installation_id BIGINT CHECK (installation_id > 0),
    repository_id BIGINT CHECK (repository_id > 0),
    signature_valid BOOLEAN NOT NULL DEFAULT true,
    processing_status TEXT NOT NULL DEFAULT 'pending' CHECK
        (processing_status IN ('pending', 'processed', 'ignored', 'failed')),
    received_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    processed_at TIMESTAMPTZ,
    failure_message TEXT CHECK (char_length(failure_message) BETWEEN 1 AND 10000)
);

CREATE INDEX github_webhook_deliveries_received_idx
    ON github_webhook_deliveries (received_at DESC);

ALTER TABLE tasks
    ADD CONSTRAINT tasks_organization_id_fkey
        FOREIGN KEY (organization_id) REFERENCES organizations (id),
    ADD CONSTRAINT tasks_repository_id_fkey
        FOREIGN KEY (repository_id) REFERENCES repositories (id);

-- One active task per issue (docs/architecture/github-app.md): enforced at
-- the database so two concurrent commands cannot both create one.
CREATE UNIQUE INDEX tasks_one_active_per_issue_idx
    ON tasks (repository_id, source_issue_number)
    WHERE source_type = 'github_issue' AND phase <> 'terminal';

-- +goose Down
DROP INDEX tasks_one_active_per_issue_idx;
ALTER TABLE tasks
    DROP CONSTRAINT tasks_organization_id_fkey,
    DROP CONSTRAINT tasks_repository_id_fkey;
DROP TABLE github_webhook_deliveries;
DROP TABLE repositories;
DROP TABLE github_installations;
DROP TABLE organizations;
