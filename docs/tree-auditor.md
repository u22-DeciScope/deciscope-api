# Discussion Tree Auditor

Tree Auditorは、ライブ抽出とは別のGPT-5-mini deploymentで現在の議論ツリーを校閲し、
ツリー全体ではなくstrict JSONのfindingと小さなpatch operationだけを提案させます。

## 有効/無効

監査AIは`enabled` / `disabled`の2状態だけです。モード切替(`off`/`shadow`/
`apply_high_confidence`)は廃止されました。`TREE_AUDIT_ENABLED=true`(既定)で
単一の実行経路が有効になり、`TREE_AUDIT_ENABLED=false`で停止します。
`TREE_AUDIT_MODE`はdeprecatedであり、設定されていても無視されます(起動時に
一度だけ警告ログを出力します)。

## 単一経路

enabled時は常に次の経路を通ります。分岐はありません。

1. snapshot取得(圧縮済み、`TREE_AUDIT_MAX_*`で上限)
2. 既存`validateTreeIntegrity`によるduplicate/self-parent/cycle/orphan/
   empty group/fixed agenda/depth等の構造検証(不正ならAIを呼ばず終了)
3. title・description・candidate・evidence・parent metadataを使う
   deterministic semantic precheck
4. 圧縮snapshotを使うGPT tree audit / final tree review呼び出し
5. strict JSON応答のparse(schema・type・confidence・重複ID・依存関係のみを検証)
6. **canonicalization**: parseずみのID風文字列をtree/item/candidateの
   canonical ID空間へ解決
7. operation単位のvalidation(confidence、parent一致、evidence role、
   subject score、stickiness、status、直近の親変更をサーバーで再検証)
8. 安全と判定されたoperationだけを実際に適用(dry-runを直列に積み上げる)
9. 適用後treeの`validateTreeIntegrity`再検証
10. DB version CAS(compare-and-swap)で新versionを保存
11. 既存WebSocketへbroadcast

安全機構(whitelist相当の分類、confidence、integrity、transaction、rate limit、
manual保護、parent stickiness、stale検出)はいずれも削除されていません。

finding/operationの一部だけが非canonical IDや無効dependencyを含む場合は、
無効要素だけを隔離し、残りを検証・適用します。この場合の結果は
`partial_success`です。全operationが有効でも安全条件を満たさなければ
`no_safe_operations`、有効operationがゼロなら`no_safe_operations`、
一部だけ適用されれば`partial_success`、全て適用されれば`applied`になります。

## Operationの分類

v3 schemaは23種のoperation typeを認識します。サーバーが実際に適用しうる
**applicable**は次の15種です。

`move_item`, `restore_previous_parent`, `move_node`, `merge_items`,
`rewrite_item`, `rewrite_item_title`, `rewrite_item_description`,
`deactivate_item`, `assign_item_to_candidate`, `change_evidence_role`,
`create_topic_from_candidate`, `fold_candidate_into_topic`,
`deactivate_candidate`, `rename_group`, `remove_empty_group`

残る8種は**unsupported**(schemaとしては認識されるがapplierが存在せず、
confidenceに関わらず必ず`unsupported_operation`で拒否)です。

`merge_candidates`, `promote_candidate`, `mark_candidate_tentative`,
`merge_dynamic_topics`, `create_group`, `move_items_to_group`,
`split_candidate`, `merge_fragmented_utterances`

applicableなoperationも、個別の安全条件(confidence、parent一致、evidence、
cycle、depth、subject整合等)を満たさなければ`unsafe`カテゴリとして拒否
されます。拒否理由(`reason`)は具体的な文字列(例:
`parent_stickiness_margin`, `reference_evidence_only`,
`fixed_agenda_immutable`, `cycle_target_descendant`,
`recap_only_candidate`, `deactivate_grounds_not_verified`,
`dependency_rejected`, `unresolved_canonical_id`, `ambiguous_alias`)で
記録され、`unsupported`/`unsafe`という2値だけがカテゴリとして
`validator_result`に残ります。

### move_node

topic/group container nodeを新しい親(root/topic/group)へ移動します。
fixed agenda・root・action_summaryは対象にも移動先にもできません。
自身の子孫への移動(cycle)は禁止され、移動後の部分木がtreeの最大深さを
超えないことを検証します。移動元・移動先のsubject整合(部分木テキストと
移動先系列の類似度が現状以上、または現在の親がgeneric/unclassified)も
必須です。

