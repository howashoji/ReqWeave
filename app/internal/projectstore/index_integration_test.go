//go:build integration

// 結合テスト（派生インデックスと内容ハッシュ）。

package projectstore

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// newIndexStore は決定・未決・要件・用語を持つプロジェクトと、キャッシュ領域を返す。
func newIndexStore(t *testing.T) (*Store, AppPaths) {
	t.Helper()
	base := t.TempDir()
	paths := AppPaths{Base: filepath.Join(base, AppID)}
	root := filepath.Join(base, "proj")
	s, err := CreateProject(root, CreateOptions{TargetSystemName: "在庫管理システム", Author: testAuthor()})
	if err != nil {
		t.Fatalf("プロジェクト作成に失敗: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })

	if _, err := s.CreateDecision(Decision{TopicKey: "background/current-state",
		Body: "現状は Excel 台帳。", Evidence: []string{"S-0001#utt-00001"}}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.CreateOpenIssue(OpenIssue{Owner: "倉庫長", Body: "棚卸の頻度",
		Evidence: []string{"S-0001#utt-00002"}}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.CreateRequirement("INV", Requirement{Title: "在庫引当",
		Chapter: "functional-requirements", Kind: RequirementFunctional, Priority: PriorityMust,
		Body: "受注確定時に在庫を引き当てること。", Evidence: []string{"S-0001#utt-00003"}}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.upsertTerm(Term{Name: "在庫引当", NameEn: "Stock Allocation",
		Definition: "受注に対して在庫を確保すること。"}); err != nil {
		t.Fatal(err)
	}
	return s, paths
}

// 共有レコードの内容ハッシュを索引で保持する。
func TestRecordIndexHashesRecords(t *testing.T) {
	s, paths := newIndexStore(t)

	ix, err := LoadRecordIndex(paths, s)
	if err != nil {
		t.Fatalf("索引を作れない: %v", err)
	}
	for _, id := range []string{"DEC-001", "ISS-001", "FR-INV-001", TermIndexID("在庫引当")} {
		hash, ok := ix.Hash(id)
		if !ok || hash == "" {
			t.Fatalf("%s のハッシュが無い: %+v", id, ix.IDs())
		}
	}
	// 索引はアプリ設定領域に置き、プロジェクトフォルダを汚さない。
	if _, err := os.Stat(indexPath(paths, s.Project().ProjectID)); err != nil {
		t.Errorf("索引がアプリ設定領域に保存されていない: %v", err)
	}
	if _, err := os.Stat(filepath.Join(s.Root(), "index.json")); !os.IsNotExist(err) {
		t.Errorf("プロジェクトフォルダに索引が作られた: %v", err)
	}

	// 同一内容なら同一値、内容が変われば別値。
	same, err := LoadRecordIndex(paths, s)
	if err != nil {
		t.Fatal(err)
	}
	before, _ := ix.Hash("FR-INV-001")
	after, _ := same.Hash("FR-INV-001")
	if before != after {
		t.Errorf("内容が同じなのにハッシュが変わった: %q → %q", before, after)
	}

	if _, err := s.updateRequirement("FR-INV-001", func(r *Requirement) error {
		r.Body = "出荷指示時に在庫を引き当てること。"
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	updated, err := LoadRecordIndex(paths, s)
	if err != nil {
		t.Fatal(err)
	}
	changed, _ := updated.Hash("FR-INV-001")
	if changed == before {
		t.Errorf("内容を変えてもハッシュが変わらない: %q", changed)
	}
	// 変えていないレコードのハッシュは変わらない。
	decBefore, _ := ix.Hash("DEC-001")
	decAfter, _ := updated.Hash("DEC-001")
	if decBefore != decAfter {
		t.Errorf("無関係のレコードのハッシュが変わった: %q → %q", decBefore, decAfter)
	}
}

// 通常の読み込みでは実体を読み直さない。
// 更新時刻の食い違い（利用者の直接編集・復元）を検出したときだけ全再構築する。
func TestRecordIndexRebuildsOnlyWhenInconsistent(t *testing.T) {
	s, paths := newIndexStore(t)
	ix, err := LoadRecordIndex(paths, s)
	if err != nil {
		t.Fatal(err)
	}

	// 索引の中身を書き換えても、更新時刻が同じレコードは読み直さない（差分再読込の確認）。
	ix.Entries["DEC-001"] = IndexEntry{
		ID: "DEC-001", File: ix.Entries["DEC-001"].File,
		ModTime: ix.Entries["DEC-001"].ModTime, Hash: "sentinel-not-recomputed",
	}
	if err := ix.save(paths); err != nil {
		t.Fatal(err)
	}
	reloaded, err := LoadRecordIndex(paths, s)
	if err != nil {
		t.Fatal(err)
	}
	if got, _ := reloaded.Hash("DEC-001"); got != "sentinel-not-recomputed" {
		t.Errorf("食い違いが無いのに実体を読み直した（通常操作で再読込しない）: %q", got)
	}

	// 直接編集（更新時刻が変わる）は不整合として検出し、全再構築で最新化する。
	path := filepath.Join(s.Root(), filepath.FromSlash(DecisionFile("DEC-001")))
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, append(data, []byte("\n他端末による追記。\n")...), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(path, time.Now().Add(time.Second), time.Now().Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	after, err := LoadRecordIndex(paths, s)
	if err != nil {
		t.Fatal(err)
	}
	got, _ := after.Hash("DEC-001")
	if got == "sentinel-not-recomputed" || got == "" {
		t.Errorf("不整合を検出した全再構築が行われていない: %q", got)
	}
	if got != contentHashOfFile(t, path) {
		t.Errorf("索引のハッシュが実ファイルと一致しない")
	}
}

// 索引が不在・不整合なら全再構築する（正はファイル側）。
func TestRecordIndexRebuildsWhenBroken(t *testing.T) {
	s, paths := newIndexStore(t)
	if _, err := LoadRecordIndex(paths, s); err != nil {
		t.Fatal(err)
	}
	path := indexPath(paths, s.Project().ProjectID)

	// 壊れた索引・別プロジェクトの索引はどちらも全再構築される。
	for _, broken := range []string{"{壊れた JSON", `{"projectId":"other","entries":{}}`} {
		if err := os.WriteFile(path, []byte(broken), 0o644); err != nil {
			t.Fatal(err)
		}
		ix, err := LoadRecordIndex(paths, s)
		if err != nil {
			t.Fatalf("再構築に失敗: %v", err)
		}
		if _, ok := ix.Hash("DEC-001"); !ok {
			t.Fatalf("再構築されていない（%s）: %+v", broken, ix.IDs())
		}
		if ix.ProjectID != s.Project().ProjectID {
			t.Errorf("別プロジェクトの索引が使われた: %+v", ix.ProjectID)
		}
	}

	// 索引が無い状態からも作れる。
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	ix, err := LoadRecordIndex(paths, s)
	if err != nil {
		t.Fatal(err)
	}
	if len(ix.IDs()) < 4 {
		t.Errorf("不在からの再構築が不足している: %+v", ix.IDs())
	}
	// 実体が消えたレコードは索引から落ちる。
	if err := os.Remove(filepath.Join(s.Root(), filepath.FromSlash(DecisionFile("DEC-001")))); err != nil {
		t.Fatal(err)
	}
	after, err := LoadRecordIndex(paths, s)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := after.Hash("DEC-001"); ok {
		t.Errorf("消えたレコードが索引に残っている: %+v", after.IDs())
	}
}

// 用語は 1 ファイルに同居するため、更新時に全件のハッシュを計算し直す。
func TestRecordIndexHandlesTerms(t *testing.T) {
	s, paths := newIndexStore(t)
	ix, err := LoadRecordIndex(paths, s)
	if err != nil {
		t.Fatal(err)
	}
	before, ok := ix.Hash(TermIndexID("在庫引当"))
	if !ok {
		t.Fatalf("用語のハッシュが無い: %+v", ix.IDs())
	}

	if _, err := s.upsertTerm(Term{Name: "在庫引当", NameEn: "Stock Allocation",
		Definition: "受注に対して在庫を確保すること（改訂）。"}); err != nil {
		t.Fatal(err)
	}
	after, err := LoadRecordIndex(paths, s)
	if err != nil {
		t.Fatal(err)
	}
	got, _ := after.Hash(TermIndexID("在庫引当"))
	if got == before {
		t.Errorf("用語の内容を変えてもハッシュが変わらない: %q", got)
	}
	if !strings.HasPrefix(after.IDs()[0], "DEC-") && !strings.HasPrefix(after.IDs()[0], "FR-") {
		t.Errorf("索引の並びが昇順でない: %+v", after.IDs())
	}
}

func contentHashOfFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return contentHash(data)
}

// 取り込みで変更されたパスだけを索引へ反映する（全再構築しない）。
func TestRecordIndexApplyChangedPaths(t *testing.T) {
	s, paths := newIndexStore(t)
	ix, err := LoadRecordIndex(paths, s)
	if err != nil {
		t.Fatal(err)
	}
	// 変更していないレコードを読み直さないことを見るための目印を索引へ置く。
	untouched := ix.Entries["FR-INV-001"]
	untouched.Hash = "sentinel-not-recomputed"
	ix.Entries["FR-INV-001"] = untouched

	// 取り込みで DEC-001 が変わり、ISS-900 が増え、DEC-002 が消えた状況を作る。
	decPath := filepath.Join(s.Root(), filepath.FromSlash(DecisionFile("DEC-001")))
	data, err := os.ReadFile(decPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(decPath, append(data, []byte("\n取り込みで入った追記。\n")...), 0o644); err != nil {
		t.Fatal(err)
	}
	added := filepath.Join(s.Root(), filepath.FromSlash(OpenIssueFile("ISS-900")))
	if err := os.WriteFile(added, []byte("---\nid: ISS-900\n---\n取り込みで入った未決。\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	removedID := "DEC-002"
	ix.Entries[removedID] = IndexEntry{ID: removedID, File: DecisionFile(removedID), Hash: "gone"}

	changed := []string{DecisionFile("DEC-001"), OpenIssueFile("ISS-900"), DecisionFile(removedID),
		"sessions/S-0001.md", "audit/history/2026-09.x.ndjson"}
	if err := ix.ApplyChangedPaths(paths, s, changed); err != nil {
		t.Fatalf("取り込みの反映に失敗: %v", err)
	}

	if got, _ := ix.Hash("DEC-001"); got != contentHashOfFile(t, decPath) {
		t.Errorf("変更されたレコードが反映されていない: %q", got)
	}
	if got, _ := ix.Hash("ISS-900"); got != contentHashOfFile(t, added) {
		t.Errorf("追加されたレコードが反映されていない: %q", got)
	}
	if _, ok := ix.Hash(removedID); ok {
		t.Error("実体の無いレコードが索引に残っている")
	}
	if got, _ := ix.Hash("FR-INV-001"); got != "sentinel-not-recomputed" {
		t.Errorf("変更されていないレコードを読み直した（差分再構築になっていない）: %q", got)
	}

	// 反映結果はキャッシュへ保存され、次の読み込みでそのまま使われる。
	reloaded, err := LoadRecordIndex(paths, s)
	if err != nil {
		t.Fatal(err)
	}
	if got, _ := reloaded.Hash("ISS-900"); got == "" {
		t.Error("反映結果が保存されていない")
	}
}

// terms.yaml が取り込みで変わったときは用語の索引を作り直す（1 ファイルに同居するため）。
func TestRecordIndexApplyChangedPathsRebuildsTerms(t *testing.T) {
	s, paths := newIndexStore(t)
	ix, err := LoadRecordIndex(paths, s)
	if err != nil {
		t.Fatal(err)
	}
	before, hadTerm := ix.Hash(TermIndexID("在庫引当"))
	if !hadTerm {
		t.Fatal("前提の用語が索引に無い")
	}
	path := filepath.Join(s.Root(), FileTerms)
	if err := os.WriteFile(path, []byte("terms:\n  - name: 在庫引当\n    name_en: stock\n    definition: 取り込みで変わった定義\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := ix.ApplyChangedPaths(paths, s, []string{FileTerms}); err != nil {
		t.Fatalf("用語の反映に失敗: %v", err)
	}
	after, ok := ix.Hash(TermIndexID("在庫引当"))
	if !ok || after == before {
		t.Errorf("用語の索引が作り直されていない: %q → %q", before, after)
	}
}
