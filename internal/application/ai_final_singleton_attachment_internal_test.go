package application

import (
	"fmt"
	"strings"
	"testing"
)

// このファイルは「topic-unclassified直下に残った単独itemを、既存の関連topicへ
// 接続する」修復の回帰テスト。再現例(監視運用)だけで通る実装にならないよう、
// 別ドメインの正例、誤接続を許さない負例、言い換え・entity置換・sequence変更に
// 対する不変性、合成主題によるproperty testを同じ判定経路で検証する。

type attachmentFixtureItem struct {
	ID       string
	Kind     string
	Title    string
	Body     string
	Sequence int64
	Manual   bool
}

type attachmentFixtureTopic struct {
	ID          string
	Label       string
	Description string
	Origin      string
	AgendaRefs  []string
	Items       []attachmentFixtureItem
}

func attachmentFixtureLiveItem(source attachmentFixtureItem, assigned bool) liveAnalysisItem {
	item := liveAnalysisItem{
		ID: source.ID, Kind: source.Kind, Title: source.Title, Body: source.Body,
		Status: "open", Severity: "medium",
		EvidenceSequenceNos: []int64{source.Sequence},
		GroundingDecision:   "accepted",
		InformationStatus:   informationStatusGrounded,
	}
	if assigned {
		item.ClassificationStatus = classificationAssigned
		item.AssignmentSource = assignmentSourceRule
		item.AssignmentConfidence = 0.9
		return item
	}
	item.ClassificationStatus = classificationTentative
	item.CandidateTopicID = "candidate-" + source.ID
	return item
}

// buildSingletonAttachmentState は「既存topic群 + 追加論点の箱に残った単独item群」
// という最終ツリー直前の形を作る。
func buildSingletonAttachmentState(
	topics []attachmentFixtureTopic,
	staged []attachmentFixtureItem,
) liveAnalysisPayload {
	nodes := []liveAnalysisTreeNode{
		{ID: treeRootNodeID, Kind: "topic", Label: "定例会議", Origin: topicOriginSystem},
		{ID: treeUnclassifiedTopicID, Kind: "topic", ParentID: treeRootNodeID, Label: "追加論点", Origin: topicOriginSystem},
	}
	items := make([]liveAnalysisItem, 0, 8)
	candidates := make([]emergingTopicCandidate, 0, len(staged))
	for _, topic := range topics {
		origin := topic.Origin
		if origin == "" {
			origin = topicOriginDynamic
		}
		nodes = append(nodes, liveAnalysisTreeNode{
			ID: topic.ID, Kind: "topic", ParentID: treeRootNodeID,
			Label: topic.Label, Description: topic.Description, Origin: origin,
			AgendaRefs: append([]string(nil), topic.AgendaRefs...), Materialized: true,
			CreatedAtVersion: 5, UpdatedAtVersion: 5,
		})
		for _, source := range topic.Items {
			item := attachmentFixtureLiveItem(source, true)
			items = append(items, item)
			nodes = append(nodes, liveAnalysisTreeNode{
				ID: item.ID, Kind: item.Kind, ParentID: topic.ID,
				Label: item.Title, Description: item.Body,
			})
		}
	}
	for _, source := range staged {
		item := attachmentFixtureLiveItem(source, false)
		items = append(items, item)
		node := liveAnalysisTreeNode{
			ID: item.ID, Kind: item.Kind, ParentID: treeUnclassifiedTopicID,
			Label: item.Title, Description: item.Body,
		}
		if source.Manual {
			node.LastParentChangeSource = "user"
			node.LastParentChangeVersion = 9
		}
		nodes = append(nodes, node)
		candidates = append(candidates, emergingTopicCandidate{
			ID: item.CandidateTopicID, Label: source.Title,
			EvidenceItemIDs: []string{item.ID}, FirstRound: 6, LastRound: 6, RoundCount: 1,
		})
	}
	tree := &liveAnalysisTree{Nodes: nodes}
	rebuildTreeAuditEdges(tree)
	return liveAnalysisPayload{Items: items, Tree: tree, EmergingTopics: candidates, TreeVersion: 12}
}

