package dialogue

// 本ファイルは取り込み分析（既存資料・議事録を AI で分析して候補にする）を担う。
//
// 取り込みモジュール（importer）が保持した資料の抽出テキストを入力とする AI 分析で、
// 対話ループとは独立の 1 回呼び出しとして実行する。対話セッションの状態
// （発話列・現在論点・対話状態）には触れない（対話の途中でも議事録を随時取り込めるように）。
//
// 分割送信・同意ゲートは importchunk.go / aiprovider が担い、
// 承認・反映は対話の候補と同一処理（applyApproval）を通す（反映の実装を二重に持たない）。
// 未承認候補は取り込み側の一時状態ファイル analysis.meta.yaml へ保全する（中断しても候補を失わないため）。

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/howashoji/ReqWeave/app/internal/aiprovider"
	"github.com/howashoji/ReqWeave/app/internal/auditlog"
	"github.com/howashoji/ReqWeave/app/internal/importer"
	"github.com/howashoji/ReqWeave/app/internal/projectstore"
)

// ModeMaterialAnalysis は取り込み資料の分析。
//
// ModeImportAnalysis（= 回答取込時の分析）とは別物である。
// 前者の入力は取り込み資料の抽出テキスト、後者はステークホルダーの回答であり、
// 根拠参照の形式も異なる（IMP-nnn#Lm-Ln / QS-nnn#q-nn）。
const ModeMaterialAnalysis = "material-analysis"

// MaterialAnalysis は取り込み分析の結果。
//
// この値を作る時点でプロジェクトデータのレコード（決定・未決・要件項目・用語）は変更していない
// （反映は承認後の ApplyMaterialApproval で行う）。
type MaterialAnalysis struct {
	ImportID   string `json:"importId"`
	Kind       string `json:"kind"`
	SourceName string `json:"sourceName"`
	// Extraction は候補（対話の抽出と同一スキーマ + perspective_candidates）。
	Extraction *Extraction `json:"extraction"`
	// ChunkCount は送信した分割数（1 = 分割なし）。
	ChunkCount int `json:"chunkCount"`
	// EstimatedTokens は送信前プレビューと同じ概算入力トークン。
	EstimatedTokens int `json:"estimatedTokens"`
	// FailedChunks は応答が得られなかったチャンク番号（再実行の対象）。
	FailedChunks []int `json:"failedChunks,omitempty"`
	// Fallback は候補を 1 件も取得できなかったか（AI 障害）。
	Fallback bool `json:"fallback"`
	// Notice は画面へ出す案内文（原因＋次の行動）。無ければ空。
	Notice string `json:"notice,omitempty"`
}

// MissingEvidenceCandidate は根拠を解決できなかった候補（参照欠落）。
type MissingEvidenceCandidate struct {
	// Kind は候補の種別（projectstore.RecordKind* と KindPerspective）。
	Kind string `json:"kind"`
	// Title は一覧表示の見出し（決定は本文の 1 行目、未決事項は論点、要件項目は要件名）。
	Title string `json:"title"`
}

// KindPerspective は観点候補の種別（承認先が perspectives.yaml のためレコード種別に無い）。
const KindPerspective = "perspective"

// MissingEvidenceCandidates は根拠が空になった候補を一覧で返す（承認前に参照欠落に気づけるように）。
//
// 承認後の記録側の一覧（projectstore.MissingEvidenceRecords）と同じ「参照欠落」であり、
// 候補と記録で判定を分けない。決定事項・未決事項は根拠なしでは記録できない（その
// 検証がプロジェクトストア側にある）ため、この一覧が承認前の唯一の気づき所になる。
func (a *MaterialAnalysis) MissingEvidenceCandidates() []MissingEvidenceCandidate {
	var out []MissingEvidenceCandidate
	if a == nil || a.Extraction == nil {
		return nil
	}
	for _, c := range a.Extraction.Decisions {
		if c.MissingEvidence {
			out = append(out, MissingEvidenceCandidate{Kind: projectstore.RecordKindDecision, Title: firstLine(c.Body)})
		}
	}
	for _, c := range a.Extraction.OpenIssues {
		if c.MissingEvidence {
			out = append(out, MissingEvidenceCandidate{Kind: projectstore.RecordKindOpenIssue, Title: firstLine(c.Topic)})
		}
	}
	for _, c := range a.Extraction.RequirementUpdates {
		if c.MissingEvidence {
			out = append(out, MissingEvidenceCandidate{Kind: projectstore.RecordKindRequirement, Title: c.Title})
		}
	}
	for _, c := range a.Extraction.PerspectiveCandidates {
		if c.MissingEvidence {
			out = append(out, MissingEvidenceCandidate{Kind: KindPerspective, Title: c.Name})
		}
	}
	return out
}

