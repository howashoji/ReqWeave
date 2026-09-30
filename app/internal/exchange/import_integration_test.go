//go:build integration

// 結合テスト（実ファイル I/O）。実行: make -C app test-integration

package exchange_test

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/howashoji/ReqWeave/app/internal/exchange"
	"github.com/howashoji/ReqWeave/app/internal/projectstore"
)

// returnSpec は返送ファイルの作り方（異常系を作るための差し替え点）。
type returnSpec struct {
	// questionnaireMarkdown を差し替えると改変検出・宛先不一致を作れる。
	questionnaireMarkdown []byte
	answers               *projectstore.Answers
	sessions              map[string][]byte
	projectID             string
	questionnaireID       string
	kind                  string
	recipientPublic       []byte
}

// writeReturn は返送ファイルを作る（回答モードが出力するものに相当）。
func writeReturn(t *testing.T, dir string, store *projectstore.Store, q *projectstore.Questionnaire, spec returnSpec) string {
	t.Helper()
	kp, err := exchange.EnsureProjectKeyPair(store)
	if err != nil {
		t.Fatal(err)
	}
	markdown := spec.questionnaireMarkdown
	if markdown == nil {
		// 回答モードは発行ファイル内の写し（発行者の利用者 ID と状態を含まない）をそのまま返す。
		markdown, err = q.MarshalForExchange()
		if err != nil {
			t.Fatal(err)
		}
	}
	answers := spec.answers
	if answers == nil {
		answers = defaultAnswers(t, q)
	}
	answersMD, err := answers.Marshal()
	if err != nil {
		t.Fatal(err)
	}
	content := exchange.ContentMeta{ContentHash: exchange.ContentHash(markdown)}
	contentYAML := []byte("content_hash: " + content.ContentHash + "\n")

	payload := exchange.Payload{
		exchange.EntryQuestionnaire: markdown,
		exchange.EntryContent:       contentYAML,
		exchange.EntryAnswers:       answersMD,
	}
	for name, body := range spec.sessions {
		payload[name] = body
	}

	manifest := exchange.Manifest{
		ExchangeFormatVersion: exchange.CurrentFormatVersion,
		Kind:                  orString(spec.kind, exchange.KindReturn),
		ProjectID:             orString(spec.projectID, store.Project().ProjectID),
		QuestionnaireID:       orString(spec.questionnaireID, q.ID),
		IssuedAt:              q.IssuedAt,
	}
	recipient := spec.recipientPublic
	if recipient == nil {
		recipient = kp.Public
	}
	dst := filepath.Join(dir, q.ID+"-return"+exchange.ExtReturn)
	if manifest.Kind == exchange.KindIssue {
		if err := exchange.WriteIssueFile(dst, manifest, payload, "AbCdEfGh2345"); err != nil {
			t.Fatalf("発行用ファイルを作れない: %v", err)
		}
		return dst
	}
	if err := exchange.WriteReturnFile(dst, manifest, payload, recipient); err != nil {
		t.Fatalf("返送ファイルを作れない: %v", err)
	}
	return dst
}

func orString(v, fallback string) string {
	if v == "" {
		return fallback
	}
	return v
}

func defaultAnswers(t *testing.T, q *projectstore.Questionnaire) *projectstore.Answers {
	t.Helper()
	at := time.Date(2026, 8, 28, 6, 0, 0, 0, time.UTC)
	a := &projectstore.Answers{
		QuestionnaireID: q.ID, Respondent: "佐藤", AnsweredAt: at,
		Answers: []projectstore.Answer{
			{QuestionID: "q-01", Kind: projectstore.AnswerKindAnswered, Selected: []string{"即時"}, At: at},
			{QuestionID: "q-02", Kind: projectstore.AnswerKindUnknown, At: at, Body: "理由: 判断できません。確認先: 経理部"},
		},
	}
	return a
}

func issueQuestionnaire(t *testing.T, store *projectstore.Store, q *projectstore.Questionnaire) {
	t.Helper()
	c := buildContent(t, store, q)
	if _, err := exchange.WriteIssue(store, c, filepath.Join(t.TempDir(), q.ID)); err != nil {
		t.Fatalf("発行に失敗: %v", err)
	}
}

