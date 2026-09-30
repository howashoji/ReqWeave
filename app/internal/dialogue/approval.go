package dialogue

// 本ファイルは候補の承認・編集・破棄と、その反映（状態は反映中）を担う。
//
// 承認操作なしに候補が確定記録されることはない（AI の抽出をそのまま記録にしないため）。
// 破棄した候補はいかなるレコードにも記録しない（渡された候補だけを反映する構造で担保する）。

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/howashoji/ReqWeave/app/internal/auditlog"
	"github.com/howashoji/ReqWeave/app/internal/projectstore"
)

// DecisionApproval は決定事項候補の承認（編集して承認した場合は編集後の内容を入れる）。
type DecisionApproval struct {
	Candidate DecisionCandidate `json:"candidate"`
	// ResolvesIssueIDs はこの決定で決着させる未決事項 ID（決定の記録と未決の決着を 1 操作で行う）。
	ResolvesIssueIDs []string `json:"resolvesIssueIds,omitempty"`
}

// RequirementApproval は要件項目の反映案の承認。
type RequirementApproval struct {
	Candidate RequirementCandidate `json:"candidate"`
	// Group は create 時の ID グループ（FR-<グループ>-nnn の <グループ>。空なら候補に解決済みの値を使う）。
	Group string `json:"group,omitempty"`
	// Priority は create 時の優先度（未指定は must）。
	Priority string `json:"priority,omitempty"`
	// Kind は create 時の種別（未指定は functional）。
	Kind string `json:"kind,omitempty"`
}

// MergeResolution は競合を確認したうえでの反映の承認。
//
// 競合パネルで三面を確認した担当者本人が「自分の案を通す」「両立編集した内容を反映する」を
// 選んだときに、確認した**現在の内容の基準版**を添えて再送する。これが無い限り、競合した
// レコードは書き換わらない（後勝ち上書きをしない。競合の解消は反映を行う担当者本人が承認する）。
type MergeResolution struct {
	ID string `json:"id"`
	// BaselineHash は競合パネルで確認した現在の内容のハッシュ（RecordConflict.CurrentHash）。
	BaselineHash string `json:"baselineHash"`
	// Reference は競合相手の変更の参照（変更履歴 merge-applied へ残す）。
	Reference string `json:"reference,omitempty"`
}

// ApprovalRequest は承認する候補だけを含む（破棄した候補は渡さない）。
type ApprovalRequest struct {
	Decisions          []DecisionApproval    `json:"decisions,omitempty"`
	OpenIssues         []OpenIssueCandidate  `json:"openIssues,omitempty"`
	RequirementUpdates []RequirementApproval `json:"requirementUpdates,omitempty"`
	TermCandidates     []TermCandidate       `json:"termCandidates,omitempty"`
	// Merges は競合を確認したうえでの反映の承認。
	Merges []MergeResolution `json:"merges,omitempty"`
}

// IsEmpty は承認する候補が 1 件も無いか（= 全破棄）を返す。
func (r ApprovalRequest) IsEmpty() bool {
	return len(r.Decisions) == 0 && len(r.OpenIssues) == 0 &&
		len(r.RequirementUpdates) == 0 && len(r.TermCandidates) == 0
}

// ApprovalResult は反映結果（反映後の一覧提示に使う）。
type ApprovalResult struct {
	DecisionIDs    []string `json:"decisionIds,omitempty"`
	OpenIssueIDs   []string `json:"openIssueIds,omitempty"`
	RequirementIDs []string `json:"requirementIds,omitempty"`
	TermNames      []string `json:"termNames,omitempty"`
	// ResolvedIssueIDs は決着した未決事項。
	ResolvedIssueIDs []string `json:"resolvedIssueIds,omitempty"`
	// UnblockedRequirementIDs は決着によってブロックが外れた要件項目（反映漏れを残さない）。
	UnblockedRequirementIDs []string `json:"unblockedRequirementIds,omitempty"`
	// State は反映後の対話状態。
	State string `json:"state"`
}