// monitoringAttachmentFixture は session_12b05d9837fbcd2d で観測した形の再現。
// 監視運用の既存topicがあり、その運用パラメータが未決定という単独Issueだけが
// 追加論点の箱に取り残されていた。
func monitoringAttachmentFixture() ([]attachmentFixtureTopic, []attachmentFixtureItem) {
	topics := []attachmentFixtureTopic{
		{
			ID: "topic-dyn-monitoring", Label: "監視強化の方針", Description: "監視対象と監視項目の見直し",
			Items: []attachmentFixtureItem{
				{ID: "item-monitor-fact", Kind: "fact", Sequence: 40,
					Title: "監視対象のアルファ基盤が20台に増えた",
					Body:  "監視対象のアルファ基盤が20台に増えた"},
				{ID: "item-monitor-risk", Kind: "risk", Sequence: 42,
					Title: "監視対象を増やすとアラートが多くなりすぎる",
					Body:  "監視対象を増やすとアラートが多くなりすぎる可能性がある"},
			},
		},
	}
	staged := []attachmentFixtureItem{
		{ID: "item-monitor-issue", Kind: "issue", Sequence: 44,
			Title: "監視間隔と通知条件が未決定",
			Body:  "監視間隔と通知条件については次回までに検討が必要"},
		{ID: "item-projector-issue", Kind: "issue", Sequence: 43,
			Title: "第三会議室のプロジェクタが映らない",
			Body:  "第三会議室のプロジェクタが映らないことがある"},
	}
	return topics, staged
}

func runSingletonAttachment(state *liveAnalysisPayload, mc *meetingContext) *finalRepairStats {
	stats := &finalRepairStats{}
	repairFinalUnclassifiedItems(state, mc, 13, stats)
	return stats
}

func TestSingletonAttachmentGroundsMonitoringIssueIntoExistingTopic(t *testing.T) {
	topics, staged := monitoringAttachmentFixture()
	state := buildSingletonAttachmentState(topics, staged)
	stats := runSingletonAttachment(&state, nil)

	if got := topicIDForItem(state, "item-monitor-issue"); got != "topic-dyn-monitoring" {
		t.Fatalf("singleton issue parent = %q, want topic-dyn-monitoring", got)
	}
	item := itemByIDForTest(state, "item-monitor-issue")
	if item.ClassificationStatus != classificationAssigned {
		t.Fatalf("attached singleton classification = %q", item.ClassificationStatus)
	}
	if item.Kind != "issue" || item.Title == "" || len(item.EvidenceSequenceNos) != 1 {
		t.Fatalf("attachment mutated the proposition: %+v", item)
	}
	if len(dynamicTopicIDs(state)) != 1 {
		t.Fatalf("attachment created extra topics: %v", dynamicTopicIDs(state))
	}
	if stats.SingletonAttachmentApplied != 1 {
		t.Fatalf("applied attachment count = %d, want 1", stats.SingletonAttachmentApplied)
	}
	// 近接発話でも主題が違う単独itemは動かさない。
	if got := topicIDForItem(state, "item-projector-issue"); got != treeUnclassifiedTopicID {
		t.Fatalf("unrelated nearby singleton was attached to %q", got)
	}
}

