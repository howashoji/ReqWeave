package dialogue

// 本ファイルは対話履歴の要約（コンテキスト圧縮）を担う。
//
// 履歴は「直近 N 往復の原文 + それ以前の要約ブロック」で構成する。
// 予算超過時は最古の原文往復から順に要約ブロックへ併合する。
// 中断フラグ付き発話は原文にも要約にも含めない（中断した応答を後続の文脈に混ぜないため）。

import (
	"fmt"
	"strings"

	"github.com/howashoji/ReqWeave/app/internal/aiprovider"
	"github.com/howashoji/ReqWeave/app/internal/projectstore"
)

// 履歴の圧縮の調整値（括弧内は調整してよい範囲）。
const (
	// historyBudgetRatio は入力トークン予算率（60〜80%）。
	historyBudgetRatio = 70
	// keepRawExchanges は原文で保持する往復数（4〜10）。
	keepRawExchanges = 6
	// fallbackHeadRunes は要約失敗時に残す発話先頭の文字数（AI を使わない機械的フォールバック）。
	fallbackHeadRunes = 200
)

// Exchange は 1 往復（利用者の発話とエージェントの応答の並び）。
//
// 話者の交替で区切る。片方だけの往復（応答待ちなど）も 1 往復として扱う。
type Exchange struct {
	Utterances []projectstore.Utterance
}

// LastID は往復の最後の発話 ID を返す。
func (e Exchange) LastID() string {
	if len(e.Utterances) == 0 {
		return ""
	}
	return e.Utterances[len(e.Utterances)-1].ID
}

// CompressedHistory は注入する履歴（要約ブロック + 原文往復）。
type CompressedHistory struct {
	Summaries []projectstore.SummaryBlock
	Raw       []Exchange
}

// Text は注入用のテキストを返す。
func (h CompressedHistory) Text() string {
	var b strings.Builder
	for _, s := range h.Summaries {
		fmt.Fprintf(&b, "（%s までの要約）%s\n", s.CoversUntil, strings.TrimSpace(s.Body))
		if len(s.RecordIDs) > 0 {
			fmt.Fprintf(&b, "  関連記録: %s\n", strings.Join(s.RecordIDs, ", "))
		}
	}
	if len(h.Summaries) > 0 && len(h.Raw) > 0 {
		b.WriteString("\n")
	}
	for _, ex := range h.Raw {
		for _, u := range ex.Utterances {
			fmt.Fprintf(&b, "[%s] %s\n", speakerLabel(u.Speaker), strings.TrimSpace(u.Body))
		}
	}
	return strings.TrimRight(b.String(), "\n")
}

func speakerLabel(speaker string) string {
	if speaker == projectstore.SpeakerAgent {
		return "エージェント"
	}
	return "担当者"
}

// HistoryBudget は履歴に使えるトークン予算の算出材料。
type HistoryBudget struct {
	// ContextWindow は選択中モデルの最大入力トークン（0 = 不明）。
	ContextWindow int
	// FixedTokens は履歴以外に必ず載る文脈（システムプロンプト・レコード類）の概算トークン。
	FixedTokens int
	// MaxOutputTokens は最大出力トークン（安全余裕として差し引く）。
	MaxOutputTokens int
}

// Available は履歴に割り当てられるトークン数を返す（0 = 予算不明のため制限しない）。
//
// 予算 = コンテキスト長 × 70% から、履歴以外の文脈と最大出力トークンを差し引く。
func (b HistoryBudget) Available() int {
	if b.ContextWindow <= 0 {
		return 0
	}
	budget := b.ContextWindow * historyBudgetRatio / 100
	budget -= b.FixedTokens
	budget -= b.MaxOutputTokens
	if budget < 0 {
		return 0
	}
	return budget
}

// SplitExchanges は発話列を往復へ分ける。中断発話は除外する。
//
// 1 往復 = エージェントの質問と担当者の回答の組（1 論点 1 質問の反復構造）。
// 応答待ち・回答のみの片側だけの並びも 1 往復として扱う。
func SplitExchanges(utterances []projectstore.Utterance) []Exchange {
	var out []Exchange
	var cur Exchange
	var lastSpeaker string
	for _, u := range utterances {
		if u.IsInterrupted() {
			continue
		}
		// エージェントの発話で新しい往復を始める（質問 → 回答 を 1 往復とする）。
		if u.Speaker == projectstore.SpeakerAgent && lastSpeaker == projectstore.SpeakerUser {
			out = append(out, cur)
			cur = Exchange{}
		}
		cur.Utterances = append(cur.Utterances, u)
		lastSpeaker = u.Speaker
	}
	if len(cur.Utterances) > 0 {
		out = append(out, cur)
	}
	return out
}