// 正常な返送ファイルの検証・突合・取込。
func TestValidateAndApplyReturn(t *testing.T) {
	store, q := newIssueProject(t)
	issueQuestionnaire(t, store, q)
	src := writeReturn(t, t.TempDir(), store, q, returnSpec{})

	review, err := exchange.ValidateReturn(store, src)
	if err != nil {
		t.Fatalf("検証に失敗: %v", err)
	}
	if review.NeedsConfirmation() {
		t.Fatalf("確認不要のはずが警告が出ました: %+v", review.Warnings)
	}
	if len(review.Matches) != 2 {
		t.Fatalf("対応表の件数が %d です（期待 2）", len(review.Matches))
	}
	// 質問 → source_issue を介して発行元未決事項へ紐づく。
	if review.Matches[0].SourceIssue != "ISS-003" || review.Matches[1].SourceIssue != "ISS-004" {
		t.Fatalf("発行元未決事項の対応が違います: %+v", review.Matches)
	}
	if review.Matches[0].Answer == nil || review.Matches[0].Answer.Selected[0] != "即時" {
		t.Fatalf("回答が対応づいていません: %+v", review.Matches[0])
	}
	if review.Matches[1].Answer == nil || review.Matches[1].Answer.Kind != projectstore.AnswerKindUnknown {
		t.Fatalf("「不明」の回答が対応づいていません: %+v", review.Matches[1])
	}

	if _, err := exchange.ApplyReturn(store, review, exchange.ImportConfirmation{}); err != nil {
		t.Fatalf("取込に失敗: %v", err)
	}
	reloaded, err := store.LoadQuestionnaire(q.ID)
	if err != nil {
		t.Fatal(err)
	}
	if reloaded.Status != projectstore.QuestionnaireAnswered {
		t.Fatalf("取込後の状態が %q です（期待 answered）", reloaded.Status)
	}
	saved, err := store.LoadAnswers(q.ID)
	if err != nil || saved == nil {
		t.Fatalf("回答が保存されていません: %v", err)
	}
	if len(saved.Answers) != 2 || saved.Respondent != "佐藤" {
		t.Fatalf("保存された回答が違います: %+v", saved)
	}
	// 受領原本が exchange/ に無変更で残る。
	entries, err := os.ReadDir(filepath.Dir(store.ExchangeArtifactPath(q.ID, "x")))
	if err != nil {
		t.Fatal(err)
	}
	original, err := os.ReadFile(src)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, e := range entries {
		if !strings.HasSuffix(e.Name(), exchange.ExtReturn) {
			continue
		}
		body, err := os.ReadFile(store.ExchangeArtifactPath(q.ID, e.Name()))
		if err != nil {
			t.Fatal(err)
		}
		if bytes.Equal(body, original) {
			found = true
		}
	}
	if !found {
		t.Fatalf("受領原本が無変更で保存されていません")
	}
}