// AnalyzeMaterial は取り込み資料を分析して候補を返す。
//
// consentGiven が false の場合は抽象化層の同意ゲートが送信を止めるため、
// 候補は得られず Fallback として返る（同意なしに資料は外部送信されない）。
// レコードは変更しない。未承認候補は analysis.meta.yaml へ保全する。
func (e *Engine) AnalyzeMaterial(ctx context.Context, importID string, consentGiven bool) (*MaterialAnalysis, error) {
	return e.analyzeImport(ctx, importID, consentGiven, analysisProfile{Mode: ModeMaterialAnalysis})
}

// analysisProfile は取り込み分析の差分（資料の分析とフィードバック論点化の共通部分を 1 経路に保つ）。
//
// 同意ゲート・分割送信・候補の併合・根拠の検証・一時状態の保全は両者で完全に共通であり、
// 違うのは「用途（プロンプトの出力契約とスキーマ）」「追加の注入文脈」「候補の後処理」だけである。
type analysisProfile struct {
	// Mode はプロンプトの用途（出力契約）。スキーマも同じ用途で選ぶ。
	Mode string
	// RequireKind が空でないとき、その種別の資料しか分析しない。
	RequireKind importer.Kind
	// ExtraContext は用途固有の追加文脈（返り値は本文と送信文脈の内訳ラベル）。
	ExtraContext func(records Records, meta importer.Meta, text string) (string, []string)
	// PostProcess は候補の後処理（用途固有の参照検証など）。戻り値は案内文へ足す一文。
	PostProcess func(ex *Extraction, records Records) string
}

// importSendPlan は取り込み分析で実際に送る内容。
//
// 分析（analyzeImport）と送信前プレビュー（PreviewImportAnalysis）は本構造を**同じ関数**で作る。
// プレビュー用の説明文を別に持たない（表示と送信内容の乖離を構造的に排除する）。
type importSendPlan struct {
	Meta    *importer.Meta
	Text    string
	Records Records
	// Perspectives は注入した既存の質問観点（観点候補の重複指摘の検証に使う）。
	Perspectives []SelectedPerspective
	Send         ImportSend
	Plan         *ImportChunkPlan
}

// buildImportSend は送信内容（システムプロンプト・注入文脈・分割計画）を組み立てる。
//
// 副作用を持たない（レコードも一時状態も書き換えない）。分割上限の超過は ErrImportTooLarge を返す。
func (e *Engine) buildImportSend(importID string, profile analysisProfile) (*importSendPlan, error) {
	im := importer.New(e.cfg.Store)
	meta, err := im.Load(importID)
	if err != nil {
		return nil, err
	}
	if profile.RequireKind != "" && meta.Kind != profile.RequireKind {
		return nil, fmt.Errorf("この資料は%sではありません（%s）",
			materialKindLabel(profile.RequireKind), meta.ID)
	}
	text, err := im.ReadExtracted(importID)
	if err != nil {
		return nil, err
	}

	records, err := e.Records()
	if err != nil {
		return nil, err
	}
	system, err := BuildSystemPrompt(SystemPromptInput{
		Phase: e.cfg.Store.Project().Phase, Effort: e.cfg.Effort, Mode: profile.Mode})
	if err != nil {
		return nil, err
	}
	contextText, labels := buildMaterialContext(records, *meta)
	// 既存の質問観点（プリセット / プロジェクト）を注入する。
	// 観点候補の重複指摘（duplicate_of）の対象がこの一覧であり、注入しないと重複を指摘できない。
	perspectives, err := e.perspectives()
	if err != nil {
		return nil, err
	}
	if section := perspectiveContextSection(perspectives); section != "" {
		contextText += "\n\n" + section
		labels = append(labels, LabelPerspectives)
	}
	if profile.ExtraContext != nil {
		extra, extraLabels := profile.ExtraContext(records, *meta, text)
		if strings.TrimSpace(extra) != "" {
			contextText += "\n\n" + extra
			labels = append(labels, extraLabels...)
		}
	}

	effort := e.effortParams()

	plan, err := PlanImportChunks(text, ImportChunkBudget{
		ContextWindow:   e.cfg.Model.ContextWindow,
		FixedTokens:     aiprovider.EstimateTokens(system) + aiprovider.EstimateTokens(contextText),
		MaxOutputTokens: effort.MaxOutputTokens,
	}, DefaultMaxImportChunks)
	if err != nil {
		// 分割上限の超過は分析を開始しない。
		// 原本・抽出テキストは取り込み済みのまま保持される。
		return nil, err
	}

	return &importSendPlan{
		Meta: meta, Text: text, Records: records, Perspectives: perspectives, Plan: plan,
		Send: ImportSend{
			Refs: []aiprovider.ImportRef{{
				ID: meta.ID, SourceName: meta.SourceName, ImportedAt: meta.ImportedAt}},
			System:  system,
			Context: contextText,
			Schema:  SchemaForMode(profile.Mode),
			Effort:  effort,
			Labels:  labels,
		},
	}, nil
}

