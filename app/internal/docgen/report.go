package docgen

// 本ファイルは進捗レポートの章1〜6 と全体の組立てを担う。
// 章7（開発AIフィードバック集計）は report_feedback.go（FeedbackChapter）が正本。
//
// レコードからの機械組立てのみで生成し、AI 呼び出しを一切行わない
// （オフライン・API 障害中も動作するように。本ファイルが
// aiprovider へ依存しないことが構造的な担保）。
//
// レポートはプロジェクトデータへ保存しない（使い切りの出力）。
// 本ファイルは読み出しのみで書き込み関数を持たない（呼び出し側が受け取った文字列を
// ファイル出力・クリップボードへ渡す）。
//
// 完成度の算出は対話エンジンが正本であり、ここでは算出関数を
// 受け取って呼ぶ（対話エンジンは本パッケージを import するため、逆向きの依存を作らない）。

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/howashoji/ReqWeave/app/internal/auditlog"
	"github.com/howashoji/ReqWeave/app/internal/importer"
	"github.com/howashoji/ReqWeave/app/internal/projectstore"
)

// ChapterProgress は章観点 1 件の充足率（対話エンジンの完成度の算出結果のうちレポートが使う部分）。
type ChapterProgress struct {
	ChapterID string
	Name      string
	Percent   int
	Satisfied int
	Total     int
}

// CompletenessFunc は章観点別充足率の算出。
//
// 対話エンジンの Completeness を呼び出し側が適合させて渡す。レポート側で判定条件を
// 再実装しない（完成度の正本を二重に持たず、画面の完成度とレポートの数字を一致させる）。
type CompletenessFunc func(phase string, r Records) ([]ChapterProgress, error)

// ProgressReportInput は進捗レポートの入力。
//
// 期間・レコード・変更履歴・質問票・フィードバック集計はすべて呼び出し側が読み出して渡す
// （本パッケージはプロジェクトデータを書き換えない）。
type ProgressReportInput struct {
	// ProjectName は対象システム名（project.yaml）。
	ProjectName string
	// Phase は現在フェーズ。完成度の算出に使う。
	Phase string
	// From / To は対象期間（境界を含む）。保存値と同じ UTC の時刻で渡す。
	From, To time.Time
	// Now は経過日数の基準時刻（章5）。ゼロ値なら To を使う。
	Now time.Time
	// Location は日時表示のタイムゾーン（保存は UTC・表示はローカル）。nil なら time.Local。
	Location *time.Location
	// ProjectCreatedAt はプロジェクト作成日時。期間開始がこれ以前なら期首は「レコードなし」と確定できる。
	ProjectCreatedAt time.Time
	// Records は期末（生成時点）のレコード。
	Records Records
	// Changes は変更履歴（全期間・全作業者をマージしたもの）。期首値の復元に使う。
	Changes []auditlog.ChangeRecord
	// Questionnaires は質問票のフロントマター一覧（章5。質問票の一覧画面と同一データ）。
	Questionnaires []projectstore.Questionnaire
	// Feedback は期間内のフィードバック集計（章7）。nil・0 件なら章ごと省略する。
	Feedback *importer.FeedbackSummary
	// Completeness は充足率の算出（必須）。
	Completeness CompletenessFunc
}

// UnknownLabel は復元できなかった期首値の表示（推定値で埋めない）。
const UnknownLabel = "不明"

