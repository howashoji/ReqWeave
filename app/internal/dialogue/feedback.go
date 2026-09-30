package dialogue

// 本ファイルはフィードバック論点化（開発AIから返ってきたフィードバックを論点の候補にする）を担う。
//
// 開発AIフィードバック（取り込み資料の kind: dev-ai-feedback）の取り込み分析であり、
// 同意ゲート・分割送信・承認・障害時の扱いは資料の取り込み分析と共通（analyzeImport を通す）。
// 本ファイルが足すのは次の 3 点:
//  1. 定型テンプレ（エクスポートに同梱するフィードバック用テンプレート）の記入項目単位への機械パース（AI 呼び出しの前）
//  2. 紐づけ候補 related_ids と、エクスポート済み成果物の ID 一覧の注入
//  3. operation: update の承認を差し戻し手順（合意済みの要件項目を draft へ戻す）へ接続すること

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"

	"github.com/howashoji/ReqWeave/app/internal/auditlog"
	"github.com/howashoji/ReqWeave/app/internal/importer"
	"github.com/howashoji/ReqWeave/app/internal/projectstore"
)

// ModeFeedbackAnalysis はフィードバック論点化。
const ModeFeedbackAnalysis = "feedback-analysis"

// テンプレの種別（3 値）。
const (
	FeedbackKindQuestion = "質問"
	FeedbackKindIssue    = "指摘"
	FeedbackKindRequest  = "修正依頼"
)

// 記入項目の見出し（4 項目。テンプレ生成側 = docgen と同じ版で保守する）。
const (
	feedbackFieldKind    = "種別"
	feedbackFieldRelated = "関連 ID"
	feedbackFieldBody    = "内容"
	feedbackFieldImpact  = "実装への影響"
)

// exampleHeading は記入例の節見出し（記入例のブロックは論点として扱わない）。
const exampleHeading = "## 記入例"

// blockHeadingRe は 1 論点 = 1 ブロックの見出し（`### フィードバック n`）。
var blockHeadingRe = regexp.MustCompile(`^###\s+(.+)$`)

// fieldLineRe は記入項目の行（`- 種別: 質問`）。
var fieldLineRe = regexp.MustCompile(`^\s*[-*]\s*([^:：]+)[:：]\s*(.*)$`)

// idRe は関連 ID として受け付ける形式（成果物に出る要件・設計・決定・未決の ID）。
var idRe = regexp.MustCompile(`(FR|NFR)-[A-Z]{2,4}-\d{3}|BD-[A-Z]{2,4}-\d{3}|UC-\d{2}|DEC-\d{3}|ISS-\d{3}`)

// FeedbackEntry は定型テンプレの 1 ブロック（1 論点）。
type FeedbackEntry struct {
	// Heading はブロックの見出し（`フィードバック 2` 等）。
	Heading string `json:"heading"`
	// StartLine / EndLine は抽出テキスト内の行範囲（根拠参照 IMP-nnn#Lm-Ln の組み立てに使う）。
	StartLine int `json:"startLine"`
	EndLine   int `json:"endLine"`

	Kind       string   `json:"kind"`
	RelatedIDs []string `json:"relatedIds,omitempty"`
	Body       string   `json:"body"`
	Impact     string   `json:"impact,omitempty"`
}

// InitialCandidate は種別に対応する候補の初期区分（種別との対応表）。
//
// 質問 → 未決事項候補 / 指摘・修正依頼 → 要件変更候補。3 値以外は区分を決めない
// （担当者が承認時に選ぶ。値の誤りでテンプレ全体を落とさない）。
func (e FeedbackEntry) InitialCandidate() string {
	switch e.Kind {
	case FeedbackKindQuestion:
		return projectstore.RecordKindOpenIssue
	case FeedbackKindIssue, FeedbackKindRequest:
		return projectstore.RecordKindRequirement
	default:
		return ""
	}
}

// Ref は当該ブロックの根拠参照（IMP-nnn#Lm-Ln）を返す。
func (e FeedbackEntry) Ref(importID string) string {
	return importer.FormatRef(importID, e.StartLine, e.EndLine)
}

