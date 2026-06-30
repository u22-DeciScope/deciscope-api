ALTER TABLE transcript_segments
    ADD COLUMN IF NOT EXISTS speaker_id TEXT;

ALTER TABLE transcript_segments
    ADD COLUMN IF NOT EXISTS speaker_name TEXT;
