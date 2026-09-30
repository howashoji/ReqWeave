//go:build integration

// 結合テスト（競合検知とマージ）。

package projectstore

import (
	"errors"
	"strings"
	"testing"
	"time"
)

// newConflictStore は要件項目・未決事項・用語を持つプロジェクトを返す。
func newConflictStore(t *testing.T) *Store {
	t.Helper()
	s, _ := newIndexStore(t)
	return s
}

// 基準版と現在が一致する反映は追加の操作なしで書き込む。
func TestGuardedWriteAppliesWhenBaselineMatches(t *testing.T) {
	s := newConflictStore(t)

	base, err := s.CurrentBaseline("FR-INV-001")
	if err != nil {
		t.Fatal(err)
	}
	if base.IsNew() || base.Body == "" {
		t.Fatalf("基準版が取れていない: %+v", base)
	}
	updated, err := s.UpdateRequirementGuarded(base, "FR-INV-001", func(r *Requirement) error {
		r.Body = "出荷指示時に在庫を引き当てること。"
		return nil
	})
	if err != nil {
		t.Fatalf("競合していないのに書き込めない: %v", err)
	}
	if !strings.Contains(updated.Body, "出荷指示時") {
		t.Errorf("反映されていない: %+v", updated)
	}
}

// 基準版と現在が相違する反映は書き込み前に中断し、三面の材料を返す。
func TestGuardedWriteDetectsConflict(t *testing.T) {
	s := newConflictStore(t)
	base, err := s.CurrentBaseline("FR-INV-001")
	if err != nil {
		t.Fatal(err)
	}

	// 他のメンバーの変更（基準版を取った後に入る）。
	if _, err := s.updateRequirement("FR-INV-001", func(r *Requirement) error {
		r.Body = "他のメンバーが書き換えた本文。"
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	otherBody, err := s.CurrentBaseline("FR-INV-001")
	if err != nil {
		t.Fatal(err)
	}

	_, err = s.UpdateRequirementGuarded(base, "FR-INV-001", func(r *Requirement) error {
		r.Body = "自分の反映案。"
		return nil
	})
	var conflict *ErrRecordConflict
	if !errors.As(err, &conflict) {
		t.Fatalf("競合として中断されない: %v", err)
	}
	if len(conflict.Conflicts) != 1 {
		t.Fatalf("競合の件数が違う: %+v", conflict.Conflicts)
	}
	c := conflict.Conflicts[0]
	if c.ID != "FR-INV-001" {
		t.Errorf("競合の対象が違う: %+v", c)
	}
	if !strings.Contains(c.BaselineBody, "受注確定時") {
		t.Errorf("基準版の内容が返らない: %.60s", c.BaselineBody)
	}
	if !strings.Contains(c.CurrentBody, "他のメンバーが書き換えた本文") {
		t.Errorf("現在の内容が返らない: %.60s", c.CurrentBody)
	}
	if c.CurrentHash != otherBody.Hash {
		t.Errorf("現在のハッシュが違う: %q", c.CurrentHash)
	}
	// 中断したので書き換わっていない（承認なしに反映しない）。
	after, err := s.LoadRequirement("FR-INV-001")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(after.Body, "自分の反映案") {
		t.Errorf("競合したのに書き込まれた: %+v", after)
	}

	// マージの承認 = 現在の内容を新しい基準版として再実行する（「自分の案を通す」選択）。
	merged, err := s.UpdateRequirementGuarded(
		RecordBaseline{ID: c.ID, Hash: c.CurrentHash, Body: c.CurrentBody},
		"FR-INV-001", func(r *Requirement) error {
			r.Body = "自分の反映案。"
			return nil
		})
	if err != nil {
		t.Fatalf("マージ承認後の反映に失敗: %v", err)
	}
	if !strings.Contains(merged.Body, "自分の反映案") {
		t.Errorf("マージが反映されていない: %+v", merged)
	}
}

// 未決事項・用語も同じ規則で守られる（決着・用語更新）。
func TestGuardedWriteCoversIssuesAndTerms(t *testing.T) {
	s := newConflictStore(t)
	d, err := s.CreateDecision(Decision{TopicKey: "background/scope", Body: "決めた。",
		Evidence: []string{"S-0001#utt-00009"}})
	if err != nil {
		t.Fatal(err)
	}

	issueBase, err := s.CurrentBaseline("ISS-001")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.updateOpenIssue("ISS-001", func(i *OpenIssue) error {
		i.Owner = "他のメンバーが変えた担当"
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	var conflict *ErrRecordConflict
	if _, err := s.ResolveOpenIssueGuarded(issueBase, "ISS-001", d.ID); !errors.As(err, &conflict) {
		t.Fatalf("未決事項の競合が検知されない: %v", err)
	}

	termBase, err := s.CurrentBaseline(TermIndexID("在庫引当"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.upsertTerm(Term{Name: "在庫引当", NameEn: "Stock Allocation",
		Definition: "他のメンバーが直した定義。"}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.UpsertTermGuarded(termBase, Term{Name: "在庫引当", NameEn: "Stock Allocation",
		Definition: "自分の定義。"}); !errors.As(err, &conflict) {
		t.Fatalf("用語の競合が検知されない: %v", err)
	}
	// 別の用語の追加は競合しない（新規作成）。
	if _, err := s.UpsertTermGuarded(RecordBaseline{}, Term{Name: "バックオーダー",
		NameEn: "back-order", Definition: "欠品時の未出荷受注。"}); err != nil {
		t.Fatalf("新規の用語追加が競合になった: %v", err)
	}

	// 新規作成のつもりで既に他のメンバーが作っていた場合は競合（並行作成）。
	newBase, err := s.CurrentBaseline(TermIndexID("引当単位"))
	if err != nil {
		t.Fatal(err)
	}
	if !newBase.IsNew() {
		t.Fatalf("未作成の用語が新規と判定されない: %+v", newBase)
	}
	if _, err := s.upsertTerm(Term{Name: "引当単位", NameEn: "allocation unit",
		Definition: "他のメンバーが先に追加した定義。"}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.UpsertTermGuarded(newBase, Term{Name: "引当単位", NameEn: "allocation unit",
		Definition: "自分の定義。"}); !errors.As(err, &conflict) {
		t.Fatalf("並行作成が競合として検知されない: %v", err)
	}
}

// 検証はロックの中で行う（待っている間に入った変更も検知する）。
func TestGuardedWriteReverifiesInsideLock(t *testing.T) {
	s := newConflictStore(t)
	base, err := s.CurrentBaseline("FR-INV-001")
	if err != nil {
		t.Fatal(err)
	}
	s.lockPolicy.shortTimeout = 3 * time.Second
	s.lockPolicy.retryMin = 10 * time.Millisecond
	s.lockPolicy.retryMax = 50 * time.Millisecond

	lock, err := s.AcquireLock(LockRecords)
	if err != nil {
		t.Fatal(err)
	}

	// ロック待ちの間に他のメンバーの変更が入る状況を作る。
	done := make(chan error, 1)
	go func() {
		_, werr := s.UpdateRequirementGuarded(base, "FR-INV-001", func(r *Requirement) error {
			r.Body = "待たされた側の反映案。"
			return nil
		})
		done <- werr
	}()

	time.Sleep(100 * time.Millisecond)
	if _, err := s.updateRequirement("FR-INV-001", func(r *Requirement) error {
		r.Body = "ロック保持中に他のメンバーが入れた変更。"
		return nil
	}); err != nil {
		_ = lock.Release()
		t.Fatal(err)
	}
	if err := lock.Release(); err != nil {
		t.Fatal(err)
	}

	var conflict *ErrRecordConflict
	if err := <-done; !errors.As(err, &conflict) {
		t.Fatalf("ロック取得後の再検証で競合を検知しない: %v", err)
	}
	after, err := s.LoadRequirement("FR-INV-001")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(after.Body, "待たされた側の反映案") {
		t.Error("待っている間に入った変更を踏み潰した（後勝ち上書き）")
	}
}

// 検証と書き込みは locks/records の中で行う（保持中は成立しない）。
func TestGuardedWriteWaitsForRecordsLock(t *testing.T) {
	s := newConflictStore(t)
	base, err := s.CurrentBaseline("FR-INV-001")
	if err != nil {
		t.Fatal(err)
	}
	s.lockPolicy.shortTimeout = 200 * time.Millisecond
	lock, err := s.AcquireLock(LockRecords)
	if err != nil {
		t.Fatal(err)
	}

	if _, err := s.UpdateRequirementGuarded(base, "FR-INV-001", func(r *Requirement) error {
		r.Body = "ロック中の書き込み。"
		return nil
	}); err == nil {
		_ = lock.Release()
		t.Fatal("ロック保持中に書き込みが成立した")
	}
	after, err := s.LoadRequirement("FR-INV-001")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(after.Body, "ロック中の書き込み") {
		t.Fatal("部分書き込みが発生している")
	}
	if err := lock.Release(); err != nil {
		t.Fatal(err)
	}
	if _, err := s.UpdateRequirementGuarded(base, "FR-INV-001", func(r *Requirement) error {
		r.Body = "解放後の書き込み。"
		return nil
	}); err != nil {
		t.Fatalf("解放後の書き込みに失敗: %v", err)
	}
}

// 決定事項は追記のみのため、競合は「同一論点キーへの並行決定」= 重複として検知する。
func TestDuplicateDecisionOnSameTopic(t *testing.T) {
	s := newConflictStore(t)

	dup, err := s.DuplicateDecisionOf("background/current-state")
	if err != nil {
		t.Fatal(err)
	}
	if dup == nil || dup.ID != "DEC-001" {
		t.Fatalf("同一論点キーの既存決定が検知されない: %+v", dup)
	}
	// 覆された決定は対象にしない（整理済みのため）。
	if err := s.markSuperseded("DEC-001", "DEC-999"); err != nil {
		t.Fatal(err)
	}
	dup, err = s.DuplicateDecisionOf("background/current-state")
	if err != nil {
		t.Fatal(err)
	}
	if dup != nil {
		t.Errorf("覆された決定が重複として返った: %+v", dup)
	}
	// 別の論点キーは重複でない。
	none, err := s.DuplicateDecisionOf("business-flow/main-flow")
	if err != nil {
		t.Fatal(err)
	}
	if none != nil {
		t.Errorf("別論点が重複として返った: %+v", none)
	}
}
