package dialogue

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/howashoji/ReqWeave/app/internal/aiprovider"
	"github.com/howashoji/ReqWeave/app/internal/projectstore"
)

func utterance(n int, speaker, body string) projectstore.Utterance {
	return projectstore.Utterance{
		ID:      fmt.Sprintf("utt-%05d", n),
		Speaker: speaker,
		At:      time.Date(2026, 8, 27, 10, n, 0, 0, time.UTC),
		Status:  projectstore.UtteranceCompleted,
		Body:    body,
	}
}

// exchanges は n 往復ぶんの発話列を作る（エージェント → 担当者 の繰り返し）。
func exchanges(n int) []projectstore.Utterance {
	var out []projectstore.Utterance
	for i := 0; i < n; i++ {
		out = append(out, utterance(i*2+1, projectstore.SpeakerAgent, fmt.Sprintf("質問 %d", i+1)))
		out = append(out, utterance(i*2+2, projectstore.SpeakerUser, fmt.Sprintf("回答 %d", i+1)))
	}
	return out
}

// 履歴は「直近 6 往復の原文 + それ以前の要約」で構成する。
func TestCompressHistoryKeepsRecentExchanges(t *testing.T) {
	history, need := CompressHistory(exchanges(9), nil, HistoryBudget{})
	if len(history.Raw) != keepRawExchanges {
		t.Fatalf("原文の保持数が違う: %d（期待 %d）", len(history.Raw), keepRawExchanges)
	}
	if len(need) != 3 {
		t.Fatalf("要約対象の往復数が違う: %d", len(need))
	}
	// 最古の往復から順に要約対象へ回る。
	if need[0].LastID() != "utt-00002" || need[2].LastID() != "utt-00006" {
		t.Errorf("要約対象が最古からでない: %+v", need)
	}
	if !strings.Contains(history.Text(), "質問 4") || strings.Contains(history.Text(), "質問 3") {
		t.Errorf("原文の範囲が違う:\n%s", history.Text())
	}
}

// 予算超過時は最古の原文往復から順に併合し、予算内に収める。
func TestCompressHistoryRespectsBudget(t *testing.T) {
	long := make([]projectstore.Utterance, 0, 8)
	for i := 0; i < 4; i++ {
		long = append(long, utterance(i*2+1, projectstore.SpeakerAgent, strings.Repeat("あ", 300)))
		long = append(long, utterance(i*2+2, projectstore.SpeakerUser, strings.Repeat("い", 300)))
	}
	// 予算 = コンテキスト長 70% − 固定文脈 − 最大出力。1 往復ぶん程度しか入らない値にする。
	budget := HistoryBudget{ContextWindow: 3000, FixedTokens: 0, MaxOutputTokens: 1000}
	history, need := CompressHistory(long, nil, budget)

	if len(need) == 0 {
		t.Fatal("予算超過なのに要約対象が無い")
	}
	if got := aiprovider.EstimateTokens(history.Text()); got > budget.Available() {
		t.Errorf("予算に収まっていない: %d > %d", got, budget.Available())
	}
	// 残るのは直近の往復。
	if !strings.Contains(history.Text(), "い") {
		t.Errorf("直近の往復が残っていない:\n%s", history.Text())
	}
	// 予算不明（コンテキスト長 0）のときは制限しない。
	unknown := HistoryBudget{}
	if unknown.Available() != 0 {
		t.Errorf("予算不明が 0 にならない: %d", unknown.Available())
	}
}

// 中断発話は原文にも要約にも含めない。
func TestInterruptedUtterancesExcluded(t *testing.T) {
	us := []projectstore.Utterance{
		utterance(1, projectstore.SpeakerAgent, "質問"),
		{ID: "utt-00002", Speaker: projectstore.SpeakerAgent, At: time.Now(),
			Status: projectstore.UtteranceInterrupted, Body: "中断された応答"},
		utterance(3, projectstore.SpeakerUser, "回答"),
	}
	history, need := CompressHistory(us, nil, HistoryBudget{})
	if strings.Contains(history.Text(), "中断された応答") {
		t.Errorf("中断発話が原文に含まれる:\n%s", history.Text())
	}
	summary := FallbackSummary(SummaryRequest{Exchanges: append(need, history.Raw...)})
	if strings.Contains(summary.Body, "中断された応答") {
		t.Errorf("中断発話が要約に含まれる:\n%s", summary.Body)
	}
}