// 復号・ファイルの種別・宛先のプロジェクト・質問票の有無の検証で不合格なら取込を停止し、プロジェクトデータを変更しない。
func TestValidateReturnStopsWithoutChangingProject(t *testing.T) {
	cases := map[string]func(t *testing.T, store *projectstore.Store, q *projectstore.Questionnaire, dir string) string{
		"手順0: 別プロジェクトの鍵で暗号化": func(t *testing.T, store *projectstore.Store, q *projectstore.Questionnaire, dir string) string {
			other, err := exchange.GenerateKeyPair()
			if err != nil {
				t.Fatal(err)
			}
			return writeReturn(t, dir, store, q, returnSpec{recipientPublic: other.Public})
		},
		"手順2: 発行用ファイル": func(t *testing.T, store *projectstore.Store, q *projectstore.Questionnaire, dir string) string {
			return writeReturn(t, dir, store, q, returnSpec{kind: exchange.KindIssue})
		},
		"手順3: 別プロジェクト宛": func(t *testing.T, store *projectstore.Store, q *projectstore.Questionnaire, dir string) string {
			return writeReturn(t, dir, store, q, returnSpec{projectID: "00000000-0000-4000-8000-000000000999"})
		},
		"手順4: 質問票が存在しない": func(t *testing.T, store *projectstore.Store, q *projectstore.Questionnaire, dir string) string {
			return writeReturn(t, dir, store, q, returnSpec{questionnaireID: "QS-099"})
		},
		"受け渡しファイルでない": func(t *testing.T, store *projectstore.Store, q *projectstore.Questionnaire, dir string) string {
			p := filepath.Join(dir, "broken"+exchange.ExtReturn)
			if err := os.WriteFile(p, []byte("これは受け渡しファイルではありません"), 0o644); err != nil {
				t.Fatal(err)
			}
			return p
		},
	}
	for name, makeFile := range cases {
		t.Run(name, func(t *testing.T) {
			store, q := newIssueProject(t)
			issueQuestionnaire(t, store, q)
			src := makeFile(t, store, q, t.TempDir())

			before := snapshotDir(t, store.Root())
			review, err := exchange.ValidateReturn(store, src)
			if err == nil {
				t.Fatalf("不合格のファイルが受理されました: %+v", review)
			}
			if after := snapshotDir(t, store.Root()); after != before {
				t.Fatalf("検証で停止したのにプロジェクトデータが変化しました")
			}
		})
	}
}

// 改変検出は差分つきで警告し、確認操作なしには続行しない。
func TestValidateReturnDetectsModifiedContent(t *testing.T) {
	store, q := newIssueProject(t)
	issueQuestionnaire(t, store, q)

	markdown, err := q.MarshalForExchange()
	if err != nil {
		t.Fatal(err)
	}
	tampered := bytes.Replace(markdown, []byte("在庫の引き当ては"), []byte("在庫の引当ては"), 1)
	if bytes.Equal(tampered, markdown) {
		t.Fatalf("前提が崩れています（改変できていない）")
	}
	src := writeReturn(t, t.TempDir(), store, q, returnSpec{questionnaireMarkdown: tampered})

	review, err := exchange.ValidateReturn(store, src)
	if err != nil {
		t.Fatalf("検証に失敗: %v", err)
	}
	if !review.HasWarning(exchange.WarnContentModified) {
		t.Fatalf("改変が検出されていません: %+v", review.Warnings)
	}
	var warning exchange.ImportWarning
	for _, w := range review.Warnings {
		if w.Kind == exchange.WarnContentModified {
			warning = w
		}
	}
	if warning.Before == "" || warning.After == "" || warning.Before == warning.After {
		t.Fatalf("差分表示の材料がありません: %+v", warning)
	}

	before := snapshotDir(t, store.Root())
	if _, err := exchange.ApplyReturn(store, review, exchange.ImportConfirmation{}); !errors.Is(err, exchange.ErrConfirmationRequired) {
		t.Fatalf("確認操作なしに続行しました: %v", err)
	}
	if after := snapshotDir(t, store.Root()); after != before {
		t.Fatalf("確認前にプロジェクトデータが変化しました")
	}

	if _, err := exchange.ApplyReturn(store, review, exchange.ImportConfirmation{AcceptModifiedContent: true}); err != nil {
		t.Fatalf("確認後の取込に失敗: %v", err)
	}
	reloaded, err := store.LoadQuestionnaire(q.ID)
	if err != nil {
		t.Fatal(err)
	}
	if reloaded.Status != projectstore.QuestionnaireAnswered {
		t.Fatalf("確認後に取り込まれていません: %q", reloaded.Status)
	}
}