// ParseFeedbackTemplate は定型テンプレを記入項目単位へ分解する。
//
// パースに失敗した場合はエラーを返す。呼び出し側は**自由形式として分析を続ける**
// （テンプレに沿わない書き方でも取り込み自体は失敗させない）。
//
// 失敗条件（形式の非互換を確実に落とす最低条件）:
//   - 論点ブロック（`### 見出し`）が 1 件も無い
//   - ブロック内に記入項目の見出し（種別 / 関連 ID / 内容 / 実装への影響）が欠けている
//   - 同一ブロック内に同じ記入項目の見出しが 2 回以上ある
//
// 記入値そのもの（種別が 3 値でない・関連 ID が空 等）は失敗にしない。値の誤りは
// 担当者が承認時に直せるが、見出し構造の非互換は機械対応付けが成立しないためである。
func ParseFeedbackTemplate(text string) ([]FeedbackEntry, error) {
	lines := strings.Split(text, "\n")
	var entries []FeedbackEntry
	var cur *FeedbackEntry
	var seen map[string]int
	inExample := false

	flush := func(end int) error {
		if cur == nil {
			return nil
		}
		cur.EndLine = end
		if err := validateFeedbackFields(*cur, seen); err != nil {
			return err
		}
		entries = append(entries, *cur)
		cur = nil
		return nil
	}

	for i, line := range lines {
		lineNo := i + 1
		if strings.HasPrefix(line, "## ") {
			if err := flush(lineNo - 1); err != nil {
				return nil, err
			}
			// 記入例の節は論点として扱わない（テンプレ同梱の見本を候補にしない）。
			inExample = strings.TrimSpace(line) == exampleHeading
			continue
		}
		if m := blockHeadingRe.FindStringSubmatch(line); m != nil {
			if err := flush(lineNo - 1); err != nil {
				return nil, err
			}
			if inExample {
				continue
			}
			cur = &FeedbackEntry{Heading: strings.TrimSpace(m[1]), StartLine: lineNo}
			seen = map[string]int{}
			continue
		}
		if cur == nil {
			continue
		}
		m := fieldLineRe.FindStringSubmatch(line)
		if m == nil {
			// 記入項目の行でない本文は、直前の項目の続きとして内容へ足す。
			if strings.TrimSpace(line) != "" && cur.Body != "" {
				cur.Body += "\n" + strings.TrimSpace(line)
			}
			continue
		}
		name := strings.TrimSpace(strings.ReplaceAll(m[1], "　", " "))
		value := strings.TrimSpace(m[2])
		seen[name]++
		switch name {
		case feedbackFieldKind:
			cur.Kind = normalizeFeedbackKind(value)
		case feedbackFieldRelated:
			cur.RelatedIDs = idRe.FindAllString(value, -1)
		case feedbackFieldBody:
			cur.Body = value
		case feedbackFieldImpact:
			cur.Impact = value
		}
	}
	if err := flush(len(lines)); err != nil {
		return nil, err
	}
	if len(entries) == 0 {
		return nil, fmt.Errorf("定型テンプレの論点ブロック（### 見出し）が見つかりません")
	}
	return entries, nil
}

// validateFeedbackFields は記入項目見出しの欠落・重複を検出する。
func validateFeedbackFields(e FeedbackEntry, seen map[string]int) error {
	for _, name := range []string{feedbackFieldKind, feedbackFieldRelated, feedbackFieldBody, feedbackFieldImpact} {
		switch seen[name] {
		case 0:
			return fmt.Errorf("記入項目「%s」がありません（%s）", name, e.Heading)
		case 1:
		default:
			return fmt.Errorf("記入項目「%s」が %d 回あります（%s）", name, seen[name], e.Heading)
		}
	}
	return nil
}

// normalizeFeedbackKind は種別の記入値を 3 値へ正規化する（未記入・記入例の説明文は空）。
func normalizeFeedbackKind(value string) string {
	for _, kind := range []string{FeedbackKindQuestion, FeedbackKindIssue, FeedbackKindRequest} {
		if value == kind {
			return kind
		}
	}
	return ""
}

