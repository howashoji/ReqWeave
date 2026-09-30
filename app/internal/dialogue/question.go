package dialogue

// 本ファイルは章観点の走査順と論点の優先順位付け、既決論点の再質問防止、
// 追問上限の機械的な強制を担う。

import (
	"fmt"
	"strings"
)

// PresentedQuestion は提示した質問（セッションファイルの presented_question に保存する内部構造）。
//
// 回答後もクリアせず「直近に提示した質問」として保持する。同じ論点での提示回数を
// 保持することで、再開後も追問上限を機械的に強制できる。
type PresentedQuestion struct {
	TopicKey string `yaml:"topic_key" json:"topicKey"`
	Text     string `yaml:"text" json:"text"`
	// FollowUpIndex は同じ論点での提示回数（1 始まり）。
	FollowUpIndex int `yaml:"follow_up_index" json:"followUpIndex"`
	// Answered は回答済みか（未回答なら再開時にそのまま提示する）。
	Answered bool `yaml:"answered" json:"answered"`
	// UtteranceID は提示した発話の ID。
	UtteranceID string `yaml:"utterance_id,omitempty" json:"utteranceId,omitempty"`
	// Reconfirm は既決論点への再確認として提示したか（画面で再確認であることを明示する）。
	Reconfirm bool `yaml:"reconfirm,omitempty" json:"reconfirm,omitempty"`
}

// TopicSelection は次に確認する論点（選定結果）。
type TopicSelection struct {
	TopicKey  string
	ChapterID string
	ItemID    string
	ItemName  string
	// Reason は選定理由（優先順位のどれで選ばれたか。プロンプトへは載せず記録・表示用）。
	Reason string
	// FollowUpIndex は選定時点での提示回数（継続なら前回 +1、新規論点なら 1）。
	FollowUpIndex int
}

// 選定理由（優先順位の高い順）。
const (
	ReasonContradiction = "回答取込で検出した矛盾・未反映差分の解消"
	ReasonBlocking      = "確定をブロックしている未決事項の解消"
	ReasonFollowUp      = "着手済み論点の継続"
	ReasonChapterOrder  = "章観点の並び順で最初に現れる未充足の論点"
)

// ReasonPreset はドメインプリセット観点による選定。
const ReasonPreset = "選択した業務領域のプリセット観点"

// ReasonProjectPerspective は登録済みプロジェクト観点による選定。
const ReasonProjectPerspective = "登録されたプロジェクト観点"

