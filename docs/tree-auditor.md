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
   empty group/agenda reference/depth等の構造検証(不正ならAIを呼ばず終了)
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

v8 schemaは26種のoperation typeとagenda lifecycle findingを認識します。サーバーが実際に適用しうる
**applicable**は次の18種です。

`move_item`, `restore_previous_parent`, `move_node`, `merge_items`,
`rewrite_item`, `rewrite_item_title`, `rewrite_item_description`,
`reclassify_kind`, `reclassify_subtype`,
`deactivate_item`, `assign_item_to_candidate`, `change_evidence_role`,
`create_topic_from_candidate`, `fold_candidate_into_topic`,
`deactivate_candidate`, `rename_group`, `rename_topic`, `remove_empty_group`

残る8種は**unsupported**(schemaとしては認識されるがapplierが存在せず、
confidenceに関わらず必ず`unsupported_operation`で拒否)です。

`merge_candidates`, `promote_candidate`, `mark_candidate_tentative`,
`merge_dynamic_topics`, `create_group`, `move_items_to_group`,
`split_candidate`, `merge_fragmented_utterances`

applicableなoperationはさらに3段階のrisk class(`safe`/`moderate`/
`destructive`、後述の「Risk classとconfidence閾値」参照)へ分類され、
個別の安全条件(confidence、parent一致、evidence、
cycle、depth、subject整合等)を満たさなければ拒否されます。拒否理由
(`reason`)は具体的な文字列(例:
`parent_stickiness_margin`, `reference_evidence_only`,
`root_immutable`, `cycle_target_descendant`,
`recap_only_candidate`, `deactivate_grounds_not_verified`,
`dependency_rejected`, `unresolved_canonical_id`, `ambiguous_alias`)で
記録されます。`validator_result`の各operation評価
(`evaluations[].category`)には、unsupported operationなら
`unsupported`、applicableなoperationならそのrisk class名
(`safe`/`moderate`/`destructive`)が、採否(accept/reject)に関わらず
常に記録されます。

監査snapshotの`agendaIds`は論理agenda recordの参照IDです。tree operationの
対象・移動先には`nodes[].canonicalNodeId`の`topic-*` IDを使い、agenda IDを
node IDとして指定しません。agendaとの対応はnodeの`agendaRefs`で解決します。

v7では、`stale_no_agenda_span`、`agenda_reentry_missed`、
`agenda_item_forced_to_no_agenda`、`unclassified_todo_after_agenda_reentry`、
`parent_child_same_title`、`low_information_child`、
`generic_question_without_subject`、`agenda_title_copied_as_item`、
`meeting_end_as_decision`、`action_summary_missing_active_todos`を追加しました。
no-agenda区間そのものは保存ツリーnodeではないため専用のclose operationは追加せず、
保存済み誤配置は既存`move_item`、低情報itemは`rewrite_item` / `merge_items` /
`deactivate_item`、冗長groupは`rename_group` / `remove_empty_group`で安全条件を
満たすものだけ修復します。明確なagenda/dynamic重複はライブの決定的mergeを優先し、
unsupportedの`merge_dynamic_topics`は引き続き適用しません。

v8ではさらに`generic_topic_label`、`generic_candidate_label`、
`topic_label_not_derived_from_children`、`single_child_generic_topic`、
`risk_todo_subject_fragmentation`、`related_action_outside_risk_topic`、
`leading_particle_fragment`、`anaphora_target_missing`、
`incomplete_stt_segment_item`、`decision_missing_object`、
`no_agenda_false_positive_from_modifier`を検出します。genericなagenda/dynamic
topicは、子itemから具体名を復元できる場合に限り`rename_topic`で修復できます。

### move_node

topic/group container nodeを新しい親(root/topic/group)へ移動します。
agenda anchor自体は不変ですが、`agendaRefs`付きmaterialized topicは通常topicと同様に移動できます。root・action_summaryは対象にできません。
自身の子孫への移動(cycle)は禁止され、移動後の部分木がtreeの最大深さを
超えないことを検証します。移動元・移動先のsubject整合(部分木テキストと
移動先系列の類似度が現状以上、または現在の親がgeneric/unclassified)も
必須です。

### merge_items

2件以上の現存itemを1件へ統合します。統合可能な組は既存の
`sameKindSemanticDuplicate`/`sameCanonicalProposition`判定、または連結された
重複グラフで判定します。decision/todo/riskや、issueの異なるsubtypeは
別命題として保持し、同じ意味分類かつcanonical propositionが明確な場合のみ統合します。survivorは指定順の
先頭itemで、evidenceは和集合、assignee/deadline/statusなど非空フィールドは
survivor優先で欠落させません。被統合itemはtreeから除去されますが、
`Items[]`には`mergedIntoId`付きで残ります。

