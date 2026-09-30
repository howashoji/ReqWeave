package exchange

// 回答モードの AI 対話回答で AI へ送る文脈の組み立て（送信範囲）。
//
// **ホワイトリスト方式**（発行用ファイルの内容物と同じ）。下の「含める」に挙げたものだけを組み立て、
// それ以外は触らない。担当者モードの文脈注入とは別の規定であり、そちらを流用しない。
//
// 含める: 全質問の本文・背景説明・回答形式・選択肢 / 用語の定義の全件 / 本人が入力済みの全回答。
// 含めない: 宛先の氏名と所属・名簿 ID / 質問票 ID・質問 ID・発行元未決事項 ID・プロジェクト ID /
// content_hash・return_key・salt・KDF パラメータ / パスコードと導出鍵 / シークレットキーと認証情報 /
// ファイルパス・ファイル名・端末情報 / 担当者側のプロジェクトデータ。
//
// **質問は内部識別子ではなく本文中の並び（「1 つめの質問」等）で指す**（内部 ID を AI へ送らない）。
// 組み立てはここ 1 か所に閉じる（送信範囲の検査が 1 か所で済むようにするため）。

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/howashoji/ReqWeave/app/internal/projectstore"
)

// answerFormatLabels は回答形式の表示文言（内部のコード値を送信本文へ出さない）。
//
// 網羅は TestAnswerFormatLabelsCoverAllFormats が固定する（形式を増やしてここへ足し忘れると赤くなる）。
// 未知の値はコード値を出さずに「形式不明」へ倒す（生の値を外へ出さない）。
var answerFormatLabels = map[string]string{
	projectstore.AnswerFormatChoice:         "選択肢から 1 つ選ぶ",
	projectstore.AnswerFormatMultiChoice:    "選択肢から複数選ぶ",
	projectstore.AnswerFormatFree:           "自由記述",
	projectstore.AnswerFormatChoiceWithFree: "選択肢から選び、必要なら自由記述で補う",
}

// answerFormatLabel は回答形式の表示文言を返す。未知の値はコード値を出さずに倒す。
func answerFormatLabel(format string) string {
	if label, ok := answerFormatLabels[format]; ok {
		return label
	}
	return "形式不明"
}

// ordinalQuestionLabel は質問の指し示し方（内部 ID を使わない）。
func ordinalQuestionLabel(index int) string {
	return fmt.Sprintf("%d つめの質問", index+1)
}

// AIContext は AI 対話回答の毎回の呼び出しに載せる文脈を組み立てる。
//
// 発話履歴（同表の 4）は含めない。呼び出し側が対話の発話として積む。
func (s *RespondSession) AIContext() string {
	var b strings.Builder

	b.WriteString("# 回答中の質問票\n\n")
	fmt.Fprintf(&b, "全 %d 問です。質問は番号で指してください（内部の識別子はありません）。\n\n",
		len(s.Questionnaire.Questions))

	answers := map[string]projectstore.Answer{}
	if s.Answers != nil {
		for _, a := range s.Answers.Answers {
			answers[a.QuestionID] = a
		}
	}

	for i, q := range s.Questionnaire.Questions {
		fmt.Fprintf(&b, "## %s\n\n", ordinalQuestionLabel(i))
		fmt.Fprintf(&b, "- 質問: %s\n", strings.TrimSpace(q.Text))
		if bg := strings.TrimSpace(q.Background); bg != "" {
			fmt.Fprintf(&b, "- 背景説明: %s\n", bg)
		}
		fmt.Fprintf(&b, "- 回答形式: %s\n", answerFormatLabel(q.AnswerFormat))
		if len(q.Choices) > 0 {
			b.WriteString("- 選択肢:\n")
			for _, c := range q.Choices {
				fmt.Fprintf(&b, "  - %s\n", c)
			}
		}
		fmt.Fprintf(&b, "- あなたの現在の回答: %s\n\n", answerSummary(answers[q.ID]))
	}

	if len(s.Terms) > 0 {
		b.WriteString("# 用語の定義\n\n")
		for _, t := range s.Terms {
			if t.NameEn != "" {
				fmt.Fprintf(&b, "- %s（%s）: %s\n", t.Name, t.NameEn, strings.TrimSpace(t.Definition))
				continue
			}
			fmt.Fprintf(&b, "- %s: %s\n", t.Name, strings.TrimSpace(t.Definition))
		}
	}
	return strings.TrimRight(b.String(), "\n") + "\n"
}