// SelectTopic は次に確認する論点を優先順位に従って選ぶ。
//
// 回答取込の矛盾と質問票未発行の未決による選定は、
// 呼び出し側が pendingContradiction / answerableIssues を与えたときのみ効く。
//
// perspectives は追加の質問観点（プリセット観点・プロジェクト観点）。
// 関連章観点の走査の際に**追加の論点として**連結する（充足率の分母には加えない）。
// 関連章観点を持たない観点（Chapter が空 = プロジェクト観点）は全章の走査後に連結する。未選択なら nil。
func SelectTopic(phase string, r Records, completeness []ChapterCompleteness,
	last *PresentedQuestion, perspectives []SelectedPerspective) (*TopicSelection, error) {
	m, err := LoadMetaModel()
	if err != nil {
		return nil, err
	}
	p, err := m.Phase(phase)
	if err != nil {
		return nil, err
	}
	decided := decidedTopicKeys(r)

	// 3. 着手済みで追問上限に達していない論点の継続。
	if last != nil && last.Answered && !decided[last.TopicKey] {
		if chapter, item, ok := findItem(p, last.TopicKey); ok {
			return &TopicSelection{
				TopicKey: last.TopicKey, ChapterID: chapter.ID, ItemID: item.ID, ItemName: item.Name,
				Reason: ReasonFollowUp, FollowUpIndex: last.FollowUpIndex + 1,
			}, nil
		}
		// 観点（プリセット・プロジェクト）の継続（論点キーはメタモデルに無いため観点側から引く）。
		if v, ok := findPerspective(perspectives, last.TopicKey); ok {
			return &TopicSelection{
				TopicKey: v.TopicKey, ChapterID: v.Chapter, ItemID: v.ID, ItemName: v.Name,
				Reason: ReasonFollowUp, FollowUpIndex: last.FollowUpIndex + 1,
			}, nil
		}
	}

	// 4〜5. 章観点の並び順で最初に現れる未充足の章観点の、必須項目リストの定義順。
	byChapter := map[string]ChapterCompleteness{}
	for _, c := range completeness {
		byChapter[c.ChapterID] = c
	}
	for _, c := range p.Chapters {
		cc, ok := byChapter[c.ID]
		if !ok || cc.Percent < 100 {
			for _, item := range c.Items {
				key := c.TopicKey(item.ID)
				if decided[key] || satisfied(c, item, r) {
					continue
				}
				return &TopicSelection{
					TopicKey: key, ChapterID: c.ID, ItemID: item.ID, ItemName: item.Name,
					Reason: ReasonChapterOrder, FollowUpIndex: 1,
				}, nil
			}
		}
		// 当該章観点に紐づくプリセット観点を追加の論点として連結する。
		// 章観点が充足済みでもプリセット観点は残るため、充足率とは独立に走査する。
		for _, v := range perspectives {
			if v.Chapter != c.ID || decided[v.TopicKey] {
				continue
			}
			return &TopicSelection{
				TopicKey: v.TopicKey, ChapterID: c.ID, ItemID: v.ID, ItemName: v.Name,
				Reason: ReasonPreset, FollowUpIndex: 1,
			}, nil
		}
	}
	// 関連章観点を持たない観点（プロジェクト観点）は全章の走査後に連結する。
	// 章観点の充足率とは独立に走査するため、全章が充足済みでも観点は残る。
	for _, v := range perspectives {
		if v.Chapter != "" || decided[v.TopicKey] {
			continue
		}
		return &TopicSelection{
			TopicKey: v.TopicKey, ItemID: v.ID, ItemName: v.Name,
			Reason: ReasonProjectPerspective, FollowUpIndex: 1,
		}, nil
	}
	return nil, nil // 全章観点・プリセット観点・プロジェクト観点が既決（次の質問は無い）
}

// findItem は論点キーから章観点・必須項目を引く。
func findItem(p *Phase, topicKey string) (Chapter, Item, bool) {
	chapterID, itemID, ok := strings.Cut(topicKey, "/")
	if !ok {
		return Chapter{}, Item{}, false
	}
	for _, c := range p.Chapters {
		if c.ID != chapterID {
			continue
		}
		for _, item := range c.Items {
			if item.ID == itemID {
				return c, item, true
			}
		}
	}
	return Chapter{}, Item{}, false
}

// findPerspective は論点キーからプリセット観点を引く。
func findPerspective(perspectives []SelectedPerspective, topicKey string) (SelectedPerspective, bool) {
	for _, v := range perspectives {
		if v.TopicKey == topicKey {
			return v, true
		}
	}
	return SelectedPerspective{}, false
}

// decidedTopicKeys は既決の論点キー集合を返す（覆された決定は除く）。
func decidedTopicKeys(r Records) map[string]bool {
	out := map[string]bool{}
	for _, d := range r.Decisions {
		if d.SupersededBy == "" {
			out[d.TopicKey] = true
		}
	}
	return out
}

// IsDecided は論点キーが既決かを返す（AI に頼らない機械判定）。
func IsDecided(r Records, topicKey string) bool { return decidedTopicKeys(r)[topicKey] }

// ExceedsFollowUpLimit は追問上限を超えたかを返す。
//
// AI の遵守に依存せず、アプリ側で論点の往復数を数えて次論点への移行を強制するために使う。
func ExceedsFollowUpLimit(followUpIndex, limit int) bool { return followUpIndex > limit }

// ParsedQuestion は生成された質問の解釈結果（出力契約の質問提示形式）。
type ParsedQuestion struct {
	TopicKey   string
	Question   string
	Background string
}

