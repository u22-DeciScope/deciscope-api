-- Upgrade the bundled sample meeting created by older releases to the
-- completed-meeting contract used by the current UI. User-created meetings
-- are excluded by the exact sample title and sample session-id prefix.

INSERT INTO meeting_session_ai_analyses (
    session_id, analysis_type, status, version, payload, model,
    segment_count, input_chars, created_at, updated_at
)
SELECT
    session.id, 'context', 'completed', 1,
    jsonb_build_object(
        'title', session.title,
        'purpose', session.purpose,
        'background', session.context,
        'agendaItems', jsonb_build_array(
            jsonb_build_object('id', 'agenda-1', 'title', '値上げ対象顧客の範囲', 'order', 1, 'role', 'primary'),
            jsonb_build_object('id', 'agenda-2', 'title', '値上げ率', 'order', 2, 'role', 'primary'),
            jsonb_build_object('id', 'agenda-3', 'title', '適用タイミング', 'order', 3, 'role', 'primary')
        ),
        'aiDirectives', jsonb_build_array('財務影響は数値で示すこと')
    ),
    'sample', 0, 0, session.created_at::timestamptz, session.ended_at::timestamptz
FROM meeting_sessions AS session
WHERE session.id LIKE 'session_sample_%'
  AND session.title = '【サンプル】価格改定方針の検討会議'
ON CONFLICT (session_id, analysis_type) DO NOTHING;