// analyzeImport は取り込み分析の本体。
func (e *Engine) analyzeImport(ctx context.Context, importID string, consentGiven bool,
	profile analysisProfile) (*MaterialAnalysis, error) {

	built, err := e.buildImportSend(importID, profile)
	if err != nil {
		return nil, err
	}
	im := importer.New(e.cfg.Store)
	meta, text, records, plan := built.Meta, built.Text, built.Records, built.Plan
	perspectives := built.Perspectives

	run, err := NewImportChunkRun(plan)
	if err != nil {
		return nil, err
	}

	analysis := &MaterialAnalysis{
		ImportID: meta.ID, Kind: string(meta.Kind), SourceName: meta.SourceName,
		Extraction: &Extraction{}, ChunkCount: plan.ChunkCount(), EstimatedTokens: plan.EstimatedTokens,
	}
	// 送信前に「分析中」を保全する（中断・障害で戻ってこられなくても状態が残る）。
	if err := im.SaveAnalysis(meta.ID, importer.AnalysisMeta{
		AnalysisState: importer.AnalysisAnalyzing}); err != nil {
		return nil, err
	}

	// 同意は送信の直前に載せる（同意なしの送信は抽象化層の同意ゲートが止める）。
	send := built.Send
	send.ConsentGiven = consentGiven
	if err := e.RunImportChunks(ctx, run, send); err != nil {
		return nil, err
	}

	merged, causes := mergeChunkExtractions(run)
	for _, r := range run.Failed() {
		analysis.FailedChunks = append(analysis.FailedChunks, r.Index)
		if r.Err != nil {
			causes = append(causes, r.Err.Message)
		}
	}
	analysis.Extraction = merged

	// 根拠は当該資料の実在する行範囲に限る（範囲外・別資料の参照は除去）。
	lines := countLines(text)
	merged.VerifyEvidenceWith(func(ref string) bool { return validImportRef(ref, meta.ID, lines) })
	// 新規の要件項目の ID グループ・種別をアプリが確定する（利用者に入力させない）。
	merged.ResolveRequirementIDs(e.requirementGroupsByChapter())
	// 重複・矛盾の指摘先は実在するレコードに限る（存在しない ID は落とす）。
	droppedDup, droppedContra := verifyRecordRefs(merged, recordIDs(records))
	// 観点候補の重複指摘先は実在する観点に限る（PRS-nnn / 選択中のプリセット観点キー）。
	droppedDup += verifyPerspectiveRefs(merged, perspectiveKeys(perspectives))

	analysis.Fallback = merged.IsEmpty() && len(analysis.FailedChunks) > 0
	analysis.Notice = materialNotice(analysis, causes, droppedDup, droppedContra)
	if profile.PostProcess != nil {
		if extra := profile.PostProcess(merged, records); extra != "" {
			analysis.Notice = strings.TrimSpace(analysis.Notice + " " + extra)
		}
	}

	state := importer.AnalysisAwaitingApproval
	if merged.IsEmpty() {
		state = importer.AnalysisAnalyzing
	}
	// 候補が書き換える共有レコードの基準版を候補と一緒に保全する（承認時に他の変更との競合を検知するため）。
	captured, err := e.captureBaselines(merged)
	if err != nil {
		return nil, err
	}
	if err := im.SaveAnalysis(meta.ID, importer.AnalysisMeta{
		AnalysisState: state, PendingCandidates: encodeExtraction(merged),
		Baselines: captured}); err != nil {
		return nil, err
	}
	return analysis, nil
}