func TestSingletonAttachmentGroundsAcrossBusinessDomains(t *testing.T) {
	cases := []struct {
		name    string
		topics  []attachmentFixtureTopic
		staged  []attachmentFixtureItem
		context *meetingContext
		itemID  string
		wantID  string
	}{
		{
			name: "release_rollback_uses_agenda_semantic_hint",
			context: &meetingContext{Agenda: []agendaItem{{
				ID: "agenda-2", Title: "段階的リリースの進め方", Order: 1,
				SemanticHints: []string{"ロールバック", "段階公開"},
			}}},
			topics: []attachmentFixtureTopic{{
				ID: "topic-agenda-release", Label: "段階的リリースの進め方",
				Description: "公開範囲の段階拡大", Origin: topicOriginAgenda,
				AgendaRefs: []string{"agenda-2"},
				Items: []attachmentFixtureItem{{
					ID: "item-release-risk", Kind: "risk", Sequence: 12,
					Title: "一斉公開すると障害が広範囲に拡大する",
					Body:  "一斉公開すると障害が広範囲に拡大する",
				}},
			}},
			staged: []attachmentFixtureItem{{
				ID: "item-release-issue", Kind: "issue", Sequence: 14,
				Title: "ロールバック条件が未決定",
				Body:  "ロールバック条件が未決定である",
			}},
			itemID: "item-release-issue", wantID: "topic-agenda-release",
		},
		{
			name: "hiring_evaluation_shares_subject_with_descendant",
			topics: []attachmentFixtureTopic{{
				ID: "topic-dyn-hiring", Label: "面接評価プロセスの見直し",
				Description: "評価者間のばらつき",
				Items: []attachmentFixtureItem{{
					ID: "item-hiring-fact", Kind: "fact", Sequence: 22,
					Title: "評価者ごとに合否の基準がばらついている",
					Body:  "評価者ごとに合否の基準がばらついている",
				}},
			}},
			staged: []attachmentFixtureItem{{
				ID: "item-hiring-issue", Kind: "issue", Sequence: 25,
				Title: "最終判定の評価基準が未決定",
				Body:  "最終判定の評価基準が未決定である",
			}},
			itemID: "item-hiring-issue", wantID: "topic-dyn-hiring",
		},
		{
			name: "inventory_alert_threshold",
			topics: []attachmentFixtureTopic{{
				ID: "topic-dyn-inventory", Label: "在庫アラート運用の見直し",
				Description: "在庫アラートの出し方",
				Items: []attachmentFixtureItem{{
					ID: "item-inventory-risk", Kind: "risk", Sequence: 31,
					Title: "通知が増えると重要な警告が埋もれる",
					Body:  "通知が増えると重要な警告が埋もれる",
				}},
			}},
			staged: []attachmentFixtureItem{{
				ID: "item-inventory-issue", Kind: "issue", Sequence: 34,
				Title: "アラート通知の閾値が未決定",
				Body:  "在庫アラート通知の閾値が未決定",
			}},
			itemID: "item-inventory-issue", wantID: "topic-dyn-inventory",
		},
		{
			name: "permission_request_flow_todo",
			topics: []attachmentFixtureTopic{{
				ID: "topic-dyn-approval", Label: "権限申請フローの整備",
				Description: "申請から承認までの流れ",
				Items: []attachmentFixtureItem{{
					ID: "item-approval-fact", Kind: "fact", Sequence: 60,
					Title: "権限申請の承認が部門長で滞留している",
					Body:  "権限申請の承認が部門長で滞留している",
				}},
			}},
			staged: []attachmentFixtureItem{{
				ID: "item-approval-todo", Kind: "todo", Sequence: 63,
				Title: "管理者が承認期限を確認する",
				Body:  "管理者が権限申請フローの承認期限を確認する",
			}},
			itemID: "item-approval-todo", wantID: "topic-dyn-approval",
		},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			state := buildSingletonAttachmentState(testCase.topics, testCase.staged)
			before := len(state.Tree.Nodes)
			stats := runSingletonAttachment(&state, testCase.context)

			if got := topicIDForItem(state, testCase.itemID); got != testCase.wantID {
				t.Fatalf("%s parent = %q, want %q", testCase.itemID, got, testCase.wantID)
			}
			if stats.SingletonAttachmentApplied != 1 {
				t.Fatalf("applied attachment count = %d, want 1", stats.SingletonAttachmentApplied)
			}
			if len(state.Tree.Nodes) > before {
				t.Fatalf("attachment added %d tree nodes", len(state.Tree.Nodes)-before)
			}
		})
	}
}