// ImportFeedback は開発AIフィードバックを取り込む。
//
// 抽出したテキストが定型テンプレとして読めた場合のみ from_template を true にする。
// 読めなくても取り込みは成功させる（自由形式として保持する。資料の取り込みと同じ扱いで原本は無変更）。
func (e *Engine) ImportFeedback(in importer.Input) (*importer.Meta, error) {
	in.Kind = importer.KindDevAIFeedback
	text, err := importer.Extract(in.SourceFormat, in.Content)
	if err == nil {
		in.ExtractionStatus = importer.StatusExtracted
		in.ExtractedText = text
		fromTemplate := false
		if _, perr := ParseFeedbackTemplate(text); perr == nil {
			fromTemplate = true
		}
		in.FromTemplate = &fromTemplate
	} else {
		if !errors.Is(err, importer.ErrNotExtractable) {
			return nil, err
		}
		in.ExtractionStatus = importer.StatusFailed
		in.ExtractedText = ""
		fromTemplate := false
		in.FromTemplate = &fromTemplate
	}
	return importer.New(e.cfg.Store).Import(in)
}

// FeedbackAnalysis はフィードバック論点化の結果。
type FeedbackAnalysis struct {
	*MaterialAnalysis
	// FromTemplate は定型テンプレとしてパースできたか（取り込み資料のメタデータの from_template と同じ判定）。
	FromTemplate bool `json:"fromTemplate"`
	// Entries は記入項目単位に分解した論点（パース成功時のみ。画面で論点と記入項目を対応付けて表示する）。
	Entries []FeedbackEntry `json:"entries,omitempty"`
	// TemplateNotice はパースに失敗した理由（自由形式として続行した旨の案内）。
	TemplateNotice string `json:"templateNotice,omitempty"`
	// RevertTargets は差し戻し（operation: update）になる候補の対象要件項目 ID。
	// 承認前に影響一覧を確認する対象である。
	RevertTargets []string `json:"revertTargets,omitempty"`
}

// feedbackEntries は定型テンプレのパース結果と、失敗時の案内文を返す。
//
// パースに失敗しても自由形式として分析を続ける（取り込み自体は失敗しない）。
// 分析（AnalyzeFeedback）と送信前プレビュー（analysisProfileFor）で同じ結果を使う。
func (e *Engine) feedbackEntries(meta *importer.Meta) ([]FeedbackEntry, string, error) {
	if meta.ExtractionStatus != importer.StatusExtracted {
		return nil, "", nil
	}
	text, err := importer.New(e.cfg.Store).ReadExtracted(meta.ID)
	if err != nil {
		return nil, "", err
	}
	entries, perr := ParseFeedbackTemplate(text)
	if perr != nil {
		return nil, "定型テンプレとして読めなかったため自由形式として分析します（" + perr.Error() + "）。", nil
	}
	return entries, "", nil
}

// AnalyzeFeedback は開発AIフィードバックを論点化する。
//
// 定型テンプレのパースは AI 呼び出しの前に行い、結果を分析の入力文脈にも載せる。
// パースに失敗しても自由形式として分析を続ける（取り込み自体は失敗しない）。
func (e *Engine) AnalyzeFeedback(ctx context.Context, importID string, consentGiven bool) (*FeedbackAnalysis, error) {
	im := importer.New(e.cfg.Store)
	meta, err := im.Load(importID)
	if err != nil {
		return nil, err
	}
	// 種別の取り違えは分析の前に弾く（資料・議事録をフィードバックとして送らない）。
	if meta.Kind != importer.KindDevAIFeedback {
		return nil, fmt.Errorf("この資料は%sではありません（%s）",
			materialKindLabel(importer.KindDevAIFeedback), meta.ID)
	}
	entries, templateNotice, err := e.feedbackEntries(meta)
	if err != nil {
		return nil, err
	}
	out := &FeedbackAnalysis{Entries: entries, FromTemplate: len(entries) > 0 && templateNotice == "",
		TemplateNotice: templateNotice}

	// 用途（注入文脈・出力契約）は送信前プレビューと同一の関数で決める
	// （プレビューと実際の送信内容が食い違わないようにする）。
	profile, err := e.analysisProfileFor(meta)
	if err != nil {
		return nil, err
	}
	analysis, err := e.analyzeImport(ctx, importID, consentGiven, profile)
	if err != nil {
		return nil, err
	}
	out.MaterialAnalysis = analysis
	out.RevertTargets = revertTargets(analysis.Extraction)
	if out.TemplateNotice != "" {
		out.Notice = strings.TrimSpace(out.TemplateNotice + " " + out.Notice)
	}
	return out, nil
}