// answerSummary は 1 件の回答の表し方（未入力・「不明」・入力済みを区別する）。
func answerSummary(a projectstore.Answer) string {
	if a.QuestionID == "" {
		return "未入力"
	}
	if a.Kind == projectstore.AnswerKindUnknown {
		if reason := strings.TrimSpace(a.Body); reason != "" {
			return "「不明」（理由・確認先: " + reason + "）"
		}
		return "「不明」"
	}
	var parts []string
	if len(a.Selected) > 0 {
		parts = append(parts, "選んだもの: "+strings.Join(a.Selected, " / "))
	}
	if free := strings.TrimSpace(a.FreeText); free != "" {
		parts = append(parts, "記述: "+free)
	}
	if body := strings.TrimSpace(a.Body); body != "" {
		parts = append(parts, "補足: "+body)
	}
	if len(parts) == 0 {
		return "未入力"
	}
	return strings.Join(parts, " / ")
}

// ---------------------------------------------------------------------------
// 対話ログ（確定前は回答作業領域へ暗号化して置き、確定時に返送へ載せる）
// ---------------------------------------------------------------------------

// fileRespondSessions は確定前の対話ログの置き場（暗号化）。
//
// 平文で置くと、回答作業領域を暗号化している意味が無くなる（回答は作業中も保護する）。
const fileRespondSessions = "sessions.enc"

// respondSessionPhase は返送に載せるステークホルダーセッションのフェーズ。
//
// 回答モードにはプロジェクトのフェーズ概念が無い（初期設定もメンバーも持たない）。
// セッションの形式はフェーズを必須とするため、回答であることを表す固定値を入れる。
const respondSessionPhase = "stakeholder-answer"

// respondSessionID は返送に載せるときの仮の ID（取り込み側が再採番する）。
const respondSessionID = "S-0001"

// AppendUtterance は AI 対話回答の発話を 1 件足して保存する。
//
// 保存は暗号化した sessions.enc へ**毎回全体を書き直す**（発話は多くないため追記の複雑さを持ち込まない）。
// 中断して開き直したときは restore が復元する。
func (s *RespondSession) AppendUtterance(speaker, body string) error {
	u := projectstore.Utterance{
		ID:      projectstore.NextUtteranceID(len(s.Utterances)),
		Speaker: speaker,
		At:      time.Now().UTC().Truncate(time.Second),
		Status:  projectstore.UtteranceCompleted,
		Body:    body,
	}
	if err := u.Validate(); err != nil {
		return err
	}
	s.Utterances = append(s.Utterances, u)
	return s.saveUtterances()
}

// saveUtterances は対話ログを暗号化して回答作業領域へ書く。
func (s *RespondSession) saveUtterances() error {
	if len(s.Utterances) == 0 {
		return nil // 対話を行わなければファイルを作らない
	}
	sealed, err := sealWithKey(s.marshalUtterances(), s.key)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(s.dir, respondDirMode); err != nil {
		return fmt.Errorf("作業データの保存先を作れません: %w", err)
	}
	if err := writeFileAtomicMode(filepath.Join(s.dir, fileRespondSessions), sealed, respondFileMode); err != nil {
		return err
	}
	// 平文メタデータも更新する。**これが無いと、回答を 1 件も入力せずに対話だけした場合に
	// 作業領域が「無い」と判定され、開き直しで対話が復元されない**（実際に起きた不具合）。
	return s.saveMeta()
}