// CompressHistory は履歴を予算内へ収める。
//
// 返す needSummary は「要約ブロックへ併合すべき往復」であり、呼び出し側が
// AI 要約（失敗時は FallbackSummary）で SummaryBlock を作って永続化する。
func CompressHistory(utterances []projectstore.Utterance, existing []projectstore.SummaryBlock, budget HistoryBudget) (CompressedHistory, []Exchange) {
	exchanges := SplitExchanges(utterances)
	// 既に要約済みの往復（要約ブロックが覆う範囲）は原文から外す。
	exchanges = dropSummarized(exchanges, existing)

	history := CompressedHistory{Summaries: existing, Raw: exchanges}
	var needSummary []Exchange

	// まず原文の保持数で切る（直近 keepRawExchanges 往復）。
	if len(history.Raw) > keepRawExchanges {
		cut := len(history.Raw) - keepRawExchanges
		needSummary = append(needSummary, history.Raw[:cut]...)
		history.Raw = history.Raw[cut:]
	}

	// 次に予算で切る（最古の原文往復から順に併合する）。
	available := budget.Available()
	if available > 0 {
		for len(history.Raw) > 1 && aiprovider.EstimateTokens(history.Text()) > available {
			needSummary = append(needSummary, history.Raw[0])
			history.Raw = history.Raw[1:]
		}
	}
	return history, needSummary
}

// dropSummarized は要約ブロックが覆う範囲（covers_until までの発話）を原文から外す。
func dropSummarized(exchanges []Exchange, blocks []projectstore.SummaryBlock) []Exchange {
	covered := ""
	for _, b := range blocks {
		if b.CoversUntil > covered {
			covered = b.CoversUntil
		}
	}
	if covered == "" {
		return exchanges
	}
	var out []Exchange
	for _, ex := range exchanges {
		if ex.LastID() <= covered {
			continue
		}
		out = append(out, ex)
	}
	return out
}

// SummaryRequest は要約 AI 呼び出しの入力（呼び出しは対話ループが行う）。
type SummaryRequest struct {
	Exchanges []Exchange
	// RecordIDs は当該往復で承認された決定・未決・要件項目の ID（要約でも ID を保持する）。
	RecordIDs []string
}

// Text は要約対象の原文を返す。
func (r SummaryRequest) Text() string {
	var b strings.Builder
	for _, ex := range r.Exchanges {
		for _, u := range ex.Utterances {
			fmt.Fprintf(&b, "[%s] %s\n", speakerLabel(u.Speaker), strings.TrimSpace(u.Body))
		}
	}
	return strings.TrimRight(b.String(), "\n")
}

// CoversUntil は要約が覆う最後の発話 ID を返す。
func (r SummaryRequest) CoversUntil() string {
	if len(r.Exchanges) == 0 {
		return ""
	}
	return r.Exchanges[len(r.Exchanges)-1].LastID()
}

// FallbackSummary は要約呼び出しが失敗したときの機械的フォールバック。
//
// 各発話の先頭 200 文字と、その往復で承認された記録の ID 一覧で構成する（AI 呼び出しをしない）。
func FallbackSummary(r SummaryRequest) projectstore.SummaryBlock {
	var b strings.Builder
	for _, ex := range r.Exchanges {
		for _, u := range ex.Utterances {
			fmt.Fprintf(&b, "[%s] %s\n", speakerLabel(u.Speaker), head(u.Body, fallbackHeadRunes))
		}
	}
	return projectstore.SummaryBlock{
		CoversUntil: r.CoversUntil(),
		Body:        strings.TrimRight(b.String(), "\n"),
		RecordIDs:   r.RecordIDs,
	}
}

// head は先頭 n 文字（rune 単位）を返す。切り詰めたときは省略記号を付ける。
func head(s string, n int) string {
	s = strings.TrimSpace(s)
	runes := []rune(s)
	if len(runes) <= n {
		return s
	}
	return string(runes[:n]) + "…"
}
