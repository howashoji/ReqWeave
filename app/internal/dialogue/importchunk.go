package dialogue

// 本ファイルは大容量入力（モデルのコンテキスト長を超える取り込み資料）の分割の、対話エンジン側の責務を担う。
//
// 抽象化層は「各チャンクを独立の StreamMessage として送る」だけで、チャンクの組み立ては
// 本層が行う。分割は見出し・段落境界で区切り、各チャンクに同一の
// 注入文脈を付す。チャンク間で候補の受け渡しはしない（各チャンク独立分析）。
//
// 候補の生成・重複/矛盾指摘・承認反映は取り込み分析（materialanalysis.go）の責務であり、本ファイルは
// 「どう割って・どう送り・どのチャンクが失敗したか」までを持つ。

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/howashoji/ReqWeave/app/internal/aiprovider"
)

// 分割の調整値。
const (
	// DefaultMaxImportChunks は分割数の上限（調整してよい範囲は 5〜20）。
	DefaultMaxImportChunks = 10
	// minSafetyTokens は最大出力トークンが不明なときに確保する最低限の安全余裕。
	//
	// 安全余裕は最大出力トークン以上とする。最大出力トークンが
	// 分かるときはその値を、分からないとき（0）でも余裕が消えないようこの値を用いる。
	minSafetyTokens = 1024
)

// ErrImportTooLarge は分割数の上限を超えた資料。
//
// 分析を開始せず、対象範囲の絞り込みを案内する。原本の取り込み・保持は制限しない。
var ErrImportTooLarge = errors.New("資料が大きすぎます。分析する範囲を選んで再実行してください。")

// ImportChunk は分割した抽出テキストの 1 片。
type ImportChunk struct {
	// Index は 1 始まりのチャンク番号（送信前プレビューの「n/N」表示に使う）。
	Index int `json:"index"`
	// StartLine / EndLine は抽出テキスト（extracted.md）内の行番号（1 始まり・両端含む）。
	// 根拠参照 IMP-nnn#Lm-Ln の組み立てに使う。
	StartLine int `json:"startLine"`
	EndLine   int `json:"endLine"`
	// Text は当該チャンクの本文。全チャンクを順に連結すると元の抽出テキストに戻る。
	Text string `json:"text"`
}

// ImportChunkPlan は分割計画（送信前プレビューの表示材料）。
type ImportChunkPlan struct {
	Chunks []ImportChunk `json:"chunks"`
	// EstimatedTokens は全チャンク送信ぶんの入力トークン概算（注入文脈の重複を含む合計）。
	EstimatedTokens int `json:"estimatedTokens"`
}

// ChunkCount は分割数を返す（1 = 分割なし）。
func (p *ImportChunkPlan) ChunkCount() int {
	if p == nil {
		return 0
	}
	return len(p.Chunks)
}

// ImportChunkBudget は 1 チャンクの本文に使えるトークン数の算出材料。
type ImportChunkBudget struct {
	// ContextWindow は選択中モデルの最大入力トークン（0 = 不明）。
	ContextWindow int
	// FixedTokens はチャンク本文以外に毎回載る分（システムプロンプト＋注入文脈）の概算。
	FixedTokens int
	// MaxOutputTokens は最大出力トークン。
	MaxOutputTokens int
}

// Available は 1 チャンクの本文に割り当てられるトークン数を返す。
//
// 「抽出テキスト＋注入文脈＋最大出力トークン＋安全余裕」がコンテキスト長を超えるときに
// 分割するため、割り当ては
// コンテキスト長 −（注入文脈＋最大出力トークン＋安全余裕）で求める。
// 0 = 判定不能（コンテキスト長が不明、または固定分だけで埋まる）＝ 分割しない。
func (b ImportChunkBudget) Available() int {
	if b.ContextWindow <= 0 {
		return 0
	}
	safety := b.MaxOutputTokens
	if safety < minSafetyTokens {
		safety = minSafetyTokens
	}
	available := b.ContextWindow - b.FixedTokens - b.MaxOutputTokens - safety
	if available <= 0 {
		return 0
	}
	return available
}

