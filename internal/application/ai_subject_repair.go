package application

import "strings"

// genericTopicLabel reports staging/meta labels that do not identify a
// discussion subject. The system-owned topic-unclassified container may use
// one internally, but it must disappear from the visible tree when all of its
// items can be safely assigned elsewhere.
func genericTopicLabel(label string) bool {
	switch normalizeForMatch(label) {
	case "追加論点", "アジェンダ外の追加論点", "別件", "新しい問題", "新しい論点", "その他", "関連事項", "確認事項", "別の対応":
		return true
	default:
		return false
	}
}

func repairGenericCandidateLabels(candidates []emergingTopicCandidate, items []liveAnalysisItem, stats *liveAnalysisTreeMergeStats) {
	itemByID := make(map[string]liveAnalysisItem, len(items))
	for _, item := range items {
		itemByID[item.ID] = item
	}
	for i := range candidates {
		initializeCandidateSubject(&candidates[i])
		if !genericTopicLabel(candidates[i].Label) {
			continue
		}
		label := concreteTopicLabel(candidates[i].SubjectKey, candidates[i].EvidenceItemIDs, itemByID)
		if label == "" || genericTopicLabel(label) {
			continue
		}
		candidates[i].Label = label
		candidates[i].CurrentSubject = strings.TrimSpace(label + " " + candidates[i].Description)
		candidates[i].SubjectHistory = appendUniqueText(candidates[i].SubjectHistory, candidates[i].CurrentSubject)
		_, candidates[i].SubjectKey = canonicalCandidateID(label, candidates[i].Description)
		if stats != nil {
			stats.GenericCandidateLabelsRewritten++
		}
	}
}

func repairGenericTopicLabels(items []liveAnalysisItem, topics map[string]liveAnalysisTreeNode, parents map[string]string, round int64, stats *liveAnalysisTreeMergeStats) {
	itemByID := make(map[string]liveAnalysisItem, len(items))
	for _, item := range items {
		if !item.Inactive && item.MergedIntoID == "" {
			itemByID[item.ID] = item
		}
	}
	for id, topic := range topics {
		if id == treeRootNodeID || id == treeUnclassifiedTopicID || !genericTopicLabel(topic.Label) {
			continue
		}
		childIDs := descendantItemIDs(id, parents, itemByID)
		label := concreteTopicLabel("", childIDs, itemByID)
		if label == "" || genericTopicLabel(label) {
			continue
		}
		topic.Label = label
		topic.UpdatedAtVersion = round
		topics[id] = topic
		if stats != nil {
			stats.GenericTopicLabelsRewritten++
		}
	}
}

func descendantItemIDs(containerID string, parents map[string]string, items map[string]liveAnalysisItem) []string {
	ids := make([]string, 0)
	for itemID := range items {
		current := parents[itemID]
		seen := make(map[string]struct{})
		for current != "" {
			if current == containerID {
				ids = append(ids, itemID)
				break
			}
			if _, loop := seen[current]; loop {
				break
			}
			seen[current] = struct{}{}
			current = parents[current]
		}
	}
	return ids
}

func concreteTopicLabel(subjectKey string, itemIDs []string, items map[string]liveAnalysisItem) string {
	texts := make([]string, 0, len(itemIDs)*2+1)
	if strings.TrimSpace(subjectKey) != "" && !genericTopicLabel(subjectKey) {
		texts = append(texts, subjectKey)
	}
	bestTitle := ""
	for _, id := range itemIDs {
		item, exists := items[id]
		if !exists {
			continue
		}
		texts = append(texts, item.Title, item.Body)
		if topicTitleSpecificity(item.Title) > topicTitleSpecificity(bestTitle) {
			bestTitle = item.Title
		}
	}
	combined := strings.Join(texts, " ")
	subject := concreteBusinessSubject(combined)
	if subject != "" {
		switch {
		case strings.Contains(combined, "更新"):
			return truncateRunes(subject+"の更新対応", liveAnalysisTopicLabelMaxRunes)
		case strings.Contains(combined, "期限切れ") || strings.Contains(combined, "有効期限"):
			return truncateRunes(subject+"の期限切れ対応", liveAnalysisTopicLabelMaxRunes)
		case strings.Contains(combined, "作成") || strings.Contains(combined, "策定"):
			return truncateRunes(subject+"の作成", liveAnalysisTopicLabelMaxRunes)
		default:
			return truncateRunes(subject+"の対応", liveAnalysisTopicLabelMaxRunes)
		}
	}
	bestTitle = strings.Trim(strings.TrimSpace(bestTitle), "、。！？!? ")
	if bestTitle == "" || genericTopicLabel(bestTitle) || isDiscourseOnlyText(bestTitle) {
		return ""
	}
	return truncateRunes(bestTitle, liveAnalysisTopicLabelMaxRunes)
}

