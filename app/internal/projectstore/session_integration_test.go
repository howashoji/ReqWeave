//go:build integration

// 結合テスト（実ファイル I/O）。対話セッション・発話・併置メタデータ。

package projectstore

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// 発話は 1 件ごとに保存され、追記のみで積まれる。
func TestAppendUtterancePersistsEachOne(t *testing.T) {
	s := createTestProject(t)
	sess, err := s.CreateSession(SessionOwner, "requirements")
	if err != nil {
		t.Fatalf("セッションを作成できない: %v", err)
	}
	if sess.ID != "S-0001" || sess.Author != testAuthor().AuthorID {
		t.Fatalf("セッションの初期値が違う: %+v", sess)
	}

	bodies := []string{"最初の質問です。", "回答です。\n\n複数行の本文も入ります。", "次の質問です。"}
	var ids []string
	for i, body := range bodies {
		speaker := SpeakerAgent
		if i == 1 {
			speaker = SpeakerUser
		}
		id, err := s.AppendUtterance(sess.ID, Utterance{Speaker: speaker, Body: body})
		if err != nil {
			t.Fatalf("発話 %d の追記に失敗: %v", i, err)
		}
		ids = append(ids, id)

		// 1 件ごとにファイルへ反映されている（強制終了しても直前の発話まで残る）。
		_, got, err := s.LoadSession(sess.ID)
		if err != nil {
			t.Fatalf("読み出しに失敗: %v", err)
		}
		if len(got) != i+1 {
			t.Fatalf("発話 %d の時点で件数が違う: %d", i, len(got))
		}
		if got[i].Body != strings.TrimRight(body, "\n") {
			t.Errorf("本文が一致しない: %q", got[i].Body)
		}
	}
	if ids[0] != "utt-00001" || ids[2] != "utt-00003" {
		t.Errorf("発話 ID の採番が違う: %v", ids)
	}

	// 追記のみ（既存の発話ブロックが残っている）。
	raw, err := os.ReadFile(filepath.Join(s.Root(), filepath.FromSlash(SessionFile(sess.ID))))
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range ids {
		if !strings.Contains(string(raw), "### "+id) {
			t.Errorf("発話 %s がファイルに残っていない", id)
		}
	}
	if !strings.Contains(string(raw), "author: k.sato@example.co.jp") {
		t.Errorf("フロントマターに作業者が無い:\n%s", raw)
	}
}

// 中断発話は状態つきで保存され、読み出しで識別できる。
func TestInterruptedUtterance(t *testing.T) {
	s := createTestProject(t)
	sess, _ := s.CreateSession(SessionOwner, "requirements")
	if _, err := s.AppendUtterance(sess.ID, Utterance{Speaker: SpeakerAgent, Body: "途中まで", Status: UtteranceInterrupted}); err != nil {
		t.Fatalf("追記に失敗: %v", err)
	}
	if _, err := s.AppendUtterance(sess.ID, Utterance{Speaker: SpeakerUser, Body: "続き"}); err != nil {
		t.Fatalf("追記に失敗: %v", err)
	}

	_, got, err := s.LoadSession(sess.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("件数が違う: %d", len(got))
	}
	if !got[0].IsInterrupted() {
		t.Errorf("中断発話が識別できない: %+v", got[0])
	}
	if got[1].IsInterrupted() || got[1].Status != UtteranceCompleted {
		t.Errorf("通常発話が中断扱いになっている: %+v", got[1])
	}
}

