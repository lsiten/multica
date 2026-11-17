CREATE TABLE document_comment (
    id uuid NOT NULL DEFAULT gen_random_uuid(),
    workspace_id uuid NOT NULL,
    resource_key text NOT NULL,
    resource_ref jsonb NOT NULL,
    parent_id uuid,
    anchor jsonb,
    content text NOT NULL,
    author_id uuid NOT NULL,
    resolved_at timestamptz,
    resolved_by uuid,
    revision bigint NOT NULL DEFAULT 1,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    CHECK (length(content) BETWEEN 1 AND 16000),
    CHECK (revision > 0),
    CHECK (parent_id IS NULL OR anchor IS NULL)
);