// marshalUtterances は対話ログを対話セッションの形式で組み立てる（返送に載せる形と同じ）。
func (s *RespondSession) marshalUtterances() []byte {
	return projectstore.MarshalSession(projectstore.Session{
		ID:        respondSessionID,
		Type:      projectstore.SessionStakeholder,
		Phase:     respondSessionPhase,
		StartedAt: s.utterancesStartedAt(),
	}, s.Utterances)
}

// utterancesStartedAt は対話の開始日時（最初の発話の日時）を返す。
func (s *RespondSession) utterancesStartedAt() time.Time {
	if len(s.Utterances) == 0 {
		return time.Now().UTC().Truncate(time.Second)
	}
	return s.Utterances[0].At
}

// restoreUtterances は中断再開時に対話ログを復元する。
func (s *RespondSession) restoreUtterances() error {
	sealed, err := os.ReadFile(filepath.Join(s.dir, fileRespondSessions))
	if err != nil {
		if os.IsNotExist(err) {
			return nil // 対話を行っていない
		}
		return fmt.Errorf("対話の記録を読み込めません: %w", err)
	}
	plain, err := openWithKey(sealed, s.key)
	if err != nil {
		return fmt.Errorf("対話の記録を読み取れません（作業データが壊れています）。担当者へ再発行を依頼してください")
	}
	_, utterances, err := projectstore.ParseSession(plain)
	if err != nil {
		return fmt.Errorf("対話の記録を解釈できません: %w", err)
	}
	s.Utterances = utterances
	return nil
}

// ---------------------------------------------------------------------------
// 1 回の呼び出しに載せる指示文
// ---------------------------------------------------------------------------

// aiSystemInstruction は AI 対話回答の指示文。
//
// **利用者データを一切含めない固定文**とする（可変部を持たせると、ここが送信範囲の抜け道になる）。
// 担当者モードのシステムプロンプトとは別物であり、そちらを流用しない
// （回答モードには要件項目・決定事項・プロジェクトの文脈が無い）。
const aiSystemInstruction = `あなたは、担当者から届いた質問票に回答しようとしている方の相談相手です。

- 回答者本人の言葉で、短く具体的に答えてください。
- 質問は「1 つめの質問」のように番号で指してください（内部の識別子はありません）。
- 分からないことを推測で断定せず、「担当者に確認するとよい点」として示してください。
- 回答の下書きを求められたら、そのまま回答欄へ書ける文章だけを示してください。
- 何をどう回答するかを決めるのは回答者本人です。本人の代わりに決めないでください。`

// AISystemPrompt は 1 回の呼び出しに載せるシステムプロンプト（指示文 + 質問票の文脈）。
//
// **呼び出しのたびに組み立て直す**（入力済み回答が変わるため、毎回送る）。
// 送信本文のうち発話履歴以外はすべてここを通る（送信範囲の検査が 1 か所で済む）。
func (s *RespondSession) AISystemPrompt() string {
	return aiSystemInstruction + "\n\n" + s.AIContext()
}

// AppendInterruptedUtterance は受信済みの本文を中断発話として残す（担当者モードの中断と同方針）。
//
// 本文が空のときは何も残さない（中身の無い発話を作らない）。
func (s *RespondSession) AppendInterruptedUtterance(body string) error {
	if strings.TrimSpace(body) == "" {
		return nil
	}
	u := projectstore.Utterance{
		ID:      projectstore.NextUtteranceID(len(s.Utterances)),
		Speaker: projectstore.SpeakerAgent,
		At:      time.Now().UTC().Truncate(time.Second),
		Status:  projectstore.UtteranceInterrupted,
		Body:    body,
	}
	if err := u.Validate(); err != nil {
		return err
	}
	s.Utterances = append(s.Utterances, u)
	return s.saveUtterances()
}