func topicTitleSpecificity(text string) int {
	if genericTopicLabel(text) || isDiscourseOnlyText(text) {
		return -1
	}
	key := specificSubjectText(text)
	return len([]rune(key))
}

func concreteBusinessSubject(text string) string {
	// These are object heads, not meeting-specific phrases. Keeping the head in
	// the extracted label prevents action/actor wording from becoming the topic
	// subject when a risk and its response TODO use different predicates.
	heads := []string{
		"証明書", "チェックリスト", "ライセンス", "アカウント", "アクセストークン", "トークン",
		"秘密鍵", "公開鍵", "契約", "許可証", "通知条件", "監視項目", "設定ファイル", "設定値",
	}
	for _, head := range heads {
		at := strings.Index(text, head)
		if at < 0 {
			continue
		}
		prefixRunes := []rune(text[:at])
		if len(prefixRunes) > 12 {
			prefixRunes = prefixRunes[len(prefixRunes)-12:]
		}
		prefix := string(prefixRunes)
		for _, separator := range []string{"。", "、", "，", ",", "！", "!", "？", "?", " ", "　", "について", "ところ"} {
			if index := strings.LastIndex(prefix, separator); index >= 0 {
				prefix = prefix[index+len(separator):]
			}
		}
		prefix = strings.TrimLeft(prefix, "のをがはにでとへも")
		subject := strings.TrimSpace(prefix + head)
		if len([]rune(semanticItemKey(subject))) >= len([]rune(head)) {
			return subject
		}
	}
	return ""
}