// ApproveCandidates は承認された候補を記録へ反映する（状態は反映中）。
//
// 全候補を破棄した場合（req が空）は何も記録せず質問生成中へ戻る。
func (e *Engine) ApproveCandidates(sessionID string, req ApprovalRequest) (*ApprovalResult, error) {
	state, err := e.LoadState(sessionID)
	if err != nil {
		return nil, err
	}
	// 反映中のまま候補が残っているのは、反映に失敗して承認待ちへ戻れなかった場合
	// （反映に失敗すると反映中のまま残っていた旧版で保存されたセッション）か、反映中に強制終了した場合。
	// 候補は保全されているので承認待ちへ戻して受け付ける（押し直せないまま詰まらせない）。
	if state.DialogueState == StateApplying && state.PendingCandidates != nil {
		if err := e.setState(sessionID, state, StateAwaitingApproval); err != nil {
			return nil, err
		}
	}
	if state.DialogueState != StateAwaitingApproval {
		return nil, fmt.Errorf("承認できる状態ではありません（現在: %s）", state.DialogueState)
	}
	if req.IsEmpty() {
		// 全破棄: 記録なしで質問生成中へ戻る。
		state.PendingCandidates = nil
		state.Baselines = nil
		if err := e.setState(sessionID, state, StateQuestioning); err != nil {
			return nil, err
		}
		return &ApprovalResult{State: state.DialogueState}, nil
	}
	if err := e.setState(sessionID, state, StateApplying); err != nil {
		return nil, err
	}

	result, err := e.applyApprovalWithBaselines(req, state.Baselines)
	if err != nil {
		// 候補を保全したまま承認待ちへ戻す（競合の三面確認のあとで押し直せる）。
		if serr := e.setState(sessionID, state, StateAwaitingApproval); serr != nil {
			return nil, errors.Join(err, serr)
		}
		return nil, err
	}

	// 反映が終わったら未承認候補を消し、次の質問生成へ戻る。
	state.PendingCandidates = nil
	state.Baselines = nil
	if err := e.setState(sessionID, state, StateQuestioning); err != nil {
		return nil, err
	}
	result.State = state.DialogueState
	return result, nil
}

// applyApproval は承認された候補をレコードへ反映する（反映の本体）。
//
// 対話セッションの状態遷移を含まないため、ステークホルダーの回答取込からも
// 同一処理を通せる（反映の実装を二重に持たない）。
//
// baselines は候補生成時に採取した基準版。既存レコードの書き換えは
// この基準版を要求する Guarded API だけを通し、相違があれば書き込みを中断して競合を返す
// （後勝ち上書きの経路を持たない）。
func (e *Engine) applyApproval(req ApprovalRequest) (*ApprovalResult, error) {
	return e.applyApprovalWithBaselines(req, nil)
}

// applyApprovalWithBaselines は基準版つきで反映する（競合検知あり）。
func (e *Engine) applyApprovalWithBaselines(req ApprovalRequest,
	baselines map[string]projectstore.RecordBaseline) (*ApprovalResult, error) {

	// 書き込みの前に全候補を検証する。途中で失敗すると失敗までの分だけが記録され、
	// 候補が残ったまま押し直した利用者が同じ決定事項を重複して記録してしまう。
	// 対話・回答取込・資料取込の反映はすべてここを通る。
	if err := ValidateApproval(req); err != nil {
		return nil, err
	}

	// 競合を確認したうえでの承認は、確認した現在の内容を基準版に差し替える。
	baselines = withMergeResolutions(baselines, req.Merges)

	result := &ApprovalResult{}
	if err := e.applyDecisions(req, result, baselines); err != nil {
		return nil, err
	}
	if err := e.applyOpenIssues(req, result, baselines); err != nil {
		return nil, err
	}
	if err := e.applyRequirements(req, result, baselines); err != nil {
		return nil, err
	}
	if err := e.applyTerms(req, result, baselines); err != nil {
		return nil, err
	}
	if err := e.collectUnblocked(result); err != nil {
		return nil, err
	}
	// マージの実行を記録する（競合相手の変更参照つき）。
	for _, m := range req.Merges {
		e.record(auditlog.ChangeRecord{Target: m.ID, Change: auditlog.ChangeMergeApplied,
			Before: m.BaselineHash, Evidence: m.Reference})
	}
	return result, nil
}

