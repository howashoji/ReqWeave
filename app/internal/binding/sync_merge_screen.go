package binding

// 本ファイルは**取り込み時の三面マージ**のバインディング。
//
// 反映時のレコード競合（merge_screen.go）とは別の経路であり、提示単位（レコード /
// エントリ / 項目 / 章）と 4 択（相手を採る / 自分を採る / 両立させる / 未決事項として起票する）を持つ。
//
// **承認の受け渡し方**（反映時の競合と同じ「競合はエラーではなく結果として返す」形）:
//
//  1. 画面は承認なしで取り込みを実行する（IncorporateSync に空の承認を渡す）。
//  2. 競合があれば同期モジュールが統合せずに中止し（取り込み前の復帰点へ戻る）、
//     本ファイルの syncResolver が受け取った競合を結果として画面へ返す。
//  3. 画面は三面を表示して本人の承認を得て、承認を添えて同じ操作を再実行する。
//
// この形にすることで、**承認を省いて統合する経路がバインディング層に存在しない**
// （既定の選択を置かない）。承認が 1 件でも欠ければ同期モジュール側が中止する。

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/howashoji/ReqWeave/app/internal/auditlog"
	"github.com/howashoji/ReqWeave/app/internal/projectstore"
	syncmod "github.com/howashoji/ReqWeave/app/internal/sync"
)

// SyncConflictView は取り込み時の競合 1 件（三面 1 つ分）。
//
// 内部のパス・ブランチ名は出さない（利用者に git を意識させない）。
type SyncConflictView struct {
	// ID は承認を対応づける識別子（画面には表示しない）。
	ID string `json:"id"`
	// Label は対象名（「用語「在庫」」等）。
	Label string `json:"label"`
	// UnitLabel は提示単位の表示名（レコード / エントリ / 項目 / 章）。
	UnitLabel string `json:"unitLabel"`
	// CategoryLabel は変更の区分の表示名（並び替え・見出し用）。
	CategoryLabel string `json:"categoryLabel"`
	// TheirsAuthor は相手の作業者（表示名が分かる場合は表示名）。
	TheirsAuthor string `json:"theirsAuthor"`
	// Base / Theirs / Ours は三面の内容（空 = その面では対象が存在しない）。
	Base   string `json:"base"`
	Theirs string `json:"theirs"`
	Ours   string `json:"ours"`
}

// SyncChoiceOption は解決の選択肢（4 択。**既定の選択を置かない**）。
type SyncChoiceOption struct {
	Choice string `json:"choice"`
	Label  string `json:"label"`
	// Hint は選択の意味（「両立させる」は統合後の内容の入力が要る、等）。
	Hint string `json:"hint"`
}

// syncChoiceOptions は 4 択の並び（値集合はこの 4 つで閉じている）。
var syncChoiceOptions = []SyncChoiceOption{
	{Choice: string(syncmod.ChoiceTheirs), Label: syncmod.ChoiceLabel[syncmod.ChoiceTheirs],
		Hint: "相手の内容で置き換えます。"},
	{Choice: string(syncmod.ChoiceOurs), Label: syncmod.ChoiceLabel[syncmod.ChoiceOurs],
		Hint: "自分の内容を残します。"},
	{Choice: string(syncmod.ChoiceBoth), Label: syncmod.ChoiceLabel[syncmod.ChoiceBoth],
		Hint: "両方を踏まえた内容を入力してください（空のままでは進めません）。"},
	{Choice: string(syncmod.ChoiceOpenIssue), Label: syncmod.ChoiceLabel[syncmod.ChoiceOpenIssue],
		Hint: "未決事項を起票し、決まるまでどちらの内容を使うかを選びます。"},
}