// revertTargets は差し戻しになる候補（operation: update）の対象要件項目 ID を返す。
func revertTargets(ex *Extraction) []string {
	var out []string
	if ex == nil {
		return nil
	}
	for _, c := range ex.RequirementUpdates {
		if c.Operation == OperationUpdate && c.TargetID != "" && !contains(out, c.TargetID) {
			out = append(out, c.TargetID)
		}
	}
	return out
}

// exportedDesignChapters はエクスポート済み成果物の基本設計の章一覧を返す（設計要素の所在）。
//
// 設計要素 ID（BD-*）はレコードではなく成果物本文にあるため、章とその収載 ID を注入して
// 紐づけ候補の手がかりとする（本文全文は載せない。AI へ送る範囲を必要最小限にするため）。
func (e *Engine) exportedDesignChapters() []projectstore.DocumentChapter {
	chapters, err := e.cfg.Store.LoadDraft(projectstore.DocKindBasicDesign)
	if err != nil {
		return nil
	}
	return chapters
}

// relatedIDUniverse は related_ids として実在を認める ID の集合。
//
// レコード（決定・未決事項・要件項目）に加え、基本設計の章が収載する ID を含める。
func (e *Engine) relatedIDUniverse(records Records) map[string]bool {
	out := recordIDs(records)
	for _, c := range e.exportedDesignChapters() {
		for _, id := range c.Covers {
			out[id] = true
		}
	}
	return out
}

// verifyRelatedIDs は紐づけ候補を実在する ID に限定する（存在しない ID は落とす）。
func verifyRelatedIDs(ex *Extraction, existing map[string]bool) string {
	dropped := 0
	filter := func(ids []string) []string {
		kept := make([]string, 0, len(ids))
		for _, id := range ids {
			if !existing[id] {
				dropped++
				continue
			}
			kept = append(kept, id)
		}
		if len(kept) == 0 {
			return nil
		}
		return kept
	}
	for i := range ex.Decisions {
		ex.Decisions[i].RelatedIDs = filter(ex.Decisions[i].RelatedIDs)
	}
	for i := range ex.OpenIssues {
		ex.OpenIssues[i].RelatedIDs = filter(ex.OpenIssues[i].RelatedIDs)
	}
	for i := range ex.RequirementUpdates {
		ex.RequirementUpdates[i].RelatedIDs = filter(ex.RequirementUpdates[i].RelatedIDs)
	}
	if dropped == 0 {
		return ""
	}
	return fmt.Sprintf("実在しない ID を指す紐づけ候補 %d 件は外しました。", dropped)
}

// buildFeedbackContext はフィードバック論点化の追加文脈。
func buildFeedbackContext(records Records, meta importer.Meta, entries []FeedbackEntry,
	designChapters []projectstore.DocumentChapter) (string, []string) {

	var b strings.Builder
	labels := []string{}

	if len(entries) > 0 {
		b.WriteString("## 定型テンプレの記入内容（記入項目単位に機械分解済み）\n\n")
		for _, e := range entries {
			fmt.Fprintf(&b, "- %s（%s。種別: %s）\n", e.Heading, e.Ref(meta.ID), kindOrUnknown(e.Kind))
			if len(e.RelatedIDs) > 0 {
				fmt.Fprintf(&b, "  - 関連 ID: %s\n", strings.Join(e.RelatedIDs, ", "))
			}
			fmt.Fprintf(&b, "  - 内容: %s\n", oneLine(e.Body))
			if strings.TrimSpace(e.Impact) != "" {
				fmt.Fprintf(&b, "  - 実装への影響: %s\n", oneLine(e.Impact))
			}
		}
		b.WriteString("\n")
		labels = append(labels, "feedback-template")
	}

	if len(designChapters) > 0 {
		b.WriteString("## エクスポート済みの基本設計（章と収載 ID。紐づけ候補に使う）\n\n")
		for _, c := range designChapters {
			fmt.Fprintf(&b, "- %s（%s）: %s\n", c.Chapter, c.FileName, strings.Join(c.Covers, ", "))
		}
		b.WriteString("\n")
		labels = append(labels, "design-chapters")
	}
	return strings.TrimRight(b.String(), "\n"), labels
}

