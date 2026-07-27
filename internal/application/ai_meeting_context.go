package application

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
)

// このファイルは会議前入力(目的・前提・アジェンダ・補足指示)を、プロンプトへ
// 連結するだけの平文ではなく、構造化された Meeting Context として扱うための
// 型と正規化処理を持つ。Meeting Context は全AIタスク(抽出・分類・再編成・最終
// 整理)が共通に参照する canonical な事前情報で、アジェンダは stable ID を持つ
// 分類anchorになる。実際に議論されるまでtree topicは生成しない。

const (
	// treeRootNodeID is the stable id of the single root node of every
	// discussion tree. The root is always kind "topic" and is created by the
	// server, never by the model.
	treeRootNodeID = "root"
	// treeUnclassifiedTopicID is the stable id of the system-managed topic
	// that collects orphan/unclassifiable detail nodes. It is created lazily,
	// only when a node actually needs it.
	treeUnclassifiedTopicID = "topic-unclassified"
	// treeUnclassifiedTopicLabel is the display label of the unclassified
	// topic.
	treeUnclassifiedTopicLabel = "追加論点"

	// agendaIDPrefix + 1-based order is the stable ID of each logical agenda
	// record (e.g. "agenda-1"). It may be an assignment target, but it is
	// never reused as a discussion-tree node ID.
	agendaIDPrefix = "agenda-"

	meetingContextMaxAgendaItems = 10
	meetingContextMaxDirectives  = 10

	agendaRolePrimary       = "primary"
	agendaRoleActionSummary = "action_summary"
	// virtualActionSummaryProjectionID is a reference-only view key used when
	// pre-meeting context omitted an action-summary agenda. It is never a tree
	// node or agenda anchor and therefore cannot become a second parent.
	virtualActionSummaryProjectionID = "action-summary-fallback"
)

// meetingContext is the structured, role-separated form of the pre-meeting
// inputs. It is deterministic for a given meeting session (agenda ids are
// derived from order), so every AI task sees the same context.
type meetingContext struct {
	Title      string       `json:"title,omitempty"`
	Purpose    string       `json:"purpose,omitempty"`
	Background string       `json:"background,omitempty"`
	Agenda     []agendaItem `json:"agendaItems,omitempty"`
	// Directives are the user's "AIへの補足指示" lines. They influence what
	// gets extracted and how it is phrased, but they are always rendered as
	// reference data below the system rules and can never override tree/
	// schema constraints (see buildLiveAnalysisUserPrompt).
	Directives []string `json:"aiDirectives,omitempty"`
	// Legacy pre-context fields kept for older sessions that stored them.
	DecisionPoints string `json:"decisionPoints,omitempty"`
	Concerns       string `json:"concerns,omitempty"`
	ExpectedOutput string `json:"expectedOutput,omitempty"`
}

type agendaItem struct {
	ID            string   `json:"id"`
	Title         string   `json:"title"`
	Description   string   `json:"description,omitempty"`
	Goal          string   `json:"goal,omitempty"`
	SemanticHints []string `json:"semanticHints,omitempty"`
	Order         int      `json:"order"`
	// Role separates a normal content agenda from a cross-cutting action
	// summary. It is optional for old context payloads; an empty role is
	// treated as primary everywhere.
	Role string `json:"role,omitempty"`
}

func normalizeAgendaRole(role string) string {
	switch strings.ToLower(strings.TrimSpace(role)) {
	case agendaRoleActionSummary:
		return agendaRoleActionSummary
	default:
		return agendaRolePrimary
	}
}

var actionSummaryAgendaTitles = []string{
	"今後の対応事項",
	"対応事項",
	"アクションアイテム",
	"次のアクション",
	"今後の予定",
	"担当期限",
	"フォローアップ",
}