// ValidateApproval は書き込みの前に、承認された候補がすべて記録できるかを確かめる（途中まで記録して失敗すると、押し直しで同じ記録が重複するため）。
//
// 記録層（projectstore の Validate）と同じ条件を、**利用者が画面で直せる言葉**で先に判定する。
// 1 件でも記録できなければ何も書かない。文言は「原因＋次の行動」とし、
// どの候補かを本文の冒頭で示す（要件 ID・まだ採番されていない記録 ID を出さない）。
func ValidateApproval(req ApprovalRequest) error {
	var problems []string
	add := func(format string, args ...any) { problems = append(problems, fmt.Sprintf(format, args...)) }
	for _, a := range req.Decisions {
		c := a.Candidate
		if strings.TrimSpace(c.Body) == "" {
			add("本文が空の決定事項候補があります。本文を書いてから反映してください。")
		}
		if len(c.EvidenceRefs) == 0 {
			add("決定事項候補「%s」は、根拠にした発言を特定できないため記録できません。"+
				"この候補は破棄してください（必要なら回答に書き足して送り直すと、改めて候補になります）。",
				candidateLabel(c.Body))
		}
	}
	for _, c := range req.OpenIssues {
		label := candidateLabel(c.Topic)
		if strings.TrimSpace(c.Topic) == "" {
			add("論点が空の未決事項候補があります。この候補は破棄してください。")
		}
		if strings.TrimSpace(c.Owner) == "" {
			add("未決事項候補「%s」の「決める人」が空です。決める人を入れてから反映してください。", label)
		}
		if c.Due != "" {
			if _, err := time.Parse("2006-01-02", c.Due); err != nil {
				add("未決事項候補「%s」の期限は 2026-09-30 のように年-月-日で入れてください。", label)
			}
		}
		if len(c.EvidenceRefs) == 0 {
			add("未決事項候補「%s」は、根拠にした発言を特定できないため記録できません。"+
				"この候補は破棄してください（必要なら回答に書き足して送り直すと、改めて候補になります）。", label)
		}
	}
	for _, a := range req.RequirementUpdates {
		c := a.Candidate
		label := candidateLabel(c.Title)
		if strings.TrimSpace(c.Title) == "" {
			add("要件名が空の要件項目の反映案があります。この反映案は破棄してください。")
		}
		if strings.TrimSpace(c.BodyAfter) == "" {
			add("要件項目の反映案「%s」の本文が空です。本文を書いてから反映してください。", label)
		}
		// ID グループ・種別は利用者の入力ではなくアプリが決めるため、ここでは検証しない
		// （解決は applyRequirements / ResolveRequirementIDs）。
	}
	if len(problems) == 0 {
		return nil
	}
	return errors.New(strings.Join(problems, ""))
}

// candidateLabel は候補を文中で指すための短い表記（本文の 1 行目・24 文字まで）。
func candidateLabel(text string) string {
	line := []rune(strings.TrimSpace(firstLine(text)))
	if len(line) > 24 {
		return string(line[:24]) + "…"
	}
	return string(line)
}