func kindOrUnknown(kind string) string {
	if kind == "" {
		return "未記入"
	}
	return kind
}

// FeedbackApproval はフィードバック論点化の承認内容。
type FeedbackApproval struct {
	MaterialApproval
	// RevertConfirmed は影響一覧を確認済みの要件項目 ID。
	//
	// operation: update の候補を承認するには、その対象 ID がここに無ければならない
	// （差し戻し前に影響一覧を確認する手順を構造で担保する）。
	RevertConfirmed []string `json:"revertConfirmed,omitempty"`
}

// FeedbackApplyResult はフィードバック反映の結果。
type FeedbackApplyResult struct {
	*MaterialApplyResult
	// RevertedRequirementIDs は差し戻した要件項目（agreed → draft）。
	RevertedRequirementIDs []string `json:"revertedRequirementIds,omitempty"`
}

// ApplyFeedbackApproval は承認された候補を反映し、要件変更を差し戻し手順へ接続する。
//
// operation: update の承認は「合意済み要件の変更」であり、確定状態のまま書き換えない。
// 反映後に対象要件項目を draft へ戻し、理由としてフィードバック参照を記録する
// （理由 = IMP-nnn#Lm-Ln）。
func (e *Engine) ApplyFeedbackApproval(importID string, req FeedbackApproval) (*FeedbackApplyResult, error) {
	targets := revertTargetsFromApproval(req)
	for _, id := range targets {
		if !contains(req.RevertConfirmed, id) {
			return nil, fmt.Errorf(
				"要件項目 %s の変更影響を確認していません。影響一覧を確認してから差し戻してください。", id)
		}
	}

	applied, err := e.ApplyMaterialApproval(importID, req.MaterialApproval)
	if err != nil {
		return nil, err
	}
	out := &FeedbackApplyResult{MaterialApplyResult: applied}

	for _, c := range req.RequirementUpdates {
		if c.Candidate.Operation != OperationUpdate || c.Candidate.TargetID == "" {
			continue
		}
		reason := feedbackRevertReason(importID, c.Candidate)
		// 差し戻しも共有レコードの書き換えであり、基準版を検証してから行う（他メンバーの変更を上書きしないため）。
		base, err := e.cfg.Store.CurrentBaseline(c.Candidate.TargetID)
		if err != nil {
			return nil, err
		}
		updated, err := e.cfg.Store.UpdateRequirementGuarded(base, c.Candidate.TargetID,
			func(r *projectstore.Requirement) error {
				r.Status = projectstore.RequirementDraft
				r.RevertedReason = reason
				return nil
			})
		if err != nil {
			return nil, err
		}
		out.RevertedRequirementIDs = append(out.RevertedRequirementIDs, updated.ID)
		e.record(auditlog.ChangeRecord{Target: updated.ID, Change: auditlog.ChangeStatusChanged,
			Before: projectstore.RequirementAgreed, After: projectstore.RequirementDraft,
			Evidence: reason})
	}
	return out, nil
}

// revertTargetsFromApproval は承認内容のうち差し戻しになる対象要件項目 ID を返す。
func revertTargetsFromApproval(req FeedbackApproval) []string {
	var out []string
	for _, c := range req.RequirementUpdates {
		if c.Candidate.Operation == OperationUpdate && c.Candidate.TargetID != "" &&
			!contains(out, c.Candidate.TargetID) {
			out = append(out, c.Candidate.TargetID)
		}
	}
	return out
}

// feedbackRevertReason は差し戻し理由（フィードバック参照を必ず含める。なぜ差し戻したかを後からたどれるように）。
func feedbackRevertReason(importID string, c RequirementCandidate) string {
	ref := importID
	if len(c.EvidenceRefs) > 0 {
		ref = c.EvidenceRefs[0]
	}
	return fmt.Sprintf("開発AIフィードバック（%s）による要件変更", ref)
}