// mergeChunkExtractions は各チャンクの応答を 1 つの候補集合へまとめる。
//
// チャンク間で候補の受け渡しをしないため、重複はここでは統合せずそのまま並べる
// （担当者が duplicate_of と承認操作で整理する）。
// 解釈に失敗したチャンクは候補なしとして扱い、原因を返す（縮退の案内に使う）。
func mergeChunkExtractions(run *ImportChunkRun) (*Extraction, []string) {
	merged := &Extraction{}
	var causes []string
	for _, res := range run.Succeeded() {
		ex, err := ParseExtraction(res.Body)
		if err != nil {
			causes = append(causes, fmt.Sprintf("%d/%d 部の応答を解釈できませんでした（%s）",
				res.Index, len(run.Plan.Chunks), err.Error()))
			continue
		}
		merged.Decisions = append(merged.Decisions, ex.Decisions...)
		merged.OpenIssues = append(merged.OpenIssues, ex.OpenIssues...)
		merged.RequirementUpdates = append(merged.RequirementUpdates, ex.RequirementUpdates...)
		merged.TermCandidates = append(merged.TermCandidates, ex.TermCandidates...)
		merged.Contradictions = append(merged.Contradictions, ex.Contradictions...)
		merged.PerspectiveCandidates = append(merged.PerspectiveCandidates, ex.PerspectiveCandidates...)
	}
	return merged, causes
}

// materialNotice は画面へ出す案内文を組み立てる（原因＋次の行動）。
func materialNotice(a *MaterialAnalysis, causes []string, droppedDup, droppedContra int) string {
	var parts []string
	if len(a.FailedChunks) > 0 {
		nums := make([]string, 0, len(a.FailedChunks))
		for _, n := range a.FailedChunks {
			nums = append(nums, strconv.Itoa(n))
		}
		parts = append(parts, fmt.Sprintf(
			"資料 %d 部のうち %s 部の分析ができませんでした（%s）。原本と抽出テキストは保持しています。分析だけをやり直せます。",
			a.ChunkCount, strings.Join(nums, "・"), firstCause(causes)))
	} else if len(causes) > 0 {
		parts = append(parts, fmt.Sprintf("%s。分析だけをやり直せます。", firstCause(causes)))
	}
	if droppedDup > 0 {
		parts = append(parts, fmt.Sprintf("実在しない指摘先の重複指摘 %d 件は外しました。", droppedDup))
	}
	if droppedContra > 0 {
		parts = append(parts, fmt.Sprintf("実在しない決定を指す矛盾指摘 %d 件は外しました。", droppedContra))
	}
	return strings.Join(parts, " ")
}

func firstCause(causes []string) string {
	if len(causes) == 0 {
		return "原因不明"
	}
	return causes[0]
}

// importRefRe は取り込み資料の根拠参照（IMP-nnn#Lm-Ln）。
var importRefRe = regexp.MustCompile(`^(IMP-\d{3})#L(\d+)-L(\d+)$`)

// validImportRef は根拠参照が当該資料の実在する行範囲を指しているかを返す。
//
// 別資料の ID・行番号 0・逆転した範囲・末尾行を超える範囲はいずれも実在しない参照として扱う
// （存在しない参照は除去する）。
func validImportRef(ref, importID string, lines int) bool {
	m := importRefRe.FindStringSubmatch(ref)
	if m == nil || m[1] != importID {
		return false
	}
	from, err1 := strconv.Atoi(m[2])
	to, err2 := strconv.Atoi(m[3])
	if err1 != nil || err2 != nil {
		return false
	}
	return from >= 1 && to >= from && to <= lines
}

// recordIDs は実在するレコード ID の集合（重複・矛盾の指摘先の検証に使う）。
func recordIDs(r Records) map[string]bool {
	out := map[string]bool{}
	for _, d := range r.Decisions {
		out[d.ID] = true
	}
	for _, i := range r.OpenIssues {
		out[i.ID] = true
	}
	for _, q := range r.Requirements {
		out[q.ID] = true
	}
	return out
}