// 再開に必要なメタデータを保存・復元できる（再計算しない）。
func TestSessionMetaRoundTrip(t *testing.T) {
	s := createTestProject(t)
	sess, _ := s.CreateSession(SessionOwner, "requirements")

	if got, err := s.LoadSessionMeta(sess.ID); err != nil || got != nil {
		t.Fatalf("未保存のメタデータが nil でない: %+v %v", got, err)
	}

	meta := &SessionMeta{
		DialogueState: "承認待ち",
		SummaryBlocks: []SummaryBlock{{
			CoversUntil: "utt-00006",
			Body:        "在庫引当のタイミングを受注確定時と決めた。",
			RecordIDs:   []string{"DEC-001", "FR-INV-001"},
		}},
		PendingCandidates: map[string]any{
			"decisions": []any{map[string]any{
				"topic_key":     "scope/in-scope",
				"body":          "受注チャネルは EDI と Web の 2 つ",
				"evidence_refs": []any{"S-0001#utt-00003"},
			}},
		},
		PresentedQuestion: map[string]any{
			"topic_key": "users/roles",
			"text":      "利用者区分はいくつありますか。",
		},
	}
	if err := s.SaveSessionMeta(sess.ID, meta); err != nil {
		t.Fatalf("保存に失敗: %v", err)
	}

	got, err := s.LoadSessionMeta(sess.ID)
	if err != nil {
		t.Fatalf("読み出しに失敗: %v", err)
	}
	if got.DialogueState != "承認待ち" {
		t.Errorf("対話状態が復元されない: %+v", got)
	}
	if len(got.SummaryBlocks) != 1 || got.SummaryBlocks[0].CoversUntil != "utt-00006" ||
		len(got.SummaryBlocks[0].RecordIDs) != 2 {
		t.Errorf("要約ブロックが復元されない: %+v", got.SummaryBlocks)
	}
	cands, ok := got.PendingCandidates.(map[string]any)
	if !ok || cands["decisions"] == nil {
		t.Errorf("未承認候補が復元されない: %+v", got.PendingCandidates)
	}
	q, ok := got.PresentedQuestion.(map[string]any)
	if !ok || q["topic_key"] != "users/roles" {
		t.Errorf("提示済み質問が復元されない: %+v", got.PresentedQuestion)
	}

	// 発話ファイルとは別ファイルで保持する（更新頻度の分離）。
	if _, err := os.Stat(filepath.Join(s.Root(), filepath.FromSlash(SessionMetaFile(sess.ID)))); err != nil {
		t.Errorf("併置ファイルが作られていない: %v", err)
	}
}

// 担当者セッションは作成者の利用者 ID を必ず持つ（セッションは作成した作業者が所有する）。
func TestSessionValidation(t *testing.T) {
	s := createTestProject(t)
	if _, err := s.CreateSession("respondent", "requirements"); err == nil {
		t.Error("種別が値集合外のセッションが作られた")
	}
	if _, err := s.CreateSession(SessionOwner, ""); err == nil {
		t.Error("フェーズなしのセッションが作られた")
	}
	sess, err := s.CreateSession(SessionStakeholder, "requirements")
	if err != nil {
		t.Fatalf("ステークホルダーセッションを作成できない: %v", err)
	}
	if sess.Author != "" {
		t.Errorf("ステークホルダーセッションに作業者が入っている: %+v", sess)
	}

	if _, err := s.AppendUtterance("S-9999", Utterance{Speaker: SpeakerUser, Body: "x"}); err == nil {
		t.Error("存在しないセッションへ追記できた")
	}
	if _, err := s.AppendUtterance(sess.ID, Utterance{Speaker: "system", Body: "x"}); err == nil {
		t.Error("話者が値集合外の発話が追記された")
	}
}

