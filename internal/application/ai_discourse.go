package application

import (
	"regexp"
	"strings"
)

// このファイルは会話制御発話(discourse act)の判定を持つ。
// 「以上をまとめます」「次の話題へ移ります」のような、会議の進行だけを表す
// 発話は議論内容ではないため、fact/issue/question/todo/decision/topicの
// いずれにもしない。判定は文字列完全一致の禁止リストではなく、正規化した
// 発話全体が制御表現で構成されているかどうかで行う。

type discourseAct string

const (
	// discourseContent: 通常の議論内容(既定)。
	discourseContent discourseAct = "content"
	// discourseRecapIntro: まとめ・整理の開始宣言。「以上をまとめます」等。
	discourseRecapIntro discourseAct = "recap_intro"
	// discourseTopicTransition: 議題遷移の宣言。「次の話題へ移ります」等。
	discourseTopicTransition discourseAct = "topic_transition"
	// discourseMeetingControl: 開始・終了・挨拶などの会議運営発話。
	discourseMeetingControl discourseAct = "meeting_control"
)

// discourseFillerPattern は判定前に取り除く前置き・フィラー。制御表現の
// 前後に付いても判定が揺れないようにする。
var discourseFillerPattern = regexp.MustCompile(`^(それでは|それじゃ|では|じゃあ|はい|ええと|えっと|ええ|あの|さて|続いて|最後に|次に|一旦|それで)+`)

// 各discourse actの中核表現。正規化(句読点・空白除去)済みの発話全体が
// これらで構成される場合のみ制御発話とみなす。部分一致では内容発話を
// 巻き込むため、アンカー付きで全体一致に近い形にしている。
var (
	recapIntroPatterns = []*regexp.Regexp{
		regexp.MustCompile(`^(以上|ここまで|本日の内容|今日の内容|これまでの内容)?(を|で|は)?(まとめ|整理|要約|振り返り)(ます|ましょう|させていただきます|たいと思います|します|すると|ると)?$`),
		regexp.MustCompile(`^(結論|決定事項|本日のまとめ)(として|を)?(確認|整理|共有)(します|させてください|しましょう)?$`),
	}
	topicTransitionPatterns = []*regexp.Regexp{
		regexp.MustCompile(`^次の(話題|議題|テーマ|アジェンダ|項目)(へ|に)?(移り|移し|進み|入り)(ます|ましょう|たいと思います)?$`),
		regexp.MustCompile(`^(以上で)?(この|本)(議題|話題|テーマ|項目)(は|を)?(終わり|終了|以上)(ます|です|とします|にします)?$`),
	}
	meetingControlPatterns = []*regexp.Regexp{
		regexp.MustCompile(`^(会議|ミーティング|定例)(を|は)?(開始|終了|再開|中断)(します|しましょう|とします)?$`),
		regexp.MustCompile(`^(よろしくお願いします|お疲れ様でした|ありがとうございました|聞こえますか|始めましょう|終わりましょう)$`),
	}
)

// classifyDiscourseAct は発話テキストのdiscourse actを判定する。
// 制御表現とフィラーだけで構成された短い発話のみを制御発話として扱い、
// 実質的な内容を含む発話は content のままにする。
func classifyDiscourseAct(text string) discourseAct {
	normalized := normalizeDiscourseText(text)
	if normalized == "" {
		return discourseContent
	}
	// 制御発話は宣言のみの短い発話に限る。長い発話はまとめ宣言で始まっても
	// 具体的内容を含むため content とする。
	if len([]rune(normalized)) > 30 {
		return discourseContent
	}
	stripped := discourseFillerPattern.ReplaceAllString(normalized, "")
	if stripped == "" {
		return discourseMeetingControl
	}
	for _, pattern := range recapIntroPatterns {
		if pattern.MatchString(stripped) {
			return discourseRecapIntro
		}
	}
	for _, pattern := range topicTransitionPatterns {
		if pattern.MatchString(stripped) {
			return discourseTopicTransition
		}
	}
	for _, pattern := range meetingControlPatterns {
		if pattern.MatchString(stripped) {
			return discourseMeetingControl
		}
	}
	return discourseContent
}

// isDiscourseOnlyText は発話・ラベルが制御発話のみかどうかを返す。
func isDiscourseOnlyText(text string) bool {
	return classifyDiscourseAct(text) != discourseContent
}

// isDiscourseOnlyItem はitemのtitle/bodyがどちらも制御発話(または空)で、
// 議論内容を含まない場合にtrueを返す。
func isDiscourseOnlyItem(title, body string) bool {
	title = strings.TrimSpace(title)
	body = strings.TrimSpace(body)
	if title == "" && body == "" {
		return false
	}
	if title != "" && !isDiscourseOnlyText(title) {
		return false
	}
	if body != "" && !isDiscourseOnlyText(body) {
		return false
	}
	return true
}

// normalizeDiscourseText は句読点・空白・記号を除いた判定用の文字列を返す。
func normalizeDiscourseText(text string) string {
	var b strings.Builder
	for _, r := range strings.TrimSpace(text) {
		switch r {
		case '。', '、', '，', '．', '!', '！', '?', '？', ' ', '　', '\t', '\n', '\r', '・', '…':
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}