// verifyRecordRefs は duplicate_of と contradictions の指摘先を実在レコードへ限定する。
//
// duplicate_of は空へ倒し（候補自体は残す = 担当者が新規として承認できる）、
// 指摘先の無い矛盾は候補ごと落とす（対比表示ができず判断材料にならないため）。
func verifyRecordRefs(e *Extraction, existing map[string]bool) (droppedDup, droppedContra int) {
	clear := func(id *string) {
		if *id != "" && !existing[*id] {
			*id = ""
			droppedDup++
		}
	}
	for i := range e.Decisions {
		clear(&e.Decisions[i].DuplicateOf)
		clear(&e.Decisions[i].SupersedesDecisionID)
	}
	for i := range e.OpenIssues {
		clear(&e.OpenIssues[i].DuplicateOf)
	}
	for i := range e.RequirementUpdates {
		clear(&e.RequirementUpdates[i].DuplicateOf)
	}
	kept := make([]Contradiction, 0, len(e.Contradictions))
	for _, c := range e.Contradictions {
		if !existing[c.WithDecisionID] {
			droppedContra++
			continue
		}
		kept = append(kept, c)
	}
	e.Contradictions = kept
	return droppedDup, droppedContra
}

// perspectiveContextSection は既存の質問観点の一覧を組み立てる（観点候補の重複指摘用）。
//
// 登録済みプロジェクト観点と選択中のプリセット観点を並べる。
// 論理削除された観点は e.perspectives() の時点で除かれている。
func perspectiveContextSection(perspectives []SelectedPerspective) string {
	if len(perspectives) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("## 既存の質問観点（観点候補の重複指摘 duplicate_of に使う。値は括弧内のキー）\n\n")
	for _, v := range perspectives {
		fmt.Fprintf(&b, "- %s / %s（%s）: %s\n", v.DomainName, v.Name, v.TopicKey, v.Topics)
	}
	return strings.TrimRight(b.String(), "\n")
}

// perspectiveKeys は観点候補の duplicate_of として受け付ける値の集合を返す。
//
// プロジェクト観点は論点キー（custom/PRS-nnn）と ID（PRS-nnn）のどちらでも受ける
// （プロンプトではキーを示すが、モデルが ID だけを返す揺れを候補の破棄にしない）。
func perspectiveKeys(perspectives []SelectedPerspective) map[string]bool {
	out := map[string]bool{}
	for _, v := range perspectives {
		out[v.TopicKey] = true
		if IsProjectTopicKey(v.TopicKey) {
			out[v.ID] = true
		}
	}
	return out
}

// verifyPerspectiveRefs は観点候補の duplicate_of を実在する観点に限定する。
//
// レコード側（verifyRecordRefs）と同じ扱い: 指摘先が実在しなければ duplicate_of を空へ倒し、
// 候補自体は残す（担当者が新規として承認できる）。
func verifyPerspectiveRefs(e *Extraction, valid map[string]bool) (dropped int) {
	for i := range e.PerspectiveCandidates {
		if id := e.PerspectiveCandidates[i].DuplicateOf; id != "" && !valid[id] {
			e.PerspectiveCandidates[i].DuplicateOf = ""
			dropped++
		}
	}
	return dropped
}