// PlanImportChunks は抽出テキストを予算内のチャンクへ分割する。
//
// maxChunks が 0 のときは DefaultMaxImportChunks を用いる。
// 分割数が上限を超える場合は分析を開始せず ErrImportTooLarge を返す。
// 予算が判定不能（コンテキスト長不明）のときは分割しない（超過を判定できないため）。
func PlanImportChunks(text string, budget ImportChunkBudget, maxChunks int) (*ImportChunkPlan, error) {
	if strings.TrimSpace(text) == "" {
		return nil, errors.New("抽出テキストが空です")
	}
	if maxChunks <= 0 {
		maxChunks = DefaultMaxImportChunks
	}
	available := budget.Available()

	var chunks []ImportChunk
	if available == 0 || aiprovider.EstimateTokens(text) <= available {
		chunks = []ImportChunk{{StartLine: 1, EndLine: countLines(text), Text: text}}
	} else {
		chunks = packBlocks(splitBlocks(text), available)
	}
	for i := range chunks {
		chunks[i].Index = i + 1
	}
	if len(chunks) > maxChunks {
		return nil, fmt.Errorf("%w（分割 %d 件・上限 %d 件）", ErrImportTooLarge, len(chunks), maxChunks)
	}

	plan := &ImportChunkPlan{Chunks: chunks}
	for _, c := range chunks {
		// 注入文脈は全チャンクに同一で付くため、チャンク数ぶん計上する（プレビューの概算量）。
		plan.EstimatedTokens += budget.FixedTokens + aiprovider.EstimateTokens(c.Text)
	}
	return plan, nil
}

// block は分割の最小単位（見出し 1 つ、または段落 1 つ）。
type block struct {
	startLine int
	endLine   int
	text      string
}

// splitBlocks は抽出テキストを見出し・段落の境界で区切る。
//
// 新しいブロックが始まるのは「見出し行」または「空行の直後の非空行」。
// 空行は直前のブロックの末尾に残すため、ブロックを順に連結すると元のテキストに戻る。
func splitBlocks(text string) []block {
	lines := strings.Split(text, "\n")
	var blocks []block
	var cur []string
	start := 1
	prevBlank := false

	flush := func(end int) {
		if len(cur) == 0 {
			return
		}
		blocks = append(blocks, block{startLine: start, endLine: end, text: strings.Join(cur, "\n")})
		cur = nil
	}
	for i, line := range lines {
		lineNo := i + 1
		blank := strings.TrimSpace(line) == ""
		if len(cur) > 0 && !blank && (isHeading(line) || prevBlank) {
			flush(lineNo - 1)
			start = lineNo
		}
		cur = append(cur, line)
		prevBlank = blank
	}
	flush(len(lines))
	return blocks
}

// isHeading は Markdown の見出し行かを返す（extracted.md は Markdown）。
func isHeading(line string) bool {
	trimmed := strings.TrimLeft(line, " ")
	if !strings.HasPrefix(trimmed, "#") {
		return false
	}
	level := len(trimmed) - len(strings.TrimLeft(trimmed, "#"))
	return level >= 1 && level <= 6 && strings.HasPrefix(trimmed[level:], " ")
}

// packBlocks はブロックを予算内へ詰め込む（貪欲法。境界はブロック境界を優先する）。
//
// 1 ブロック単独で予算を超える場合のみ、そのブロックを行境界（さらに超える行は文字境界）で割る。
// テキストを落とさないこと（全チャンクの連結 = 元テキスト）を不変条件とする。
func packBlocks(blocks []block, available int) []ImportChunk {
	var chunks []ImportChunk
	var cur *ImportChunk
	appendCur := func() {
		if cur != nil {
			chunks = append(chunks, *cur)
			cur = nil
		}
	}
	for _, b := range blocks {
		if aiprovider.EstimateTokens(b.text) > available {
			appendCur()
			chunks = append(chunks, splitOversizedBlock(b, available)...)
			continue
		}
		if cur == nil {
			cur = &ImportChunk{StartLine: b.startLine, EndLine: b.endLine, Text: b.text}
			continue
		}
		joined := cur.Text + "\n" + b.text
		if aiprovider.EstimateTokens(joined) > available {
			appendCur()
			cur = &ImportChunk{StartLine: b.startLine, EndLine: b.endLine, Text: b.text}
			continue
		}
		cur.Text = joined
		cur.EndLine = b.endLine
	}
	appendCur()
	return chunks
}