// ProgressReport は指定期間の進捗レポートを Markdown で組み立てる。
//
// 単一様式（宛先別の出し分けをしない）で、要約を先頭に詳細を続ける。
func ProgressReport(in ProgressReportInput) (string, error) {
	if strings.TrimSpace(in.ProjectName) == "" {
		return "", fmt.Errorf("対象システム名がありません")
	}
	if in.From.IsZero() || in.To.IsZero() {
		return "", fmt.Errorf("対象期間を指定してください")
	}
	if in.To.Before(in.From) {
		return "", fmt.Errorf("対象期間の終わりが開始より前です")
	}
	if in.Completeness == nil {
		return "", fmt.Errorf("完成度の算出方法が渡されていません")
	}
	if in.Location == nil {
		in.Location = time.Local
	}
	if in.Now.IsZero() {
		in.Now = in.To
	}

	end, err := in.Completeness(in.Phase, in.Records)
	if err != nil {
		return "", err
	}
	start, startRestored, err := in.startCompleteness()
	if err != nil {
		return "", err
	}

	decisions := decisionsInPeriod(in.Records.Decisions, in.From, in.To)
	created, resolved := openIssueMovement(in.Records.OpenIssues, in.Changes, in.From, in.To)
	waiting := waitingQuestionnaires(in.Questionnaires)
	blocking := blockingOpenIssues(in.Records)

	var b strings.Builder
	fmt.Fprintf(&b, "# 進捗レポート: %s\n\n", in.ProjectName)
	fmt.Fprintf(&b, "- 対象期間: %s 〜 %s\n", in.formatDate(in.From), in.formatDate(in.To))
	fmt.Fprintf(&b, "- 生成日時: %s\n", in.formatDateTime(in.Now))
	fmt.Fprintf(&b, "- フェーズ: %s\n\n", phaseLabel(in.Phase))

	b.WriteString(reportSummary(in, decisions, created, resolved, blocking, start, startRestored, end))
	b.WriteString("\n")
	b.WriteString(in.decisionChapter(2, decisions))
	b.WriteString("\n")
	b.WriteString(in.openIssueChapter(3, created, resolved))
	b.WriteString("\n")
	b.WriteString(in.completenessChapter(4, start, startRestored, end))
	b.WriteString("\n")
	b.WriteString(in.questionnaireChapter(5, waiting))
	b.WriteString("\n")
	b.WriteString(in.blockingChapter(6, blocking))
	if chapter := FeedbackChapter(7, in.Feedback); chapter != "" {
		b.WriteString("\n")
		b.WriteString(chapter)
	}
	return b.String(), nil
}

// startCompleteness は期首の充足率を返す（復元できなければ 2 つ目の戻り値が false）。
func (in ProgressReportInput) startCompleteness() ([]ChapterProgress, bool, error) {
	restored, ok := restoreRecordsAt(in.Records, in.Changes, in.From, in.ProjectCreatedAt)
	if !ok {
		return nil, false, nil
	}
	start, err := in.Completeness(in.Phase, restored)
	if err != nil {
		return nil, false, err
	}
	return start, true, nil
}

// ---- 章1 要約 ---------------------------------------------------------------

func reportSummary(in ProgressReportInput, decisions []projectstore.Decision,
	created, resolved []projectstore.OpenIssue, blocking []projectstore.OpenIssue,
	start []ChapterProgress, startRestored bool, end []ChapterProgress) string {

	var b strings.Builder
	b.WriteString("## 1. 要約\n\n")
	fmt.Fprintf(&b, "対象期間（%s 〜 %s）に承認した決定事項は %d 件です。"+
		"未決事項は %d 件を新規に起票し、%d 件が決着しました。"+
		"章観点の充足率（全体）は %s → %s です。"+
		"確定をブロックしている未決事項は %d 件です。\n",
		in.formatDate(in.From), in.formatDate(in.To), len(decisions),
		len(created), len(resolved),
		overallLabel(start, startRestored), overallLabel(end, true), len(blocking))
	return b.String()
}

// overallLabel は全章の必須項目を合算した充足率の表示（復元できていなければ「不明」）。
func overallLabel(progress []ChapterProgress, restored bool) string {
	if !restored {
		return UnknownLabel
	}
	satisfied, total := 0, 0
	for _, c := range progress {
		satisfied += c.Satisfied
		total += c.Total
	}
	if total == 0 {
		return "0%"
	}
	return fmt.Sprintf("%d%%", 100*satisfied/total)
}

// ---- 章2 決定事項 -----------------------------------------------------------

// decisionsInPeriod は decided_at が期間内（境界を含む）の決定事項を日時順で返す。
func decisionsInPeriod(all []projectstore.Decision, from, to time.Time) []projectstore.Decision {
	var out []projectstore.Decision
	for _, d := range all {
		if d.DecidedAt.Before(from) || d.DecidedAt.After(to) {
			continue
		}
		out = append(out, d)
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].DecidedAt.Before(out[j].DecidedAt) })
	return out
}

