-- Requirement lifecycle, 1:1 with a kind='requirement' issue.
--
-- No FK / cascade (DB rules): the app layer keeps issue<->requirement in sync.
-- The requirement document is the backing issue's description (Markdown); its
-- revisions live in document_content_version. `confirmed_revision` pins the
-- document revision that was confirmed; editing after confirmation sets
-- `unconfirmed_change` and returns the requirement to pending_confirm.
CREATE TABLE requirement (
    id                 UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    workspace_id       UUID NOT NULL,
    issue_id           UUID NOT NULL,
    lifecycle_status   TEXT NOT NULL DEFAULT 'draft'
        CHECK (lifecycle_status IN ('draft', 'pending_confirm', 'confirmed')),
    confirmed_revision BIGINT,
    confirmed_by       UUID,
    confirmed_at       TIMESTAMPTZ,
    unconfirmed_change BOOLEAN NOT NULL DEFAULT false,
    created_at         TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at         TIMESTAMPTZ NOT NULL DEFAULT now()
);
