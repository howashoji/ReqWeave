//go:build integration

// 結合テスト（実ファイル I/O）: 規模上限の計数。
// 判定式そのものの境界値は scalelimit_test.go（単体）が担い、ここは
// **実体を正しく数えているか**と**保存のたびに数え直しているか**を見る。
// 実行: make -C app test-integration

package projectstore

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// usageOf は種別を指定して 1 件取り出す（見つからなければ失敗させる）。
func usageOf(t *testing.T, s *Store, kind ScaleKind) ScaleUsage {
	t.Helper()
	all, err := s.ScaleUsageAll(DefaultScaleWarnRatio)
	if err != nil {
		t.Fatalf("規模の判定に失敗: %v", err)
	}
	for _, u := range all {
		if u.Kind == kind {
			return u
		}
	}
	t.Fatalf("%s の判定が返ってこない（返り値 %d 件）", kind, len(all))
	return ScaleUsage{}
}

// プロジェクト内 7 種のうち、判定対象になるのは 7 種すべて
// （同時プロジェクト数はプロジェクトの外側の数のため含まない）。
func TestScaleUsageAllCoversInProjectKinds(t *testing.T) {
	s := createTestProject(t)
	all, err := s.ScaleUsageAll(DefaultScaleWarnRatio)
	if err != nil {
		t.Fatalf("規模の判定に失敗: %v", err)
	}
	got := map[ScaleKind]bool{}
	for _, u := range all {
		got[u.Kind] = true
	}
	for _, k := range []ScaleKind{
		ScaleSessions, ScaleUtterances, ScaleRequirements, ScaleDecisions,
		ScaleOpenIssues, ScaleQuestionnaires, ScaleTotalBytes,
	} {
		if !got[k] {
			t.Errorf("%s の判定が返っていない", k)
		}
	}
	if got[ScaleProjects] {
		t.Error("同時プロジェクト数はプロジェクト内の判定に含めない（アプリ設定側で数える）")
	}
}

// 作ったぶんだけ数えること（レコード種別ごとの計数）。
func TestScaleUsageCountsRecords(t *testing.T) {
	s := createTestProject(t)

	for i := 0; i < 3; i++ {
		if _, err := s.CreateSession(SessionOwner, "requirements"); err != nil {
			t.Fatalf("セッションを作成できません: %v", err)
		}
	}
	if got := usageOf(t, s, ScaleSessions); got.Current != 3 {
		t.Errorf("対話セッションの計数が %d（期待 3）", got.Current)
	}

	sess, err := s.CreateSession(SessionOwner, "requirements")
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 5; i++ {
		if _, err := s.AppendUtterance(sess.ID, Utterance{
			Speaker: SpeakerUser, At: time.Now(), Body: "確認したい点があります。\n\n### これは見出しではない",
		}); err != nil {
			t.Fatalf("発話を追記できません: %v", err)
		}
	}
	utter := usageOf(t, s, ScaleUtterances)
	if utter.Current != 5 {
		t.Errorf("発話の計数が %d（期待 5）", utter.Current)
	}
	if utter.Scope != sess.ID {
		t.Errorf("発話が最も多いセッションが %q（期待 %q）", utter.Scope, sess.ID)
	}
}

// 総量 500MB の判定に imports/ 配下（原本・抽出テキスト）を含めること。
func TestScaleTotalBytesIncludesImports(t *testing.T) {
	s := createTestProject(t)
	before := usageOf(t, s, ScaleTotalBytes).Current

	const payload = 256 * 1024
	dir := filepath.Join(s.Root(), "imports", "IMP-001")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "original.pdf"), make([]byte, payload), 0o600); err != nil {
		t.Fatal(err)
	}
	after := usageOf(t, s, ScaleTotalBytes).Current
	if after-before != payload {
		t.Errorf("imports/ 配下が総量に含まれていない: 増分 %d バイト（期待 %d）", after-before, payload)
	}
}

// 判定は保存のたびに再評価する（値をキャッシュしない）。
// 同じ Store で続けて呼んでも、間に行った保存が必ず反映されること。
func TestScaleUsageReevaluatesAfterSave(t *testing.T) {
	s := createTestProject(t)
	first := usageOf(t, s, ScaleSessions).Current
	if _, err := s.CreateSession(SessionOwner, "requirements"); err != nil {
		t.Fatal(err)
	}
	second := usageOf(t, s, ScaleSessions).Current
	if second != first+1 {
		t.Errorf("保存後に数え直していない: %d → %d", first, second)
	}

	// 実体を直接消した場合も実体側が勝つ（派生インデックスに引きずられない）。
	entries, err := os.ReadDir(filepath.Join(s.Root(), dirSessions))
	if err != nil {
		t.Fatal(err)
	}
	removed := false
	for _, e := range entries {
		if !e.IsDir() && filepath.Ext(e.Name()) == ".md" {
			if err := os.Remove(filepath.Join(s.Root(), dirSessions, e.Name())); err != nil {
				t.Fatal(err)
			}
			removed = true
			break
		}
	}
	if !removed {
		t.Fatal("走査が空振りしている: 消せるセッションファイルが無い")
	}
	if third := usageOf(t, s, ScaleSessions).Current; third != second-1 {
		t.Errorf("実体の削除が反映されていない: %d → %d", second, third)
	}
}

// 上限に達しても操作を拒否しないこと。
// 上限がいちばん小さい種別（対話セッション 100）で実際に上限へ到達させ、その先も作れることを見る。
func TestScaleLimitDoesNotRejectOperations(t *testing.T) {
	s := createTestProject(t)
	limit := ScaleLimitOf(ScaleSessions)
	for i := int64(0); i < limit; i++ {
		if _, err := s.CreateSession(SessionOwner, "requirements"); err != nil {
			t.Fatalf("%d 件目のセッションを作成できません: %v", i+1, err)
		}
	}
	at := usageOf(t, s, ScaleSessions)
	if at.Current != limit || at.Level != ScaleLevelExceeded {
		t.Fatalf("上限到達の判定になっていない: %+v", at)
	}
	// ここから先も作れること（拒否ではなく警告）。
	if _, err := s.CreateSession(SessionOwner, "requirements"); err != nil {
		t.Fatalf("上限到達後にセッションを作成できない（拒否してはならない）: %v", err)
	}
	over := usageOf(t, s, ScaleSessions)
	if over.Current != limit+1 || over.Level != ScaleLevelExceeded {
		t.Errorf("上限超過後の判定が想定と違う: %+v", over)
	}
}
