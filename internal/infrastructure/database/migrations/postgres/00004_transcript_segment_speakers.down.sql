ALTER TABLE transcript_segments
    DROP COLUMN IF EXISTS speaker_name;

ALTER TABLE transcript_segments
    DROP COLUMN IF EXISTS speaker_id;