// ParseQuestion は生成本文から論点キー・質問・背景を取り出す。
//
// 形式が崩れている場合も本文全体を質問として扱い、対話を止めない（論点キーは空になる）。
func ParseQuestion(body string) ParsedQuestion {
	var out ParsedQuestion
	var section string
	var question, background []string
	for _, line := range strings.Split(body, "\n") {
		trimmed := strings.TrimSpace(line)
		switch {
		case strings.HasPrefix(trimmed, "論点キー:"):
			out.TopicKey = strings.TrimSpace(strings.TrimPrefix(trimmed, "論点キー:"))
			section = ""
			continue
		case strings.HasPrefix(trimmed, "質問:"):
			section = "question"
			if rest := strings.TrimSpace(strings.TrimPrefix(trimmed, "質問:")); rest != "" {
				question = append(question, rest)
			}
			continue
		case strings.HasPrefix(trimmed, "背景:"):
			section = "background"
			if rest := strings.TrimSpace(strings.TrimPrefix(trimmed, "背景:")); rest != "" {
				background = append(background, rest)
			}
			continue
		case trimmed == "```" || strings.HasPrefix(trimmed, "```"):
			continue
		}
		switch section {
		case "question":
			question = append(question, line)
		case "background":
			background = append(background, line)
		}
	}
	out.Question = strings.TrimSpace(strings.Join(question, "\n"))
	out.Background = strings.TrimSpace(strings.Join(background, "\n"))
	if out.Question == "" {
		out.Question = strings.TrimSpace(body)
	}
	return out
}

// ReconfirmNotice は既決論点への再確認であることを明示する前置き。
const ReconfirmNotice = "（この論点は決定済みです。決定内容の変更を目的とした再確認として質問します）"

// QuestionUtteranceBody は保存・表示する発話本文を組み立てる。
func QuestionUtteranceBody(q ParsedQuestion, reconfirm bool) string {
	var b strings.Builder
	if reconfirm {
		b.WriteString(ReconfirmNotice + "\n\n")
	}
	if q.TopicKey != "" {
		fmt.Fprintf(&b, "論点キー: %s\n", q.TopicKey)
	}
	fmt.Fprintf(&b, "質問: %s\n", q.Question)
	if q.Background != "" {
		fmt.Fprintf(&b, "背景: %s\n", q.Background)
	}
	return strings.TrimRight(b.String(), "\n")
}

// encodePresentedQuestion は併置メタデータへ保存できる形（YAML の map）へ変換する。
//
// projectstore は presented_question の構造を解釈しない（スキーマの正本は対話エンジン側）ため、
// ここで相互変換する。
func encodePresentedQuestion(q *PresentedQuestion) any {
	if q == nil {
		return nil
	}
	m := map[string]any{
		"topic_key":       q.TopicKey,
		"text":            q.Text,
		"follow_up_index": q.FollowUpIndex,
		"answered":        q.Answered,
	}
	if q.UtteranceID != "" {
		m["utterance_id"] = q.UtteranceID
	}
	if q.Reconfirm {
		m["reconfirm"] = true
	}
	return m
}

// decodePresentedQuestion は保存済みの presented_question を復元する。
func decodePresentedQuestion(v any) (*PresentedQuestion, error) {
	m, ok := v.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("提示済み質問の形式が不正です: %T", v)
	}
	q := &PresentedQuestion{
		TopicKey:      asString(m["topic_key"]),
		Text:          asString(m["text"]),
		FollowUpIndex: asInt(m["follow_up_index"]),
		UtteranceID:   asString(m["utterance_id"]),
	}
	if b, ok := m["answered"].(bool); ok {
		q.Answered = b
	}
	if b, ok := m["reconfirm"].(bool); ok {
		q.Reconfirm = b
	}
	return q, nil
}

func asString(v any) string {
	s, _ := v.(string)
	return s
}

func asInt(v any) int {
	switch n := v.(type) {
	case int:
		return n
	case int64:
		return int(n)
	case float64:
		return int(n)
	default:
		return 0
	}
}