### rewrite_item / rewrite_item_title / rewrite_item_description

新しいlabelが旧title/body(またはoperationのevidence)と主題語を共有する
場合のみ書き換えを許可します。kindの変更やevidenceにない固有名詞・期限・
担当者の追加は認めません。

### reclassify_kind / reclassify_subtype

`kind`（`issue | risk | fact | decision | todo`）とissueの`subtype`
（`discussion | confirmation | question | investigation`）を、statusやツリー階層とは
独立して修正します。primary evidenceが必須です。decisionへの変更とdecisionからの
自動変更は引き続き保護されます。`fact` / `issue` / `risk` / `todo`間の変更は、
共通semantic kind validatorが同じ変更先をaudit閾値以上で判定した場合だけ許可します。
適用時はitemと対応tree nodeを同時に更新し、evidenceとstatusはkindから独立して維持します。

## Semantic grounding validator

表示可能なdetail itemの一次証拠は、同一sessionのcovered range内にあるfinal transcriptです。
`partial_transcript`は採用せず、`recap_transcript`は既存命題の再確認を優先して、新規命題には
通常より厳しい直接一致を要求します。入力は次のsource typeとして区別します。

- `final_transcript` / `partial_transcript` / `recap_transcript`
- `pre_meeting_input` / `agenda_title` / `agenda_metadata` / `semantic_hint`
- `existing_tree` / `audit_finding` / `model_inference`

事前入力とagenda系情報はagenda作成、topic・parent候補、同義語補助には利用できますが、
fact / issue / risk / todo / decisionの事実根拠にはなりません。sequenceの存在・final・covered
rangeを確認するstructural validationの後、subject、predicate、entity、qualifierを検査します。
人名、担当者、場所、階数、日時、数値、識別子、原因、対策、決定、将来影響は引用発言による
直接支持を必須とします。

live extraction v18以降の`evidenceSnippets`は、指定sequenceのfinal発言からの短い引用です。
全角半角、空白、句読点、数字表記、英字大小文字を正規化して実在性と命題支持を確認します。
安全に発言範囲へ縮約できる場合は`rewritten`、一部だけ支持される場合は`tentative`、
事前contextだけにある場合は`candidate_only`、支持されない場合は`rejected`とし、後三者は
通常の表示ツリーへ追加しません。旧v17以前のpersisted payloadは、grounding metadataが
一件もないsnapshotに限ってfinal repairで破壊的に再解釈しない後方互換を維持します。

semantic splitでは各fragmentのsubject・predicateを各evidence sequenceへ再照合し、対応する
sequenceとsnippetだけを引き継ぎます。元itemがgroundedでも、対応sequenceを特定できない
fragmentは棄却します。その後にlow-information、kind、dedup、agenda assignmentを実行します。

`later_confirmed_evidence_supersedes_open_state`は、primary/correctionのfinal sequenceが
`createdThroughSequenceNo`と`initialEvidenceMaxSequenceNo`の両方より大きく、同じ命題を
明示的に確認し、recapでない場合だけ使用します。同一sequence、同一roundの別fragment、
事前context、agenda metadata、existing treeはlater evidenceになりません。

tree auditのrewrite / merge / reclassifyとfinal deterministic repairにも同じvalidatorを適用します。
operation評価には`groundingDecision`、`groundingConfidence`、hash化した`unsupportedAtoms`、
`groundingSourceTypes`を記録し、未検証の意味追加は`semantic_grounding_not_verified`で拒否します。
本文を出さない`AI item grounding evaluated.`、`AI split fragment grounding evaluated.`、
`AI context leakage prevented.`ログで判定を追跡できます。

## Semantic kind validator

live prompt v20とサーバー共通validatorは、単語だけでなく次の特徴を組み合わせて
`fact` / `issue` / `risk` / `todo`を判定します。

- 時制: `past` / `current` / `future` / `unknown`
- 確実性: `confirmed` / `reported` / `hypothesis` / `uncertain` /
  `unresolved` / `proposed` / `committed`
- 意味役割: 状態、原因仮説、未解決の問い、将来悪影響、行動、提案
- 補助特徴: 悪影響、不確実性、未来事象、現在問題、確認表現、実行動詞、
  完了行動、scheduled event、event date、担当者、行動節に係る期限、commitment、
  調査意図、mitigation意図

