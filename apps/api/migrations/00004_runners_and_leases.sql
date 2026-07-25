-- +goose Up
-- Runner registry and task-attempt leasing. Spec: docs/architecture/runner.md
-- (task leasing), docs/architecture/data-model.md (runner). Claiming is
-- FOR UPDATE SKIP LOCKED against task_attempts (ADR-0003); a lease expires
-- unless the owning runner extends it, so a lost runner's attempt becomes
-- claimable again without immediate duplicate execution.

CREATE TABLE runners (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    runner_type TEXT NOT NULL
        CHECK (runner_type IN ('process', 'docker', 'kubernetes')),
    hostname_or_pod TEXT NOT NULL
        CHECK (char_length(hostname_or_pod) BETWEEN 1 AND 255),
    status TEXT NOT NULL DEFAULT 'online'
        CHECK (status IN ('online', 'lost', 'offline')),
    capacity INTEGER NOT NULL DEFAULT 1 CHECK (capacity >= 1),
    labels_json JSONB NOT NULL DEFAULT '{}',
    last_heartbeat_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- Reaper scan: online runners ordered by heartbeat age.
CREATE INDEX runners_status_heartbeat_idx
    ON runners (status, last_heartbeat_at);

-- runner_id records which runner ran the attempt (kept after the lease is
-- released); lease_owner + lease_expires_at are the live lease. heartbeat_at
-- is the last lease extension, for diagnostics.
ALTER TABLE task_attempts
    ADD CONSTRAINT task_attempts_runner_id_fkey
        FOREIGN KEY (runner_id) REFERENCES runners (id),
    ADD COLUMN lease_owner UUID REFERENCES runners (id),
    ADD COLUMN lease_expires_at TIMESTAMPTZ,
    ADD COLUMN heartbeat_at TIMESTAMPTZ,
    ADD CONSTRAINT task_attempts_lease_pair CHECK (
        (lease_owner IS NULL) = (lease_expires_at IS NULL));

-- Claim scan: active attempts by lease expiry.
CREATE INDEX task_attempts_claim_idx
    ON task_attempts (lease_expires_at) WHERE status = 'active';

-- +goose Down
DROP INDEX task_attempts_claim_idx;
ALTER TABLE task_attempts
    DROP CONSTRAINT task_attempts_lease_pair,
    DROP COLUMN heartbeat_at,
    DROP COLUMN lease_expires_at,
    DROP COLUMN lease_owner,
    DROP CONSTRAINT task_attempts_runner_id_fkey;
DROP TABLE runners;
