# 議論ツリー品質回帰評価

`meeting-quality-eval`は、ライブ分析、candidate昇格、grounding、kind
validator、tree構築、coverage、final deterministic repair、配信用projectionを固定会議
シナリオで再生し、議論ツリー品質の回帰を軸ごとに検出します。

評価器本体は`internal/application`にあり、ファイル、環境変数、ネットワークを
読みません。JSONの読み込み、レポート表示、明示的なbaseline更新だけを
`cmd/meeting-quality-eval`が担当します。この依存方向により、通常テストは
外部AIを必要としません。

## 実行

決定論suite:

```powershell
go run ./cmd/meeting-quality-eval -suite deterministic
```

承認済みbaselineとの比較:

```powershell
go run ./cmd/meeting-quality-eval -suite deterministic -compare-baseline
```

比較は全体合計ではなく`scenario ID × metric`単位です。結果はJSONで、
改善／悪化した評価軸、新規失敗、修復済みシナリオ、
消失した必須命題、新規unsupported命題、親path差分、kind分布差分を別々に
表示します。hard invariant、required proposition、required relation、kind、
evidenceの新規不一致もscenario単位で比較します。改善値で別軸や別scenarioの
悪化を相殺しません。1軸でも悪化すれば終了コードは非0です。

改善を検出した比較も、baseline未更新のままでは非0になります。レビューで改善を
承認した場合に限り、次の明示操作で改善軸だけをratchetします。

```powershell
go run ./cmd/meeting-quality-eval -suite deterministic -accept-improvements
```

`-update-baseline`による全置換は拒否されます。`-accept-improvements`も、
失敗中のsuite、悪化軸、scenario削除、metric schema削除、schema version不一致を
拒否します。出力される`baselineUpdate.appliedMetrics`をPRでレビューします。
fixtureも現在の出力へ自動追従させず、期待する会議上の事実・行動・関係を先に
レビューします。

## シナリオ形式

固定シナリオは
`internal/application/testdata/qualityeval/scenarios.json`、承認済みbaselineは
同じディレクトリの`baseline.json`にあります。各シナリオは次を保持します。

- `transcriptSegments`: sequence、speaker、final発話
- `meetingContext`: 目的、背景、stable agenda record、semantic hints
- `seedPayload`: durable snapshotから始めるreplayだけが使用
- `rounds`: roundのsequence集合と固定AI応答
- `requiredPropositions`: 意味テキスト、必須／許容kind、evidence、agenda
- `requiredRelations`: 命題ID間の論理関係と必要なbranch／ancestor
- `forbiddenResults`: 未発話命題、誤agenda、low-information、重複等
- `safetyExpectations`: repairで保持すべき命題と安全原則
- `finalCoverage`: 最終的に到達すべきsequence

必須命題は生成ID、配列順、全文snapshotでは照合しません。外部AIを使わず、
`subject terms / predicate family / object / qualifier / temporal scope /
epistemic status`を決定論的に抽出します。数値・階・曜日・期限、確定と仮説、
述語familyが矛盾する候補を先に除外し、その後に既存semantic similarityと
日本語bigramを使います。自然な言い換えは許容しますが、単純な部分文字列包含を
即一致にはしません。

初期suiteには次の15件があります。

1. 予定アジェンダへの誤割り当て
2. 予定外candidateの複数round昇格
3. 同一batchの複数項目による昇格
4. recap後のtree保持
5. 未発話情報の混入
6. fact／issue／risk／todo／decision分類
7. low-information node修復
8. 訂正後の古い命題除去
9. evidenceSequenceNosの意味的不一致
10. split fragmentのfuture evidence
11. finalizationのin-flight相当roundとtail flush
12. label rewrite失敗時の重要risk保持
13. fact／原因仮説／適用範囲の論理階層
14. 同一命題のsemantic duplicate
15. VPN証明書更新のカード／tree整合

finalizationの実際のgoroutine待機境界は、固定round replayに加えて
`TestMeetingFinalizationWaitsForInFlightLiveAnalysis`が開始／releaseチャネルで
同期して検証します。どちらにもsleepやtimeout延長による順序固定はありません。

15 scenarioはすべてtranscript、meeting context、fixed AI responseを入力として
productionのmerge関数群を呼びます。12件は空状態から、3件はdurable seedへ
新しいroundを適用します。完成済みsnapshotだけを評価するdeterministic scenarioは
ありません。一方、このrunnerはrepository、scheduler、MeetingAnalysisServiceの
finalization orchestration、`persistFinalTreeSnapshot`を通しません。したがって
「service orchestrationと永続化まで含むfull production pipeline」のscenario数は
0です。この境界は、評価専用にservice処理を模倣せず明示的な監査上の未対応事項と
します。

## Hard invariant

次はbaselineに関係なく1件でscenarioを失敗させます。