優先順位は、強い担当・期限・commitment付き行動、確認済み事実、原因仮説・
現在の未解決事項、将来の不確定な悪影響、その他の明示的行動です。
原因である可能性は`issue/investigation`、将来の悪影響・不確実性・negative
impactをすべて満たす場合だけ`risk`、担当・期限・commitmentのある対策行動は`todo`として扱います。
完了した作業は`fact`へ戻し、対象物の失効・満了日はevent dateとして作業期限と分離します。
未確定の提案、必要性の指摘、不完全な目的節は`issue/discussion`とし、同一発言の
fact / risk / todoはfragmentごとに証拠、担当、期限を局所化します。既存IssueのidをTodo更新に
再利用した場合も、別itemとして分離して両方を保持します。

担当者（話者本人を含む）・将来の実行動詞・具体的な行動対象・commitmentが同一節に揃う発話は、
期限の有無にかかわらず決定論的Todo safety netでも検査します。1 sequenceからの採用は
3件を上限とし、上限前でも候補密度が高い場合は`deterministic_candidate_density_anomaly`を
本文なしで記録します。方針採用、必須化、運用開始はTodoではなくDecisionとして分離します。
モデルが同じ分析batchの
低情報発話だけを返した場合や、行動をIssueへ誤分類した場合でも、後続の強いTodo節を
final transcriptから補完します。複数の担当者または期限を持つ節は別Todoとして保持し、
既存Todoのenrichmentは高い命題一致がある一件に限定します。この補完は追加のAI呼び出しを
行いません。

明示的な訂正は、置換Factがgroundingを通らなかった時点で旧命題を
`correction_pending_replacement`として通常ツリーから退避します。final repairは同じ
final transcriptから訂正節だけを再構築し、成功時は旧Itemをinactiveにして
`superseded` tombstoneへ記録します。再実行時は同じ置換を増やしません。訂正でない
過去の「設定を修正しました」やrecapだけからFactを新設することはありません。

同じkind・同じItem IDの更新でも、主命題または日時が異なる場合は別Itemへdetachし、
古い発生事実と後続の復旧事実のevidenceを和集合にしません。recapは既存の担当・期限付き
Todoや未解決Issueを上書きせず、該当する命題節だけへevidenceを局所化します。
Item IDは生成後はopaqueな不変識別子です。`item-todo-*`等の歴史的prefixは現在の
`kind`を保証するAPIではなく、表示・分類は常に`items[].kind`を参照します。
同じIDを使う更新はsubject / predicate / object / qualifierを比較し、同一命題の
言い換え・具体化だけを許可します。明示訂正、または中心命題に互換性がない更新は
新しいIDへdetachし、`event=item_proposition_changed`へ本文を含めず記録します。

coverage retryを含むlive入力は`currentRoundSegments` / `retrySegments` /
`contextOnlySegments` / `recapSegments`へ分離してpromptへ渡します。retryは未反映発話を
次の通常roundで一度だけ再提示する仕組みで、新規発話の命題やIDを上書きする権限では
ありません。agenda照合ではprimary/source evidenceを優先し、recapはsourceが存在しない
場合だけfallbackとして使います。判定は`event=agenda_assignment_decision`、
retry結果は`event=meaningful_coverage_retry_result`、low-information検出は
`event=low_information_item_detected`、決定論的補完候補は
`event=deterministic_synthesis_candidate`に、session/version/sequence/item ID、
判定特徴、採否、reasonだけを記録します。

同一itemのdescriptionに複数の強い意味役割を持つ文がある場合は、各文を再検証して
別itemへ分割します。subjectを持たない短いfragmentはlow-information gateで棄却し、
元itemのID、parent、evidenceを保持します。分割または既存の別命題として共存する
itemには、既存`tree.relations`で`todo -> risk: mitigates`、
`todo -> issue: addresses`、`fact -> issue: supports`を追加します。
この追加は既存Web payloadのoptional fieldを使うためschema変更はありません。

validatorはmodel受信後、split後、deterministic synthesis後、semantic dedup後、
legacy normalization、audit operation適用前、final deterministic repairで再利用します。
変更閾値はlive `0.90`、legacy/audit `0.92`、final `0.88`です。閾値未満は元kindを
維持し、ambiguous/tentativeとして観測します。

本文を出さない`AI item kind validation evaluated`、
`AI item semantic split completed`、`Final item kind distribution evaluated`
ログで、判定特徴、変更理由、confidence、分割数、kind分布の偏りを確認できます。

### deactivate_item