// splitOversizedBlock は単独で予算を超えるブロックを行境界で割る。
//
// 1 行だけで予算を超える場合は文字境界で割る（テキストを落とさないため。
// 行番号は当該行の番号を保つ = 根拠参照は行単位）。
func splitOversizedBlock(b block, available int) []ImportChunk {
	lines := strings.Split(b.text, "\n")
	var out []ImportChunk
	var cur *ImportChunk
	for i, line := range lines {
		lineNo := b.startLine + i
		if aiprovider.EstimateTokens(line) > available {
			if cur != nil {
				out = append(out, *cur)
				cur = nil
			}
			for _, part := range splitLongLine(line, available) {
				out = append(out, ImportChunk{StartLine: lineNo, EndLine: lineNo, Text: part})
			}
			continue
		}
		if cur == nil {
			cur = &ImportChunk{StartLine: lineNo, EndLine: lineNo, Text: line}
			continue
		}
		joined := cur.Text + "\n" + line
		if aiprovider.EstimateTokens(joined) > available {
			out = append(out, *cur)
			cur = &ImportChunk{StartLine: lineNo, EndLine: lineNo, Text: line}
			continue
		}
		cur.Text = joined
		cur.EndLine = lineNo
	}
	if cur != nil {
		out = append(out, *cur)
	}
	return out
}

// splitLongLine は 1 行を予算内の断片へ割る（文字境界。ルーンを壊さない）。
func splitLongLine(line string, available int) []string {
	var out []string
	var b strings.Builder
	for _, r := range line {
		if b.Len() > 0 && aiprovider.EstimateTokens(b.String()+string(r)) > available {
			out = append(out, b.String())
			b.Reset()
		}
		b.WriteRune(r)
	}
	if b.Len() > 0 {
		out = append(out, b.String())
	}
	return out
}

func countLines(text string) int {
	return strings.Count(text, "\n") + 1
}

// ImportSend は取り込み分析の分割送信の入力。
type ImportSend struct {
	// Refs は送信記録へ残す対象資料の識別（送信記録の import_refs。どの資料を AI へ送ったかを後から確かめられるように）。
	Refs []aiprovider.ImportRef
	// ConsentGiven は担当者の同意操作済みフラグ。false のまま送ると抽象化層の
	// 同意ゲートが送信を止める。
	ConsentGiven bool
	// System はシステムプロンプト。
	System string
	// Context は全チャンクに同一で付す注入文脈（既存レコード要約・用語集）。
	Context string
	// Schema は出力契約の JSON Schema。
	Schema json.RawMessage
	// Effort はエフォート段階の写像結果（縮退適用後）。
	Effort aiprovider.EffortParams
	// Labels は送信記録の included（文脈種別ラベル）。
	Labels []string
}

// ImportChunkResult は 1 チャンクの送信結果。
type ImportChunkResult struct {
	Index int `json:"index"`
	// Body は応答本文（構造化 JSON。解釈は取り込み分析の責務）。
	Body string `json:"body,omitempty"`
	// Err は失敗した場合の正規化済みエラー（利用者向け文言は公開バインディング層が作る）。
	Err *aiprovider.ProviderError `json:"-"`
	// Interrupted は利用者の中断で終わったか。
	Interrupted bool `json:"interrupted,omitempty"`
}

// OK は候補として使える応答が得られたかを返す。
func (r ImportChunkResult) OK() bool {
	return r.Err == nil && !r.Interrupted && strings.TrimSpace(r.Body) != ""
}

// ImportChunkRun は分割送信の実行状態。
//
// 成功済みチャンクの結果を保持し、失敗・未実行のチャンクだけを再実行できる。
type ImportChunkRun struct {
	Plan    *ImportChunkPlan
	results map[int]ImportChunkResult
}

// NewImportChunkRun は分割計画から実行状態を作る。
func NewImportChunkRun(plan *ImportChunkPlan) (*ImportChunkRun, error) {
	if plan == nil || len(plan.Chunks) == 0 {
		return nil, errors.New("分割計画がありません")
	}
	return &ImportChunkRun{Plan: plan, results: map[int]ImportChunkResult{}}, nil
}

// Pending は未実行・失敗のチャンクを番号順に返す（再実行の対象）。
func (r *ImportChunkRun) Pending() []ImportChunk {
	var out []ImportChunk
	for _, c := range r.Plan.Chunks {
		if res, ok := r.results[c.Index]; !ok || !res.OK() {
			out = append(out, c)
		}
	}
	return out
}

// Succeeded は成功済みチャンクの結果を番号順に返す（再実行で失われない）。
func (r *ImportChunkRun) Succeeded() []ImportChunkResult {
	var out []ImportChunkResult
	for _, c := range r.Plan.Chunks {
		if res, ok := r.results[c.Index]; ok && res.OK() {
			out = append(out, res)
		}
	}
	return out
}

