-- Normalize historical live/tree JSON payloads without changing item/node IDs
-- or any source/evidence arrays. The UPDATE is idempotent: canonical rows are
-- rewritten to the same value when this SQL is applied again manually.
WITH items_normalized AS (
    SELECT analysis.session_id, analysis.analysis_type, CASE
        WHEN jsonb_typeof(analysis.payload -> 'items') = 'array' THEN
            jsonb_set(
                analysis.payload,
                '{items}',
                COALESCE((
                    SELECT jsonb_agg(
                        CASE item ->> 'kind'
                            WHEN 'open_issue' THEN (item - 'kind' - 'subtype' - 'status') ||
                                jsonb_build_object('kind', 'issue', 'subtype', 'discussion', 'status', CASE WHEN item ->> 'status' IN ('open', 'updated', 'resolved') THEN item ->> 'status' ELSE 'open' END)
                            WHEN 'question' THEN (item - 'kind' - 'subtype' - 'status') || jsonb_build_object('kind', 'issue', 'subtype', 'question', 'status', CASE WHEN item ->> 'status' IN ('open', 'updated', 'resolved') THEN item ->> 'status' ELSE 'open' END)
                            WHEN 'confirmation' THEN (item - 'kind' - 'subtype' - 'status') || jsonb_build_object('kind', 'issue', 'subtype', 'confirmation', 'status', CASE WHEN item ->> 'status' IN ('open', 'updated', 'resolved') THEN item ->> 'status' ELSE 'open' END)
                            WHEN 'investigation' THEN (item - 'kind' - 'subtype' - 'status') || jsonb_build_object('kind', 'issue', 'subtype', 'investigation', 'status', CASE WHEN item ->> 'status' IN ('open', 'updated', 'resolved') THEN item ->> 'status' ELSE 'open' END)
                            WHEN 'resolved' THEN (item - 'kind' - 'subtype' - 'status') || jsonb_build_object('kind', 'issue', 'subtype', 'discussion', 'status', 'resolved')
                            WHEN 'issue' THEN item || jsonb_build_object(
                                'subtype', CASE WHEN item ->> 'subtype' IN ('discussion', 'confirmation', 'question', 'investigation') THEN item ->> 'subtype' ELSE 'discussion' END,
                                'status', CASE WHEN item ->> 'status' IN ('open', 'updated', 'resolved') THEN item ->> 'status' ELSE 'open' END
                            )
                            ELSE item - 'subtype'
                        END
                        ORDER BY ordinal
                    )
                    FROM jsonb_array_elements(analysis.payload -> 'items') WITH ORDINALITY AS source(item, ordinal)
                ), '[]'::jsonb),
                false
            )
        ELSE analysis.payload
    END AS with_items
    FROM meeting_session_ai_analyses AS analysis
    WHERE analysis.payload IS NOT NULL
      AND analysis.analysis_type IN ('live', 'tree')
), normalized AS (
    SELECT items_normalized.session_id, items_normalized.analysis_type, CASE
        WHEN jsonb_typeof(items_normalized.with_items #> '{tree,nodes}') = 'array' THEN
            jsonb_set(
                items_normalized.with_items,
                '{tree,nodes}',
                COALESCE((
                    SELECT jsonb_agg(
                        CASE node ->> 'kind'
                            WHEN 'open_issue' THEN (node - 'kind' - 'subtype' - 'status') ||
                                jsonb_build_object('kind', 'issue', 'subtype', 'discussion', 'status', CASE WHEN node ->> 'status' IN ('open', 'updated', 'resolved') THEN node ->> 'status' ELSE 'open' END)
                            WHEN 'question' THEN (node - 'kind' - 'subtype' - 'status') || jsonb_build_object('kind', 'issue', 'subtype', 'question', 'status', CASE WHEN node ->> 'status' IN ('open', 'updated', 'resolved') THEN node ->> 'status' ELSE 'open' END)
                            WHEN 'confirmation' THEN (node - 'kind' - 'subtype' - 'status') || jsonb_build_object('kind', 'issue', 'subtype', 'confirmation', 'status', CASE WHEN node ->> 'status' IN ('open', 'updated', 'resolved') THEN node ->> 'status' ELSE 'open' END)
                            WHEN 'investigation' THEN (node - 'kind' - 'subtype' - 'status') || jsonb_build_object('kind', 'issue', 'subtype', 'investigation', 'status', CASE WHEN node ->> 'status' IN ('open', 'updated', 'resolved') THEN node ->> 'status' ELSE 'open' END)
                            WHEN 'resolved' THEN (node - 'kind' - 'subtype' - 'status') || jsonb_build_object('kind', 'issue', 'subtype', 'discussion', 'status', 'resolved')
                            WHEN 'issue' THEN node || jsonb_build_object(
                                'subtype', CASE WHEN node ->> 'subtype' IN ('discussion', 'confirmation', 'question', 'investigation') THEN node ->> 'subtype' ELSE 'discussion' END,
                                'status', CASE WHEN node ->> 'status' IN ('open', 'updated', 'resolved') THEN node ->> 'status' ELSE 'open' END
                            )
                            ELSE node - 'subtype'
                        END
                        ORDER BY ordinal
                    )
                    FROM jsonb_array_elements(items_normalized.with_items #> '{tree,nodes}') WITH ORDINALITY AS source(node, ordinal)
                ), '[]'::jsonb),
                false
            )
        ELSE items_normalized.with_items
    END AS with_nodes
    FROM items_normalized
)
UPDATE meeting_session_ai_analyses AS analysis
SET payload = normalized.with_nodes
FROM normalized
WHERE normalized.session_id = analysis.session_id
  AND normalized.analysis_type = analysis.analysis_type
  AND normalized.with_nodes IS DISTINCT FROM analysis.payload;