// 宛先が発行時と一致しないときは警告し、確認後のみ続行する。
func TestValidateReturnDetectsAddresseeMismatch(t *testing.T) {
	store, q := newIssueProject(t)
	issueQuestionnaire(t, store, q)

	altered := *q
	altered.AddresseeRef = "STK-002"
	altered.Addressee = "鈴木（物流部）"
	markdown, err := altered.MarshalForExchange()
	if err != nil {
		t.Fatal(err)
	}
	src := writeReturn(t, t.TempDir(), store, q, returnSpec{questionnaireMarkdown: markdown})

	review, err := exchange.ValidateReturn(store, src)
	if err != nil {
		t.Fatalf("検証に失敗: %v", err)
	}
	if !review.HasWarning(exchange.WarnAddresseeMismatch) {
		t.Fatalf("宛先の不一致が検出されていません: %+v", review.Warnings)
	}
	if _, err := exchange.ApplyReturn(store, review, exchange.ImportConfirmation{AcceptModifiedContent: true}); !errors.Is(err, exchange.ErrConfirmationRequired) {
		t.Fatalf("宛先の確認なしに続行しました: %v", err)
	}
	if _, err := exchange.ApplyReturn(store, review, exchange.ImportConfirmation{
		AcceptModifiedContent: true, AcceptAddresseeMismatch: true}); err != nil {
		t.Fatalf("確認後の取込に失敗: %v", err)
	}
}

// 取込済みへの再取込は明示的な選択がなければ続行しない。
func TestValidateReturnDetectsReimport(t *testing.T) {
	store, q := newIssueProject(t)
	issueQuestionnaire(t, store, q)
	src := writeReturn(t, t.TempDir(), store, q, returnSpec{})

	review, err := exchange.ValidateReturn(store, src)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := exchange.ApplyReturn(store, review, exchange.ImportConfirmation{}); err != nil {
		t.Fatalf("1 回目の取込に失敗: %v", err)
	}
	// 反映承認の完了に相当（回答済み → 取込済み）。
	if err := store.SetQuestionnaireStatus(q.ID, projectstore.QuestionnaireImported); err != nil {
		t.Fatal(err)
	}

	again, err := exchange.ValidateReturn(store, src)
	if err != nil {
		t.Fatalf("再検証に失敗: %v", err)
	}
	if !again.HasWarning(exchange.WarnReimport) {
		t.Fatalf("二重取込が検出されていません: %+v", again.Warnings)
	}
	// 状態遷移（issued → answered → imported）で質問票の status は変わるが、
	// 突合の基準は発行時点のハッシュ（発行控えメタデータ）であり改変扱いにしない。
	if again.HasWarning(exchange.WarnContentModified) {
		t.Fatalf("状態遷移が改変として検出されました: %+v", again.Warnings)
	}
	before := snapshotDir(t, store.Root())
	if _, err := exchange.ApplyReturn(store, again, exchange.ImportConfirmation{}); !errors.Is(err, exchange.ErrConfirmationRequired) {
		t.Fatalf("再取込が確認なしに続行しました: %v", err)
	}
	if after := snapshotDir(t, store.Root()); after != before {
		t.Fatalf("確認前にプロジェクトデータが変化しました")
	}
	if _, err := exchange.ApplyReturn(store, again, exchange.ImportConfirmation{AllowReimport: true}); err != nil {
		t.Fatalf("再取込に失敗: %v", err)
	}
	reloaded, err := store.LoadQuestionnaire(q.ID)
	if err != nil {
		t.Fatal(err)
	}
	if reloaded.Status != projectstore.QuestionnaireAnswered {
		t.Fatalf("再取込後の状態が %q です（期待 answered）", reloaded.Status)
	}
}

