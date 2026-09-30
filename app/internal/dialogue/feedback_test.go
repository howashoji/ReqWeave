package dialogue

import (
	"strings"
	"testing"

	"github.com/howashoji/ReqWeave/app/internal/projectstore"
)

// filledTemplate はフィードバック用テンプレへ 2 件記入した返送ファイル。
const filledTemplate = `# フィードバック記入用テンプレート

- 1 ブロック 1 論点で記入してください（ブロックは複数記入できます）。

## 記入例

### フィードバック 1

- 種別: 質問
- 関連 ID: FR-INV-001
- 内容: 在庫引当の対象に予約在庫を含めるかが読み取れませんでした。
- 実装への影響: 該当箇所の実装を保留しています。

## 記入欄

### フィードバック 2

- 種別: 質問
- 関連 ID: FR-INV-001
- 内容: 欠品時のバックオーダー起票は自動ですか。
- 実装への影響: 起票処理を仮実装しています。

### フィードバック 3

- 種別: 修正依頼
- 関連 ID: FR-INV-002, BD-DAT-010
- 内容: 引当のタイミングを受注確定時から出荷指示時へ変えてください。
- 実装への影響: 
`

// 定型テンプレは記入項目単位にパースされる。
func TestParseFeedbackTemplate(t *testing.T) {
	got, err := ParseFeedbackTemplate(filledTemplate)
	if err != nil {
		t.Fatalf("定型テンプレをパースできない: %v", err)
	}
	// 記入例のブロックは論点として扱わない。
	if len(got) != 2 {
		t.Fatalf("論点の件数が違う（記入例を拾っていないか）: %d 件 %+v", len(got), got)
	}
	first := got[0]
	if first.Heading != "フィードバック 2" || first.Kind != FeedbackKindQuestion {
		t.Errorf("1 件目の見出し・種別が違う: %+v", first)
	}
	if len(first.RelatedIDs) != 1 || first.RelatedIDs[0] != "FR-INV-001" {
		t.Errorf("関連 ID が取れていない: %+v", first.RelatedIDs)
	}
	if !strings.Contains(first.Body, "バックオーダー起票は自動ですか") {
		t.Errorf("内容が取れていない: %q", first.Body)
	}
	if !strings.Contains(first.Impact, "仮実装") {
		t.Errorf("実装への影響が取れていない: %q", first.Impact)
	}
	second := got[1]
	if second.Kind != FeedbackKindRequest || len(second.RelatedIDs) != 2 {
		t.Errorf("2 件目の種別・関連 ID が違う: %+v", second)
	}
	if second.Impact != "" {
		t.Errorf("任意項目の未記入が空にならない: %q", second.Impact)
	}
	// 行範囲は根拠参照の組み立てに使う（当該ブロックを指すこと）。
	lines := strings.Split(filledTemplate, "\n")
	if !strings.Contains(lines[first.StartLine-1], "フィードバック 2") {
		t.Errorf("開始行が違う: L%d = %q", first.StartLine, lines[first.StartLine-1])
	}
	if first.EndLine < first.StartLine || first.EndLine >= second.StartLine {
		t.Errorf("行範囲が重なっている: %d-%d / %d-", first.StartLine, first.EndLine, second.StartLine)
	}
	if ref := first.Ref("IMP-001"); !strings.HasPrefix(ref, "IMP-001#L") {
		t.Errorf("根拠参照の形式が違う: %q", ref)
	}
}

// 種別と候補の対応: 質問 → 未決事項候補 / 指摘・修正依頼 → 要件変更候補。
func TestFeedbackInitialCandidate(t *testing.T) {
	cases := map[string]string{
		FeedbackKindQuestion: projectstore.RecordKindOpenIssue,
		FeedbackKindIssue:    projectstore.RecordKindRequirement,
		FeedbackKindRequest:  projectstore.RecordKindRequirement,
		"":                   "",
		"要望":                 "",
	}
	for kind, want := range cases {
		if got := (FeedbackEntry{Kind: kind}).InitialCandidate(); got != want {
			t.Errorf("種別 %q の初期区分 = %q, want %q", kind, got, want)
		}
	}
}