func TestSingletonAttachmentRejectsGenericTermOnlyOverlap(t *testing.T) {
	topics := []attachmentFixtureTopic{{
		ID: "topic-dyn-customer-notice", Label: "顧客への通知方針",
		Description: "顧客への通知手段",
		Items: []attachmentFixtureItem{{
			ID: "item-customer-fact", Kind: "fact", Sequence: 5,
			Title: "顧客への通知はメールで行う",
			Body:  "顧客への通知はメールで行う",
		}},
	}}
	// 発話位置は近く、補助的な関係(近接)は成立する。それでも共有語が汎用語
	// 「通知」一語だけなら対象一致とみなさない。
	staged := []attachmentFixtureItem{{
		ID: "item-monitor-notice-issue", Kind: "issue", Sequence: 7,
		Title: "監視通知条件の検討が必要",
		Body:  "監視通知条件の検討が必要である",
	}}
	state := buildSingletonAttachmentState(topics, staged)
	stats := runSingletonAttachment(&state, nil)

	if got := topicIDForItem(state, "item-monitor-notice-issue"); got != treeUnclassifiedTopicID {
		t.Fatalf("singleton attached on a generic shared word alone: %q", got)
	}
	if stats.SingletonAttachmentApplied != 0 {
		t.Fatalf("applied attachment count = %d, want 0", stats.SingletonAttachmentApplied)
	}
	if stats.SingletonAttachmentDeferred == 0 {
		t.Fatalf("deferred attachment was not recorded: %+v", stats)
	}
}

func TestSingletonAttachmentIgnoresProximityAndSpeakerContinuityAlone(t *testing.T) {
	// 同じ話者が連続して別テーマを話した状況。itemのsignatureは話者を含まず、
	// sequence近接だけでも接続しない。
	topics := []attachmentFixtureTopic{{
		ID: "topic-dyn-network", Label: "ネットワーク障害の経緯",
		Description: "拠点間回線の断続",
		Items: []attachmentFixtureItem{{
			ID: "item-network-fact", Kind: "fact", Sequence: 20,
			Title: "拠点間回線が朝夕に不安定になっている",
			Body:  "拠点間回線が朝夕に不安定になっている",
		}},
	}}
	staged := []attachmentFixtureItem{{
		ID: "item-supply-issue", Kind: "issue", Sequence: 21,
		Title: "会議室備品の購入判断が保留",
		Body:  "会議室備品の購入判断が保留になっている",
	}}
	state := buildSingletonAttachmentState(topics, staged)
	runSingletonAttachment(&state, nil)

	if got := topicIDForItem(state, "item-supply-issue"); got != treeUnclassifiedTopicID {
		t.Fatalf("singleton attached on sequence proximity alone: %q", got)
	}
}

func TestSingletonAttachmentDefersWhenCandidatesAreEquallyPlausible(t *testing.T) {
	topics := []attachmentFixtureTopic{
		{
			ID: "topic-dyn-alert-notice", Label: "在庫アラートの通知設計",
			Items: []attachmentFixtureItem{{
				ID: "item-alert-notice-risk", Kind: "risk", Sequence: 18,
				Title: "在庫アラートの宛先が重複している",
				Body:  "在庫アラートの宛先が重複している",
			}},
		},
		{
			ID: "topic-dyn-alert-threshold", Label: "在庫アラートの閾値運用",
			Items: []attachmentFixtureItem{{
				ID: "item-alert-threshold-risk", Kind: "risk", Sequence: 22,
				Title: "在庫アラートの宛先が古いままである",
				Body:  "在庫アラートの宛先が古いままである",
			}},
		},
	}
	staged := []attachmentFixtureItem{{
		ID: "item-alert-frequency-issue", Kind: "issue", Sequence: 20,
		Title: "在庫アラートの通知頻度が未決定",
		Body:  "在庫アラートの通知頻度が未決定",
	}}
	state := buildSingletonAttachmentState(topics, staged)
	stats := runSingletonAttachment(&state, nil)

	if got := topicIDForItem(state, "item-alert-frequency-issue"); got != treeUnclassifiedTopicID {
		t.Fatalf("ambiguous singleton was attached to %q", got)
	}
	if stats.SingletonAttachmentAmbiguous == 0 {
		t.Fatalf("ambiguous attachment was not recorded: %+v", stats)
	}
}

