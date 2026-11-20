CREATE TABLE meeting_segment (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    meeting_id UUID NOT NULL,
    sequence BIGINT NOT NULL,
    start_ms BIGINT NOT NULL DEFAULT 0,
    end_ms BIGINT NOT NULL DEFAULT 0,
    speaker_label TEXT NOT NULL DEFAULT '',
    text TEXT NOT NULL,
    is_final BOOLEAN NOT NULL DEFAULT true,
    confidence REAL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