WITH sample_live AS (
    SELECT analysis.session_id, analysis.payload
    FROM meeting_session_ai_analyses AS analysis
    JOIN meeting_sessions AS session ON session.id = analysis.session_id
    WHERE analysis.analysis_type = 'live'
      AND session.id LIKE 'session_sample_%'
      AND session.title = '【サンプル】価格改定方針の検討会議'
), modern_items AS (
    SELECT sample_live.session_id,
        COALESCE(jsonb_agg(
            item.value || jsonb_build_object(
                'projectionStatus', 'stable',
                'classificationStatus', 'assigned',
                'assignmentSource', 'rule',
                'evidenceSequenceNos', CASE item.value ->> 'id'
                    WHEN 'issue-target-scope' THEN '[1,2,4,7]'::jsonb
                    WHEN 'risk-smb-churn' THEN '[2,7]'::jsonb
                    WHEN 'decision-ent-repricing' THEN '[3,7]'::jsonb
                    WHEN 'question-renewal-timing' THEN '[5,6]'::jsonb
                    WHEN 'risk-revenue-timing' THEN '[6]'::jsonb
                    WHEN 'todo-customer-list' THEN '[8]'::jsonb
                    ELSE '[]'::jsonb
                END,
                'relatedAgendaIds', CASE item.value ->> 'id'
                    WHEN 'issue-target-scope' THEN '["agenda-1"]'::jsonb
                    WHEN 'risk-smb-churn' THEN '["agenda-1"]'::jsonb
                    WHEN 'decision-ent-repricing' THEN '["agenda-1","agenda-2","agenda-3"]'::jsonb
                    WHEN 'question-renewal-timing' THEN '["agenda-3"]'::jsonb
                    WHEN 'risk-revenue-timing' THEN '["agenda-3"]'::jsonb
                    WHEN 'todo-customer-list' THEN '["agenda-3"]'::jsonb
                    ELSE '[]'::jsonb
                END
            ) ORDER BY item.ordinal
        ), '[]'::jsonb) AS items
    FROM sample_live
    CROSS JOIN LATERAL jsonb_array_elements(COALESCE(sample_live.payload -> 'items', '[]'::jsonb))
        WITH ORDINALITY AS item(value, ordinal)
    GROUP BY sample_live.session_id
), modern_payload AS (
    SELECT sample_live.session_id,
        sample_live.payload || jsonb_build_object(
            'items', modern_items.items,
            'tree', jsonb_build_object(
                'nodes', jsonb_build_array(
                    jsonb_build_object('id', 'root', 'kind', 'topic', 'label', '【サンプル】価格改定方針の検討会議', 'status', 'open', 'description', '来期の価格改定方針を決め、対象顧客リストの作成につなげる。', 'origin', 'system'),
                    jsonb_build_object('id', 'topic-price-scope', 'kind', 'topic', 'parentId', 'root', 'label', '値上げ対象顧客の範囲', 'status', 'resolved', 'description', '値上げ対象とする顧客セグメントの検討。', 'origin', 'agenda', 'agendaRefs', jsonb_build_array('agenda-1'), 'materialized', true),
                    jsonb_build_object('id', 'topic-price-rate', 'kind', 'topic', 'parentId', 'root', 'label', '値上げ率', 'status', 'resolved', 'description', '原価上昇を踏まえた値上げ率の検討。', 'origin', 'agenda', 'agendaRefs', jsonb_build_array('agenda-2'), 'materialized', true),
                    jsonb_build_object('id', 'topic-price-rollout', 'kind', 'topic', 'parentId', 'root', 'label', '適用タイミング', 'status', 'resolved', 'description', '契約更新月にあわせた段階適用の検討。', 'origin', 'agenda', 'agendaRefs', jsonb_build_array('agenda-3'), 'materialized', true),
                    jsonb_build_object('id', 'issue-target-scope', 'kind', 'issue', 'subtype', 'discussion', 'parentId', 'topic-price-scope', 'label', '値上げ対象顧客の範囲', 'status', 'resolved', 'description', 'エンタープライズ限定か全顧客かを検討し、エンタープライズ限定で合意した。', 'relatedItemIds', jsonb_build_array('issue-target-scope')),
                    jsonb_build_object('id', 'risk-smb-churn', 'kind', 'risk', 'parentId', 'topic-price-scope', 'label', '中小顧客の解約リスク', 'status', 'resolved', 'description', '中小顧客への値上げは解約リスクが高く、据え置きで回避する。', 'relatedItemIds', jsonb_build_array('risk-smb-churn')),
                    jsonb_build_object('id', 'decision-ent-repricing', 'kind', 'decision', 'parentId', 'topic-price-rate', 'label', 'ENTは8%値上げ・中小は据え置き', 'status', 'open', 'description', 'エンタープライズは更新月から8%値上げし、中小は据え置く。', 'relatedItemIds', jsonb_build_array('decision-ent-repricing')),
                    jsonb_build_object('id', 'question-renewal-timing', 'kind', 'issue', 'subtype', 'question', 'parentId', 'topic-price-rollout', 'label', '更新タイミングのばらつき', 'status', 'resolved', 'description', '顧客ごとに異なる契約更新月にあわせ、段階適用する。', 'relatedItemIds', jsonb_build_array('question-renewal-timing')),
                    jsonb_build_object('id', 'risk-revenue-timing', 'kind', 'risk', 'parentId', 'topic-price-rollout', 'label', '値上げ効果の発現遅延', 'status', 'open', 'description', '段階適用のため効果が全顧客に及ぶまで最長1年かかる。', 'relatedItemIds', jsonb_build_array('risk-revenue-timing')),
                    jsonb_build_object('id', 'todo-customer-list', 'kind', 'todo', 'parentId', 'topic-price-rollout', 'label', '対象顧客リストの展開', 'status', 'open', 'description', '佐藤が値上げ対象顧客リストを今週中に作成・共有する。', 'relatedItemIds', jsonb_build_array('todo-customer-list'))
                ),
                'edges', jsonb_build_array(
                    jsonb_build_object('source', 'root', 'target', 'topic-price-scope'),
                    jsonb_build_object('source', 'root', 'target', 'topic-price-rate'),
                    jsonb_build_object('source', 'root', 'target', 'topic-price-rollout'),
                    jsonb_build_object('source', 'topic-price-scope', 'target', 'issue-target-scope'),
                    jsonb_build_object('source', 'topic-price-scope', 'target', 'risk-smb-churn'),
                    jsonb_build_object('source', 'topic-price-rate', 'target', 'decision-ent-repricing'),
                    jsonb_build_object('source', 'topic-price-rollout', 'target', 'question-renewal-timing'),
                    jsonb_build_object('source', 'topic-price-rollout', 'target', 'risk-revenue-timing'),
                    jsonb_build_object('source', 'topic-price-rollout', 'target', 'todo-customer-list')
                )
            ),
            'agendaAnchors', jsonb_build_array(
                jsonb_build_object('agendaId', 'agenda-1', 'originalTitle', '値上げ対象顧客の範囲', 'normalizedSubject', '値上げ対象顧客の範囲', 'order', 1, 'role', 'primary', 'status', 'discussed', 'materializedTopicIds', jsonb_build_array('topic-price-scope')),
                jsonb_build_object('agendaId', 'agenda-2', 'originalTitle', '値上げ率', 'normalizedSubject', '値上げ率', 'order', 2, 'role', 'primary', 'status', 'discussed', 'materializedTopicIds', jsonb_build_array('topic-price-rate')),
                jsonb_build_object('agendaId', 'agenda-3', 'originalTitle', '適用タイミング', 'normalizedSubject', '適用タイミング', 'order', 3, 'role', 'primary', 'status', 'discussed', 'materializedTopicIds', jsonb_build_array('topic-price-rollout'))
            ),
            'agendaProgress', jsonb_build_object(
                'entries', jsonb_build_array(
                    jsonb_build_object('id', 'agenda-1', 'sourceType', 'fixed_agenda', 'title', '値上げ対象顧客の範囲', 'order', 1, 'computedStatus', 'discussed', 'outcomeStatus', 'concluded', 'focusNodeIds', jsonb_build_array('topic-price-scope', 'issue-target-scope', 'risk-smb-churn'), 'materializedTopicIds', jsonb_build_array('topic-price-scope'), 'primaryNodeId', 'topic-price-scope', 'linkState', 'materialized-topic', 'lastProgressAtVersion', 8),
                    jsonb_build_object('id', 'agenda-2', 'sourceType', 'fixed_agenda', 'title', '値上げ率', 'order', 2, 'computedStatus', 'discussed', 'outcomeStatus', 'concluded', 'focusNodeIds', jsonb_build_array('topic-price-rate', 'decision-ent-repricing'), 'materializedTopicIds', jsonb_build_array('topic-price-rate'), 'primaryNodeId', 'topic-price-rate', 'linkState', 'materialized-topic', 'lastProgressAtVersion', 8),
                    jsonb_build_object('id', 'agenda-3', 'sourceType', 'fixed_agenda', 'title', '適用タイミング', 'order', 3, 'computedStatus', 'discussed', 'outcomeStatus', 'concluded', 'focusNodeIds', jsonb_build_array('topic-price-rollout', 'question-renewal-timing', 'risk-revenue-timing', 'todo-customer-list'), 'materializedTopicIds', jsonb_build_array('topic-price-rollout'), 'primaryNodeId', 'topic-price-rollout', 'linkState', 'materialized-topic', 'lastProgressAtVersion', 8)
                ),
                'updatedAtVersion', 8
            ),
            'currentTopic', '会議終了',
            'treeVersion', 8,
            'analysisVersion', 8,
            'aiAssistantAnalysisVersion', 8,
            'treeAnalysisVersion', 8,
            'highestAvailableFinalSequenceNo', 8,
            'itemProjectionVersion', 8,
            'treeProjectionVersion', 8,
            'itemProjectionCompleted', true,
            'treeProjectionCompleted', true,
            'treeProjectionDisposition', 'updated',
            'payloadKind', 'full_snapshot',
            'nodeCount', 10,
            'edgeCount', 9,
            'coveredThroughSequenceNo', 8,
            'meaningfullyCoveredThroughSequenceNo', 8,
            'pendingTreeProjectionItemCount', 0
        ) AS payload
    FROM sample_live
    JOIN modern_items USING (session_id)
)
UPDATE meeting_session_ai_analyses AS analysis
SET payload = modern_payload.payload,
    version = 8,
    status = 'completed',
    model = 'sample'