// buildMaterialContext は取り込み分析へ注入する文脈を組み立てる。
//
// 内容は「既存の決定事項・未決事項の要約一覧＋要件項目の ID・タイトル一覧＋用語集」に限る。
// **対話履歴は注入しない**（取り込み分析は対話ループと独立した 1 回呼び出しであるため）。
func buildMaterialContext(r Records, meta importer.Meta) (string, []string) {
	var b strings.Builder
	var labels []string

	fmt.Fprintf(&b, "## 分析対象の資料\n\n- %s（%s / 取り込み日時 %s）\n\n",
		meta.ID, materialKindLabel(meta.Kind), meta.ImportedAt.Format("2006-01-02 15:04"))
	labels = append(labels, meta.ID)

	if len(r.Decisions) > 0 {
		b.WriteString("## 既存の決定事項（重複・矛盾の指摘に使う。結論 1 行の要約）\n\n")
		for _, d := range r.Decisions {
			fmt.Fprintf(&b, "- %s [%s]: %s\n", d.ID, d.TopicKey, firstLine(d.Body))
		}
		b.WriteString("\n")
		labels = append(labels, LabelDecisions)
	}
	if len(r.OpenIssues) > 0 {
		b.WriteString("## 既存の未決事項（決着に相当する記述があれば決着案として出す）\n\n")
		for _, i := range r.OpenIssues {
			fmt.Fprintf(&b, "- %s: %s ／ 決める人: %s ／ 状態: %s\n",
				i.ID, firstLine(i.Body), i.Owner, i.Status)
		}
		b.WriteString("\n")
		labels = append(labels, LabelOpenIssues)
	}
	if len(r.Requirements) > 0 {
		b.WriteString("## 既存の要件項目（ID と要件名のみ。重複指摘・更新対象の特定に使う）\n\n")
		for _, q := range r.Requirements {
			fmt.Fprintf(&b, "- %s %s（%s）\n", q.ID, q.Title, q.Chapter)
		}
		b.WriteString("\n")
		labels = append(labels, LabelRequirements)
	}
	if len(r.Terms) > 0 {
		b.WriteString("## 用語集（この表記を使う。同義語を新造しない）\n\n")
		for _, t := range r.Terms {
			fmt.Fprintf(&b, "- %s（%s）: %s\n", t.Name, t.NameEn, firstLine(t.Definition))
		}
		b.WriteString("\n")
		labels = append(labels, LabelTerms)
	}
	return strings.TrimRight(b.String(), "\n"), labels
}

// materialKindLabel は資料種別の日本語表示（取り込み資料の kind）。
func materialKindLabel(kind importer.Kind) string {
	switch kind {
	case importer.KindMinutes:
		return "議事録"
	case importer.KindDevAIFeedback:
		return "開発AIフィードバック"
	default:
		return "資料"
	}
}

// validateMaterialApproval は資料取込の反映を書き込みの前に検証する（途中まで記録して失敗すると、押し直しで同じ記録が重複するため）。
//
// 記録の候補は ValidateApproval と同じ条件、観点候補は取り込み元の該当箇所
// （プロジェクト観点の evidence。origin: import では必須）を持つこと。文言は「原因＋次の行動」。
func validateMaterialApproval(req MaterialApproval) error {
	var problems []string
	if err := ValidateApproval(req.ApprovalRequest); err != nil {
		problems = append(problems, err.Error())
	}
	for _, c := range req.Perspectives {
		if len(c.EvidenceRefs) == 0 || strings.TrimSpace(c.EvidenceRefs[0]) == "" {
			problems = append(problems, fmt.Sprintf(
				"質問観点の候補「%s」は、資料の該当箇所を特定できないため登録できません。"+
					"この候補は破棄し、必要なら観点の一覧から手動で登録してください。", candidateLabel(c.Name)))
		}
	}
	if len(problems) == 0 {
		return nil
	}
	return errors.New(strings.Join(problems, ""))
}

// applyPerspectives は承認された観点候補をプロジェクト観点として登録する。
//
// 由来は import（承認による登録）で固定し、根拠は候補の 1 件目の IMP-nnn#Lm-Ln を保存する
// （プロジェクト観点の evidence。origin: import では必須）。根拠を解決できなかった候補
// （MissingEvidence = 実在しない行範囲を指していた）は登録しない。
func (e *Engine) applyPerspectives(candidates []PerspectiveCandidate, out *MaterialApplyResult) error {
	for _, c := range candidates {
		var evidence string
		if len(c.EvidenceRefs) > 0 {
			evidence = c.EvidenceRefs[0]
		}
		if evidence == "" {
			return fmt.Errorf(
				"観点候補「%s」に取り込み元の該当箇所がありません。資料の該当箇所を指定するか、観点の一覧から手動で登録してください。",
				c.Name)
		}
		added, err := e.cfg.Store.AddPerspective(c.Name, c.Summary, projectstore.PerspectiveOriginImport, evidence)
		if err != nil {
			return err
		}
		out.PerspectiveIDs = append(out.PerspectiveIDs, added.ID)
		e.record(auditlog.ChangeRecord{Target: added.ID, Change: auditlog.ChangeCreated,
			After: added.Name, Evidence: added.Evidence})
	}
	return nil
}