// 欠落は未回答として続行、未知の質問 ID は無視して記録に残す。
func TestValidateReturnMatchesQuestionIDs(t *testing.T) {
	store, q := newIssueProject(t)
	issueQuestionnaire(t, store, q)

	at := time.Date(2026, 8, 28, 6, 0, 0, 0, time.UTC)
	answers := &projectstore.Answers{
		QuestionnaireID: q.ID, Respondent: "佐藤", AnsweredAt: at,
		Answers: []projectstore.Answer{
			{QuestionID: "q-01", Kind: projectstore.AnswerKindAnswered, Selected: []string{"即時"}, At: at},
			{QuestionID: "q-09", Kind: projectstore.AnswerKindAnswered, FreeText: "存在しない質問への回答", At: at},
		},
	}
	src := writeReturn(t, t.TempDir(), store, q, returnSpec{answers: answers})

	review, err := exchange.ValidateReturn(store, src)
	if err != nil {
		t.Fatalf("検証に失敗: %v", err)
	}
	if strings.Join(review.MissingQuestionIDs, ",") != "q-02" {
		t.Fatalf("欠落した質問が %v です（期待 [q-02]）", review.MissingQuestionIDs)
	}
	if strings.Join(review.UnknownQuestionIDs, ",") != "q-09" {
		t.Fatalf("未知の質問 ID が %v です（期待 [q-09]）", review.UnknownQuestionIDs)
	}
	if review.Matches[1].Answer != nil {
		t.Fatalf("未回答の質問に回答が付いています: %+v", review.Matches[1])
	}

	if _, err := exchange.ApplyReturn(store, review, exchange.ImportConfirmation{}); err != nil {
		t.Fatalf("欠落があるのに取込が止まりました: %v", err)
	}
	saved, err := store.LoadAnswers(q.ID)
	if err != nil || saved == nil {
		t.Fatal(err)
	}
	if len(saved.Answers) != 1 || saved.Answers[0].QuestionID != "q-01" {
		t.Fatalf("未知の質問 ID の回答が取り込まれています: %+v", saved.Answers)
	}
}

// 返送ファイルに含まれるステークホルダーセッションは検証結果として保持する（再採番は反映時）。
func TestValidateReturnKeepsSessions(t *testing.T) {
	store, q := newIssueProject(t)
	issueQuestionnaire(t, store, q)
	src := writeReturn(t, t.TempDir(), store, q, returnSpec{sessions: map[string][]byte{
		"sessions/S-0001.md": []byte("---\nid: S-0001\ntype: stakeholder\nphase: requirements\nstarted_at: 2026-08-28T06:00:00Z\n---\n\n### utt-00001\n"),
	}})

	review, err := exchange.ValidateReturn(store, src)
	if err != nil {
		t.Fatalf("検証に失敗: %v", err)
	}
	if len(review.Sessions) != 1 {
		t.Fatalf("セッションが保持されていません: %+v", review.Sessions)
	}
	if _, ok := review.Sessions["sessions/S-0001.md"]; !ok {
		t.Fatalf("セッションのパスが違います: %+v", review.Sessions)
	}
}