func (in ProgressReportInput) decisionChapter(number int, decisions []projectstore.Decision) string {
	var b strings.Builder
	fmt.Fprintf(&b, "## %d. 決定事項\n\n", number)
	fmt.Fprintf(&b, "期間内に承認した決定事項: %d 件\n\n", len(decisions))
	if len(decisions) == 0 {
		b.WriteString("（この期間に承認した決定事項はありません）\n")
		return b.String()
	}
	b.WriteString("| ID | 決定内容 | 決定日 |\n|---|---|---|\n")
	for _, d := range decisions {
		fmt.Fprintf(&b, "| %s | %s | %s |\n", d.ID, cell(firstLine(d.Body)), in.formatDate(d.DecidedAt))
	}
	return b.String()
}

// ---- 章3 未決事項の動き -----------------------------------------------------

// openIssueMovement は期間内に新規起票された未決事項と決着した未決事項を返す。
//
// 起票・決着の日時はレコードに持たないため、変更履歴から判定する
// （created = 起票、status-changed の open → resolved = 決着）。
func openIssueMovement(all []projectstore.OpenIssue, changes []auditlog.ChangeRecord,
	from, to time.Time) (created, resolved []projectstore.OpenIssue) {

	byID := map[string]projectstore.OpenIssue{}
	for _, i := range all {
		byID[i.ID] = i
	}
	seenCreated, seenResolved := map[string]bool{}, map[string]bool{}
	for _, c := range changes {
		issue, ok := byID[c.Target]
		if !ok || !inPeriod(c.At, from, to) {
			continue
		}
		switch {
		case c.Change == auditlog.ChangeCreated && !seenCreated[c.Target]:
			seenCreated[c.Target] = true
			created = append(created, issue)
		case c.Change == auditlog.ChangeStatusChanged && c.After == projectstore.OpenIssueResolved &&
			!seenResolved[c.Target]:
			seenResolved[c.Target] = true
			resolved = append(resolved, issue)
		}
	}
	return created, resolved
}

func (in ProgressReportInput) openIssueChapter(number int, created, resolved []projectstore.OpenIssue) string {
	var b strings.Builder
	fmt.Fprintf(&b, "## %d. 未決事項の動き\n\n", number)
	fmt.Fprintf(&b, "新規に起票: %d 件 ／ 決着: %d 件\n\n", len(created), len(resolved))
	writeOpenIssueTable(&b, "新規に起票した未決事項", created)
	b.WriteString("\n")
	writeOpenIssueTable(&b, "決着した未決事項", resolved)
	return b.String()
}

func writeOpenIssueTable(b *strings.Builder, title string, issues []projectstore.OpenIssue) {
	fmt.Fprintf(b, "%s:\n\n", title)
	if len(issues) == 0 {
		b.WriteString("（該当なし）\n")
		return
	}
	b.WriteString("| ID | 論点 | 決める人 | 期限 | 状態 |\n|---|---|---|---|---|\n")
	for _, i := range issues {
		fmt.Fprintf(b, "| %s | %s | %s | %s | %s |\n",
			i.ID, cell(firstLine(i.Body)), cell(i.Owner), dueLabel(i.Due), openIssueStatusLabel(i.Status))
	}
}

func dueLabel(due string) string {
	if strings.TrimSpace(due) == "" {
		return "未設定"
	}
	return due
}

// openIssueStatusLabel は状態の日本語表示（値集合は未決事項のデータ形式で閉じている）。
func openIssueStatusLabel(status string) string {
	switch status {
	case projectstore.OpenIssueOpen:
		return "未決"
	case projectstore.OpenIssueResolved:
		return "決着済み"
	default:
		return "状態不明"
	}
}

// ---- 章4 完成度 -------------------------------------------------------------