// PendingMaterialAnalysis は保全されている未承認候補を返す（中断からの復元）。
//
// 保全が無い場合は nil を返す（分析前・承認済み）。
func (e *Engine) PendingMaterialAnalysis(importID string) (*Extraction, error) {
	im := importer.New(e.cfg.Store)
	meta, err := im.LoadAnalysis(importID)
	if err != nil || meta == nil || meta.PendingCandidates == nil {
		return nil, err
	}
	return decodeExtraction(meta.PendingCandidates)
}

// MaterialApproval は取り込み分析の承認内容。
//
// 観点候補は取り込み分析でのみ生成される。対話・回答取込の承認要求
// （ApprovalRequest）には含めず、承認経路を型で分ける（対話の承認から観点が登録される
// 誤りを構造的に排除する）。
type MaterialApproval struct {
	ApprovalRequest
	// Perspectives は登録を承認したプロジェクト観点の候補（perspectives.yaml へ登録する）。
	//
	// 承認していない候補は渡らないため登録されない（承認なしの自動追加をしない）。
	// 編集して承認した場合は編集後の内容が入る。
	Perspectives []PerspectiveCandidate `json:"perspectives,omitempty"`
}

// IsEmpty は承認した候補が 1 件も無いか（= 全破棄）を返す。
func (r MaterialApproval) IsEmpty() bool {
	return r.ApprovalRequest.IsEmpty() && len(r.Perspectives) == 0
}

// MaterialApplyResult は取り込み分析の反映結果。
type MaterialApplyResult struct {
	*ApprovalResult
	ImportID string `json:"importId"`
	// PerspectiveIDs は登録したプロジェクト観点（PRS-nnn）。
	PerspectiveIDs []string `json:"perspectiveIds,omitempty"`
	// Completeness は反映後に再算出した章観点ごとの充足状況（取り込みの反映が完成度にどう効いたかを示す）。
	Completeness []ChapterCompleteness `json:"completeness"`
}

// ApplyMaterialApproval は承認された候補を記録へ反映する。
//
// 承認していない候補は渡らないため反映されない（承認なしの自動確定をしない）。
// 反映は対話・回答取込と同一処理（applyApproval）を通す。全候補の承認・破棄が済むため、
// 完了時に analysis.meta.yaml を削除する。
//
// 観点候補（perspective_candidates）は perspectives.yaml へ登録する。
func (e *Engine) ApplyMaterialApproval(importID string, req MaterialApproval) (*MaterialApplyResult, error) {
	im := importer.New(e.cfg.Store)
	if _, err := im.Load(importID); err != nil {
		return nil, err
	}
	meta, err := im.LoadAnalysis(importID)
	if err != nil {
		return nil, err
	}
	if meta == nil {
		return nil, fmt.Errorf("承認できる分析結果がありません（%s。先に取り込み分析を実行してください）", importID)
	}

	// 書き込みの前に全候補を検証する。観点の登録は記録の反映より後に行うため、
	// 観点で失敗すると決定事項などだけが記録され、分析結果が残ったまま押し直すと重複する。
	if err := validateMaterialApproval(req); err != nil {
		return nil, err
	}

	out := &MaterialApplyResult{ImportID: importID, ApprovalResult: &ApprovalResult{}}
	if !req.ApprovalRequest.IsEmpty() {
		// 候補生成時に保全した基準版で競合を検知する。
		result, err := e.applyApprovalWithBaselines(req.ApprovalRequest, meta.Baselines)
		if err != nil {
			return nil, err
		}
		out.ApprovalResult = result
	}
	if err := e.applyPerspectives(req.Perspectives, out); err != nil {
		return nil, err
	}
	// 全候補の承認・破棄が完了したので一時状態を破棄する。
	if err := im.DeleteAnalysis(importID); err != nil {
		return nil, err
	}

	records, err := e.Records()
	if err != nil {
		return nil, err
	}
	completeness, err := Completeness(e.cfg.Store.Project().Phase, records)
	if err != nil {
		return nil, err
	}
	out.Completeness = completeness
	return out, nil
}

// ---- 送信前プレビュー -----------------------

