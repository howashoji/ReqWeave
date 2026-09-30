//go:build integration

// 結合テスト（AI 利用量上限設定 × 実ファイル・ロック）。

package projectstore

import (
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func newUsageLimitStore(t *testing.T) *Store {
	t.Helper()
	root := filepath.Join(t.TempDir(), "proj")
	s, err := CreateProject(root, CreateOptions{TargetSystemName: "在庫管理システム", Author: testAuthor()})
	if err != nil {
		t.Fatalf("プロジェクト作成に失敗: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

func projectYAML(t *testing.T, s *Store) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(s.Root(), FileProject))
	if err != nil {
		t.Fatalf("project.yaml を読めません: %v", err)
	}
	return string(b)
}

func projectHash(t *testing.T, s *Store) string {
	t.Helper()
	return fmt.Sprintf("%x", sha256.Sum256([]byte(projectYAML(t, s))))
}

// markProjectUnwritten は project.yaml の更新時刻を過去へ倒す。
// 書き込みが起きたかどうかは内容ハッシュでは分からない（同じ内容を書き直しても一致する）ため、
// 更新時刻で「書き込みそのもの」を観測する。
func markProjectUnwritten(t *testing.T, s *Store) time.Time {
	t.Helper()
	past := time.Date(2020, 1, 2, 3, 4, 5, 0, time.UTC)
	path := filepath.Join(s.Root(), FileProject)
	if err := os.Chtimes(path, past, past); err != nil {
		t.Fatalf("更新時刻を設定できません: %v", err)
	}
	return past
}

// assertProjectNotWritten は project.yaml が書き直されていないことを確認する。
func assertProjectNotWritten(t *testing.T, s *Store, past time.Time) {
	t.Helper()
	info, err := os.Stat(filepath.Join(s.Root(), FileProject))
	if err != nil {
		t.Fatalf("project.yaml を stat できません: %v", err)
	}
	if !info.ModTime().Equal(past) {
		t.Errorf("project.yaml が書き直された（更新時刻が %v → %v）", past, info.ModTime())
	}
}

// 設定・変更・解除が project.yaml へ書き込まれ、再読込で読める。
func TestUsageLimitLifecycle(t *testing.T) {
	s := newUsageLimitStore(t)

	if s.UsageLimitSetting() != nil {
		t.Fatalf("既定が未設定でない: %+v", s.UsageLimitSetting())
	}

	before, after, err := s.SetUsageLimit(1_000_000, nil)
	if err != nil {
		t.Fatalf("上限の設定に失敗: %v", err)
	}
	if before != nil {
		t.Errorf("設定前の値が未設定でない: %+v", before)
	}
	if after == nil || after.TokensMax != 1_000_000 || after.WarnRatioOrDefault() != DefaultWarnRatio {
		t.Fatalf("設定後の値が違う: %+v", after)
	}
	if !strings.Contains(projectYAML(t, s), "usage_limit:") {
		t.Errorf("project.yaml に usage_limit が書かれていない:\n%s", projectYAML(t, s))
	}

	before, after, err = s.SetUsageLimit(2_000_000, ratio(0.5))
	if err != nil {
		t.Fatalf("上限の変更に失敗: %v", err)
	}
	if before == nil || before.TokensMax != 1_000_000 {
		t.Errorf("変更前の値が違う: %+v", before)
	}
	if after == nil || after.TokensMax != 2_000_000 || after.WarnRatioOrDefault() != 0.5 {
		t.Errorf("変更後の値が違う: %+v", after)
	}

	// 再読込（別インスタンス）でも同じ値が読めること。
	reopened, err := Open(s.Root(), testAuthor())
	if err != nil {
		t.Fatalf("開き直しに失敗: %v", err)
	}
	defer reopened.Close()
	if got := reopened.UsageLimitSetting(); got == nil || got.TokensMax != 2_000_000 || got.WarnRatioOrDefault() != 0.5 {
		t.Errorf("開き直したときの値が違う: %+v", got)
	}

	cleared, err := s.ClearUsageLimit()
	if err != nil {
		t.Fatalf("上限の解除に失敗: %v", err)
	}
	if cleared == nil || cleared.TokensMax != 2_000_000 {
		t.Errorf("解除前の値が返らない: %+v", cleared)
	}
	if s.UsageLimitSetting() != nil {
		t.Errorf("解除後も上限が残っている: %+v", s.UsageLimitSetting())
	}
	// 解除は「キーごと消す」= 未設定に戻す（tokens_max: 0 のレコードを残さない）。
	if strings.Contains(projectYAML(t, s), "usage_limit") {
		t.Errorf("解除後も project.yaml に usage_limit が残っている:\n%s", projectYAML(t, s))
	}
}

// 値が変わらない設定・解除では書き込みを行わない（無意味な変更履歴を作らせない）。
func TestUsageLimitUnchangedDoesNotWrite(t *testing.T) {
	s := newUsageLimitStore(t)

	// 未設定のまま解除しても書き込まない。
	hash := projectHash(t, s)
	past := markProjectUnwritten(t, s)
	before, err := s.ClearUsageLimit()
	if err != nil {
		t.Fatalf("未設定での解除でエラー: %v", err)
	}
	if before != nil {
		t.Errorf("未設定なのに解除前の値が返った: %+v", before)
	}
	if projectHash(t, s) != hash {
		t.Error("未設定の解除で project.yaml が書き換わった")
	}
	assertProjectNotWritten(t, s, past)

	if _, _, err := s.SetUsageLimit(500_000, ratio(0.8)); err != nil {
		t.Fatalf("上限の設定に失敗: %v", err)
	}
	hash = projectHash(t, s)
	past = markProjectUnwritten(t, s)

	// 同値の再設定（既定値の 0.8 を省略した形でも同値）。
	before, after, err := s.SetUsageLimit(500_000, nil)
	if err != nil {
		t.Fatalf("同値の設定でエラー: %v", err)
	}
	if !before.Equal(after) {
		t.Errorf("同値なのに変更として返った: before=%+v after=%+v", before, after)
	}
	if projectHash(t, s) != hash {
		t.Error("同値の設定で project.yaml が書き換わった")
	}
	assertProjectNotWritten(t, s, past)
}

// 書き込みは records ロック内で行う（保持中は成立しない・部分書き込みも起きない）。
func TestUsageLimitUsesRecordsLock(t *testing.T) {
	s := newUsageLimitStore(t)
	if _, _, err := s.SetUsageLimit(100_000, nil); err != nil {
		t.Fatalf("上限の設定に失敗: %v", err)
	}
	hash := projectHash(t, s)

	// 待ち時間は本テストの主題ではないため短縮する（既定 20 秒）。
	s.lockPolicy.shortTimeout = 200 * time.Millisecond
	lock, err := s.AcquireLock(LockRecords)
	if err != nil {
		t.Fatalf("ロックを取得できません: %v", err)
	}
	if _, _, err := s.SetUsageLimit(999_999, nil); err == nil {
		_ = lock.Release()
		t.Fatal("ロック保持中に上限の変更が成立しました")
	}
	if _, err := s.ClearUsageLimit(); err == nil {
		_ = lock.Release()
		t.Fatal("ロック保持中に上限の解除が成立しました")
	}
	if err := lock.Release(); err != nil {
		t.Fatalf("ロックを解放できません: %v", err)
	}
	if projectHash(t, s) != hash {
		t.Error("ロック保持中の失敗で project.yaml が書き換わった（部分書き込み）")
	}
	if got := s.UsageLimitSetting(); got == nil || got.TokensMax != 100_000 {
		t.Errorf("ロック解放後の値が変わっている: %+v", got)
	}
}

// 不正な値は書き込み前に拒否する（ファイルへ到達させない）。
func TestUsageLimitRejectsInvalidValuesBeforeWriting(t *testing.T) {
	s := newUsageLimitStore(t)
	hash := projectHash(t, s)

	// どの層が・どんな理由で拒否したかまで固定する（多層防御があるため「拒否された」だけでは
	// 入力検証が効いているか分からない）。ここで期待するのは利用者向けの入力エラー文言。
	_, _, err := s.SetUsageLimit(0, nil)
	if err == nil {
		t.Fatal("上限 0 が拒否されなかった")
	}
	if !strings.Contains(err.Error(), "トークン上限は 1 以上の数値で入力してください") {
		t.Errorf("上限 0 の拒否が入力検証によるものでない: %v", err)
	}
	_, _, err = s.SetUsageLimit(100, ratio(1.5))
	if err == nil {
		t.Fatal("警告閾値 1.5 が拒否されなかった")
	}
	if !strings.Contains(err.Error(), "警告閾値は 0 より大きく 1 以下の割合で入力してください") {
		t.Errorf("警告閾値 1.5 の拒否が入力検証によるものでない: %v", err)
	}
	if projectHash(t, s) != hash {
		t.Error("拒否された設定で project.yaml が書き換わった")
	}
	if s.UsageLimitSetting() != nil {
		t.Errorf("拒否された設定が反映された: %+v", s.UsageLimitSetting())
	}
}