// inferAgendaRole is the compatibility path for meetings created before an
// explicit agenda role existed. A planner response is still treated as a
// proposal: a well-known cross-cutting action-summary title cannot be turned
// into a canonical content topic merely because the model returned primary.
// Conversely, substantive agenda titles remain primary even when they contain
// generic words such as 担当 or 決定.
func inferAgendaRole(title, description string) string {
	key := normalizeForMatch(title + " " + description)
	if key == "" {
		return agendaRolePrimary
	}
	for _, substantive := range []string{"実施体制", "担当者の選定", "担当者選定", "スケジュール調整"} {
		if strings.Contains(key, normalizeForMatch(substantive)) {
			return agendaRolePrimary
		}
	}
	for _, candidate := range actionSummaryAgendaTitles {
		candidateKey := normalizeForMatch(candidate)
		if key == candidateKey || strings.HasPrefix(key, candidateKey) || strings.HasSuffix(key, candidateKey) {
			return agendaRoleActionSummary
		}
	}
	return agendaRolePrimary
}

func effectiveAgendaRole(role, title, description string) string {
	if normalizeAgendaRole(role) == agendaRoleActionSummary || inferAgendaRole(title, description) == agendaRoleActionSummary {
		return agendaRoleActionSummary
	}
	return agendaRolePrimary
}

func (c *meetingContext) actionSummaryAgendaIDs() map[string]struct{} {
	ids := make(map[string]struct{})
	if c == nil {
		return ids
	}
	for _, item := range c.Agenda {
		if effectiveAgendaRole(item.Role, item.Title, "") == agendaRoleActionSummary {
			ids[item.ID] = struct{}{}
		}
	}
	return ids
}

// logicalActionSummaryAgendaID collapses any legacy duplicate source agendas
// into one cross-cutting view. Source agenda records remain observable, but
// canonical items are projected only once and no action-summary tree node is
// created.
func (c *meetingContext) logicalActionSummaryAgendaID() string {
	if c == nil {
		return ""
	}
	for _, item := range c.Agenda {
		if effectiveAgendaRole(item.Role, item.Title, "") == agendaRoleActionSummary {
			return item.ID
		}
	}
	return ""
}

func (c *meetingContext) actionSummaryProjectionID() string {
	if c == nil {
		return ""
	}
	if agendaID := c.logicalActionSummaryAgendaID(); agendaID != "" {
		return agendaID
	}
	return virtualActionSummaryProjectionID
}

// reconcileMeetingContextWithFallback applies the planner as a bounded label
// refinement. The deterministic meeting record remains authoritative for the
// agenda count/order/IDs, preventing a hallucinated fifth agenda from becoming
// a second action-summary source.
func reconcileMeetingContextWithFallback(planned, fallback *meetingContext) *meetingContext {
	if planned == nil {
		return fallback
	}
	if fallback == nil || len(fallback.Agenda) == 0 {
		return planned
	}
	reconciled := *planned
	// The persisted meeting input, rather than the model response, owns agenda
	// cardinality and positional identity. A v4 metadata response that omits,
	// duplicates, merges, or splits an entry must therefore leave the original
	// count/order/IDs intact.
	reconciled.Agenda = make([]agendaItem, 0, len(fallback.Agenda))
	for index := range fallback.Agenda {
		source := fallback.Agenda[index]
		item := source
		if index < len(planned.Agenda) {
			item = planned.Agenda[index]
		}
		if strings.TrimSpace(item.Title) == "" {
			item.Title = source.Title
		}
		item.Role = effectiveAgendaRole(item.Role, item.Title, "")
		if strings.TrimSpace(item.Description) == "" {
			item.Description = source.Description
		}
		if strings.TrimSpace(item.Goal) == "" {
			item.Goal = source.Goal
		}
		if len(item.SemanticHints) == 0 {
			item.SemanticHints = append([]string(nil), source.SemanticHints...)
		}
		item.ID = source.ID
		item.Order = index + 1
		reconciled.Agenda = append(reconciled.Agenda, item)
	}
	return &reconciled
}

func (c *meetingContext) isEmpty() bool {
	return c == nil || (c.Title == "" && c.Purpose == "" && c.Background == "" &&
		len(c.Agenda) == 0 && len(c.Directives) == 0 &&
		c.DecisionPoints == "" && c.Concerns == "" && c.ExpectedOutput == "")
}

// rootLabel returns the display label of the tree root node.
func (c *meetingContext) rootLabel() string {
	if c != nil {
		if title := strings.TrimSpace(c.Title); title != "" {
			return truncateRunes(title, liveAnalysisTopicLabelMaxRunes)
		}
	}
	return "会議全体"
}