// applyDecisions は決定事項を記録し、指定された未決事項を決着させる。
func (e *Engine) applyDecisions(req ApprovalRequest, result *ApprovalResult,
	baselines map[string]projectstore.RecordBaseline) error {
	for _, approval := range req.Decisions {
		c := approval.Candidate
		body := c.Body
		if strings.TrimSpace(c.Rationale) != "" {
			body += "\n\n根拠: " + c.Rationale
		}
		d, err := e.cfg.Store.CreateDecision(projectstore.Decision{
			TopicKey:   c.TopicKey,
			Body:       body,
			Evidence:   c.EvidenceRefs,
			Supersedes: c.SupersedesDecisionID,
		})
		if err != nil {
			return err
		}
		result.DecisionIDs = append(result.DecisionIDs, d.ID)
		e.record(auditlog.ChangeRecord{Target: d.ID, Change: auditlog.ChangeCreated,
			After: firstLine(d.Body), Evidence: strings.Join(d.Evidence, " ")})
		if c.SupersedesDecisionID != "" {
			e.record(auditlog.ChangeRecord{Target: c.SupersedesDecisionID,
				Change: auditlog.ChangeUpdated, After: "superseded_by: " + d.ID})
		}

		for _, issueID := range approval.ResolvesIssueIDs {
			base, err := e.baselineOf(baselines, issueID)
			if err != nil {
				return err
			}
			if _, err := e.cfg.Store.ResolveOpenIssueGuarded(base, issueID, d.ID); err != nil {
				return err
			}
			result.ResolvedIssueIDs = append(result.ResolvedIssueIDs, issueID)
			e.record(auditlog.ChangeRecord{Target: issueID, Change: auditlog.ChangeStatusChanged,
				Before: projectstore.OpenIssueOpen, After: projectstore.OpenIssueResolved, Evidence: d.ID})
		}
	}
	return nil
}

// applyOpenIssues は未決事項を起票する。
func (e *Engine) applyOpenIssues(req ApprovalRequest, result *ApprovalResult,
	baselines map[string]projectstore.RecordBaseline) error {
	for _, c := range req.OpenIssues {
		issue, err := e.cfg.Store.CreateOpenIssue(projectstore.OpenIssue{
			Owner:            c.Owner,
			Due:              c.Due,
			Status:           projectstore.OpenIssueOpen,
			NeedsStakeholder: c.NeedsStakeholder,
			Evidence:         c.EvidenceRefs,
			Body:             c.Topic,
		})
		if err != nil {
			return err
		}
		result.OpenIssueIDs = append(result.OpenIssueIDs, issue.ID)
		e.record(auditlog.ChangeRecord{Target: issue.ID, Change: auditlog.ChangeCreated,
			After: firstLine(issue.Body), Evidence: strings.Join(issue.Evidence, " ")})

		// ブロック対象は要件項目側の blocked_by が正。
		for _, reqID := range c.BlocksRequirementIDs {
			base, err := e.baselineOf(baselines, reqID)
			if err != nil {
				return err
			}
			if _, err := e.cfg.Store.UpdateRequirementGuarded(base, reqID,
				func(r *projectstore.Requirement) error {
					if !contains(r.BlockedBy, issue.ID) {
						r.BlockedBy = append(r.BlockedBy, issue.ID)
					}
					return nil
				}); err != nil {
				return err
			}
			e.record(auditlog.ChangeRecord{Target: reqID, Change: auditlog.ChangeUpdated,
				After: "blocked_by: " + issue.ID})
		}
	}
	return nil
}