// 返送内のステークホルダーセッションはプロジェクト内 ID で再採番して保存し、
// 受領原本（exchange/）は無変更で保持する（受け取ったものを後から確かめられるように）。
func TestApplyReturnRenumbersStakeholderSessions(t *testing.T) {
	store, q := newIssueProject(t)
	issueQuestionnaire(t, store, q)

	// プロジェクト側で S-0001 が既に使われている状態（返送内の ID と衝突しうる）。
	sessionsDir := filepath.Join(store.Root(), "sessions")
	existingIDs := sessionIDsOnDisk(t, sessionsDir)
	if !existingIDs["S-0001"] {
		t.Fatalf("既存セッションに S-0001 がありません（衝突の検証にならない）: %v", existingIDs)
	}
	existingBody, err := os.ReadFile(filepath.Join(sessionsDir, "S-0001.md"))
	if err != nil {
		t.Fatal(err)
	}

	const returned = "---\nid: S-0001\ntype: stakeholder\nphase: requirements\n" +
		"started_at: 2026-08-28T06:00:00Z\n---\n" +
		"\n### utt-00001\n- speaker: agent\n- at: 2026-08-28T06:00:00Z\n- status: completed\n\n" +
		"在庫の引き当てはいつ行いますか。\n" +
		"\n### utt-00002\n- speaker: user\n- at: 2026-08-28T06:01:00Z\n- status: completed\n\n" +
		"受注した時点で行います。\n"

	src := writeReturn(t, t.TempDir(), store, q, returnSpec{sessions: map[string][]byte{
		"sessions/S-0001.md": []byte(returned),
	}})
	review, err := exchange.ValidateReturn(store, src)
	if err != nil {
		t.Fatalf("検証に失敗: %v", err)
	}
	result, err := exchange.ApplyReturn(store, review, exchange.ImportConfirmation{})
	if err != nil {
		t.Fatalf("取込に失敗: %v", err)
	}

	if len(result.Sessions) != 1 {
		t.Fatalf("取り込んだセッションが %d 件です（期待 1）: %+v", len(result.Sessions), result.Sessions)
	}
	imported := result.Sessions[0]
	if imported.OriginalID != "S-0001" {
		t.Fatalf("返送内の ID が %s です（期待 S-0001）", imported.OriginalID)
	}
	if existingIDs[imported.SessionID] {
		t.Fatalf("既存セッション %s と衝突しました", imported.SessionID)
	}
	if _, ok := projectstore.IDSession.Parse(imported.SessionID); !ok {
		t.Fatalf("再採番の ID が S-nnnn 形式ではありません: %q", imported.SessionID)
	}

	// 既存の S-0001 が上書きされていない。
	afterBody, err := os.ReadFile(filepath.Join(sessionsDir, "S-0001.md"))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(existingBody, afterBody) {
		t.Fatal("既存セッション S-0001 が上書きされました")
	}
	// 発話は本文・ID・日時とも無変更で写る。
	sess, utterances, err := store.LoadSession(imported.SessionID)
	if err != nil {
		t.Fatalf("再採番したセッションを読めない: %v", err)
	}
	if sess.Type != projectstore.SessionStakeholder {
		t.Fatalf("セッション種別が %s です（期待 %s）", sess.Type, projectstore.SessionStakeholder)
	}
	if len(utterances) != 2 {
		t.Fatalf("発話が %d 件です（期待 2）: %+v", len(utterances), utterances)
	}
	if utterances[0].ID != "utt-00001" || utterances[1].Body != "受注した時点で行います。" {
		t.Fatalf("発話が無変更で写っていません: %+v", utterances)
	}

	// 受領原本は exchange/ に無変更で残る。
	entries, err := os.ReadDir(filepath.Join(store.Root(), "questionnaires", q.ID, "exchange"))
	if err != nil {
		t.Fatal(err)
	}
	var archived string
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), exchange.ExtReturn) {
			archived = e.Name()
		}
	}
	if archived == "" {
		t.Fatalf("受領原本が保存されていません: %+v", entries)
	}
	saved, err := os.ReadFile(store.ExchangeArtifactPath(q.ID, archived))
	if err != nil {
		t.Fatal(err)
	}
	original, err := os.ReadFile(src)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(saved, original) {
		t.Fatalf("受領原本が書き換わっています（%d バイト → %d バイト）", len(original), len(saved))
	}
}

// 返送内のセッションが担当者セッションを騙る場合は取り込まない。
func TestApplyReturnRejectsNonStakeholderSession(t *testing.T) {
	store, q := newIssueProject(t)
	issueQuestionnaire(t, store, q)

	src := writeReturn(t, t.TempDir(), store, q, returnSpec{sessions: map[string][]byte{
		"sessions/S-0001.md": []byte("---\nid: S-0001\ntype: owner\nphase: requirements\n" +
			"started_at: 2026-08-28T06:00:00Z\nauthor: attacker@example.com\n---\n"),
	}})
	review, err := exchange.ValidateReturn(store, src)
	if err != nil {
		t.Fatalf("検証に失敗: %v", err)
	}
	if _, err := exchange.ApplyReturn(store, review, exchange.ImportConfirmation{}); err == nil {
		t.Fatal("担当者セッションを名乗るセッションを取り込みました")
	}
}

// sessionIDsOnDisk は sessions/ にあるセッション ID の集合を返す（採番の衝突検証用）。
func sessionIDsOnDisk(t *testing.T, dir string) map[string]bool {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]bool{}
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".md") {
			continue
		}
		out[strings.TrimSuffix(name, ".md")] = true
	}
	return out
}