func (in ProgressReportInput) completenessChapter(number int,
	start []ChapterProgress, startRestored bool, end []ChapterProgress) string {

	startByID := map[string]ChapterProgress{}
	for _, c := range start {
		startByID[c.ChapterID] = c
	}
	var b strings.Builder
	fmt.Fprintf(&b, "## %d. 完成度\n\n", number)
	b.WriteString("| 章観点 | 期首 | 期末 |\n|---|---|---|\n")
	for _, c := range end {
		from := UnknownLabel
		if startRestored {
			from = fmt.Sprintf("%d%%", startByID[c.ChapterID].Percent)
		}
		fmt.Fprintf(&b, "| %s | %s | %d%% |\n", cell(c.Name), from, c.Percent)
	}
	b.WriteString("\n")
	if startRestored {
		b.WriteString("期末値は生成時点の算出値、期首値は変更履歴（作成・状態変化）から復元した期間開始時点の状態による算出値です。\n")
	} else {
		fmt.Fprintf(&b, "期首値は「%s」です。期間開始時点の状態を変更履歴から復元できませんでした"+
			"（履歴の欠損）。推定値では埋めていません。\n", UnknownLabel)
	}
	return b.String()
}

// ---- 章5 回答待ち質問票 -----------------------------------------------------

// waitingQuestionnaires は発行済みのまま回答が返っていない質問票を発行日順で返す。
func waitingQuestionnaires(all []projectstore.Questionnaire) []projectstore.Questionnaire {
	var out []projectstore.Questionnaire
	for _, q := range all {
		if q.Status == projectstore.QuestionnaireIssued {
			out = append(out, q)
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].IssuedAt.Before(out[j].IssuedAt) })
	return out
}

func (in ProgressReportInput) questionnaireChapter(number int, waiting []projectstore.Questionnaire) string {
	var b strings.Builder
	fmt.Fprintf(&b, "## %d. 回答待ち質問票\n\n", number)
	fmt.Fprintf(&b, "回答待ち: %d 件\n\n", len(waiting))
	if len(waiting) == 0 {
		b.WriteString("（回答待ちの質問票はありません）\n")
		return b.String()
	}
	b.WriteString("| ID | 宛先 | 発行日 | 経過日数 |\n|---|---|---|---|\n")
	for _, q := range waiting {
		fmt.Fprintf(&b, "| %s | %s | %s | %d 日 |\n",
			q.ID, cell(q.Addressee), in.formatDate(q.IssuedAt), elapsedDays(q.IssuedAt, in.Now))
	}
	return b.String()
}

// elapsedDays は発行からの経過日数（24 時間 = 1 日で切り捨て。負値は 0）。
func elapsedDays(from, now time.Time) int {
	d := now.Sub(from)
	if d < 0 {
		return 0
	}
	return int(d / (24 * time.Hour))
}

// ---- 章6 確定をブロックしている要因 -----------------------------------------

// blockingOpenIssues は確定をブロックしている未決事項を返す（確定可否判定と同一の判定）。
//
// 判定材料は未決事項の状態と要件項目の blocked_by（ブロックの関係は要件項目側が正）。
func blockingOpenIssues(r Records) []projectstore.OpenIssue {
	blocked := map[string]bool{}
	for _, req := range r.Requirements {
		for _, id := range req.BlockedBy {
			blocked[id] = true
		}
	}
	var out []projectstore.OpenIssue
	for _, issue := range r.OpenIssues {
		if issue.Status == projectstore.OpenIssueOpen && blocked[issue.ID] {
			out = append(out, issue)
		}
	}
	return out
}

// blockedRequirements はその未決事項がブロックしている要件項目 ID を昇順で返す。
func blockedRequirements(r Records, issueID string) []string {
	var out []string
	for _, req := range r.Requirements {
		for _, id := range req.BlockedBy {
			if id == issueID {
				out = append(out, req.ID)
				break
			}
		}
	}
	sort.Strings(out)
	return out
}

func (in ProgressReportInput) blockingChapter(number int, blocking []projectstore.OpenIssue) string {
	var b strings.Builder
	fmt.Fprintf(&b, "## %d. 確定をブロックしている要因\n\n", number)
	fmt.Fprintf(&b, "ブロックしている未決事項: %d 件\n\n", len(blocking))
	if len(blocking) == 0 {
		b.WriteString("（確定をブロックしている未決事項はありません）\n")
		return b.String()
	}
	b.WriteString("| ID | 論点 | 決める人 | 期限 | ブロック中の要件項目 |\n|---|---|---|---|---|\n")
	for _, i := range blocking {
		fmt.Fprintf(&b, "| %s | %s | %s | %s | %s |\n",
			i.ID, cell(firstLine(i.Body)), cell(i.Owner), dueLabel(i.Due),
			strings.Join(blockedRequirements(in.Records, i.ID), " "))
	}
	return b.String()
}