### merge_items

2件以上の現存itemを1件へ統合します。統合可能な組は既存の
`sameKindSemanticDuplicate`/`sameCanonicalProposition`判定、または連結された
重複グラフで判定します。decisionと未決定種別(todo/issue/question)の統合は
canonical propositionが明確な場合のみ許可されます。survivorは指定順の
先頭itemで、evidenceは和集合、assignee/deadline/statusなど非空フィールドは
survivor優先で欠落させません。被統合itemはtreeから除去されますが、
`Items[]`には`mergedIntoId`付きで残ります。

### rewrite_item / rewrite_item_title / rewrite_item_description

新しいlabelが旧title/body(またはoperationのevidence)と主題語を共有する
場合のみ書き換えを許可します。kindの変更やevidenceにない固有名詞・期限・
担当者の追加は認めません。

### deactivate_item

重複・recap/reference-only・discourse-only(会話制御発話)・低情報
decisionのいずれかをサーバー側で再検証できた場合のみtreeから除去し、
`inactive:true`を記録します。モデルの主張のみでは適用されません。

### assign_item_to_candidate / change_evidence_role

前者はitemを未昇格candidateへtentativeとして割り当てます。後者は
指定したevidence sequenceのみを`reference_recap`へ格下げします
(reference_recapへの降格のみが許可され、対象sequenceはitemの
evidenceに実在する必要があります)。降格は次回以降のevidence role
分類でも尊重されます。

### create_topic_from_candidate

未昇格・非inactive・非recap-onlyのcandidateを、そのIDのままroot直下の
dynamic topicへ昇格させます。既存の動的topicや固定agendaと意味的に
重複する場合は拒否され(`duplicate_topic`/`should_fold_into_fixed_agenda`)、
fold_candidate_into_topicや既存agendaへの割当が推奨されます。

### fold_candidate_into_topic / deactivate_candidate / rename_group / remove_empty_group

fold_candidate_into_topicはcandidateのevidence itemを既存topicへ移し、
candidateを非活性化します。deactivate_candidateはroundCount<=1の
candidateを無条件で、それ以外は他条件を満たした場合のみ非活性化します。
rename_groupはlabelの意味的cohesionが悪化しない場合のみ適用されます。

`remove_empty_group`は、子が0件のgroup、または子が0件になった
昇格済みdynamic topic(fixed agenda・root・action_summary以外)を削除
します。子が1件以上残っている場合は`group_not_empty`で拒否され、
削除対象として不適格なnode(未知ID、group/dynamic topic以外、fixed
agenda・root・action_summary)は`unknown_or_immutable_container`で
拒否されます。

`validateTreeIntegrity`は「group」kindのnodeが子0件になることを恒久的な
不整合として扱います(dynamic topicにはこの制約はありません)。このため、
あるoperationがcontainerの最後の子を除去した(移動・deactivate・merge
のいずれであれ)直後、そのcontainer自身が子0件のまま残っていると、その
operationは`tree_integrity_rejected`で拒否されてしまいます。

これを避けるため、operation単位のvalidationはoperation適用直後・
integrity再検証の直前に、**空になったcontainerのcascade自動整理**を
行います。あるoperationの結果、親子関係が変わった(除去または再親化
された)nodeの旧親について、そのnodeがgroup、または子0件になった
昇格済みdynamic topic(fixed agenda・root・action_summary・
topic-unclassified以外、remove_empty_groupと同じ対象定義)であり、
かつ子が0件であれば、そのnodeをtreeから除去します。除去後にその
node自身の親も同条件を満たせばcascadeでさらに上位へ除去を続けます
(topic-unclassifiedは決して対象になりません)。この自動整理は、その
operationが実際に子を取り除いたcontainer系列だけに限定され、無関係な
既存containerへは波及しません(監査開始時に`validateTreeIntegrity`済み
のため、そもそも無関係な空containerは存在しないはずです)。ライブ
(非監査)pipelineの`pruneEmptyDynamicTopics`(空dynamic topicの自動除去)
と対になる、監査operation列内での同種の後始末です。

これにより、2子groupの両方を別々のmove_item operationで移動する提案は、
2件目の適用直後に空になったgroup自身がこのcascadeで除去されるため、
明示的な`remove_empty_group`operationを別途提案しなくても1回の監査pass
で完結します。`remove_empty_group`operation自体は、モデルが明示的に
container削除を提案したい場合のために従来どおり利用できます。