// repairRelatedSubjectFragmentation overrides parent stickiness only when a
// nearby risk/issue and its action share a concrete subject fingerprint. Item
// kinds remain separate; only their primary topic is repaired.
func repairRelatedSubjectFragmentation(items []liveAnalysisItem, topics map[string]liveAnalysisTreeNode, groups map[string]liveAnalysisTreeNode, parents map[string]string, stats *liveAnalysisTreeMergeStats) {
	topicAncestor := func(itemID string) string {
		current := parents[itemID]
		seen := make(map[string]struct{})
		for current != "" {
			if _, loop := seen[current]; loop {
				return ""
			}
			seen[current] = struct{}{}
			if _, ok := topics[current]; ok {
				return current
			}
			if _, ok := groups[current]; !ok {
				return ""
			}
			current = parents[current]
		}
		return ""
	}
	active := make([]int, 0, len(items))
	for i := range items {
		if !items[i].Inactive && items[i].MergedIntoID == "" && items[i].Status != "dismissed" {
			active = append(active, i)
		}
	}
	componentParent := make([]int, len(active))
	for i := range componentParent {
		componentParent[i] = i
	}
	var find func(int) int
	find = func(value int) int {
		if componentParent[value] != value {
			componentParent[value] = find(componentParent[value])
		}
		return componentParent[value]
	}
	join := func(left, right int) {
		left, right = find(left), find(right)
		if left != right {
			componentParent[right] = left
		}
	}
	for left := 0; left < len(active); left++ {
		for right := left + 1; right < len(active); right++ {
			a, b := items[active[left]], items[active[right]]
			if relatedSubjectKinds(a.Kind, b.Kind) && itemEvidenceWithin(a, b, 3) &&
				specificSubjectOverlapLength(a.Title+" "+a.Body, b.Title+" "+b.Body) >= 4 {
				join(left, right)
			}
		}
	}
	components := make(map[int][]int)
	for index := range active {
		root := find(index)
		components[root] = append(components[root], active[index])
	}
	type anchorScore struct {
		topicID       string
		labelOverlap  int
		riskOrIssue   bool
		firstEvidence int64
	}
	for _, members := range components {
		if len(members) < 2 {
			continue
		}
		candidateScores := make(map[string]anchorScore)
		for _, itemAt := range members {
			item := items[itemAt]
			topicID := topicAncestor(item.ID)
			topic, exists := topics[topicID]
			if !exists || topicID == treeUnclassifiedTopicID || (topic.Origin != topicOriginDynamic && topic.Origin != topicOriginMixed) {
				continue
			}
			score := candidateScores[topicID]
			score.topicID = topicID
			for _, memberAt := range members {
				member := items[memberAt]
				if overlap := specificSubjectOverlapLength(member.Title+" "+member.Body, topic.Label+" "+topic.Description); overlap > score.labelOverlap {
					score.labelOverlap = overlap
				}
			}
			if item.Kind == "risk" || item.Kind == "issue" {
				score.riskOrIssue = true
			}
			for _, sequenceNo := range item.EvidenceSequenceNos {
				if sequenceNo > 0 && (score.firstEvidence == 0 || sequenceNo < score.firstEvidence) {
					score.firstEvidence = sequenceNo
				}
			}
			candidateScores[topicID] = score
		}
		best := anchorScore{}
		for _, candidate := range candidateScores {
			better := candidate.labelOverlap > best.labelOverlap
			if candidate.labelOverlap == best.labelOverlap {
				better = candidate.riskOrIssue && !best.riskOrIssue
				if candidate.riskOrIssue == best.riskOrIssue {
					better = best.firstEvidence == 0 || (candidate.firstEvidence > 0 && candidate.firstEvidence < best.firstEvidence) ||
						(candidate.firstEvidence == best.firstEvidence && (best.topicID == "" || candidate.topicID < best.topicID))
				}
			}
			if better {
				best = candidate
			}
		}
		if best.topicID == "" {
			continue
		}
		for _, itemAt := range members {
			item := &items[itemAt]
			currentTopic := topicAncestor(item.ID)
			if currentTopic == best.topicID {
				continue
			}
			if currentTopic != "" && currentTopic != treeUnclassifiedTopicID {
				current, exists := topics[currentTopic]
				if !exists || (current.Origin != topicOriginDynamic && current.Origin != topicOriginMixed) {
					continue
				}
			}
			parents[item.ID] = best.topicID
			item.ClassificationStatus = classificationAssigned
			item.CandidateTopicID = ""
			item.CandidateInactive = false
			item.AssignmentSource = assignmentSourceRule
			item.AssignmentConfidence = 0.85
			item.AssignmentReason = "nearby risk/issue and action evidence shares a concrete subject fingerprint"
			if stats != nil {
				stats.SubjectFragmentationRepairs++
				stats.CompanionParentInherited++
				stats.CrossKindClustered++
			}
		}
	}
}

func relatedSubjectKinds(a, b string) bool {
	if a == b {
		return a == "risk" || a == "issue"
	}
	if a == "risk" || a == "issue" {
		return b == "todo" || b == "decision" || b == "risk" || b == "issue"
	}
	if b == "risk" || b == "issue" {
		return a == "todo" || a == "decision"
	}
	return false
}

func specificSubjectText(text string) string {
	key := semanticItemKey(text)
	for _, generic := range []string{
		"確認", "検討", "実施", "対応", "更新", "作成", "決定", "可能性", "リスク", "影響", "予定", "必要",
		"する", "します", "した", "なる", "なります", "ある", "こと", "について", "による", "により", "場合", "今回", "次回", "今週", "来週",
	} {
		key = strings.ReplaceAll(key, generic, "")
	}
	return key
}

func specificSubjectOverlapLength(a, b string) int {
	left, right := []rune(specificSubjectText(a)), []rune(specificSubjectText(b))
	best := 0
	for i := range left {
		for j := range right {
			length := 0
			for i+length < len(left) && j+length < len(right) && left[i+length] == right[j+length] {
				length++
			}
			if length > best {
				best = length
			}
		}
	}
	return best
}
