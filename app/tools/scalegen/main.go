// scalegen は **規模の上限目安（internal/projectstore の scaleLimit）の全項目が上限値に達した**
// プロジェクトを作る（上限規模での応答時間の実測に使う）。
//
// なぜ要るか: 応答時間の基準は「上限規模のテストデータを投入したプロジェクトで」
// 応答時間を測ることだが、これまで**上限規模のデータを作る手段がリポジトリに無かった**ため
// 実測できていなかった。
//
// 本プログラムは検証専用であり配布物には入らない（`tools/` 配下は wails のビルド対象外）。
//
// 使い方（既定値で上限目安そのもの）:
//
//	scalegen -root /tmp/scale/在庫管理システム.rwv
//	scalegen -root /tmp/scale/在庫管理システム.rwv -app-base /tmp/scale/appdata -total-mb 500
//
// 標準出力は 1 行 1 事実（機械可読）。`scale` 行が**投入後に数え直した実測値**であり、
// 「上限規模になっていない状態で計測した」空振りを検出できるようにしている:
//
//	root <プロジェクトフォルダ>
//	scale <対象> <現在値> <上限> <判定>
//	elapsed <秒>
//
// 補足: `-total-mb` を小さくすると総量だけ上限未満になる（生成時間を縮めたいときの手加減用）。
// **応答時間の実測では既定の 500 から下げない**（下げた値で測ると保証の前提が変わる）。
package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/howashoji/ReqWeave/app/internal/importer"
	"github.com/howashoji/ReqWeave/app/internal/projectstore"
)