- root欠落
- orphan node
- missing edge endpoint
- duplicate node ID
- invalid parent kind
- self parent
- depth上限超過
- agenda record／reference不整合
- 同一agendaのmaterialized topic重複
- inactive itemのtree復活
- 各round時点で未到来のfuture evidence
- transcriptに支持されない中心命題
- 必須命題の消失
- final coverage不足

`TestMeetingQualityHardInvariantsFailIndependently`に加え、
`TestMeetingQualityEvaluatorMutationMatrix`は正常snapshotへ次の13破損を
一件ずつ加え、対応する評価軸の悪化とsuite/baseline比較の失敗を確認します。

- 必須risk削除
- 必須todo削除
- factの誤kind化
- unsupported proposition追加
- future evidence追加
- orphan node
- duplicate node ID
- semantic duplicate
- 文末切断label
- 文脈依存label
- fact／原因仮説／未解決事項の兄弟分断
- dynamic topic itemのtree外移動
- final coverage低下

`cmd/meeting-quality-eval`のテストは同じ13分類について終了判定がerror、すなわち
process exit code非0へ伝播することを確認します。

## 意味品質の評価軸

次を独立して記録・比較します。

- required proposition recall
- unsupported proposition count
- classification accuracy
- risk／todo／decision recall
- semantic duplicate count
- low-information label count
- context-dependent label count
- truncated label count
- hierarchy relation accuracy
- candidate fragmentation count
- cross-agenda contamination count

論理関係は評価器内部で`supported_by`、`caused_by`、`limits`、`resolves`、
`action_for`、`contradicts`、`refines`を表現できます。persisted
`tree.relations`があればそれを優先し、schemaにfirst-class relationがない
ケースは同一semantic branchとrequired ancestorで評価します。

ネットワーク障害scenarioでは「VLAN30の許可漏れ」factが原因仮説を支持し、
「2階遅延まで説明できるか未確認」というissueが仮説の適用範囲を限定する
構造を要求します。別top-level branchへ分断したnegative snapshotで、
両関係が失敗することも評価器自身のテストで確認します。

## 安全なrepair原則

`label-rewrite-failure-preserves-risk`は、次を明示的に評価します。

- label生成失敗だけでgroundedな重要命題を捨てない
- repair失敗時に直前の安全な表現を保持する
- repairが既存の正しいitemをinactive化しない
- classification変更と命題変更を同一操作にしない
- validatorの一時判定だけでdurable情報を消さない

評価器は本番thresholdを変更せず、本番コードへテスト専用分岐を追加しません。

## 決定論suiteと実deployment suite

PRとCIは固定応答だけを使います。通常の`go test ./...`およびCLIの
`deterministic` suiteは外部接続しません。

`TestRealMeetingQualityEvalGPT5Mini`と`TestRealTreeAuditGPT5Mini`は
`internal/app`の手動／nightly用suiteです。明示フラグがなければ即skipします。
有効時はtest専用base DB上に一時DBを作り、実Azure deployment、migration、
repository、finalizationまでを通します。実モデルの一回の出力からbaselineを
生成・更新することはありません。

```powershell
$env:RUN_REAL_AI_INTEGRATION_TESTS='true'
$env:DATABASE_REAL_AI_TEST_URL='postgres://user:password@localhost/deciscope_real_ai_test'
$env:AZURE_OPENAI_ENDPOINT='https://example.openai.azure.com'
$env:AZURE_OPENAI_API_KEY='...'
$env:AZURE_OPENAI_TREE_AUDIT_DEPLOYMENT='ds-gpt-5-mini'
go test ./internal/app -run '^TestReal(MeetingQualityEval|TreeAudit)GPT5Mini$' -count=1 -v
```

DB名が`_test`、`_integration`、`_real_ai_test`で終わらない接続先は拒否します。

## 現在baselineで観測される既知問題

現在のbaselineは、hard invariantと必須命題をすべて満たした上で、次の非ゼロ／
非満点指標を記録しています。

- 未発話情報scenario: semantic duplicate count `1`
- semantic kind scenario: candidate fragmentation count `2`
- finalization tail scenario: classification accuracy `0.5`、risk recall `0`
- duplicate scenario: semantic duplicate count `1`

これらは隠さずレポートし、値の増加（recall／accuracyは低下）を回帰として
失敗させます。各数値には`metricEvidence`として期待命題ID、実item ID、
表示label、判定理由が付属します。0へ修復された場合は改善として表示され、
baselineをratchetするまでCIは成功しません。修復する際は、
先にscenarioと評価を維持したまま本番修正を追加します。

CLIレポートはscenarioごとに、metric、baseline/actual、期待命題本文、最良の
実item、missing/unsupported proposition、relation/kind/evidence mismatchを
JSONで出力します。CLIが失敗を検出した場合はJSON出力後に非0で終了します。
