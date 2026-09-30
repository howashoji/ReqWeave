package binding

// 本ファイルは工程ガイドの公開バインディング。
//
// 「いまどの工程にいて、次に何をするか」の**判断は internal/guide に置き**、
// ここは判断に要る事実をプロジェクトから集める役に徹する（バインディングに判断のロジックを持たせない）。
// 画面の一時状態（選択中の資料・同意チェックなど）は集めない（画面側の責務）。

import (
	"strings"

	"github.com/howashoji/ReqWeave/app/internal/dialogue"
	"github.com/howashoji/ReqWeave/app/internal/guide"
	"github.com/howashoji/ReqWeave/app/internal/importer"
	"github.com/howashoji/ReqWeave/app/internal/projectstore"
)

// GuideView は工程ガイドの表示内容。工程の並びも一緒に返す（画面側で定義を持たない）。
type GuideView struct {
	guide.Guide
	// Stages は工程の並び（現在地の前後を利用者が見渡せるようにする）。
	Stages []guide.Stage `json:"stages"`
}

// WorkflowGuide は「いまの工程」と「次にやること」を返す。
//
// 画面を開くたび・状態が変わるたびに呼ばれる読み出し専用の API であり、何も書き換えない。
func (a *API) WorkflowGuide() (GuideView, error) {
	s, err := a.current()
	if err != nil {
		return GuideView{}, err
	}
	state, err := a.guideState(s)
	if err != nil {
		return GuideView{}, err
	}
	return GuideView{Guide: guide.Derive(state), Stages: guide.Stages}, nil
}

// guideState は導出に要る事実を集める。
func (a *API) guideState(s *dialogueSession) (guide.State, error) {
	var out guide.State

	records, err := s.engine.Records()
	if err != nil {
		return out, err
	}
	out.Requirements = len(records.Requirements)
	for _, r := range records.Requirements {
		if r.Status != projectstore.RequirementAgreed {
			out.DraftRequirements++
		}
	}
	for _, i := range records.OpenIssues {
		if i.Status == projectstore.OpenIssueOpen {
			out.OpenIssues++
		}
	}

	chapters, err := dialogue.Completeness(s.store.Project().Phase, records)
	if err != nil {
		return out, err
	}
	out.Completeness = averagePercent(chapters)
	confirmation := dialogue.Confirmable(records)
	out.Confirmable = confirmation.Confirmable
	out.BlockingIssues = len(confirmation.BlockingIssues)

	if err := a.fillImportState(s, &out); err != nil {
		return out, err
	}
	if err := a.fillQuestionnaireState(s, &out); err != nil {
		return out, err
	}
	if err := a.fillDocumentState(s, &out); err != nil {
		return out, err
	}
	return out, nil
}

// averagePercent は章観点の充足率の平均（画面に出す総合の充足率と同じ出し方）。
func averagePercent(chapters []dialogue.ChapterCompleteness) int {
	if len(chapters) == 0 {
		return 0
	}
	sum := 0
	for _, c := range chapters {
		sum += c.Percent
	}
	return sum / len(chapters)
}

// fillImportState は「まだ分析していない資料」の件数を数える。
//
// 分析済みかどうかを表す項目は取り込みメタに無い。そこで
// **その資料を根拠に持つ承認済みレコードがあるか**で判定する（要件から根拠の資料へたどる向きの逆）。
// 承認済みレコードが 1 件も無い資料は「まだ分析していない（または分析したが何も採らなかった）」
// として扱う。後者は次の一手を出しすぎる側の誤りであり、作業を止めない。
// テキストを取り出せなかった資料は分析できないため数えない（原本は保持するが、分析の対象にはならない）。
func (a *API) fillImportState(s *dialogueSession, out *guide.State) error {
	im := importer.New(s.store)
	list, err := im.List()
	if err != nil {
		return err
	}
	cited, err := s.store.CitingRecords("IMP-")
	if err != nil {
		return err
	}
	used := make(map[string]bool, len(cited))
	for _, c := range cited {
		used[importIDOf(c.Ref)] = true
	}
	for _, meta := range list {
		if meta.ExtractionStatus != importer.StatusExtracted || used[meta.ID] {
			continue
		}
		// 承認待ちの候補が残っている資料は「承認する」側の話であり、分析済みとして扱う。
		if analysis, err := im.LoadAnalysis(meta.ID); err == nil && analysis != nil {
			continue
		}
		out.UnanalyzedImports++
	}
	return nil
}

// importIDOf は根拠参照（`IMP-004#L11-L11`）から資料 ID を取り出す。
func importIDOf(ref string) string {
	if i := strings.IndexByte(ref, '#'); i >= 0 {
		return ref[:i]
	}
	return ref
}

// fillQuestionnaireState は質問票の待ち状況を数える（質問票の状態遷移に沿って数える）。
func (a *API) fillQuestionnaireState(s *dialogueSession, out *guide.State) error {
	list, err := s.store.ListQuestionnaires()
	if err != nil {
		return err
	}
	for _, q := range list {
		switch q.Status {
		case projectstore.QuestionnaireIssued:
			out.IssuedQuestionnaires++
		case projectstore.QuestionnaireAnswered:
			out.AnsweredQuestionnaires++
		}
	}
	return nil
}

// fillDocumentState は要件定義書のドラフト・確定版の有無を見る。
func (a *API) fillDocumentState(s *dialogueSession, out *guide.State) error {
	kind := s.store.Project().Phase
	draft, err := s.store.LoadDraft(kind)
	if err != nil {
		return err
	}
	out.HasDraftDocument = len(draft) > 0
	versions, err := s.store.Versions(kind)
	if err != nil {
		return err
	}
	out.ConfirmedDocument = len(versions) > 0
	return nil
}