// 要約呼び出しの失敗時は「各発話の先頭 200 文字 + 承認記録の ID 一覧」へ縮退する。
func TestFallbackSummary(t *testing.T) {
	longBody := strings.Repeat("あ", 500)
	req := SummaryRequest{
		Exchanges: SplitExchanges([]projectstore.Utterance{
			utterance(1, projectstore.SpeakerAgent, longBody),
			utterance(2, projectstore.SpeakerUser, "短い回答"),
		}),
		RecordIDs: []string{"DEC-001", "ISS-002"},
	}
	got := FallbackSummary(req)
	if got.CoversUntil != "utt-00002" {
		t.Errorf("覆う範囲が違う: %s", got.CoversUntil)
	}
	if len(got.RecordIDs) != 2 {
		t.Errorf("記録 ID が保持されていない: %+v", got.RecordIDs)
	}
	lines := strings.Split(got.Body, "\n")
	if len(lines) != 2 {
		t.Fatalf("行数が違う: %+v", lines)
	}
	head := []rune(lines[0])
	// "[エージェント] " + 200 文字 + "…"
	if len([]rune(strings.TrimPrefix(lines[0], "[エージェント] "))) != fallbackHeadRunes+1 {
		t.Errorf("先頭 %d 文字に切り詰められていない: %d 文字", fallbackHeadRunes, len(head))
	}
	if !strings.Contains(lines[1], "短い回答") {
		t.Errorf("短い発話が切り詰められている: %q", lines[1])
	}
}

// 既存の要約ブロックが覆う範囲は原文から外し、再開時に再計算しない。
func TestExistingSummaryBlocksAreReused(t *testing.T) {
	blocks := []projectstore.SummaryBlock{{
		CoversUntil: "utt-00004",
		Body:        "在庫引当のタイミングを決めた。",
		RecordIDs:   []string{"DEC-001"},
	}}
	history, need := CompressHistory(exchanges(4), blocks, HistoryBudget{})

	if len(need) != 0 {
		t.Errorf("既に要約済みの往復が再要約対象になった: %+v", need)
	}
	if len(history.Raw) != 2 {
		t.Fatalf("原文の残り往復数が違う: %d", len(history.Raw))
	}
	text := history.Text()
	if !strings.Contains(text, "在庫引当のタイミングを決めた。") {
		t.Errorf("要約ブロックが注入されていない:\n%s", text)
	}
	if !strings.Contains(text, "DEC-001") {
		t.Errorf("要約が保持する記録 ID が注入されていない:\n%s", text)
	}
	if strings.Contains(text, "質問 1") || strings.Contains(text, "回答 2") {
		t.Errorf("要約済みの原文が残っている:\n%s", text)
	}
}

// 往復の区切りは担当者の発話で始まる（片方だけの往復も 1 往復として扱う）。
func TestSplitExchanges(t *testing.T) {
	got := SplitExchanges(exchanges(3))
	if len(got) != 3 {
		t.Fatalf("往復数が違う: %d", len(got))
	}
	for i, ex := range got {
		if len(ex.Utterances) != 2 {
			t.Errorf("往復 %d の発話数が違う: %d", i, len(ex.Utterances))
		}
	}
	// 応答待ち（エージェントの質問だけ）も 1 往復。
	one := SplitExchanges([]projectstore.Utterance{utterance(1, projectstore.SpeakerAgent, "質問")})
	if len(one) != 1 || one[0].LastID() != "utt-00001" {
		t.Errorf("片方だけの往復が扱えていない: %+v", one)
	}
	if len(SplitExchanges(nil)) != 0 {
		t.Error("空の履歴で往復が作られた")
	}
}