func main() {
	var o options
	flag.StringVar(&o.root, "root", "", "生成先のプロジェクトフォルダ（存在しないか空であること）")
	flag.StringVar(&o.appBase, "app-base", "", "アプリ設定領域（指定すると同時に扱うプロジェクトも上限まで用意する）")
	flag.IntVar(&o.sessions, "sessions", 100, "対話セッション数（上限目安 100）")
	flag.IntVar(&o.utterances, "utterances", 1000, "1 セッションの発話数（同 上限 1,000）")
	flag.IntVar(&o.requirements, "requirements", 1000, "要件項目数（同 上限 1,000）")
	flag.IntVar(&o.decisions, "decisions", 500, "決定事項数（同 上限 500）")
	flag.IntVar(&o.openIssues, "open-issues", 500, "未決事項数（同 上限 500）")
	flag.IntVar(&o.questionnaires, "questionnaires", 100, "質問票数（同 上限 100）")
	flag.IntVar(&o.projects, "projects", 50, "同時に扱うプロジェクト数（同 上限 50。-app-base 指定時のみ）")
	flag.IntVar(&o.totalMB, "total-mb", 500, "プロジェクトの総量（MB。同 上限 500）")
	flag.StringVar(&o.authorID, "author", "k.sato@example.co.jp", "作業者の利用者 ID")
	flag.StringVar(&o.displayName, "display-name", "佐藤", "作業者の表示名")
	flag.Parse()

	if err := run(o, os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

type options struct {
	root           string
	appBase        string
	sessions       int
	utterances     int
	requirements   int
	decisions      int
	openIssues     int
	questionnaires int
	projects       int
	totalMB        int
	authorID       string
	displayName    string
}

// 生成する本文の 1 件あたりの目安（実データの見た目に寄せる。総量は imports/ で埋める）。
const (
	// utteranceBodyBytes は 1 発話の本文の長さ。AI の応答は数百〜数千字になるため、
	// 履歴 1,000 件で数 MB になる大きさを採る。
	utteranceBodyBytes = 1200
	// importChunkBytes は既存資料 1 件の原本の大きさ。抽出テキストも同じ大きさで保存されるため、
	// 総量へは 1 件あたり約 2 倍が載る。
	importChunkBytes = 8 * 1024 * 1024
)

func run(o options, out *os.File) error {
	if strings.TrimSpace(o.root) == "" {
		return fmt.Errorf("-root を指定してください")
	}
	started := time.Now()
	author := projectstore.Author{AuthorID: o.authorID, DisplayName: o.displayName}

	store, err := projectstore.CreateProject(o.root, projectstore.CreateOptions{
		TargetSystemName: "在庫管理システム（上限規模の検証用）",
		Summary:          "応答時間を上限規模で実測するための検証用プロジェクト。",
		Author:           author,
	})
	if err != nil {
		return err
	}
	defer store.Close()

	stakeholder, err := store.AddStakeholder("回答者（検証用）", "業務部門")
	if err != nil {
		return err
	}

	if err := fillDecisions(store, o.decisions); err != nil {
		return err
	}
	issueIDs, err := fillOpenIssues(store, o.openIssues)
	if err != nil {
		return err
	}
	if err := fillRequirements(store, o.requirements, issueIDs); err != nil {
		return err
	}
	if err := fillSessions(store, o.sessions, o.utterances); err != nil {
		return err
	}
	if err := fillQuestionnaires(store, o.questionnaires, stakeholder.ID, issueIDs, o.authorID); err != nil {
		return err
	}
	if err := fillImports(store, int64(o.totalMB)*1024*1024); err != nil {
		return err
	}

	usages, err := store.ScaleUsageAll(projectstore.DefaultScaleWarnRatio)
	if err != nil {
		return err
	}
	fmt.Fprintf(out, "root %s\n", o.root)
	for _, u := range usages {
		fmt.Fprintf(out, "scale %s %d %d %s\n", u.Kind, u.Current, u.Limit, u.Level)
	}

	if o.appBase != "" {
		n, err := fillRecentProjects(o, author)
		if err != nil {
			return err
		}
		fmt.Fprintf(out, "scale %s %d %d %s\n", projectstore.ScaleProjects, n,
			projectstore.ScaleLimitOf(projectstore.ScaleProjects),
			projectstore.EvaluateScale(projectstore.ScaleProjects, int64(n), "",
				projectstore.DefaultScaleWarnRatio).Level)
	}
	fmt.Fprintf(out, "elapsed %.1f\n", time.Since(started).Seconds())
	return nil
}

// fillDecisions は決定事項を上限まで記録する（アプリと同じ正規の作成経路を通す）。
func fillDecisions(s *projectstore.Store, n int) error {
	for i := 1; i <= n; i++ {
		_, err := s.CreateDecision(projectstore.Decision{
			TopicKey:  fmt.Sprintf("background/topic-%04d", i),
			DecidedAt: time.Now().UTC().Truncate(time.Second),
			Evidence:  []string{"S-0001#utt-00002"},
			Body: fmt.Sprintf("## 決定内容\n\n論点 %04d について、現行の Excel 台帳の運用を維持したうえで"+
				"在庫の引当だけを本システムへ移す。\n\n## 根拠\n\n担当者の回答（検証用の生成データ）。\n", i),
		})
		if err != nil {
			return fmt.Errorf("決定事項 %d 件目を作れません: %w", i, err)
		}
	}
	return nil
}

// fillOpenIssues は未決事項を上限まで記録し、採番された ID を返す。
func fillOpenIssues(s *projectstore.Store, n int) ([]string, error) {
	ids := make([]string, 0, n)
	for i := 1; i <= n; i++ {
		issue, err := s.CreateOpenIssue(projectstore.OpenIssue{
			Owner:            "情報システム部",
			Due:              "2026-12-31",
			Status:           projectstore.OpenIssueOpen,
			NeedsStakeholder: i%3 == 0,
			Evidence:         []string{"S-0001#utt-00002"},
			Body:             fmt.Sprintf("## 論点\n\n拠点 %04d の棚卸の締め時刻をどう扱うか（検証用の生成データ）。\n", i),
		})
		if err != nil {
			return nil, fmt.Errorf("未決事項 %d 件目を作れません: %w", i, err)
		}
		ids = append(ids, issue.ID)
	}
	return ids, nil
}

// fillRequirements は要件項目を上限まで記録する。
//
// 一覧画面が実データで扱う形に寄せるため、受け入れ条件・根拠・ブロック元を持たせる。
func fillRequirements(s *projectstore.Store, n int, issueIDs []string) error {
	for i := 1; i <= n; i++ {
		kind, group := projectstore.RequirementFunctional, "INV"
		if i%5 == 0 {
			kind, group = projectstore.RequirementNonFunctional, "PF"
		}
		r := projectstore.Requirement{
			Title:    fmt.Sprintf("在庫引当の要件 %04d", i),
			Chapter:  "functional-requirements",
			Kind:     kind,
			Priority: projectstore.PriorityMust,
			Status:   projectstore.RequirementDraft,
			AcceptanceCriteria: []string{
				"引当済み在庫が二重に引当されないこと",
				fmt.Sprintf("拠点 %04d の在庫が 1 秒以内に一覧へ反映されること", i),
			},
			Evidence: []string{"S-0001#utt-00002"},
			Body: fmt.Sprintf("## 要件文\n\n拠点 %04d の在庫引当を本システムで行えること"+
				"（検証用の生成データ）。\n\n## 受け入れ条件\n\n"+
				"- 引当済み在庫が二重に引当されないこと\n- 一覧へ 1 秒以内に反映されること\n", i),
		}
		if len(issueIDs) > 0 && i%4 == 0 {
			r.BlockedBy = []string{issueIDs[i%len(issueIDs)]}
		}
		if _, err := s.CreateRequirement(group, r); err != nil {
			return fmt.Errorf("要件項目 %d 件目を作れません: %w", i, err)
		}
	}
	return nil
}

// fillSessions は対話セッションを上限まで作り、各セッションへ上限件数の発話を入れる。
//
// **発話は 1 件ずつ追記せず、セッションファイルを 1 回の書き込みで組み立てる**。
// 正規の追記 API（AppendUtterance）は 1 件ごとに「ファイル全体の走査 + fsync」を行うため
// （発話ごとに保存して取りこぼさないための代償）、上限規模の生成に使うと 10 万件で数十分かかる。
// そのかわり、**書き終えた直後に正規の読み出し（LoadSession）で数と本文を突き合わせる**ため、
// 書式が設計から外れれば生成時に必ず失敗する（黙って壊れたデータを渡さない）。
func fillSessions(s *projectstore.Store, sessions, utterances int) error {
	for i := 1; i <= sessions; i++ {
		sessionType, phase := projectstore.SessionOwner, "requirements"
		if i%4 == 0 {
			sessionType = projectstore.SessionStakeholder
		}
		if i%3 == 0 {
			phase = "basic-design"
		}
		sess, err := s.CreateSession(sessionType, phase)
		if err != nil {
			return fmt.Errorf("対話セッション %d 件目を作れません: %w", i, err)
		}
		body, wants := sessionFileContent(sess, utterances)
		if err := s.WriteFile(projectstore.SessionFile(sess.ID), body); err != nil {
			return err
		}
		if err := verifySession(s, sess.ID, wants); err != nil {
			return err
		}
	}
	return nil
}

// sessionFileContent はセッションファイルの全文と、入れた発話の期待値を返す。
//
// 書式は対話セッションファイルの形式（`### utt-nnnnn` の見出し + `- speaker/at/status` + 本文）。
// **正本は projectstore 側の書き出し**であり、ここは検証用の複製である。
// 複製が古くなったことは verifySession が検出する。
func sessionFileContent(sess *projectstore.Session, utterances int) ([]byte, []projectstore.Utterance) {
	var b strings.Builder
	b.WriteString("---\n")
	fmt.Fprintf(&b, "id: %s\n", sess.ID)
	fmt.Fprintf(&b, "type: %s\n", sess.Type)
	fmt.Fprintf(&b, "phase: %s\n", sess.Phase)
	fmt.Fprintf(&b, "started_at: %s\n", sess.StartedAt.Format(time.RFC3339))
	if sess.Author != "" {
		fmt.Fprintf(&b, "author: %s\n", sess.Author)
	}
	b.WriteString("---\n")

	at := sess.StartedAt.Truncate(time.Second)
	wants := make([]projectstore.Utterance, 0, utterances)
	for n := 1; n <= utterances; n++ {
		u := projectstore.Utterance{
			ID:      fmt.Sprintf("utt-%05d", n),
			Speaker: projectstore.SpeakerAgent,
			At:      at.Add(time.Duration(n) * time.Minute),
			Status:  projectstore.UtteranceCompleted,
			Body:    utteranceBody(sess.ID, n),
		}
		if n%2 == 0 {
			u.Speaker = projectstore.SpeakerUser
		}
		// 中断発話も混ぜる（履歴表示のラベル分岐を実データで通す）。
		if n%97 == 0 {
			u.Status = projectstore.UtteranceInterrupted
		}
		fmt.Fprintf(&b, "\n### %s\n", u.ID)
		fmt.Fprintf(&b, "- speaker: %s\n", u.Speaker)
		fmt.Fprintf(&b, "- at: %s\n", u.At.Format(time.RFC3339))
		fmt.Fprintf(&b, "- status: %s\n\n", u.Status)
		b.WriteString(u.Body)
		b.WriteString("\n")
		wants = append(wants, u)
	}
	return []byte(b.String()), wants
}

// utteranceBody は 1 発話の本文（実際の対話に近い長さの日本語）。
func utteranceBody(sessionID string, n int) string {
	head := fmt.Sprintf("%s の %d 番目の発話（検証用の生成データ）。", sessionID, n)
	filler := "現状の在庫管理は拠点ごとの Excel 台帳で行っており、締め時刻の違いが引当のずれを生んでいる。"
	var b strings.Builder
	b.WriteString(head)
	for b.Len() < utteranceBodyBytes {
		b.WriteString(filler)
	}
	return b.String()
}

// verifySession は書いたセッションを**正規の読み出し**で読み直し、数と本文を突き合わせる。
func verifySession(s *projectstore.Store, id string, wants []projectstore.Utterance) error {
	_, got, err := s.LoadSession(id)
	if err != nil {
		return fmt.Errorf("生成した対話セッションを読み直せません（%s）: %w", id, err)
	}
	if len(got) != len(wants) {
		return fmt.Errorf("生成した発話の数が合いません（%s）: 期待 %d / 実際 %d"+
			"（対話セッションファイルの書式が変わった可能性がある）", id, len(wants), len(got))
	}
	for i := range wants {
		w, g := wants[i], got[i]
		if g.ID != w.ID || g.Speaker != w.Speaker || g.Status != w.Status ||
			strings.TrimSpace(g.Body) != strings.TrimSpace(w.Body) {
			return fmt.Errorf("生成した発話が読み直しと一致しません（%s %s）"+
				"（対話セッションファイルの書式が変わった可能性がある）", id, w.ID)
		}
	}
	return nil
}

// fillQuestionnaires は質問票を上限まで発行済み状態で作る。
func fillQuestionnaires(s *projectstore.Store, n int, addresseeRef string, issueIDs []string, authorID string) error {
	if len(issueIDs) == 0 {
		return fmt.Errorf("質問票の発行元にする未決事項がありません")
	}
	for i := 1; i <= n; i++ {
		q := projectstore.Questionnaire{
			AddresseeRef: addresseeRef,
			Addressee:    "回答者（検証用）",
			IssuedAt:     time.Now().UTC().Truncate(time.Second),
			IssuedBy:     authorID,
			Status:       projectstore.QuestionnaireIssued,
		}
		for j := 1; j <= 5; j++ {
			q.Questions = append(q.Questions, projectstore.Question{
				ID:           projectstore.FormatQuestionID(j),
				SourceIssue:  issueIDs[(i*5+j)%len(issueIDs)],
				AnswerFormat: projectstore.AnswerFormatFree,
				Text:         fmt.Sprintf("拠点 %04d の棚卸は何時までに締めていますか。", i),
				Background:   "在庫引当のずれの原因を切り分けるために確認します（検証用の生成データ）。",
			})
		}
		if _, err := s.CreateQuestionnaire(q); err != nil {
			return fmt.Errorf("質問票 %d 件目を作れません: %w", i, err)
		}
	}
	return nil
}

// fillImports は総量が target に達するまで既存資料を取り込む（総量 500MB の内数）。
//
// **数え直した実測値で止める**（何件入れれば足りるかを計算で決め打ちしない）。
func fillImports(s *projectstore.Store, target int64) error {
	im := importer.New(s)
	for i := 1; ; i++ {
		usages, err := s.ScaleUsageAll(projectstore.DefaultScaleWarnRatio)
		if err != nil {
			return err
		}
		if scaleCurrent(usages, projectstore.ScaleTotalBytes) >= target {
			return nil
		}
		if _, err := im.ImportWithExtraction(importer.Input{
			Kind:         importer.KindMaterial,
			SourceName:   fmt.Sprintf("現行業務手順書-%03d.txt", i),
			SourceFormat: importer.FormatTxt,
			Content:      importChunk(i),
		}); err != nil {
			return fmt.Errorf("既存資料 %d 件目を取り込めません: %w", i, err)
		}
	}
}

// importChunk は既存資料 1 件の原本（テキスト）。
func importChunk(i int) []byte {
	line := fmt.Sprintf("現行の在庫管理業務の手順（資料 %03d・検証用の生成データ）。"+
		"拠点ごとの棚卸と締め処理の順序、引当の優先規則、例外時の連絡経路を記載する。\n", i)
	var b strings.Builder
	b.Grow(importChunkBytes + len(line))
	for b.Len() < importChunkBytes {
		b.WriteString(line)
	}
	return []byte(b.String())
}

func scaleCurrent(usages []projectstore.ScaleUsage, kind projectstore.ScaleKind) int64 {
	for _, u := range usages {
		if u.Kind == kind {
			return u.Current
		}
	}
	return 0
}

// fillRecentProjects は「同時に扱うプロジェクト」を上限まで用意する（アプリ設定の recent_projects）。
//
// 一覧画面は各プロジェクトの実体を読んで要約を作るため、**実在するフォルダ**を並べる
// （パス文字列だけを並べると一覧の計測が空振りする）。
func fillRecentProjects(o options, author projectstore.Author) (int, error) {
	paths := []string{o.root}
	parent := filepath.Dir(o.root)
	for i := 2; i <= o.projects; i++ {
		path := filepath.Join(parent, fmt.Sprintf("併走プロジェクト%02d.rwv", i))
		st, err := projectstore.CreateProject(path, projectstore.CreateOptions{
			TargetSystemName: fmt.Sprintf("併走プロジェクト %02d", i),
			Author:           author,
		})
		if err != nil {
			return 0, fmt.Errorf("併走プロジェクト %d 件目を作れません: %w", i, err)
		}
		_ = st.Close()
		paths = append(paths, path)
	}

	appPaths := projectstore.AppPaths{Base: o.appBase}
	settings, err := projectstore.LoadSettings(appPaths)
	if err != nil {
		return 0, err
	}
	// 先頭が上限規模のプロジェクトになるよう、末尾から積む（AddRecentProject は先頭へ積む）。
	for i := len(paths) - 1; i >= 0; i-- {
		settings.AddRecentProject(paths[i], o.projects)
	}
	if err := projectstore.SaveSettings(appPaths, settings); err != nil {
		return 0, err
	}
	return len(settings.RecentProjects), nil
}