// applyRequirements は要件項目を作成・更新する。
//
// 同じ承認操作で記録した決定事項を追跡連鎖として紐づける（要件の根拠をたどれるように）。
func (e *Engine) applyRequirements(req ApprovalRequest, result *ApprovalResult,
	baselines map[string]projectstore.RecordBaseline) error {
	for _, approval := range req.RequirementUpdates {
		c := approval.Candidate
		switch c.Operation {
		case OperationCreate:
			// 種別・ID グループは利用者に入力させない。
			// 承認操作で明示された値 > 候補に解決済みの値 > 章観点の既定、の順に採る。
			kind := approval.Kind
			if kind == "" {
				kind = c.Kind
			}
			if kind == "" {
				kind = ChapterRequirementKind(c.Chapter)
			}
			priority := approval.Priority
			if priority == "" {
				priority = projectstore.PriorityMust
			}
			group := NormalizeRequirementGroup(approval.Group)
			if group == "" {
				group = NormalizeRequirementGroup(c.IDGroup)
			}
			if group == "" {
				group = ChapterIDGroup(c.Chapter)
			}
			created, err := e.cfg.Store.CreateRequirement(group, projectstore.Requirement{
				Title:              c.Title,
				Chapter:            c.Chapter,
				Kind:               kind,
				Priority:           priority,
				Status:             projectstore.RequirementDraft,
				Body:               c.BodyAfter,
				AcceptanceCriteria: c.AcceptanceCriteria,
				Evidence:           c.EvidenceRefs,
				Decisions:          result.DecisionIDs,
			})
			if err != nil {
				return err
			}
			result.RequirementIDs = append(result.RequirementIDs, created.ID)
			e.record(auditlog.ChangeRecord{Target: created.ID, Change: auditlog.ChangeCreated,
				After: created.Title, Evidence: strings.Join(created.Evidence, " ")})
		case OperationUpdate:
			base, err := e.baselineOf(baselines, c.TargetID)
			if err != nil {
				return err
			}
			updated, err := e.cfg.Store.UpdateRequirementGuarded(base, c.TargetID, func(r *projectstore.Requirement) error {
				r.Title = c.Title
				r.Chapter = c.Chapter
				r.Body = c.BodyAfter
				if len(c.AcceptanceCriteria) > 0 {
					r.AcceptanceCriteria = c.AcceptanceCriteria
				}
				for _, ref := range c.EvidenceRefs {
					if !contains(r.Evidence, ref) {
						r.Evidence = append(r.Evidence, ref)
					}
				}
				for _, id := range result.DecisionIDs {
					if !contains(r.Decisions, id) {
						r.Decisions = append(r.Decisions, id)
					}
				}
				return nil
			})
			if err != nil {
				return err
			}
			result.RequirementIDs = append(result.RequirementIDs, updated.ID)
			e.record(auditlog.ChangeRecord{Target: updated.ID, Change: auditlog.ChangeUpdated,
				After: updated.Title, Evidence: strings.Join(updated.Evidence, " ")})
		default:
			return fmt.Errorf("要件項目の反映操作が不正です: %q", c.Operation)
		}
	}
	return nil
}

// applyTerms は用語集へ追加・更新する。
func (e *Engine) applyTerms(req ApprovalRequest, result *ApprovalResult,
	baselines map[string]projectstore.RecordBaseline) error {
	for _, c := range req.TermCandidates {
		base, err := e.baselineOf(baselines, projectstore.TermIndexID(c.Term))
		if err != nil {
			return err
		}
		if _, err := e.cfg.Store.UpsertTermGuarded(base, projectstore.Term{
			Name: c.Term, NameEn: c.English, Definition: c.Definition,
		}); err != nil {
			return err
		}
		result.TermNames = append(result.TermNames, c.Term)
		e.record(auditlog.ChangeRecord{Target: c.Term, Change: auditlog.ChangeUpdated, After: c.Definition})
	}
	return nil
}

// collectUnblocked は決着によってブロックが外れた要件項目を集める。
func (e *Engine) collectUnblocked(result *ApprovalResult) error {
	if len(result.ResolvedIssueIDs) == 0 {
		return nil
	}
	reqs, err := e.cfg.Store.ListRequirements()
	if err != nil {
		return err
	}
	for _, r := range reqs {
		for _, issueID := range r.BlockedBy {
			if contains(result.ResolvedIssueIDs, issueID) && !contains(result.UnblockedRequirementIDs, r.ID) {
				result.UnblockedRequirementIDs = append(result.UnblockedRequirementIDs, r.ID)
			}
		}
	}
	return nil
}