// セッション一覧をフェーズ・種別つきで読み出せる。
func TestListSessions(t *testing.T) {
	s := createTestProject(t)
	if got, err := s.ListSessions(); err != nil || len(got) != 0 {
		t.Fatalf("初期状態が空でない: %+v %v", got, err)
	}
	if _, err := s.CreateSession(SessionOwner, "requirements"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.CreateSession(SessionOwner, "basic-design"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.CreateSession(SessionStakeholder, "requirements"); err != nil {
		t.Fatal(err)
	}

	got, err := s.ListSessions()
	if err != nil {
		t.Fatalf("一覧の取得に失敗: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("件数が違う: %d", len(got))
	}
	if got[0].ID != "S-0001" || got[2].ID != "S-0003" {
		t.Errorf("ID 順に並んでいない: %+v", got)
	}
	var owners, designs int
	for _, sess := range got {
		if sess.Type == SessionOwner {
			owners++
		}
		if sess.Phase == "basic-design" {
			designs++
		}
	}
	if owners != 2 || designs != 1 {
		t.Errorf("種別・フェーズが読めていない: %+v", got)
	}
}

// 追記の途中で切れた末尾ブロックは読み飛ばし、直前までの発話を返す。
func TestTruncatedTailIsSkipped(t *testing.T) {
	s := createTestProject(t)
	sess, _ := s.CreateSession(SessionOwner, "requirements")
	if _, err := s.AppendUtterance(sess.ID, Utterance{Speaker: SpeakerAgent, Body: "完全な発話"}); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(s.Root(), filepath.FromSlash(SessionFile(sess.ID)))
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	// メタ行の途中で切れた状態を作る。
	if _, err := f.WriteString("\n### utt-00002\n- speaker: user\n"); err != nil {
		t.Fatal(err)
	}
	f.Close()

	_, got, err := s.LoadSession(sess.ID)
	if err != nil {
		t.Fatalf("読み出しに失敗: %v", err)
	}
	if len(got) != 1 || got[0].ID != "utt-00001" {
		t.Fatalf("不完全なブロックが採用されている: %+v", got)
	}

	// 次の追記は切れたブロックの番号を飛ばして採番する（ID を再利用しない）。
	id, err := s.AppendUtterance(sess.ID, Utterance{Speaker: SpeakerUser, Body: "次の発話"})
	if err != nil {
		t.Fatal(err)
	}
	if id != "utt-00003" {
		t.Errorf("切れたブロックの番号を再利用している: %s", id)
	}
}

// 発話を書き換え・削除する API を持たない（読み出しと追記のみ）。
func TestNoUtteranceMutationAPI(t *testing.T) {
	s := createTestProject(t)
	sess, _ := s.CreateSession(SessionOwner, "requirements")
	if _, err := s.AppendUtterance(sess.ID, Utterance{Speaker: SpeakerUser, Body: "元の本文"}); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(filepath.Join(s.Root(), filepath.FromSlash(SessionFile(sess.ID))))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.AppendUtterance(sess.ID, Utterance{Speaker: SpeakerAgent, Body: "後の本文",
		At: time.Now().Add(time.Minute)}); err != nil {
		t.Fatal(err)
	}
	after, err := os.ReadFile(filepath.Join(s.Root(), filepath.FromSlash(SessionFile(sess.ID))))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(string(after), string(before)) {
		t.Errorf("既存の内容が書き換えられている:\nbefore=%s\nafter=%s", before, after)
	}
}

// クラッシュ後の復元・対話の自動保存:
// 「対話の途中で保存操作をせずにアプリを終了・再起動し、直前の発話までが復元されていること」。
//
// 保存操作を一度も呼ばず、Store を閉じずに捨てて（プロセス強制終了に相当）開き直す。
func TestUtterancesSurviveAbruptTerminationWithoutSaveOperation(t *testing.T) {
	s := createTestProject(t)
	root := s.Root()

	sess, err := s.CreateSession(SessionOwner, "requirements")
	if err != nil {
		t.Fatal(err)
	}
	bodies := []string{
		"在庫の締め処理について教えてください。",
		"月末に一括で行っています。",
		"締め後の訂正はどう扱いますか。",
	}
	for _, body := range bodies {
		if _, err := s.AppendUtterance(sess.ID, Utterance{Speaker: SpeakerUser, Body: body}); err != nil {
			t.Fatalf("追記に失敗: %v", err)
		}
	}
	// **保存操作は呼ばない**。Close もせずに参照を捨てる（強制終了に相当）。
	s = nil

	reopened, err := Open(root, testAuthor())
	if err != nil {
		t.Fatalf("再起動後に開けない: %v", err)
	}
	defer reopened.Close()

	_, got, err := reopened.LoadSession(sess.ID)
	if err != nil {
		t.Fatalf("セッションを読めない: %v", err)
	}
	if len(got) != len(bodies) {
		t.Fatalf("復元された発話が %d 件（期待 %d 件）", len(got), len(bodies))
	}
	for i, want := range bodies {
		if got[i].Body != want {
			t.Errorf("発話 %d = %q, want %q", i, got[i].Body, want)
		}
	}

	// セッション自体も一覧から開ける（「すべての対話セッションが一覧から開ける」）。
	sessions, err := reopened.ListSessions()
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, meta := range sessions {
		if meta.ID == sess.ID {
			found = true
		}
	}
	if !found {
		t.Fatalf("再起動後の一覧にセッションが無い: %+v", sessions)
	}
}