// ImportSendPreview は取り込み分析の送信前プレビュー。
//
// 表示内容は実際に送る値そのもの（buildImportSend の結果）であり、プレビュー専用の
// 説明文を別に持たない（表示と送信内容が食い違わないことを構造で担保する）。
type ImportSendPreview struct {
	ImportID   string `json:"importId"`
	Kind       string `json:"kind"`
	SourceName string `json:"sourceName"`
	// System / Context は送信するシステムプロンプトと注入文脈そのもの。
	System  string `json:"system"`
	Context string `json:"context"`
	// Chunks は送信する本文の分割（1 件 = 1 回の送信）。
	Chunks []ImportChunk `json:"chunks"`
	// ChunkCount / EstimatedTokens は分割数と概算入力トークン。
	ChunkCount      int `json:"chunkCount"`
	EstimatedTokens int `json:"estimatedTokens"`
	// Labels は送信文脈の内訳（監査記録に残る種別）。
	Labels []string `json:"labels,omitempty"`
	// TooLarge は分割上限を超えて分析を開始できない状態。
	TooLarge bool `json:"tooLarge"`
	// Notice は画面へ出す案内（原因＋次の行動）。無ければ空。
	Notice string `json:"notice,omitempty"`
}

// PreviewImportAnalysis は取り込み分析の送信内容をプレビューする（送信はしない）。
//
// 資料の種別に応じて分析と同じ用途（資料の分析 / フィードバック論点化）で組み立てる。
// 分割上限を超える資料は TooLarge を立てて案内を返す（分析を開始しない）。
func (e *Engine) PreviewImportAnalysis(importID string) (*ImportSendPreview, error) {
	im := importer.New(e.cfg.Store)
	meta, err := im.Load(importID)
	if err != nil {
		return nil, err
	}
	profile, err := e.analysisProfileFor(meta)
	if err != nil {
		return nil, err
	}
	built, err := e.buildImportSend(importID, profile)
	if err != nil {
		if errors.Is(err, ErrImportTooLarge) {
			return &ImportSendPreview{ImportID: meta.ID, Kind: string(meta.Kind),
				SourceName: meta.SourceName, TooLarge: true, Notice: err.Error()}, nil
		}
		return nil, err
	}
	return &ImportSendPreview{
		ImportID:        meta.ID,
		Kind:            string(meta.Kind),
		SourceName:      meta.SourceName,
		System:          built.Send.System,
		Context:         built.Send.Context,
		Chunks:          built.Plan.Chunks,
		ChunkCount:      built.Plan.ChunkCount(),
		EstimatedTokens: built.Plan.EstimatedTokens,
		Labels:          built.Send.Labels,
	}, nil
}

// AnalyzeImport は資料の種別に応じた分析を実行する（資料の分析とフィードバック論点化の入口）。
//
// 開発AIフィードバックは論点化、それ以外は取り込み分析。
// 画面側で種別ごとに呼び分けない（プレビューと同じ経路で用途が決まる）。
func (e *Engine) AnalyzeImport(ctx context.Context, importID string, consentGiven bool) (*FeedbackAnalysis, error) {
	im := importer.New(e.cfg.Store)
	meta, err := im.Load(importID)
	if err != nil {
		return nil, err
	}
	if meta.Kind == importer.KindDevAIFeedback {
		return e.AnalyzeFeedback(ctx, importID, consentGiven)
	}
	analysis, err := e.AnalyzeMaterial(ctx, importID, consentGiven)
	if err != nil {
		return nil, err
	}
	return &FeedbackAnalysis{MaterialAnalysis: analysis}, nil
}

// analysisProfileFor は資料の種別に対応する分析の用途を返す（プレビューと分析で同じ選び方）。
//
// フィードバックの追加文脈（定型テンプレのパース結果）はここでも同じ手順で作る。
func (e *Engine) analysisProfileFor(meta *importer.Meta) (analysisProfile, error) {
	if meta.Kind != importer.KindDevAIFeedback {
		return analysisProfile{Mode: ModeMaterialAnalysis}, nil
	}
	entries, _, err := e.feedbackEntries(meta)
	if err != nil {
		return analysisProfile{}, err
	}
	return analysisProfile{
		Mode:        ModeFeedbackAnalysis,
		RequireKind: importer.KindDevAIFeedback,
		ExtraContext: func(records Records, meta importer.Meta, text string) (string, []string) {
			return buildFeedbackContext(records, meta, entries, e.exportedDesignChapters())
		},
		PostProcess: func(ex *Extraction, records Records) string {
			return verifyRelatedIDs(ex, e.relatedIDUniverse(records))
		},
	}, nil
}