重複・superseded・recap/reference-only・discourse-only(会話制御発話)・
kindごとの独立した命題を持たないlow-information itemのいずれかを
サーバー側で再検証できた場合のみtreeから除去し、`inactive:true`を
記録します。モデルの主張のみでは適用されません。手動編集済みの対象は
`manual_edit_protected`で拒否します。decision/TODO/risk、tentative、直近生成、子を持つ、
他ノードから参照されるitemは保護されます。またrewriteまたはmergeで回復できる場合は
`rewrite_preferred` / `merge_preferred`でdeactivateを拒否します。適用時には
`suppressionReason`とsession内tombstoneを作成します。

### assign_item_to_candidate / change_evidence_role

前者はitemを未昇格candidateへtentativeとして割り当てます。後者は
指定したevidence sequenceのみを`reference_recap`へ格下げします
(reference_recapへの降格のみが許可され、対象sequenceはitemの
evidenceに実在する必要があります)。降格は次回以降のevidence role
分類でも尊重されます。

### create_topic_from_candidate

未昇格・非inactive・非recap-onlyのcandidateを、そのIDのままroot直下の
dynamic topicへ昇格させます。既存の動的topicやagenda anchorと意味的に
重複する場合は拒否され(`duplicate_topic`/`should_fold_into_fixed_agenda`)、
fold_candidate_into_topicや既存agendaへの割当が推奨されます。

### fold_candidate_into_topic / deactivate_candidate / rename_group / rename_topic / remove_empty_group

fold_candidate_into_topicはcandidateのevidence itemを既存topicへ移し、
candidateを非活性化します。deactivate_candidateはroundCount<=1の
candidateを無条件で、それ以外は他条件を満たした場合のみ非活性化します。
rename_groupはlabelの意味的cohesionが悪化しない場合のみ適用されます。
rename_topicはagenda/dynamic topicだけを対象とし、node ID、parent、children、
`agendaRefs`を維持します。子itemとのcohesionが悪化する名前、genericな新名称、
root・`topic-unclassified`・action_summary・manual編集topicは拒否します。

`remove_empty_group`は、子が0件のgroup、子が0件になった昇格済みdynamic
topic、または空のsystem生成`topic-unclassified`(root・action_summary以外)を削除します。空のagenda materialized topicはdeterministic lifecycle処理がdematerializeします。子が1件以上残っている場合は
`group_not_empty`で拒否され、
削除対象として不適格なnode(未知ID、group/dynamic topic以外、agenda
materialized topic・root・action_summary)は`unknown_or_immutable_container`で
拒否されます。

`validateTreeIntegrity`は「group」kindのnodeが子0件になることを恒久的な
不整合として扱います(dynamic topicにはこの制約はありません)。このため、
あるoperationがcontainerの最後の子を除去した(移動・deactivate・merge
のいずれであれ)直後、そのcontainer自身が子0件のまま残っていると、その
operationは`tree_integrity_rejected`で拒否されてしまいます。

これを避けるため、operation単位のvalidationはoperation適用直後・
integrity再検証の直前に、**空になったcontainerのcascade自動整理**を
行います。あるoperationの結果、親子関係が変わった(除去または再親化
された)nodeの旧親について、そのnodeがgroup、子0件になった
昇格済みdynamic topic、または空のsystem生成`topic-unclassified`
(agenda materialized topic・root・action_summary以外、remove_empty_groupと同じ対象定義)であり、
かつ子が0件であれば、そのnodeをtreeから除去します。除去後にその
node自身の親も同条件を満たせばcascadeでさらに上位へ除去を続けます。
この自動整理は、その
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
tree側のID存在チェックは行いません。canonicalizationは、ID解決の前に
まず**operation種別ごとのフィールド正規化**を行い、そのあとparseされた
すべてのID風文字列を次の順で解決します。

### フィールド正規化

v3 schemaは全operation typeで同じID系フィールド
(`targetCanonicalItemId`, `targetCanonicalNodeId`,
`targetCanonicalItemIds`, `targetCandidateId`,
`fromParentCanonicalNodeId`, `toParentCanonicalNodeId`)を公開しますが、
実際にapplier(`applyOneTreeAuditOperation`)が読むフィールドはoperation
typeごとに異なります。フィールド正規化はID解決より前に、operationが
実際に使わないIDフィールドを空へ消去します(必須フィールド自体が
未解決・不正な場合の拒否は従来どおりID解決側で行われ、正規化そのものは
拒否にはなりません)。これは実セッションで観測された、モデルが
`move_item`の`targetCanonicalItemId`と同じitem IDを、move_itemでは
使われない`targetCanonicalNodeId`にも冗長入力し、その未使用フィールドの
node文脈チェック(`requireContainer`)で`target_not_node`となり
operation全体が破棄されていた問題への対処です。