FROM modern_payload
WHERE analysis.session_id = modern_payload.session_id
  AND analysis.analysis_type = 'live';

UPDATE meeting_session_ai_analyses AS analysis
SET payload = analysis.payload || jsonb_build_object(
        'coveredThroughSequenceNo', 8,
        'segmentCount', 8,
        'treeVersion', 8,
        'final', true
    ),
    status = 'completed',
    model = 'sample'
FROM meeting_sessions AS session
WHERE analysis.session_id = session.id
  AND analysis.analysis_type = 'final'
  AND session.id LIKE 'session_sample_%'
  AND session.title = '【サンプル】価格改定方針の検討会議';

INSERT INTO meeting_session_ai_analyses (
    session_id, analysis_type, status, version, payload, model,
    segment_count, input_chars, created_at, updated_at
)
SELECT
    session.id, 'tree', 'completed', 8,
    jsonb_build_object(
        'treeVersion', 8,
        'reason', 'meeting_ended',
        'final', true,
        'coveredThroughSequenceNo', 8,
        'segmentCount', 8,
        'generatedAtUtc', session.ended_at,
        'reorganizationStatus', 'not_needed',
        'tree', live.payload -> 'tree',
        'agendaAnchors', live.payload -> 'agendaAnchors',
        'agendaProgress', live.payload -> 'agendaProgress'
    ),
    'sample', 8, live.input_chars, session.created_at::timestamptz, session.ended_at::timestamptz
