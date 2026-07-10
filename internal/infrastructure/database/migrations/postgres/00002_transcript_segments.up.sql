CREATE TABLE IF NOT EXISTS transcript_segments (
    id BIGSERIAL PRIMARY KEY,
    event_id TEXT NOT NULL,
    call_id TEXT NOT NULL,
    sequence_no BIGINT NOT NULL,
    recognized_at_utc TEXT NOT NULL,
    offset_ticks BIGINT NOT NULL,
    duration_ticks BIGINT NOT NULL,
    text TEXT NOT NULL,
    received_at_utc TEXT NOT NULL,

    UNIQUE (event_id),
    UNIQUE (call_id, sequence_no)
);

CREATE INDEX IF NOT EXISTS idx_transcript_segments_call_order
    ON transcript_segments (call_id, sequence_no);