次の2件は、消去の前にフィールド間で値を補完する救済を行います。

- `merge_items`: `targetCanonicalItemIds`が1要素、かつ`targetCanonicalItemId`
  がそれと異なる値を持つ場合、`targetCanonicalItemId`を先頭に補って
  2要素の`targetCanonicalItemIds`にする
- `fold_candidate_into_topic`: 統合先topicはapplier・effective
  confidence計算のいずれも`toParentCanonicalNodeId`を読みます
  (`targetCanonicalNodeId`は読みません)。`toParentCanonicalNodeId`が空で
  `targetCanonicalNodeId`に値がある場合、そちらを`toParentCanonicalNodeId`
  へ補完します

消去・補完のいずれも`response`の`canonicalizationCount`へ加算されるだけで、
それ自体を理由にoperationを拒否することはありません。

### ID解決

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
破棄されません。canonicalizationを通過してvalidatorへ渡ったoperation件数は
`operationsCanonicalized`として`validator_result`に記録されます
(別名置換そのものの件数はresponse内で別管理)。JSON envelope・version不一致・
上限違反などレスポンス全体の安全性を判定できない場合だけ
`invalid_schema`になります。

## Effective confidence

`below_high_confidence_threshold`のようなmodel自己申告confidenceのみに
よる単独判定は廃止されました。move型operation(`move_item`,
`restore_previous_parent`, `move_node`, `fold_candidate_into_topic`)は、
サーバー側で合成した`effectiveConfidence`を、下記のrisk classごとの
閾値と比較します。それ以外のapplicable operationは、modelの自己申告
confidenceをそのままrisk classごとの閾値と比較します。

### Risk classとconfidence閾値

`HighConfidenceThreshold`(HCT、既定0.90、
`TREE_AUDIT_HIGH_CONFIDENCE_THRESHOLD`で設定可能)を全operation種別へ
一律適用していた単一gateは廃止し、operation typeを3段階のrisk classへ
分類してから閾値を導出します(`treeAuditOperationRiskClass`)。

- **safe**(閾値 = HCT − 0.20): `rewrite_item`, `rewrite_item_title`,
  `rewrite_item_description`, `reclassify_subtype`,
  `change_evidence_role`, `rename_group`, `rename_topic`, `remove_empty_group`,
  `assign_item_to_candidate`
- **moderate**(閾値 = HCT − 0.10): `move_item`, `restore_previous_parent`,
  `move_node`, `fold_candidate_into_topic`, `create_topic_from_candidate`,
  `deactivate_candidate`, `merge_items`, `reclassify_kind`
- **destructive**(閾値 = HCT): `deactivate_item`

いずれの閾値も0.50未満へはclampされません(下限0.50)。既定のHCT=0.90
であればsafe=0.70、moderate=0.80、destructive=0.90です。新しい環境変数は
追加していません。

次の2条件は、対象itemの現在の状態に応じてoperationのrisk classを
`destructive`(=HCT)へ強制的に格上げします(`treeAuditEffectiveRiskClass`)。

- `merge_items`の対象itemに`kind=decision`が1件でも含まれる場合
- `reclassify_kind`で対象itemの現在`kind`が`decision`/`todo`/`risk`であり、
  別kindへの変更を要求している場合(applier自身の
  `protected_semantic_kind`保護と同じ対象への、confidence gate側での
  多重防御です)

`deactivate_item`は元々HCTと比較されており、この変更後も変わりません。
manual edit保護(`manual_edit_protected`)、`dependency_rejected`、
`tree_integrity_rejected`、`heuristic_structural_quality_worsened`等の
既存の安全機構はいずれもrisk classとは独立に、従来どおり動作します。

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
- **fixedAgendaMatchBonus** (+0.05、互換名): 移動先の系列に
  `agendaRefs`付きmaterialized topicの祖先が存在し、item本文とagenda anchorの
  タイトル(および移動先系列のテキスト)に
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

**materializedアジェンダ復帰の例外**(`move_item`/`restore_previous_parent`):
次を**すべて**満たす場合、`parent_stickiness_margin`と
`recent_parent_change_sticky`の両方を免除します。

- 移動先(`toParentCanonicalNodeId`)にagendaRefs付きtopic祖先が存在する
  (action_summaryを除く)
- 現在の親(`fromParentCanonicalNodeId`)にagendaRefs付きtopic祖先が**存在しない**
  (dynamic topic・topic-unclassified・その配下のgroup)