// 記入項目見出しの欠落・重複はパース失敗（呼び出し側は自由形式として続ける）。
func TestParseFeedbackTemplateFailsOnBrokenStructure(t *testing.T) {
	cases := map[string]string{
		"記入項目の欠落（内容なし）": `## 記入欄

### フィードバック 2

- 種別: 質問
- 関連 ID: FR-INV-001
- 実装への影響: なし
`,
		"記入項目の重複（種別が 2 回）": `## 記入欄

### フィードバック 2

- 種別: 質問
- 種別: 指摘
- 関連 ID: FR-INV-001
- 内容: 本文
- 実装への影響: なし
`,
		"論点ブロックが無い": `# フィードバック

自由形式で書きました。引当のタイミングを確認したいです。
`,
		"記入例だけ": `## 記入例

### フィードバック 1

- 種別: 質問
- 関連 ID: FR-INV-001
- 内容: 記入例です。
- 実装への影響: なし
`,
	}
	for name, text := range cases {
		if _, err := ParseFeedbackTemplate(text); err == nil {
			t.Errorf("%s: パース失敗になっていない", name)
		}
	}
}

// 記入値の誤り（種別が 3 値でない・関連 ID が空）は構造の失敗にしない。
func TestParseFeedbackTemplateKeepsBlocksWithLooseValues(t *testing.T) {
	const text = `## 記入欄

### フィードバック 2

- 種別: （質問 / 指摘 / 修正依頼 のいずれか）
- 関連 ID: 
- 内容: 引当のタイミングを確認したいです。
- 実装への影響: 
`
	got, err := ParseFeedbackTemplate(text)
	if err != nil {
		t.Fatalf("記入値の誤りでパース失敗になった: %v", err)
	}
	if len(got) != 1 || got[0].Kind != "" || len(got[0].RelatedIDs) != 0 {
		t.Fatalf("記入値の扱いが違う: %+v", got)
	}
	if got[0].InitialCandidate() != "" {
		t.Errorf("種別未記入で初期区分が決まっている: %q", got[0].InitialCandidate())
	}
}

// 紐づけ候補は実在する ID に限る。
func TestVerifyRelatedIDs(t *testing.T) {
	ex := &Extraction{
		OpenIssues: []OpenIssueCandidate{{Topic: "論点",
			RelatedIDs: []string{"FR-INV-001", "FR-XXX-999"}}},
		RequirementUpdates: []RequirementCandidate{{Operation: OperationUpdate, TargetID: "FR-INV-001",
			RelatedIDs: []string{"BD-DAT-010"}}},
		Decisions: []DecisionCandidate{{TopicKey: "scope/in-scope", Body: "本文",
			RelatedIDs: []string{"NFR-PF-001"}}},
	}
	existing := map[string]bool{"FR-INV-001": true, "BD-DAT-010": true}

	notice := verifyRelatedIDs(ex, existing)

	if len(ex.OpenIssues[0].RelatedIDs) != 1 || ex.OpenIssues[0].RelatedIDs[0] != "FR-INV-001" {
		t.Errorf("実在しない紐づけが残っている: %+v", ex.OpenIssues[0].RelatedIDs)
	}
	if len(ex.RequirementUpdates[0].RelatedIDs) != 1 {
		t.Errorf("実在する紐づけが落ちた: %+v", ex.RequirementUpdates[0].RelatedIDs)
	}
	if len(ex.Decisions[0].RelatedIDs) != 0 {
		t.Errorf("実在しない紐づけが残っている: %+v", ex.Decisions[0].RelatedIDs)
	}
	if !strings.Contains(notice, "2 件") {
		t.Errorf("除去件数の案内が違う: %q", notice)
	}
}

// フィードバックのスキーマだけが related_ids を含む。
func TestFeedbackSchemaAddsRelatedIDs(t *testing.T) {
	feedback := string(SchemaForMode(ModeFeedbackAnalysis))
	if !strings.Contains(feedback, "related_ids") {
		t.Fatalf("フィードバックのスキーマに紐づけ候補が無い: %s", feedback)
	}
	if !strings.Contains(feedback, "perspective_candidates") {
		t.Error("フィードバックのスキーマが取り込み分析と同一スキーマになっていない")
	}
	for _, mode := range []string{ModeExtraction, ModeImportAnalysis, ModeMaterialAnalysis} {
		if strings.Contains(string(SchemaForMode(mode)), "related_ids") {
			t.Errorf("%s のスキーマに related_ids が含まれている（フィードバックのみのはず）", mode)
		}
	}
}