// SyncResolutionInput は 1 件の競合に対する本人の承認（画面から渡す）。
type SyncResolutionInput struct {
	// ConflictID は対象の競合（SyncConflictView.ID）。
	ConflictID string `json:"conflictId"`
	// Choice は 4 択の値（空・未知の値は承認として扱わない）。
	Choice string `json:"choice"`
	// Merged は「両立させる」のときの統合後の内容。
	Merged string `json:"merged,omitempty"`
	// Adopt は「未決事項として起票する」のときに暫定採用する側（theirs / ours）。
	Adopt string `json:"adopt,omitempty"`
	// OpenIssueID は「未決事項として起票する」で起票した未決事項の ID（CreateSyncMergeOpenIssue の戻り値）。
	OpenIssueID string `json:"openIssueId,omitempty"`
}

// syncResolver は同期モジュールの承認の受け口（sync.ConflictResolver）の実装。
//
// 画面から渡された承認をそのまま返し、提示された競合を控える。**承認を補完しない**
// （欠けているものは同期モジュール側が未承認として扱い、統合せずに中止する）。
type syncResolver struct {
	resolutions map[string]syncmod.Resolution
	collected   []syncmod.Conflict
}

// ResolveConflicts は競合を控えて、画面から受け取っている承認を返す。
func (r *syncResolver) ResolveConflicts(_ context.Context, conflicts []syncmod.Conflict) (map[string]syncmod.Resolution, error) {
	r.collected = append(r.collected, conflicts...)
	return r.resolutions, nil
}

// newSyncResolver は画面から渡された承認を同期モジュールの型へ写す。
//
// 未知の選択・不足した付随情報はそのまま渡す（成立判定は同期モジュールが行う = 判定を二重に持たない）。
func newSyncResolver(inputs []SyncResolutionInput) *syncResolver {
	r := &syncResolver{}
	if len(inputs) == 0 {
		return r
	}
	r.resolutions = make(map[string]syncmod.Resolution, len(inputs))
	for _, in := range inputs {
		id := strings.TrimSpace(in.ConflictID)
		if id == "" {
			continue
		}
		r.resolutions[id] = syncmod.Resolution{
			Choice:      syncmod.Choice(strings.TrimSpace(in.Choice)),
			Merged:      in.Merged,
			Adopt:       syncmod.Choice(strings.TrimSpace(in.Adopt)),
			OpenIssueID: strings.TrimSpace(in.OpenIssueID),
		}
	}
	return r
}

// views は控えた競合を画面表示用へ写す（同一 ID の重複は 1 件にまとめる）。
func (r *syncResolver) views(members *projectstore.Members) []SyncConflictView {
	seen := map[string]bool{}
	out := make([]SyncConflictView, 0, len(r.collected))
	for _, c := range r.collected {
		if seen[c.ID] {
			continue
		}
		seen[c.ID] = true
		out = append(out, SyncConflictView{
			ID:            c.ID,
			Label:         c.Label,
			UnitLabel:     syncUnitLabel(c.Unit),
			CategoryLabel: syncmod.CategoryLabel(c.Category),
			TheirsAuthor:  authorDisplayName(members, c.TheirsAuthor),
			Base:          c.Base,
			Theirs:        c.Theirs,
			Ours:          c.Ours,
		})
	}
	return out
}

// syncUnitLabel は提示単位の表示名（生のコード値を画面へ出さない）。
func syncUnitLabel(unit syncmod.Unit) string {
	if label, ok := syncmod.UnitLabel[unit]; ok {
		return label
	}
	return "対象"
}

// SyncMergeOptions は三面マージの選択肢（画面がラベルを二重に持たないための入口）。
func (a *API) SyncMergeOptions() []SyncChoiceOption { return syncChoiceOptions }

// ---- 「未決事項として起票する」 --------------------------------