- 現在の親がtopic-unclassified、genericなlabel、または現在scoreが
  cohesion閾値未満(stickinessが守るべき「凝集した正しい親」ではない)

「再発防止策」のような短いmaterialized topicラベルは、item本文とのbigram類似が
ほぼ0になりやすく、通常のsimilarityベースのmarginではagenda topicへの
復帰を構造的に検証できません。effective confidenceの閾値自体は変えず、
このピンポイントな状況でだけmarginを免除します。agenda topic間の移動も
一律禁止せず、通常のsubject改善・evidence条件で判定します。fromParent一致・primary evidence・evidence
binding・depth・per-operation integrity・heuristic非悪化・manual保護等の
他の検証はすべて維持されます。

**非悪化ゲートの対称除外**: `heuristic_structural_quality_worsened`
(deterministic precheckによるtree全体の不整合件数の非悪化ゲート)は、
本来operationがtreeの他の部分へ与える副作用を防ぐためのものです。しかし
agenda topicへ復帰したitem自身は、移動先の短いtopicラベルよりtree内の
別のcontainerの方が(たとえ僅差でも)高スコアと判定されることがあり、
これは`subject_mismatch`/`cross_agenda_contamination`の自己参照的な
findingとして計上されます(現在の親がagenda originになった場合、この
2種は同一itemに対しペアで発火する既存の設計であり、変更していません)。
これはmargin/stickinessの例外がすでに判断した「表層類似では検証できない
が構造的には正しい配置」を、非悪化ゲートが表層類似で二重に拒否評価して
いるにすぎません。

このため、materialized-agenda-return例外が成立したoperationに限り、非悪化ゲートは
「移動対象item**のみ**を指す(他のnodeを含まない)`subject_mismatch`・
`cross_agenda_contamination`finding」を、operation適用前後の両方の件数
から対称に除外して比較します。対称除外である点が重要です。移動前に
同itemへ既に発火していたfindingも同様に除外するため、除外は「このitem
自身の表層類似判定を一時的に無視する」だけであり、他のnode・candidate・
groupに関するfindingの増減はそのまま検出されます。除外はmaterialized-agenda-return
例外が成立したoperationにのみ適用され、それ以外のoperationの非悪化判定は
一切変更していません。バッチ内の次operationの判定に使う残存件数(累積値)は
除外なしの実件数で更新されるため、対称除外は「このoperation単体の合否
判定」だけに影響し、tree全体の品質追跡指標を歪めません。

一律のthreshold低下やmargin撤廃は行っていません。上記のbonus/exemptionは
いずれも、閾値そのものやmarginそのものを引き下げるものではなく、個別の
状況証拠が揃った場合にだけ、その状況に応じた調整を一度だけ適用します。

`fold_candidate_into_topic`にも同じgeneric-parent margin半減補正
(design D4)が適用されます。materialized agenda topicへの復帰が主な経路です。

## Live生成前gateとtombstone

live extraction v14は各final発言を`substantive`、`correction`、`recap`、
`discourse_transition`、`filler`へ分類します。モデル分類に加え、サーバーは
話題選択語・会議上のメタ対象・遷移述語の組合せ、具体的な対象・期限・担当・
判断の有無、前後のevidenceを使って決定論的に再分類します。
`discourse_transition`はno-agenda span開始には使えますが、表示itemやcandidate
にはなりません。保存前のlow-information validatorはkind、title/body、
evidence role、担当・期限、decision/correction marker、前後発言を確認し、
独立した命題を持たない新規itemを拒否します。transcript文脈を持たない
historical replayでは推測で削除せず、audit-time validatorへ委ねます。

open issue補完はmodel itemの後に実行しますが、生成前にevidenceの重なり、
未解決状態、kind/subtype、具体的subject、semantic similarityを比較します。
同じ発話内の具体的model itemが同じ未解決命題を既に表す場合は補完itemを
生成せず、独立したsubjectを持つ複数論点は維持します。指示対象を単体で
特定できないitemはtentative例外を含めて共通validatorへ通します。

low-information rewriteが複合issueを分割した場合、各fragmentに対して
subject/referent、evidence grounding、通常item validatorを再実行し、
成立したfragmentだけを採用します。recap-only evidenceから既存itemに
対応しない新規itemを確定するには、通常roundより厳しい新規subjectと、
担当・期限・数値等の具体性および直接evidence groundingが必要です。