// rootDescription returns the description of the tree root node: the meeting
// purpose, which by design is the root's説明 rather than a per-round node.
func (c *meetingContext) rootDescription() string {
	if c == nil {
		return ""
	}
	return truncateRunes(strings.TrimSpace(c.Purpose), liveAnalysisTreeDescriptionMaxRunes)
}

// buildMeetingContext converts the flat pre-meeting inputs into the
// structured meeting context. It never fails: missing fields simply stay
// empty, and a nil pre-context yields nil.
func buildMeetingContext(pre *meetingSessionPreContext) *meetingContext {
	if pre == nil {
		return nil
	}
	context := &meetingContext{
		Title:          pre.Title,
		Purpose:        joinNonEmpty(" / ", pre.Purpose, prefixNonEmpty("期待される成果: ", pre.ExpectedOutput)),
		Background:     pre.Context,
		Agenda:         parseAgendaItems(pre.Agenda),
		Directives:     parseDirectives(pre.CustomInstruction),
		DecisionPoints: pre.DecisionPoints,
		Concerns:       pre.Concerns,
		ExpectedOutput: pre.ExpectedOutput,
	}
	if context.isEmpty() {
		return nil
	}
	return context
}

// agendaLinePrefixPattern strips common bullet/number decorations from the
// head of an agenda line: "-", "*", "・", "●", "■", "1.", "1)", "(1)", "１．",
// "①" など。意図的に単純な行単位の正規化に留め、それ以上の構造推定はしない
// (必要なら Task A のAI正規化が一度だけ行う)。
var agendaLinePrefixPattern = regexp.MustCompile(`^[\s\p{Zs}]*(?:[-*・●○■□▪◦>]|[(（]?[0-9０-９]{1,2}[)）.．、:：]|[①②③④⑤⑥⑦⑧⑨⑩⑪⑫⑬⑭⑮])[\s\p{Zs}]*`)

// parseAgendaItems normalizes the free-text agenda into ordered items with
// stable ids. Splitting is line-based; bullet and number prefixes are
// stripped. A single-line agenda yields one item.
func parseAgendaItems(agenda string) []agendaItem {
	lines := strings.Split(agenda, "\n")
	items := make([]agendaItem, 0, len(lines))
	seen := make(map[string]struct{}, len(lines))
	for _, line := range lines {
		title := strings.TrimSpace(agendaLinePrefixPattern.ReplaceAllString(line, ""))
		if title == "" {
			continue
		}
		key := normalizeForMatch(title)
		if _, duplicate := seen[key]; duplicate {
			continue
		}
		seen[key] = struct{}{}
		order := len(items) + 1
		items = append(items, agendaItem{
			ID:    fmt.Sprintf("%s%d", agendaIDPrefix, order),
			Title: truncateRunes(title, liveAnalysisTopicLabelMaxRunes),
			Order: order,
			Role:  inferAgendaRole(title, ""),
		})
		if len(items) >= meetingContextMaxAgendaItems {
			break
		}
	}
	return items
}

// parseDirectives splits the free-text custom instruction into lines,
// stripping bullets, so each directive can be rendered as one reference rule.
func parseDirectives(instruction string) []string {
	lines := strings.Split(instruction, "\n")
	directives := make([]string, 0, len(lines))
	for _, line := range lines {
		text := strings.TrimSpace(agendaLinePrefixPattern.ReplaceAllString(line, ""))
		if text == "" {
			continue
		}
		directives = append(directives, text)
		if len(directives) >= meetingContextMaxDirectives {
			break
		}
	}
	return directives
}