// CreateSyncMergeOpenIssueRequest は競合を未決事項として起票する入力。
type CreateSyncMergeOpenIssueRequest struct {
	// ConflictID は対象の競合（承認と対応づけるため画面から返す）。
	ConflictID string `json:"conflictId"`
	// Label は対象名（未決事項の本文の見出しに使う）。
	Label string `json:"label"`
	// TheirsAuthor は相手の作業者名（本文に添える）。
	TheirsAuthor string `json:"theirsAuthor"`
	// Theirs / Ours は両者の内容（**両方の内容を本文に含める**）。
	Theirs string `json:"theirs"`
	Ours   string `json:"ours"`
	// Owner は決める人（未指定なら自分の表示名）。
	Owner string `json:"owner,omitempty"`
	// Due は期限（YYYY-MM-DD。任意）。
	Due string `json:"due,omitempty"`
}

// SyncMergeOpenIssueView は起票した未決事項（承認へ添える ID を返す）。
type SyncMergeOpenIssueView struct {
	ConflictID  string `json:"conflictId"`
	OpenIssueID string `json:"openIssueId"`
	Notice      string `json:"notice"`
}

// CreateSyncMergeOpenIssue は競合を未決事項として起票する。
//
// ID は自分の番号帯から採番し（他のメンバーの採番と衝突させない）、本文に相手・自分の両方の内容を残す。
// 起票しただけでは統合は進まない（返した ID を承認へ添えて取り込みを再実行する）。
func (a *API) CreateSyncMergeOpenIssue(req CreateSyncMergeOpenIssueRequest) (SyncMergeOpenIssueView, error) {
	s, err := a.current()
	if err != nil {
		return SyncMergeOpenIssueView{}, err
	}
	if err := a.requireRole(s.store, projectstore.RoleEditor, "未決事項の起票"); err != nil {
		return SyncMergeOpenIssueView{}, err
	}
	label := strings.TrimSpace(req.Label)
	if label == "" {
		return SyncMergeOpenIssueView{}, fmt.Errorf("起票する対象が分かりません。取り込みをやり直してください。")
	}
	owner := strings.TrimSpace(req.Owner)
	if owner == "" {
		owner = s.store.Author().DisplayName
	}
	issue, err := s.store.CreateOpenIssue(projectstore.OpenIssue{
		Owner:    owner,
		Due:      strings.TrimSpace(req.Due),
		Status:   projectstore.OpenIssueOpen,
		Evidence: []string{"取り込み時の三面マージ: " + label},
		Body:     syncMergeIssueBody(label, req.TheirsAuthor, req.Theirs, req.Ours),
	})
	if err != nil {
		return SyncMergeOpenIssueView{}, err
	}
	a.recordChange(s, auditlog.ChangeRecord{
		At: time.Now().UTC(), Author: s.store.Author().AuthorID,
		Target: issue.ID, Change: auditlog.ChangeCreated,
		After: "取り込み時の三面マージから起票: " + label,
	})
	return SyncMergeOpenIssueView{ConflictID: req.ConflictID, OpenIssueID: issue.ID,
		Notice: fmt.Sprintf("未決事項 %s を起票しました。決まるまでどちらの内容を使うかを選んでください。", issue.ID)}, nil
}

// syncMergeIssueBody は未決事項の論点（両方の内容を残す）。
func syncMergeIssueBody(label, theirsAuthor, theirs, ours string) string {
	var b strings.Builder
	b.WriteString("取り込みで「" + label + "」の内容が分かれました。どちらを採るかを決めてください。\n\n")
	author := strings.TrimSpace(theirsAuthor)
	if author == "" {
		author = "他のメンバー"
	}
	b.WriteString("## " + author + "の内容\n\n")
	b.WriteString(syncMergeIssueSection(theirs))
	b.WriteString("\n## 自分の内容\n\n")
	b.WriteString(syncMergeIssueSection(ours))
	return b.String()
}

// syncMergeIssueSection は片側の内容を本文へ埋める（空は「内容なし（削除）」と明示する）。
func syncMergeIssueSection(content string) string {
	if strings.TrimSpace(content) == "" {
		return "（内容なし。削除されています）\n"
	}
	return "```\n" + strings.TrimRight(content, "\n") + "\n```\n"
}