func TestSingletonAttachmentDefersWithoutAnIdentifiableSubject(t *testing.T) {
	topics := []attachmentFixtureTopic{{
		ID: "topic-dyn-monitoring", Label: "監視強化の方針",
		Items: []attachmentFixtureItem{{
			ID: "item-monitor-fact", Kind: "fact", Sequence: 40,
			Title: "監視対象が20台に増えた", Body: "監視対象が20台に増えた",
		}},
	}}
	staged := []attachmentFixtureItem{{
		ID: "item-vague-issue", Kind: "issue", Sequence: 41,
		Title: "条件については次回検討する",
		Body:  "条件については次回検討する",
	}}
	state := buildSingletonAttachmentState(topics, staged)
	runSingletonAttachment(&state, nil)

	if got := topicIDForItem(state, "item-vague-issue"); got != treeUnclassifiedTopicID {
		t.Fatalf("subject-less singleton was attached to %q", got)
	}
}

func TestSingletonAttachmentRejectsConflictingCompoundHead(t *testing.T) {
	// 同じ主要語(証明書)でも、修飾語が異なる別の対象は統合しない。
	topics := []attachmentFixtureTopic{{
		ID: "topic-dyn-tls", Label: "TLS証明書の更新管理",
		Description: "TLS証明書の有効期限",
		Items: []attachmentFixtureItem{{
			ID: "item-tls-fact", Kind: "fact", Sequence: 50,
			Title: "TLS証明書が来月失効する", Body: "TLS証明書が来月失効する",
		}},
	}}
	staged := []attachmentFixtureItem{{
		ID: "item-qualification-issue", Kind: "issue", Sequence: 52,
		Title: "資格証明書の発行手続きが未整備",
		Body:  "資格証明書の発行手続きが未整備である",
	}}
	state := buildSingletonAttachmentState(topics, staged)
	runSingletonAttachment(&state, nil)

	if got := topicIDForItem(state, "item-qualification-issue"); got != treeUnclassifiedTopicID {
		t.Fatalf("contradictory subject was merged into %q", got)
	}
}

func TestSingletonAttachmentPreservesManualPlacement(t *testing.T) {
	topics, staged := monitoringAttachmentFixture()
	for index := range staged {
		if staged[index].ID == "item-monitor-issue" {
			staged[index].Manual = true
		}
	}
	state := buildSingletonAttachmentState(topics, staged)
	stats := runSingletonAttachment(&state, nil)

	if got := topicIDForItem(state, "item-monitor-issue"); got != treeUnclassifiedTopicID {
		t.Fatalf("manually placed singleton was moved to %q", got)
	}
	if stats.SingletonAttachmentApplied != 0 {
		t.Fatalf("manual item was attached: %+v", stats)
	}
	node := liveTreeNodeByID(state.Tree, "item-monitor-issue")
	if node == nil || node.LastParentChangeSource != "user" {
		t.Fatalf("manual provenance was lost: %+v", node)
	}
}