## Canonicalization

parseは、schema・type・confidence・重複ID・依存関係のみを検証し、
tree側のID存在チェックは行いません。canonicalizationは、parseされた
すべてのID風文字列を次の順で解決します。

1. そのままnode・item・candidateのいずれかのID集合に一致すれば採用
2. candidate IDがすでにtree nodeとして存在する(昇格済み)なら、node
   IDとして扱う
3. item `clientKey`(モデルのround-local別名)が一意にitemを指す場合は
   そのitem IDへ解決する
4. 未昇格のcandidate IDがnode文脈(`toParentCanonicalNodeId`等)で
   使われた場合は解決不能とする
5. 一つの別名が複数の解へ一致する場合は、そのoperation/findingだけを
   `ambiguous_alias`で拒否する
6. どの規則でも解決できない場合は`unresolved_canonical_id`で拒否する

解決できないfinding/operationはそれ単体だけが除外され、応答全体は
破棄されません。置換件数は`operationsCanonicalized`として
`validator_result`に記録されます。JSON envelope・version不一致・
上限違反などレスポンス全体の安全性を判定できない場合だけ
`invalid_schema`になります。

## Effective confidence

`below_high_confidence_threshold`のようなmodel自己申告confidenceのみに
よる単独判定は廃止されました。move型operation(`move_item`,
`restore_previous_parent`, `move_node`, `fold_candidate_into_topic`)は、
サーバー側で合成した`effectiveConfidence`を`HighConfidenceThreshold`
(既定0.90、`TREE_AUDIT_HIGH_CONFIDENCE_THRESHOLD`で設定可能、下げる
方向の変更はしていません)と比較します。それ以外のapplicable
operationは、modelの自己申告confidenceをそのまま閾値と比較します。

```
effectiveConfidence = clamp01(modelConfidence + bonuses - penalties)
```

bonusは`modelConfidence >= 0.60`の場合のみ付与され、1件あたり+0.05、
合計上限は+0.15です。

- **unclassifiedOrGenericParentBonus** (+0.05): 現在の親が
  `topic-unclassified`、genericなlabel(「その他」「詳細」等)、または
  現在score(現在親との類似度)が`TREE_AUDIT_COHESION_THRESHOLD`未満
- **precheckAgreementBonus** (+0.05): deterministic precheckが同一
  targetについて、移動先(またはそのtop-level container)を指す
  `subject_mismatch` / `cross_agenda_contamination` /
  `candidate_should_fold_into_existing_topic`のfindingを既に検出、
  または`reference_evidence_reparent`(対象の直近の親変更自体が
  reference/recap証拠のみによるもの)を検出している
- **fixedAgendaMatchBonus** (+0.05): 移動先の系列に固定agendaの祖先が
  存在し、item本文と固定agendaのタイトル(および移動先系列のテキスト)に
  共有主題語があり、移動先とのscoreがcohesion閾値以上

penaltyは無条件に適用されます。

- **recapContaminationPenalty** (-0.10): operation自身のevidenceに
  reference/recap roleのsequenceと primary/supporting roleのsequenceが
  混在している(evidence全体がreference roleのみの場合は、penaltyでは
  なくhard reject `reference_evidence_only`のまま)

manual editが記録されている対象(`LastParentChangeSource`が
user/manual系)は、confidenceに関わらずhard reject
(`manual_edit_protected`)されます。現状のsource値はmodel/heuristic系
のみのはずですが、将来のmanual編集機能に備えた防御的な実装です。

`modelConfidence`と`effectiveConfidence`はいずれも
operation単位の評価結果とログへ記録されます。

## Parent stickiness

`TREE_AUDIT_REQUIRED_IMPROVEMENT_MARGIN`(既定0.18)は、新しい親の
subject scoreが現在の親より一定以上高くなければならないという要件を
表します。次の場合にmarginが半減(×0.5)されます。

- 現在の親が`topic-unclassified`、genericなlabel、または現在scoreが
  cohesion閾値未満
- `restore_previous_parent`の場合(既存の挙動を維持)

直近2 tree versions以内に親変更されたnodeは、原則として
`recent_parent_change_sticky`で再移動を拒否されますが、直近の親変更が
reference-evidenceのみによるものだった場合(`reference_evidence_reparent`
precheck該当)、または現在の親が`topic-unclassified`の場合は免除されます。

