-- Add agenda anchor lifecycle records to historical live/tree JSONB payloads.
-- Existing node/item IDs, evidence arrays, and unknown fields are preserved.
-- The migration is idempotent: existing agendaAnchors are never overwritten.
-- Legacy topic IDs are intentionally not rewritten in SQL. The application
-- performs a deterministic, idempotent in-memory remap into the topic-* node
-- namespace so graph-wide references remain safe during staged rollout.
WITH contexts AS (
    SELECT session_id, payload -> 'agendaItems' AS agenda_items
    FROM meeting_session_ai_analyses
    WHERE analysis_type = 'context'
      AND jsonb_typeof(payload -> 'agendaItems') = 'array'
), targets AS (
    SELECT analysis.session_id, analysis.analysis_type, analysis.payload, contexts.agenda_items
    FROM meeting_session_ai_analyses AS analysis
    JOIN contexts USING (session_id)
    WHERE analysis.analysis_type IN ('live', 'tree')
      AND analysis.payload IS NOT NULL
), nodes_backfilled AS (
    SELECT targets.session_id, targets.analysis_type,
        CASE WHEN jsonb_typeof(targets.payload #> '{tree,nodes}') = 'array' THEN
            jsonb_set(
                targets.payload,
                '{tree,nodes}',
                COALESCE((
                    SELECT jsonb_agg(
                        CASE
                            WHEN node ->> 'kind' = 'topic'
                             AND EXISTS (
                                SELECT 1
                                FROM jsonb_array_elements(targets.agenda_items) AS agenda(item)
                                WHERE agenda.item ->> 'id' = node ->> 'id'
                                  AND COALESCE(agenda.item ->> 'role', 'primary') <> 'action_summary'
                             )
                            THEN node || jsonb_build_object(
                                'agendaRefs', CASE
                                    WHEN jsonb_typeof(node -> 'agendaRefs') = 'array'
                                     AND (node -> 'agendaRefs') ? (node ->> 'id')
                                    THEN node -> 'agendaRefs'
                                    ELSE COALESCE(node -> 'agendaRefs', '[]'::jsonb) || jsonb_build_array(node ->> 'id')
                                END,
                                'materialized', true
                            )
                            ELSE node
                        END
                        ORDER BY ordinal
                    )
                    FROM jsonb_array_elements(targets.payload #> '{tree,nodes}') WITH ORDINALITY AS source(node, ordinal)
                ), '[]'::jsonb),
                false
            )
        ELSE targets.payload END AS payload,
        targets.agenda_items
    FROM targets
), migrated AS (
    SELECT nodes_backfilled.session_id, nodes_backfilled.analysis_type,
        CASE
            WHEN jsonb_typeof(nodes_backfilled.payload -> 'agendaAnchors') = 'array'
            THEN nodes_backfilled.payload
            ELSE jsonb_set(
                nodes_backfilled.payload,
                '{agendaAnchors}',
                COALESCE((
                    SELECT jsonb_agg(
                        jsonb_build_object(
                            'agendaId', agenda.item ->> 'id',
                            'originalTitle', agenda.item ->> 'title',
                            'normalizedSubject', agenda.item ->> 'title',
                            'order', COALESCE((agenda.item ->> 'order')::integer, agenda.ordinal::integer),
                            'role', COALESCE(NULLIF(agenda.item ->> 'role', ''), 'primary'),
                            'status', CASE
                                WHEN COALESCE(agenda.item ->> 'role', 'primary') = 'action_summary' THEN 'planned'
                                WHEN EXISTS (
                                    SELECT 1
                                    FROM jsonb_array_elements(COALESCE(nodes_backfilled.payload #> '{tree,nodes}', '[]'::jsonb)) AS source(node)
                                    WHERE node ->> 'kind' = 'topic'
                                      AND (
                                        node ->> 'id' = agenda.item ->> 'id'
                                        OR (jsonb_typeof(node -> 'agendaRefs') = 'array' AND (node -> 'agendaRefs') ? (agenda.item ->> 'id'))
                                      )
                                ) THEN 'materialized'
                                ELSE 'planned'
                            END,
                            'materializedTopicIds', COALESCE((
                                SELECT jsonb_agg(node ->> 'id' ORDER BY node ->> 'id')
                                FROM jsonb_array_elements(COALESCE(nodes_backfilled.payload #> '{tree,nodes}', '[]'::jsonb)) AS source(node)
                                WHERE node ->> 'kind' = 'topic'
                                  AND COALESCE(agenda.item ->> 'role', 'primary') <> 'action_summary'
                                  AND (
                                    node ->> 'id' = agenda.item ->> 'id'
                                    OR (jsonb_typeof(node -> 'agendaRefs') = 'array' AND (node -> 'agendaRefs') ? (agenda.item ->> 'id'))
                                  )
                            ), '[]'::jsonb)
                        )
                        ORDER BY agenda.ordinal
                    )
                    FROM jsonb_array_elements(nodes_backfilled.agenda_items) WITH ORDINALITY AS agenda(item, ordinal)
                    WHERE NULLIF(agenda.item ->> 'id', '') IS NOT NULL
                ), '[]'::jsonb),
                true
            )
        END AS payload
    FROM nodes_backfilled
)
UPDATE meeting_session_ai_analyses AS analysis
SET payload = migrated.payload
FROM migrated
WHERE migrated.session_id = analysis.session_id
  AND migrated.analysis_type = analysis.analysis_type
  AND migrated.payload IS DISTINCT FROM analysis.payload;
