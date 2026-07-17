# Discussion Tree Auditor

Tree Auditorは、ライブ抽出とは別のGPT-5-mini deploymentで現在の議論ツリーを校閲し、
ツリー全体ではなくstrict JSONのfindingと小さなpatch operationだけを提案させます。

## 安全境界

処理順は次の三層です。

1. 既存`validateTreeIntegrity`によるduplicate/self-parent/cycle/orphan/fixed agenda/depth等の検証
2. title・description・candidate・evidence・parent metadataを使うdeterministic semantic precheck
3. 圧縮snapshotを使うGPT tree audit / final tree review

ライブ抽出側では、隣接する同一話者の断片をlogical utteranceとして結合し、recap開始・議題遷移・
明示訂正をdiscourse timelineで追跡します。recap本文は`reference_recap`として既存命題の補助証拠には
使えますが、新規item/topic/candidateのprimary evidenceには使いません。

question/open_issue/todoの表層差は、primary/correction evidenceとsubject coreが一致する場合に限り
canonical propositionへ統合します。質問文、解決条件、次アクションは`relatedQuestions`,
`resolutionConditions`, `nextActions`として保持されます。

AI operationはcanonical ID、`basedOnTreeVersion`、現在親、対象kind、fixed agenda、depth、
evidence role、subject score、parent stickiness、status、直近の親変更をサーバーで再検証します。

`apply_high_confidence`の初期whitelistは`move_item`だけです。次をすべて満たす必要があります。

- 対象が未解決のdetail itemで、fixed agenda・topic・group・rootではない
- 現在親が一致し、同一fixed agenda境界内である（agendaをまたぐ移動は禁止）
- itemに紐づくprimary evidenceを1件以上含み、reference-onlyではない
- 新親subject scoreが現在親より`TREE_AUDIT_REQUIRED_IMPROVEMENT_MARGIN`以上高い
- 直近2 tree versions以内に親変更されておらず、hard depthとtree integrityを満たす
- deterministicなheuristic defect countが増加しない

`restore_previous_parent`、candidateのpromotion/fold/deactivate、groupの作成・統合・分割・rename・削除、
resolved item移動、agenda境界をまたぐ移動、node kind変更・削除、primary evidence変更はすべてshadow-onlyです。
model confidenceだけでwhitelist外operationが適用されることはありません。

`merge_items`, `rewrite_item`, `deactivate_item`, `split_candidate`,
`create_topic_from_candidate`, `assign_item_to_candidate`, `change_evidence_role`,
`merge_fragmented_utterances`もshadow検証専用です。shadowでは構造・参照・証拠を検証した
`validated_shadow`を記録できますが、`autoApplyEligible=false`のままでtreeを変更しません。

## Mode

- `off`: 実行しない
- `shadow`: finding・operation・validator結果を`meeting_tree_audit_runs`へ保存し、live treeを変更しない
- `apply_high_confidence`: confidence閾値と全validatorを通ったoperationだけdry-runし、
  live versionをcompare-and-swapで更新して既存WebSocketへ配信する

機能自体は`TREE_AUDIT_ENABLED=true`で明示的に有効化します。modeの既定値は`shadow`です。

## Scheduling

live auditは同一sessionでsingle-flightです。監査中の複数triggerはpending 1件へcoalesceされます。
監査は取得済みsnapshotで非同期実行し、shadow/applyのどちらもライブ抽出を停止しません。
通常live保存、reorganizer保存、audit適用はすべてDB version CASを使います。どちらが先に保存されても、
古い`basedOnTreeVersion`の結果は破棄・再試行されます。別instanceとの競合もdurable duplicate claimとDB CASで防ぎます。

既定では3 tree versions、またはtree変更後300秒で監査対象となり、semantic anomaly、
candidate作成・promotion、reparent、低confidence、fragmentation、stale tentative等は条件triggerになります。
通常triggerは最小300秒、12 provider calls/時、20 provider calls/sessionです。
`semantic_anomaly`等のhigh-severity triggerは通常枠と分離し、最小60秒・4 calls/時です。
通常session上限後もhigh-severity triggerを許可し、`final_tree_review`は全上限の別枠です。
同一tree version/snapshot hash/prompt/deploymentの同時実行はDBのactive claimで重複実行しません。
直近の同一terminal runも通常schedulerでは重複排除します。
抑制runも`trigger_class`, `suppression_reason`, `meeting_elapsed_seconds`付きで履歴保存します。

## 障害分離と終了境界

shadow modeではaudit repository、migration、provider、schema、timeoutの失敗はaudit goroutine内で完結し、
ライブ抽出・通常tree保存・WebSocket配信を待たせません。audit履歴を確保できない場合はproviderを呼びません。
apply modeのtree CASとaudit run更新は同一DB transactionで行うため、履歴のないtree変更はcommitされません。

sessionがendingへ入るとpending live auditを破棄し、実行中live auditのcontextをcancelして以後の適用を禁止します。
final reviewはlive auditとは別flightで実行されます。providerがcontextを無視してtimeout後に応答しても、
context再確認とDB CASより遅延responseは適用されません。

## Snapshot limits

`TREE_AUDIT_MAX_NODES`, `TREE_AUDIT_MAX_RECENT_SEGMENTS`,
`TREE_AUDIT_MAX_EVIDENCE_SEGMENTS`, `TREE_AUDIT_MAX_INPUT_TOKENS`で入力を制限します。
上限時はfixed agenda、precheck対象、最近変更されたnodeを優先し、evidence発話の前後を含めます。
全文transcriptはライブ監査へ無条件送信しません。final reviewだけは保存済みtranscriptを広く読み、
同じ上限付きsnapshotへ圧縮します。

## Finalization

終了pipelineはfinal transcript flush後に`final_tree_review`を実行し、その後tree snapshotとsummaryを保存します。
reviewのtimeout・schema failure・provider failure時も最後の正常live treeで継続し、
finalization/tree snapshotの`finalTreeReviewFailed`と`degraded`で観測できます。

finding/operationの一部だけが非canonical IDや無効dependencyを含む場合は、無効要素だけを隔離し、
残りを`partial_success`として検証・保存します。昇格済みcandidate IDがmoveの補助欄に残った場合は、
tree nodeとしてcanonicalであることを確認して不要な補助参照を除去します。JSON envelope、version、
上限違反などレスポンス全体の安全性を判定できない場合だけ`invalid_schema`になります。

順序は常に`final flush → final_tree_review → final tree snapshot → final summary`です。

## Replay history

`meeting_tree_audit_runs`はrunごとに、session/run ID、based/resulting version、snapshot hash、
trigger reason/class、prompt/schema version、deployment alias、provider model、圧縮後の正確なaudit input、
raw response、finding、operation、validator結果、disposition、heuristic metrics、token usage、elapsed、
error code/message、provider call有無、抑制理由、会議経過秒を保存します。
入力は`TREE_AUDIT_MAX_INPUT_TOKENS`で制限済みで、保存JSONは既定256 KiBに制限されます。
同じsnapshotでもprompt versionまたはdeploymentを変更するか、運用ツールから`manual_replay` triggerを明示すれば
将来の再評価が可能です。通常schedulerが偶発的に同じ入力を再送することはありません。

`heuristicDefectCountBefore/After`はsubject token overlap等によるdeterministic finding件数であり、
意味的品質の保証値ではありません。個別の`topicOutliers`, `candidateFragmentation`,
`crossAgendaContamination`とともに非悪化gateへ使います。日本語の表記揺れや暗黙的文脈には限界があります。