FROM meeting_sessions AS session
JOIN meeting_session_ai_analyses AS live
  ON live.session_id = session.id AND live.analysis_type = 'live'
WHERE session.id LIKE 'session_sample_%'
  AND session.title = '【サンプル】価格改定方針の検討会議'
ON CONFLICT (session_id, analysis_type) DO UPDATE
SET status = EXCLUDED.status,
    version = EXCLUDED.version,
    payload = EXCLUDED.payload,
    model = EXCLUDED.model,
    segment_count = EXCLUDED.segment_count,
    input_chars = EXCLUDED.input_chars,
    updated_at = EXCLUDED.updated_at;

INSERT INTO meeting_session_ai_analyses (
    session_id, analysis_type, status, version, payload, model,
    segment_count, input_chars, created_at, updated_at
)
SELECT
    session.id, 'finalization', 'completed', 6,
    jsonb_build_object(
        'finalizationId', 'finalization_sample',
        'stage', 'completed',
        'latestPersistedFinalSequence', 8,
        'lastSuccessfullyAnalyzedSequence', 8,
        'finalizationTargetSequence', 8,
        'pendingSegmentCount', 0,
        'treeCoveredThroughSequenceNo', 8,
        'summaryCoveredThroughSequenceNo', 8,
        'waitTimedOut', false,
        'finalizationIncomplete', false,
        'retryCount', 0,
        'finalizationStatus', 'completed',
        'finalizationStartedAt', session.ended_at,
        'finalizationUpdatedAt', session.ended_at,
        'finalizationCompletedAt', session.ended_at,
        'retryable', false,
        'attemptCount', 1,
        'sourceTreeVersion', 8,
        'sourceAnalysisVersion', 8,
        'summaryVersion', 1
    ),
    'sample', 0, 0, session.created_at::timestamptz, session.ended_at::timestamptz
FROM meeting_sessions AS session
JOIN meeting_session_ai_analyses AS live
  ON live.session_id = session.id AND live.analysis_type = 'live'
WHERE session.id LIKE 'session_sample_%'
  AND session.title = '【サンプル】価格改定方針の検討会議'
ON CONFLICT (session_id, analysis_type) DO UPDATE
SET status = EXCLUDED.status,
    version = EXCLUDED.version,
    payload = EXCLUDED.payload,
    model = EXCLUDED.model,
    segment_count = EXCLUDED.segment_count,
    input_chars = EXCLUDED.input_chars,
    updated_at = EXCLUDED.updated_at;

INSERT INTO meeting_session_ai_analysis_live_history (
    session_id, version, payload, model, updated_at
)
SELECT analysis.session_id, analysis.version, analysis.payload, analysis.model, analysis.updated_at
FROM meeting_session_ai_analyses AS analysis
JOIN meeting_sessions AS session ON session.id = analysis.session_id
WHERE analysis.analysis_type = 'live'
  AND session.id LIKE 'session_sample_%'
  AND session.title = '【サンプル】価格改定方針の検討会議'
ON CONFLICT (session_id, version) DO UPDATE
SET payload = EXCLUDED.payload,
    model = EXCLUDED.model,
    updated_at = EXCLUDED.updated_at;