// Failed は失敗した（=再実行が必要な）チャンクの結果を番号順に返す。
func (r *ImportChunkRun) Failed() []ImportChunkResult {
	var out []ImportChunkResult
	for _, c := range r.Plan.Chunks {
		if res, ok := r.results[c.Index]; ok && !res.OK() {
			out = append(out, res)
		}
	}
	return out
}

// Complete は全チャンクが成功しているかを返す。
func (r *ImportChunkRun) Complete() bool { return len(r.Pending()) == 0 }

// RunImportChunks は未実行・失敗のチャンクだけを順に送信する（失敗したチャンクだけを再実行できるように）。
//
// 成功済みチャンクは再送しない（候補は保持される）。各チャンクは独立の
// StreamMessage リクエストとして送り、チャンク間で候補を受け渡さない。
// 利用者の中断（ctx キャンセル）では残りのチャンクを送らずに戻る。
//
// 個々のチャンクの失敗はエラーとして返さず run に記録する（呼び出し側が Failed() を見て
// 再実行する）。返す error は送信を 1 件も試みられない構造的な不備のみ。
func (e *Engine) RunImportChunks(ctx context.Context, run *ImportChunkRun, send ImportSend) error {
	if run == nil || run.Plan == nil {
		return errors.New("分割計画がありません")
	}
	pending := run.Pending()
	if len(pending) == 0 {
		return nil
	}
	total := len(run.Plan.Chunks)
	for _, chunk := range pending {
		req := e.buildImportChunkRequest(send, chunk, total)
		// セッション ID は付けない（取り込み分析はセッション外の呼び出し）。
		body, res := e.streamSilently(ctx, "", req, chunkLabels(send, chunk))
		result := ImportChunkResult{Index: chunk.Index, Body: body,
			Err: res.err, Interrupted: res.interrupted}
		run.results[chunk.Index] = result
		if res.interrupted {
			return nil
		}
	}
	return nil
}

// buildImportChunkRequest は 1 チャンクぶんのリクエストを組み立てる。
//
// 注入文脈は全チャンクで同一。チャンク見出しには資料 ID と行範囲を書き、
// AI が根拠参照 IMP-nnn#Lm-Ln を作れるようにする。
func (e *Engine) buildImportChunkRequest(send ImportSend, chunk ImportChunk, total int) aiprovider.ChatRequest {
	var b strings.Builder
	if strings.TrimSpace(send.Context) != "" {
		b.WriteString(send.Context)
		b.WriteString("\n\n")
	}
	fmt.Fprintf(&b, "### 取り込み資料の本文（%s。%d/%d 部・%s）\n\n",
		importRefLabel(send.Refs), chunk.Index, total, lineRange(send.Refs, chunk))
	b.WriteString(chunk.Text)

	return aiprovider.ChatRequest{
		Model:          e.cfg.Model.ID,
		System:         send.System,
		Messages:       []aiprovider.Message{{Role: aiprovider.RoleUser, Content: b.String()}},
		Effort:         send.Effort,
		ResponseSchema: send.Schema,
		ImportRefs:     send.Refs,
		ConsentGiven:   send.ConsentGiven,
	}
}

// importRefLabel は資料の表示名（複数指定時は列挙）。
func importRefLabel(refs []aiprovider.ImportRef) string {
	if len(refs) == 0 {
		return "資料"
	}
	parts := make([]string, 0, len(refs))
	for _, r := range refs {
		parts = append(parts, fmt.Sprintf("%s %s", r.ID, r.SourceName))
	}
	return strings.Join(parts, " / ")
}

// lineRange はチャンクの行範囲を根拠参照の形式（IMP-nnn#Lm-Ln）で返す。
func lineRange(refs []aiprovider.ImportRef, chunk ImportChunk) string {
	prefix := ""
	if len(refs) == 1 {
		prefix = refs[0].ID
	}
	return fmt.Sprintf("%s#L%d-L%d", prefix, chunk.StartLine, chunk.EndLine)
}

// chunkLabels は当該チャンクの送信記録の included。
//
// どの資料のどの範囲を送ったかを記録に残す（送信記録だけで対象を特定できるようにする）。
func chunkLabels(send ImportSend, chunk ImportChunk) []string {
	labels := make([]string, 0, len(send.Labels)+1)
	labels = append(labels, send.Labels...)
	return append(labels, lineRange(send.Refs, chunk))
}