// ---- 期首状態の復元 ---------------------------------------------------------

// restoreRecordsAt は変更履歴から期間開始時点のレコード状態を機械復元する（章4）。
//
// 復元できる範囲は変更履歴が持つ「作成」と「状態変化」であり、
// 本文の編集内容は履歴に残らないため巻き戻さない。復元できない場合（履歴の欠損 = 現存する
// レコードの最初の記録が履歴に無い）は false を返し、呼び出し側が「不明」と出力する
// （推定値で埋めない）。
//
// at より前（境界を含まない）に最初の記録があるレコードだけを「期首に存在した」とみなす
// （at 以降の変更は対象期間内の動きであり巻き戻す）。
func restoreRecordsAt(cur Records, changes []auditlog.ChangeRecord, at, projectCreatedAt time.Time) (Records, bool) {
	// 期間開始がプロジェクト作成以前なら、期首はレコードが 1 件も無い状態と確定できる。
	if !projectCreatedAt.IsZero() && !at.After(projectCreatedAt) {
		return Records{}, true
	}

	// ID を持つレコードの発生時点は「作成」の記録で決まる。作成の記録が無いレコードは、
	// 後続の記録があっても期首に存在したか判定できない（履歴の欠損 = 復元不能）。
	createdAt := map[string]time.Time{}
	// 用語は作成の記録を持たない（追加も更新も change: updated で記録する）ため、
	// 最初の記録を発生時点とみなす。
	firstSeen := map[string]time.Time{}
	for _, c := range changes {
		if c.Target == "" {
			continue
		}
		if seen, ok := firstSeen[c.Target]; !ok || c.At.Before(seen) {
			firstSeen[c.Target] = c.At
		}
		if c.Change != auditlog.ChangeCreated {
			continue
		}
		if seen, ok := createdAt[c.Target]; !ok || c.At.Before(seen) {
			createdAt[c.Target] = c.At
		}
	}
	existedFrom := func(times map[string]time.Time) func(string) (bool, bool) {
		return func(id string) (bool, bool) { // (期首に存在したか, 判定できたか)
			seen, ok := times[id]
			if !ok {
				return false, false
			}
			return seen.Before(at), true
		}
	}
	existed := existedFrom(createdAt)
	existedTerm := existedFrom(firstSeen)

	var out Records
	for _, d := range cur.Decisions {
		ok, decided := existed(d.ID)
		if !decided {
			return Records{}, false
		}
		if !ok {
			continue
		}
		// 期間内に覆された決定は、期首では覆されていない状態へ戻す。
		if d.SupersededBy != "" && supersededWithin(changes, d.ID, at) {
			d.SupersededBy = ""
		}
		out.Decisions = append(out.Decisions, d)
	}
	for _, i := range cur.OpenIssues {
		ok, decided := existed(i.ID)
		if !decided {
			return Records{}, false
		}
		if !ok {
			continue
		}
		status, restored := statusAt(changes, i.ID, at, i.Status)
		if !restored {
			return Records{}, false
		}
		i.Status = status
		out.OpenIssues = append(out.OpenIssues, i)
	}
	for _, req := range cur.Requirements {
		ok, decided := existed(req.ID)
		if !decided {
			return Records{}, false
		}
		if !ok {
			continue
		}
		status, restored := statusAt(changes, req.ID, at, req.Status)
		if !restored {
			return Records{}, false
		}
		req.Status = status
		req.BlockedBy = blockedByAt(changes, req, at)
		out.Requirements = append(out.Requirements, req)
	}
	for _, t := range cur.Terms {
		// 用語の履歴は用語名を対象に記録される。
		ok, decided := existedTerm(t.Name)
		if !decided {
			return Records{}, false
		}
		if !ok {
			continue
		}
		out.Terms = append(out.Terms, t)
	}
	return out, true
}