監査の`deactivate_item`、`merge_items`、およびlive semantic mergeで除外した
itemは、live payloadの`itemTombstones`へsession scopeで保存します。各entryは
canonical item ID、proposition key、semantic key hash、evidence fingerprint、
candidate alias、理由、merge先、source/audit versionを持ち、item本文は複製
しません。次のlive mergeより前に照合し、同じitem/candidateの再active化を
防ぎます。明示reopen、実質的に異なる命題、correction、新しい担当・期限・
数値情報、またはmerge先のinactive化は再openを許可し、理由とversionを
tombstoneへ記録します。

## 結果分類と未適用警告

完了したaudit runは`clean`(finding 0 / proposal 0)、`findings_only`
(findingあり / proposal 0)、`rejected`(proposalあり / applied 0)、`applied`
(appliedあり / tree version更新)の4種類に分類します。既存のrun result
(`partial_success`等)はそのまま保持し、分類は別columnへ保存します。
同一sessionの`findings_only`/`rejected`連続数もrunごとに保存し、`clean`または
`applied`で0へ戻します。skipped/failed runは連続数を増やさず、その値を
引き継ぐため、rate limitを挟んでも検知が失われません。session scopeなので
別sessionは常に0から始まります。

`TREE_AUDIT_UNAPPLIED_WARNING_THRESHOLD`(既定3)への到達時だけ
`Tree audit findings remain unapplied.`を出し、同じstreakの4回目以降では
繰り返しません。runには分類、連続数、proposed/canonicalized/valid/applied/
rejected件数、rejection reason集計、resulting versionを永続化します。

閾値到達時にoperationが1件も安全適用できなかった場合は、追加のAI呼び出しを
せず、同じ決定的final repairを最新treeへ試行します。low-information itemの
一意な既存itemへの統合、同一evidence補完重複、recap重複、dangling candidate
など、安全に検証できる変更だけをversion CASで適用します。修復不能、
manual保護、integrity違反の場合はlast-known-good treeを保持します。
validator resultとログには、finding分類、reject理由の集計、
`deterministicFallbackEvaluated`、action、reason、適用versionを記録します。

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
制限します。上限時はagendaRefs付きtopic、precheck対象、最近変更されたnodeを優先し、
evidence発話の前後を含めます。全文transcriptはライブ監査へ無条件送信しません。
final reviewだけは保存済みtranscriptを広く読み、同じ上限付きsnapshotへ
圧縮します。

## Finalization

終了pipelineはfinal transcript flush後に`final_tree_review`を実行し、その後
tree snapshotとsummaryを保存します。reviewのtimeout・schema failure・
provider failure時も最後の正常live treeで継続し、
finalization/tree snapshotの`finalTreeReviewFailed`と`degraded`で観測できます。
final transcript repositoryの読取自体が失敗した場合も、最後の正常live projectionを
入力としてtree snapshotとsummary生成を継続します。この場合はfinalization payloadの
`transcriptFallbackUsed=true`と`finalizationIncomplete=true`、および
`Final transcript fetch failed`ログで、transcript coverageが完全でないことを明示します。

順序は常に`final flush → final_tree_review → deterministic tree repair →
deterministic agenda reconciliation → agenda lifecycle
(空topic整理・not_discussed確定) → final tree snapshot → final summary`です。
deterministic tree repairは、review後にlow-information、recap/same-evidence
duplicate、candidate参照、tree integrityを再検証します。旧agenda node IDを
使うsnapshotは既存の互換正規化をin-memoryで適用してから検証します。
agenda reconciliationは追加のAI呼び出しを行わず、未着手anchorと全final transcript、
canonical item、dynamic candidate / unclassified topicを再照合します。強く一意な一致だけを
採用し、itemのparent、topicの`agendaRefs`、anchorの`materializedTopicIds`、
`agendaProgress`を同じpassで修復します。失敗時またはintegrity違反時は修復前payloadへ
戻してfinalizationを継続します。

final snapshotを保存するときは、最終整理済みの同一treeを先にlive projectionへ
同期し、再取得したlive rowの`updatedAt`をfinal rowにも使います。同じ
treeVersion/analysisVersion/updatedAtを持つliveとfinalはnode countだけでなく
tree hashとtree payloadが一致します。live同期に失敗した場合は内容の異なる
final rowを同じversion/timeで保存しません。REST sanitizationはintegrity修復が
必要な場合に限ってtreeを再構築し、正常なcanonical treeを別形状へ投影し直しません。

agenda progressの`discussing`（UI上のin_progress相当）は、会議終了だけでは
`discussed`へ昇格しません。固定agendaに関連itemがあり、かつ2 active rounds、
4 substantive segments、またはdiscussed/merged anchorのいずれかで十分な活動根拠が
ある場合だけ昇格します。根拠が不足する場合は`discussing`のままです。
`outcomeStatus`はこの進捗statusとは別軸で、十分に議論されても結論や必要な担当・期限が
なければ`unresolved`になり得ます。終了時にはcurrent topic参照だけを解除し、
manual statusは変更しません。