**冗長group平坦化の例外**(`move_item`のみ): 移動先
(`toParentCanonicalNodeId`)が現在の親(kind=group)のさらに親(祖父)であり、
かつ現在のgroupのlabel/descriptionと移動先のlabel/descriptionに共有主題語
が成立する、または類似度0.5以上の場合、marginチェック自体を免除します。
これは、groupが移動先topicと同義の冗長な階層である(単に親topicの内容を
繰り返しているだけの中間ノード)ケースを対象とした一般則です。
`parentText()`は祖先を連結してscoreするため、group直下のitemを祖父
(移動先)へ移動しても、group自身のテキストがすでにscoreへ含まれている
分、改善が全く出ない(またはむしろscoreが下がる)構造的な事情があります。
このほかの検証(fromParent一致・evidence role・cycle・depth・cross-agenda・
tree integrity等)はすべて維持されます。

**固定アジェンダ復帰の例外**(`move_item`/`restore_previous_parent`):
次を**すべて**満たす場合、`parent_stickiness_margin`と
`recent_parent_change_sticky`の両方を免除します。

- 移動先(`toParentCanonicalNodeId`)に固定agenda祖先が存在する
  (action_summaryを除く)
- 現在の親(`fromParentCanonicalNodeId`)に固定agenda祖先が**存在しない**
  (dynamic topic・topic-unclassified・その配下のgroup)
- 現在の親がtopic-unclassified、genericなlabel、または現在scoreが
  cohesion閾値未満(stickinessが守るべき「凝集した正しい親」ではない)

「再発防止策」のような短い固定agendaラベルは、item本文とのbigram類似が
ほぼ0になりやすく、通常のsimilarityベースのmarginでは固定agendaへの
復帰を構造的に検証できません。effective confidenceの閾値自体は変えず、
このピンポイントな状況でだけmarginを免除します。fromParentに固定agenda
祖先が無いことが条件のため、別の固定agenda配下からの移動(agenda間移動)
はこの例外の対象に自然となりません(`cross_fixed_agenda_boundary`は
引き続き独立して働きます)。fromParent一致・primary evidence・evidence
binding・depth・per-operation integrity・heuristic非悪化・manual保護等の
他の検証はすべて維持されます。

**非悪化ゲートの対称除外**: `heuristic_structural_quality_worsened`
(deterministic precheckによるtree全体の不整合件数の非悪化ゲート)は、
本来operationがtreeの他の部分へ与える副作用を防ぐためのものです。しかし
固定agendaへ復帰したitem自身は、移動先の短い固定agendaラベルよりtree内の
別のcontainerの方が(たとえ僅差でも)高スコアと判定されることがあり、
これは`subject_mismatch`/`cross_agenda_contamination`の自己参照的な
findingとして計上されます(現在の親がagenda originになった場合、この
2種は同一itemに対しペアで発火する既存の設計であり、変更していません)。
これはmargin/stickinessの例外がすでに判断した「表層類似では検証できない
が構造的には正しい配置」を、非悪化ゲートが表層類似で二重に拒否評価して
いるにすぎません。

このため、fixed-agenda-return例外が成立したoperationに限り、非悪化ゲートは
「移動対象item**のみ**を指す(他のnodeを含まない)`subject_mismatch`・
`cross_agenda_contamination`finding」を、operation適用前後の両方の件数
から対称に除外して比較します。対称除外である点が重要です。移動前に
同itemへ既に発火していたfindingも同様に除外するため、除外は「このitem
自身の表層類似判定を一時的に無視する」だけであり、他のnode・candidate・
groupに関するfindingの増減はそのまま検出されます。除外はfixed-agenda-return
例外が成立したoperationにのみ適用され、それ以外のoperationの非悪化判定は
一切変更していません。バッチ内の次operationの判定に使う残存件数(累積値)は
除外なしの実件数で更新されるため、対称除外は「このoperation単体の合否
判定」だけに影響し、tree全体の品質追跡指標を歪めません。

一律のthreshold低下やmargin撤廃は行っていません。上記のbonus/exemptionは
いずれも、閾値そのものやmarginそのものを引き下げるものではなく、個別の
状況証拠が揃った場合にだけ、その状況に応じた調整を一度だけ適用します。

`fold_candidate_into_topic`にも同じgeneric-parent margin半減補正
(design D4)が適用されます。固定agendaへの復帰が主な経路です。