// statusAt は at 時点の状態を返す（at 以降の最初の状態変化の Before が at 時点の値）。
//
// 状態変化の記録に変更前の値が無い場合は復元できない（false）。
func statusAt(changes []auditlog.ChangeRecord, id string, at time.Time, current string) (string, bool) {
	for _, c := range changes {
		if c.Target != id || c.Change != auditlog.ChangeStatusChanged || c.At.Before(at) {
			continue
		}
		if strings.TrimSpace(c.Before) == "" {
			return "", false
		}
		return c.Before, true
	}
	return current, true
}

// supersededWithin は at 以降に「置き換え」が記録されたかを返す（決定事項の superseded_by）。
func supersededWithin(changes []auditlog.ChangeRecord, id string, at time.Time) bool {
	for _, c := range changes {
		if c.Target == id && c.Change == auditlog.ChangeUpdated && !c.At.Before(at) &&
			strings.HasPrefix(c.After, supersededByPrefix) {
			return true
		}
	}
	return false
}

// blockedByAt は at 時点のブロック対象を返す（at 以降に追加されたブロックを外す）。
func blockedByAt(changes []auditlog.ChangeRecord, req projectstore.Requirement, at time.Time) []string {
	added := map[string]bool{}
	for _, c := range changes {
		if c.Target != req.ID || c.Change != auditlog.ChangeUpdated || c.At.Before(at) {
			continue
		}
		if id, ok := strings.CutPrefix(c.After, blockedByPrefix); ok {
			added[strings.TrimSpace(id)] = true
		}
	}
	if len(added) == 0 {
		return req.BlockedBy
	}
	var out []string
	for _, id := range req.BlockedBy {
		if !added[id] {
			out = append(out, id)
		}
	}
	return out
}

// 変更履歴の After に書く接頭辞（記録側 = 対話エンジンの承認処理と同一の文字列）。
const (
	supersededByPrefix = "superseded_by: "
	blockedByPrefix    = "blocked_by: "
)

// ---- 共通 -------------------------------------------------------------------

func inPeriod(t, from, to time.Time) bool {
	return !t.Before(from) && !t.After(to)
}

func (in ProgressReportInput) formatDate(t time.Time) string {
	return t.In(in.Location).Format("2006-01-02")
}

func (in ProgressReportInput) formatDateTime(t time.Time) string {
	return t.In(in.Location).Format("2006-01-02 15:04")
}

// phaseLabel はフェーズの日本語表示（値集合はセッションのデータ形式で閉じている）。
func phaseLabel(phase string) string {
	switch phase {
	case projectstore.PhaseRequirements:
		return "要件定義"
	case projectstore.PhaseBasicDesign:
		return "基本設計"
	default:
		return "フェーズ不明"
	}
}

// SaveProgressReport は進捗レポートを Markdown ファイルへ書き出す。
//
// 出力先はプロジェクトフォルダの外（利用者が指定した任意の場所）であり、プロジェクトデータは
// 変化しない。改行は LF・文字コードは UTF-8。
// 一時ファイルへ書いてから移動するため、途中失敗で既存ファイルを壊さない。
func SaveProgressReport(dst, content string) error {
	if strings.TrimSpace(dst) == "" {
		return fmt.Errorf("出力先のファイル名を指定してください")
	}
	if strings.TrimSpace(content) == "" {
		return fmt.Errorf("出力する進捗レポートがありません")
	}
	parent := filepath.Dir(dst)
	if err := os.MkdirAll(parent, 0o755); err != nil {
		return fmt.Errorf("出力先を作成できません: %w", err)
	}
	tmp, err := os.CreateTemp(parent, ".progress-report-*")
	if err != nil {
		return fmt.Errorf("一時ファイルを作成できません: %w", err)
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if _, err := tmp.WriteString(content); err != nil {
		tmp.Close()
		return fmt.Errorf("進捗レポートを書き出せません: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("進捗レポートを書き出せません: %w", err)
	}
	if err := os.Chmod(tmpName, 0o644); err != nil {
		return fmt.Errorf("進捗レポートを書き出せません: %w", err)
	}
	if err := os.Rename(tmpName, dst); err != nil {
		return fmt.Errorf("進捗レポートを保存できません（%s）: %w", dst, err)
	}
	return nil
}