// TestSingletonAttachmentIsStableUnderMeaningPreservingRewrites は、同じ意味構造
// であれば表現・entity名・話者順・sequence番号が変わっても同じ結果になることを
// 確認する。特定の語や発話順に依存した実装をここで落とす。
func TestSingletonAttachmentIsStableUnderMeaningPreservingRewrites(t *testing.T) {
	type transform func([]attachmentFixtureTopic, []attachmentFixtureItem) ([]attachmentFixtureTopic, []attachmentFixtureItem)

	replaceAll := func(from, to string) transform {
		return func(topics []attachmentFixtureTopic, staged []attachmentFixtureItem) ([]attachmentFixtureTopic, []attachmentFixtureItem) {
			for i := range topics {
				topics[i].Label = strings.ReplaceAll(topics[i].Label, from, to)
				topics[i].Description = strings.ReplaceAll(topics[i].Description, from, to)
				for j := range topics[i].Items {
					topics[i].Items[j].Title = strings.ReplaceAll(topics[i].Items[j].Title, from, to)
					topics[i].Items[j].Body = strings.ReplaceAll(topics[i].Items[j].Body, from, to)
				}
			}
			for i := range staged {
				staged[i].Title = strings.ReplaceAll(staged[i].Title, from, to)
				staged[i].Body = strings.ReplaceAll(staged[i].Body, from, to)
			}
			return topics, staged
		}
	}
	rewriteStaged := func(id, title, body string) transform {
		return func(topics []attachmentFixtureTopic, staged []attachmentFixtureItem) ([]attachmentFixtureTopic, []attachmentFixtureItem) {
			for i := range staged {
				if staged[i].ID == id {
					staged[i].Title, staged[i].Body = title, body
				}
			}
			return topics, staged
		}
	}

	cases := []struct {
		name  string
		apply transform
	}{
		{name: "baseline", apply: func(tp []attachmentFixtureTopic, st []attachmentFixtureItem) ([]attachmentFixtureTopic, []attachmentFixtureItem) {
			return tp, st
		}},
		{name: "entity_renamed", apply: replaceAll("アルファ基盤", "ブラボー基盤")},
		{name: "topic_label_synonym", apply: func(tp []attachmentFixtureTopic, st []attachmentFixtureItem) ([]attachmentFixtureTopic, []attachmentFixtureItem) {
			tp[0].Label = "監視体制の強化"
			return tp, st
		}},
		{name: "sequence_shifted", apply: func(tp []attachmentFixtureTopic, st []attachmentFixtureItem) ([]attachmentFixtureTopic, []attachmentFixtureItem) {
			for i := range tp {
				for j := range tp[i].Items {
					tp[i].Items[j].Sequence += 100
				}
			}
			for i := range st {
				st[i].Sequence += 100
			}
			return tp, st
		}},
		{name: "unrelated_utterances_inserted", apply: func(tp []attachmentFixtureTopic, st []attachmentFixtureItem) ([]attachmentFixtureTopic, []attachmentFixtureItem) {
			// 論点の間に無関係な発話が入り、証拠sequenceが離れた場合。
			for i := range st {
				st[i].Sequence += 5
			}
			return tp, st
		}},
		{name: "issue_and_risk_order_reversed", apply: func(tp []attachmentFixtureTopic, st []attachmentFixtureItem) ([]attachmentFixtureTopic, []attachmentFixtureItem) {
			for i := range tp[0].Items {
				if tp[0].Items[i].ID == "item-monitor-risk" {
					tp[0].Items[i].Sequence = 46
				}
			}
			return tp, st
		}},
		{name: "nominal_ending", apply: rewriteStaged(
			"item-monitor-issue", "監視間隔と通知条件の未決定", "監視間隔と通知条件の未決定",
		)},
		{name: "polite_form", apply: rewriteStaged(
			"item-monitor-issue", "監視間隔と通知条件が未決定です", "監視間隔と通知条件については次回までに検討が必要です",
		)},
		{name: "deadline_phrase_removed", apply: rewriteStaged(
			"item-monitor-issue", "監視間隔と通知条件が未決定", "監視間隔と通知条件については検討が必要",
		)},
		{name: "paraphrased_predicate", apply: rewriteStaged(
			"item-monitor-issue", "監視間隔と通知条件は未決定である", "監視間隔と通知条件は未決定である",
		)},
		{name: "paraphrased_subject_wording", apply: rewriteStaged(
			"item-monitor-issue", "監視の頻度と判定閾値を決める必要がある", "監視の頻度と判定閾値を決める必要がある",
		)},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			topics, staged := monitoringAttachmentFixture()
			topics, staged = testCase.apply(topics, staged)
			state := buildSingletonAttachmentState(topics, staged)
			runSingletonAttachment(&state, nil)

			if got := topicIDForItem(state, "item-monitor-issue"); got != "topic-dyn-monitoring" {
				t.Fatalf("meaning-preserving rewrite changed the result: parent=%q", got)
			}
			if got := topicIDForItem(state, "item-projector-issue"); got != treeUnclassifiedTopicID {
				t.Fatalf("unrelated singleton was attached to %q", got)
			}
		})
	}
}