## Scheduling

live auditは同一sessionでsingle-flightです。監査中の複数triggerはpending
1件へcoalesceされます。監査は取得済みsnapshotで非同期実行し、ライブ抽出を
停止しません。通常live保存、reorganizer保存、audit適用はすべてDB version
CASを使います。どちらが先に保存されても、古い`basedOnTreeVersion`の結果は
破棄・再試行されます。別instanceとの競合もdurable duplicate claimとDB CAS
で防ぎます。

既定では3 tree versions、またはtree変更後300秒で監査対象となり、semantic
anomaly、candidate作成・promotion、reparent、低confidence、fragmentation、
stale tentative等は条件triggerになります。通常triggerは最小300秒、
12 provider calls/時、20 provider calls/sessionです。`semantic_anomaly`等の
high-severity triggerは通常枠と分離し、最小60秒・4 calls/時です。通常
session上限後もhigh-severity triggerを許可し、`final_tree_review`は全上限の
別枠です。同一tree version/snapshot hash/prompt/deploymentの同時実行はDBの
active claimで重複実行しません。直近の同一terminal runも通常schedulerでは
重複排除します。抑制runも`trigger_class`, `suppression_reason`,
`meeting_elapsed_seconds`付きで履歴保存します。

## 障害分離と終了境界

audit repository、migration、provider、schema、timeoutの失敗はaudit
goroutine内で完結し、ライブ抽出・通常tree保存・WebSocket配信を待たせません。
audit履歴を確保できない場合はproviderを呼びません。tree CASとaudit run
更新は同一DB transactionで行うため、履歴のないtree変更はcommitされません。

sessionがendingへ入るとpending live auditを破棄し、実行中live auditの
contextをcancelして以後の適用を禁止します。final reviewはlive auditとは
別flightで実行されます。providerがcontextを無視してtimeout後に応答しても、
context再確認とDB CASより遅延responseは適用されません。

## Snapshot limits

`TREE_AUDIT_MAX_NODES`, `TREE_AUDIT_MAX_RECENT_SEGMENTS`,
`TREE_AUDIT_MAX_EVIDENCE_SEGMENTS`, `TREE_AUDIT_MAX_INPUT_TOKENS`で入力を
制限します。上限時はfixed agenda、precheck対象、最近変更されたnodeを優先し、
evidence発話の前後を含めます。全文transcriptはライブ監査へ無条件送信しません。
final reviewだけは保存済みtranscriptを広く読み、同じ上限付きsnapshotへ
圧縮します。

## Finalization

終了pipelineはfinal transcript flush後に`final_tree_review`を実行し、その後
tree snapshotとsummaryを保存します。reviewのtimeout・schema failure・
provider failure時も最後の正常live treeで継続し、
finalization/tree snapshotの`finalTreeReviewFailed`と`degraded`で観測できます。

順序は常に`final flush → final_tree_review → final tree snapshot → final summary`
です。

## Replay history

`meeting_tree_audit_runs`はrunごとに、session/run ID、based/resulting
version、snapshot hash、trigger reason/class、prompt/schema version、
deployment alias、provider model、圧縮後の正確なaudit input、raw response、
finding、operation、validator結果(`operationsProposed`,
`operationsCanonicalized`, `operationsValid`, `operationsApplied`,
`operationsRejected`等)、disposition、heuristic metrics、token usage、
elapsed、error code/message、provider call有無、抑制理由、会議経過秒を
保存します。入力は`TREE_AUDIT_MAX_INPUT_TOKENS`で制限済みで、保存JSONは
既定256 KiBに制限されます。同じsnapshotでもprompt versionまたはdeployment
を変更するか、運用ツールから`manual_replay` triggerを明示すれば将来の
再評価が可能です。通常schedulerが偶発的に同じ入力を再送することはありません。

`heuristicDefectCountBefore/After`はsubject token overlap等による
deterministic finding件数であり、意味的品質の保証値ではありません。
個別の`topicOutliers`, `candidateFragmentation`, `crossAgendaContamination`
とともに非悪化gateへ使います。日本語の表記揺れや暗黙的文脈には限界があり、
実際の評価テキストが対象の主題語を字面上共有しない場合(例:
音声認識由来の言い回しの揺れ)、意味的には妥当な移動でもこの機構だけでは
確認できず、安全側に倒れて拒否されることがあります。