// record は変更履歴を追記する。記録先が未設定なら何もしない。
//
// 記録の失敗で反映そのものを止めない（レコードは既に書けているため。失敗は上位の通知先へ回す）。
func (e *Engine) record(rec auditlog.ChangeRecord) {
	if e.cfg.Logger == nil {
		return
	}
	if err := e.cfg.Logger.RecordChange(rec); err != nil && e.cfg.OnRecordError != nil {
		e.cfg.OnRecordError(err)
	}
}

// baselineOf は基準版を返す（候補生成時に採取したものがあればそれを、無ければ反映開始時点を採る）。
//
// 候補生成時の基準版が渡っている場合、担当者がレビューしている間に他メンバーが入れた変更を
// 競合として検知できる。渡っていない場合（画面からの単発編集など）は
// 「読み出し → 書き込み」の間に入った変更だけを検知する（それでも後勝ち上書きは起きない）。
// withMergeResolutions は承認済みマージの基準版を重ねた写しを返す（元の写像は変更しない）。
func withMergeResolutions(baselines map[string]projectstore.RecordBaseline,
	merges []MergeResolution) map[string]projectstore.RecordBaseline {

	if len(merges) == 0 {
		return baselines
	}
	out := make(map[string]projectstore.RecordBaseline, len(baselines)+len(merges))
	for id, b := range baselines {
		out[id] = b
	}
	for _, m := range merges {
		out[m.ID] = projectstore.RecordBaseline{ID: m.ID, Hash: m.BaselineHash}
	}
	return out
}

func (e *Engine) baselineOf(baselines map[string]projectstore.RecordBaseline,
	id string) (projectstore.RecordBaseline, error) {

	if base, ok := baselines[id]; ok {
		return base, nil
	}
	return e.cfg.Store.CurrentBaseline(id)
}

func contains(list []string, v string) bool {
	for _, s := range list {
		if s == v {
			return true
		}
	}
	return false
}

// ---- 基準版の採取 -----------------------

// captureBaselines は候補が書き換える共有レコードの基準版を採取する。
//
// 対象は「既存レコードを書き換える候補」だけ（新規作成は競合しない）。
// 採取した基準版は候補と一緒に保全し（対話は併置メタデータ・取り込みは analysis.meta.yaml）、
// 承認・反映の直前に再検証する。担当者がレビューしている間に他メンバーが入れた変更を
// 競合として検知できるのは、この時点の基準版を持っているからである。
func (e *Engine) captureBaselines(ex *Extraction) (map[string]projectstore.RecordBaseline, error) {
	if ex == nil {
		return nil, nil
	}
	out := map[string]projectstore.RecordBaseline{}
	add := func(id string) error {
		if id == "" {
			return nil
		}
		if _, done := out[id]; done {
			return nil
		}
		base, err := e.cfg.Store.CurrentBaseline(id)
		if err != nil {
			return err
		}
		out[id] = base
		return nil
	}

	for _, c := range ex.RequirementUpdates {
		if c.Operation == OperationUpdate {
			if err := add(c.TargetID); err != nil {
				return nil, err
			}
		}
		// 未決事項がブロックする要件項目（blocked_by の追記）も書き換え対象。
		for _, id := range c.RelatedIDs {
			_ = id // 紐づけ候補は書き換えないため基準版は採らない
		}
	}
	for _, c := range ex.OpenIssues {
		for _, reqID := range c.BlocksRequirementIDs {
			if err := add(reqID); err != nil {
				return nil, err
			}
		}
	}
	for _, c := range ex.TermCandidates {
		if err := add(projectstore.TermIndexID(c.Term)); err != nil {
			return nil, err
		}
	}
	if len(out) == 0 {
		return nil, nil
	}
	return out, nil
}