materialized topicのmodel parent aliasはlive payload内だけで管理します。topic mergeでは
統合先へ移し、splitでは曖昧なaliasを新branchへ複製せず、dematerialize/candidate削除では
消滅または有効な統合先へ移します。同一aliasの複数agenda topic所有は許可せず、現在の
topic/item evidenceとagenda scoreに一意な優位がある場合だけ所有者を選びます。以前の
server補正は証拠の一つであり、後続の明確な訂正を妨げません。

agenda lifecycleのintegrityは、tree node数ではなく`agendaRecordCount` /
`agendaRecordsPreserved`、agenda/node ID名前空間、`agendaRefs`と
`materializedTopicIds`の双方向参照、unknown/orphan/duplicate、旧agenda ID edge、
空のmaterialized topic、root到達性を検証します。ライブと最終snapshotのログにはmaterialize /
merge / rename / reparent / dematerialize、assignment結果、空topicとdynamic overlapの
before/after、`agendaTopicIdCollisions`、`agendaNodeIdNamespaceValid`、
`orphanMaterializedTopicIds`、`agendaReferenceIntegrityValid`、`treeIntegrityValid`を記録します。

agenda anchor(`agendaAnchor`)の`Status`は`planned` / `materialized` /
`discussed` / `merged` / `not_discussed`のいずれか1つだけを持つ排他的な値です。
`MaterializedTopicIDs`の
有無はこれとは直交する別属性で、たとえば`action_summary`アジェンダのanchorは
materializeされないまま`discussed`になり得ます。ログは2系統あり、意味が異なる
点に注意してください。「Live agenda anchor lifecycle」のplanned/discussed/
merged/notDiscussedAgendaCountは`validateTreeIntegrity`がtree上のagendaRefs付き
topicから数えたtree由来の値です。「Final tree snapshot persisted」の
`anchorStatusPlannedCount`/`anchorStatusDiscussedCount`/`anchorStatusNotDiscussedCount`
は`summarizeAgendaAnchorStatuses`がanchorの`Status`をそのまま集計した値で、
同じ概念でも算出元が異なるため単純比較はできません。

## 実GPT-5-mini統合テスト

`internal/app/TestRealTreeAuditGPT5Mini`は通常実行ではskipされる明示実行専用
testです。`RUN_REAL_AI_INTEGRATION_TESTS=true`、
`DATABASE_REAL_AI_TEST_URL`、Azure OpenAI endpoint/key、
`AZURE_OPENAI_TREE_AUDIT_DEPLOYMENT`がすべて必要です。DB URLはdatabase名が
`_test`、`_integration`、`_real_ai_test`のいずれかで終わらなければ、接続前に
拒否します。testはそのbase test DB上にさらに一意なdatabaseを作成し、全migration、
fixture投入、実provider、canonicalization、validator、transaction/CAS、tree integrity
確認までを通した後、その一時databaseだけをdropします。通常の`DATABASE_URL`は
読みません。

PowerShellでの実行例:

```powershell
$env:RUN_REAL_AI_INTEGRATION_TESTS='true'
$env:DATABASE_REAL_AI_TEST_URL='postgres://user:password@localhost/deciscope_real_ai_test'
$env:AZURE_OPENAI_TREE_AUDIT_DEPLOYMENT='ds-gpt-5-mini'
go test ./internal/app -run TestRealTreeAuditGPT5Mini -count=1 -v
```

response schemaとprompt versionは固定し、retryは最大2回です。失敗ログにはrun ID、
aggregate finding/operation件数、version、integrity、test database名だけを出し、
credential、prompt全文、transcript全文は出しません。

## Replay history

`meeting_tree_audit_runs`はrunごとに、session/run ID、based/resulting
version、snapshot hash、trigger reason/class、prompt/schema version、
deployment alias、provider model、圧縮後の正確なaudit input、raw response、
finding、operation、validator結果(`operationsProposed`,
`operationsCanonicalized`, `operationsValid`, `operationsApplied`,
`operationsRejected`等)、disposition、heuristic metrics、token usage、
elapsed、error code/message、provider call有無、抑制理由、会議経過秒を
保存します。さらに`result_classification`、`consecutive_unapplied_runs`、
operation件数列、`rejection_reasons`を検索可能な列として保存します。
入力は`TREE_AUDIT_MAX_INPUT_TOKENS`で制限済みで、保存JSONは
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