// TestSingletonAttachmentPropertyOverSyntheticSubjects は、実在の業務用語では
// なく合成した主題語でも、同じ主題のsingletonだけが接続されることを確認する。
func TestSingletonAttachmentPropertyOverSyntheticSubjects(t *testing.T) {
	subjects := []struct{ primary, other string }{
		{"アルファ設備", "ブラボー装置"},
		{"チャーリー端末", "デルタ倉庫"},
		{"エコー回線", "フォックス台帳"},
	}
	kinds := []string{"issue", "todo", "risk"}

	for _, subject := range subjects {
		for _, kind := range kinds {
			name := fmt.Sprintf("%s_%s", subject.primary, kind)
			t.Run(name, func(t *testing.T) {
				topics := []attachmentFixtureTopic{{
					ID: "topic-dyn-primary", Label: subject.primary + "の運用改善",
					Description: subject.primary + "の稼働状況",
					Items: []attachmentFixtureItem{
						{ID: "item-primary-fact", Kind: "fact", Sequence: 10,
							Title: subject.primary + "の稼働率が低下している",
							Body:  subject.primary + "の稼働率が低下している"},
						{ID: "item-primary-risk", Kind: "risk", Sequence: 12,
							Title: subject.primary + "の停止で出荷が遅れる",
							Body:  subject.primary + "の停止で出荷が遅れる"},
					},
				}}
				staged := []attachmentFixtureItem{
					{ID: "item-primary-open", Kind: kind, Sequence: 14,
						Title: subject.primary + "の点検周期が未決定",
						Body:  subject.primary + "の点検周期が未決定"},
					{ID: "item-other-open", Kind: kind, Sequence: 16,
						Title: subject.other + "の設置場所が未決定",
						Body:  subject.other + "の設置場所が未決定"},
				}
				state := buildSingletonAttachmentState(topics, staged)
				runSingletonAttachment(&state, nil)

				if got := topicIDForItem(state, "item-primary-open"); got != "topic-dyn-primary" {
					t.Fatalf("same-subject singleton parent = %q, want topic-dyn-primary", got)
				}
				if got := topicIDForItem(state, "item-other-open"); got == "topic-dyn-primary" {
					t.Fatalf("different-subject singleton was attached to the primary topic")
				}
			})
		}
	}
}

func TestSingletonAttachmentPreservesTreeIntegrity(t *testing.T) {
	topics, staged := monitoringAttachmentFixture()
	state := buildSingletonAttachmentState(topics, staged)
	before := itemByIDForTest(state, "item-monitor-issue")

	runSingletonAttachment(&state, nil)

	after := itemByIDForTest(state, "item-monitor-issue")
	if after.ID != before.ID || after.Kind != before.Kind || after.Title != before.Title ||
		after.Body != before.Body || after.Status != before.Status ||
		len(after.EvidenceSequenceNos) != len(before.EvidenceSequenceNos) {
		t.Fatalf("attachment changed item content: before=%+v after=%+v", before, after)
	}
	parents := 0
	for _, edge := range state.Tree.Edges {
		if edge.Target == "item-monitor-issue" {
			parents++
		}
	}
	if parents != 1 {
		t.Fatalf("attached item has %d parent edges, want 1", parents)
	}
	if node := liveTreeNodeByID(state.Tree, "topic-dyn-monitoring"); node == nil {
		t.Fatalf("attachment target topic was pruned")
	}
	integrity := validateTreeIntegrity(state.Tree, state.Items, nil, state.AgendaAnchors)
	if !integrity.Valid {
		t.Fatalf("tree integrity broke after attachment: %+v", integrity)
	}
}

func TestSingletonAttachmentIsIdempotent(t *testing.T) {
	topics, staged := monitoringAttachmentFixture()
	state := buildSingletonAttachmentState(topics, staged)
	runSingletonAttachment(&state, nil)
	firstParent := topicIDForItem(state, "item-monitor-issue")

	second := runSingletonAttachment(&state, nil)

	if got := topicIDForItem(state, "item-monitor-issue"); got != firstParent {
		t.Fatalf("second pass moved the item: %q -> %q", firstParent, got)
	}
	if second.SingletonAttachmentApplied != 0 || second.UnclassifiedTopicsMaterialized != 0 {
		t.Fatalf("second pass was not a no-op: %+v", second)
	}
}
