package binding

// 本ファイルは競合マージのバインディング。
//
// 承認・反映の経路（対話 / 回答取込 / 資料の取り込み）は、
// 競合を**エラーではなく結果として**返す。画面は同じパネルで三面を表示し、本人の承認
// （MergeResolution）を添えて同じ API を再実行する。
//
// 競合したときは何も書き込まれていない（後勝ち上書きで他メンバーの変更を消さない）。

import (
	"errors"

	"github.com/howashoji/ReqWeave/app/internal/dialogue"
	"github.com/howashoji/ReqWeave/app/internal/projectstore"
)

// ConflictView は競合 1 件の表示（三面のうち基準版と現在の内容）。
type ConflictView struct {
	// ID は対象レコード（FR-* / ISS-nnn / term:<用語名>）。
	ID string `json:"id"`
	// Label は画面表示用の対象名（内部のキー表記をそのまま出さない）。
	Label string `json:"label"`
	// BaselineBody は候補を作った時点の内容（三面の左）。
	BaselineBody string `json:"baselineBody"`
	// CurrentBody は現在の内容（他メンバーの変更。三面の中央）。
	CurrentBody string `json:"currentBody"`
	// CurrentHash はマージを承認するときに添える基準版（MergeResolution.baselineHash）。
	CurrentHash string `json:"currentHash"`
}

// ApprovalOutcome は承認・反映の結果（競合したときは Conflicts が入る）。
type ApprovalOutcome struct {
	// Applied は反映できた場合の結果（競合時は nil）。
	Applied *ApprovalApplied `json:"applied,omitempty"`
	// Conflicts は競合したレコード（三面の材料）。
	Conflicts []ConflictView `json:"conflicts,omitempty"`
	// Notice は競合時の案内（原因＋次の行動）。
	Notice string `json:"notice,omitempty"`
}

// ApprovalApplied は反映できた内容（画面の完了表示に使う）。
type ApprovalApplied struct {
	DecisionIDs             []string `json:"decisionIds,omitempty"`
	OpenIssueIDs            []string `json:"openIssueIds,omitempty"`
	RequirementIDs          []string `json:"requirementIds,omitempty"`
	TermNames               []string `json:"termNames,omitempty"`
	PerspectiveIDs          []string `json:"perspectiveIds,omitempty"`
	ResolvedIssueIDs        []string `json:"resolvedIssueIds,omitempty"`
	UnblockedRequirementIDs []string `json:"unblockedRequirementIds,omitempty"`
	RevertedRequirementIDs  []string `json:"revertedRequirementIds,omitempty"`
	// NotedIssueIDs / OwnerUpdatedIssueIDs は回答取込での経過追記・決める人の更新。
	NotedIssueIDs        []string `json:"notedIssueIds,omitempty"`
	OwnerUpdatedIssueIDs []string `json:"ownerUpdatedIssueIds,omitempty"`
	// QuestionnaireStatus は反映後の質問票の状態（回答取込のみ）。
	QuestionnaireStatus string `json:"questionnaireStatus,omitempty"`
	// State は反映後の対話状態（対話経路のみ）。
	State string `json:"state,omitempty"`
}

// conflictOutcome は競合エラーを結果へ変換する（競合でなければ ok = false）。
func conflictOutcome(err error) (ApprovalOutcome, bool) {
	var conflict *projectstore.ErrRecordConflict
	if !errors.As(err, &conflict) {
		return ApprovalOutcome{}, false
	}
	out := ApprovalOutcome{Notice: err.Error()}
	for _, c := range conflict.Conflicts {
		out.Conflicts = append(out.Conflicts, ConflictView{
			ID: c.ID, Label: conflictLabel(c.ID),
			BaselineBody: c.BaselineBody, CurrentBody: c.CurrentBody, CurrentHash: c.CurrentHash,
		})
	}
	return out, true
}

// conflictLabel は対象の表示名（用語は内部キーのまま出さない）。
func conflictLabel(id string) string {
	if name, ok := trimTermPrefix(id); ok {
		return "用語「" + name + "」"
	}
	return id
}

// trimTermPrefix は用語キー（term:<名前>）から用語名を取り出す。
func trimTermPrefix(id string) (string, bool) {
	if len(id) > len(projectstore.TermIndexPrefix) &&
		id[:len(projectstore.TermIndexPrefix)] == projectstore.TermIndexPrefix {
		return id[len(projectstore.TermIndexPrefix):], true
	}
	return "", false
}

// appliedOf は反映結果を画面表示用へ写す（対話・回答取込・取り込みで同じ形にする）。
func appliedOf(result *dialogue.ApprovalResult, feedback *dialogue.FeedbackApplyResult) *ApprovalApplied {
	return appliedFrom(result, feedback, nil)
}

// appliedFrom は回答取込の結果（質問票の状態・経過追記）も含めて写す。
func appliedFrom(result *dialogue.ApprovalResult, feedback *dialogue.FeedbackApplyResult,
	imported *dialogue.ImportApplyResult) *ApprovalApplied {
	out := &ApprovalApplied{}
	if result != nil {
		out.DecisionIDs = result.DecisionIDs
		out.OpenIssueIDs = result.OpenIssueIDs
		out.RequirementIDs = result.RequirementIDs
		out.TermNames = result.TermNames
		out.ResolvedIssueIDs = result.ResolvedIssueIDs
		out.UnblockedRequirementIDs = result.UnblockedRequirementIDs
		out.State = result.State
	}
	if feedback != nil {
		out.RevertedRequirementIDs = feedback.RevertedRequirementIDs
		if feedback.MaterialApplyResult != nil {
			out.PerspectiveIDs = feedback.PerspectiveIDs
		}
	}
	if imported != nil {
		out.QuestionnaireStatus = imported.QuestionnaireStatus
		out.NotedIssueIDs = imported.NotedIssueIDs
		out.OwnerUpdatedIssueIDs = imported.OwnerUpdatedIssueIDs
	}
	return out
}