// normalizeForMatch lowercases and strips whitespace/punctuation so that
// near-identical titles ("話者識別の精度" vs "話者識別の精度。") compare equal.
// It is used for agenda dedup and for the server-side item dedup (Task C).
func normalizeForMatch(value string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(value) {
		switch {
		case r == ' ' || r == '\t' || r == '　':
		case strings.ContainsRune("、。,.!?！？:：;；()（）[]「」『』・-ー_〜~", r):
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}

// renderMeetingContextSections renders the role-separated meeting context for
// prompts. Each input keeps its own role: purpose is the採用判断基準, the
// background is interpretation-only knowledge, and agenda items are the fixed
// classification anchors. The補足指示 is intentionally NOT rendered here; it is
// appended separately, below the system rules, so it can never read as a
// higher-priority instruction (see buildLiveAnalysisUserPrompt).
func renderMeetingContextSections(c *meetingContext) string {
	if c.isEmpty() {
		return ""
	}
	var b strings.Builder
	if c.Title != "" {
		b.WriteString("会議名: " + c.Title + "\n")
	}
	if c.Purpose != "" {
		b.WriteString("目的・ゴール(何を重要な論点として採用するかの判断基準。この文自体を毎回ノード化しない): " + c.Purpose + "\n")
	}
	if c.Background != "" {
		b.WriteString("前提・背景(発言を解釈するための既知情報。ここに書かれている内容そのものを、会議中に新しく議論された論点として登録しない): " + c.Background + "\n")
	}
	if c.DecisionPoints != "" {
		b.WriteString("決定すべき事項: " + c.DecisionPoints + "\n")
	}
	if c.Concerns != "" {
		b.WriteString("事前に挙がっている懸念点: " + c.Concerns + "\n")
	}
	return strings.TrimRight(b.String(), "\n")
}

// renderAgendaTopics renders stable agenda anchors as high-priority
// classification targets. Their presence does not imply a tree topic exists.
func renderAgendaTopics(c *meetingContext) string {
	if c == nil || len(c.Agenda) == 0 {
		return ""
	}
	var b strings.Builder
	for _, item := range c.Agenda {
		role := normalizeAgendaRole(item.Role)
		b.WriteString(item.ID + ": " + item.Title)
		if item.Description != "" {
			b.WriteString(" / 説明: " + item.Description)
		}
		if item.Goal != "" {
			b.WriteString(" / ゴール: " + item.Goal)
		}
		if len(item.SemanticHints) > 0 {
			b.WriteString(" / semanticHints: " + strings.Join(item.SemanticHints, ", "))
		}
		if role == agendaRoleActionSummary {
			b.WriteString(" [role=action_summary, 横断参照専用・primary parentにしない]")
		}
		b.WriteString("\n")
	}
	return strings.TrimRight(b.String(), "\n")
}

// renderDirectives renders the user's補足指示 as reference data with an
// explicit priority note, so the model treats it below the system rules and
// never as license to break tree constraints.
func renderDirectives(c *meetingContext) string {
	if c == nil || len(c.Directives) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("以下は会議作成者からの補足指示です。抽出対象・優先順位・表現の参考にしてください。ただし、上の更新ルール・スキーマ・ツリー構造の制約と矛盾する場合は、常に更新ルール側を優先してください。\n")
	for _, directive := range c.Directives {
		b.WriteString("- " + directive + "\n")
	}
	return strings.TrimRight(b.String(), "\n")
}

// marshalMeetingContext serializes the context for the durable "context"
// analysis row (written when the AI context planner normalizes it once at
// meeting start).
func marshalMeetingContext(c *meetingContext) (json.RawMessage, error) {
	if c == nil {
		return nil, fmt.Errorf("meeting context is empty")
	}
	return json.Marshal(c)
}

// unmarshalMeetingContext restores a persisted context row. Invalid payloads
// degrade to nil so a corrupt row can never wedge analysis.
func unmarshalMeetingContext(payload json.RawMessage) *meetingContext {
	if len(payload) == 0 {
		return nil
	}
	var c meetingContext
	if err := json.Unmarshal(payload, &c); err != nil {
		return nil
	}
	// 復元したアジェンダIDの欠損を防ぐ(古い/手書きの行への防御)。
	for i := range c.Agenda {
		if strings.TrimSpace(c.Agenda[i].ID) == "" {
			c.Agenda[i].ID = fmt.Sprintf("%s%d", agendaIDPrefix, i+1)
		}
		c.Agenda[i].Role = effectiveAgendaRole(c.Agenda[i].Role, c.Agenda[i].Title, "")
	}
	if c.isEmpty() {
		return nil
	}
	return &c
}

func joinNonEmpty(separator string, values ...string) string {
	parts := make([]string, 0, len(values))
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			parts = append(parts, trimmed)
		}
	}
	return strings.Join(parts, separator)
}

func prefixNonEmpty(prefix, value string) string {
	if trimmed := strings.TrimSpace(value); trimmed != "" {
		return prefix + trimmed
	}
	return ""
}
