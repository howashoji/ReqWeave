//go:build integration

// 結合テスト（確定版の版番号の仮採番と再採番）。

package projectstore

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// confirmVersion はドラフトを 1 版確定する（章 1 つ）。
func confirmVersion(t *testing.T, s *Store, kind, body string) *DocumentVersion {
	t.Helper()
	chapters := []DocumentChapter{{
		DocKind: kind, Chapter: "functional-requirements", Status: DocStatusGenerated,
		GeneratedAt: time.Now().UTC(), Covers: []string{"FR-INV-001"},
		FileName: "06-functional-requirements.md", Body: body,
	}}
	if err := s.ReplaceDraft(kind, chapters); err != nil {
		t.Fatalf("ドラフトを置けない: %v", err)
	}
	v, err := s.ConfirmDraft(kind, []string{"FR-INV-001"}, map[string][]string{"functional-requirements": {"FR-INV-001"}})
	if err != nil {
		t.Fatalf("確定できない: %v", err)
	}
	return v
}

// 確定時に confirmed_by / confirmed_at を記録する（再採番の決定手順の入力）。
func TestConfirmRecordsConfirmedBy(t *testing.T) {
	s := newMemberStore(t)
	got := confirmVersion(t, s, DocKindRequirements, "本文 v1")
	if got.Version != 1 || got.ConfirmedBy != testAuthor().AuthorID || got.ConfirmedAt.IsZero() {
		t.Fatalf("確定の記録が違う: %+v", got)
	}
	if got.ConfirmedAt.Location() != time.UTC {
		t.Errorf("確定日時が UTC ではない: %v", got.ConfirmedAt)
	}
	raw, err := os.ReadFile(filepath.Join(s.Root(), filepath.FromSlash(VersionsFile(DocKindRequirements))))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), "confirmed_by: "+testAuthor().AuthorID) {
		t.Errorf("versions.md に確定者が書かれていない:\n%s", raw)
	}
	// 次の確定は「見えている最大版 + 1」の仮採番。
	if second := confirmVersion(t, s, DocKindRequirements, "本文 v2"); second.Version != 2 {
		t.Errorf("仮採番が違う: %+v", second)
	}
}

// 再採番はフォルダ名・versions.md・章の版番号だけを変え、確定版の内容を変えない。
func TestRenumberVersionKeepsContent(t *testing.T) {
	s := newMemberStore(t)
	confirmVersion(t, s, DocKindRequirements, "本文 v1")
	confirmVersion(t, s, DocKindRequirements, "本文 v2")

	before, err := s.LoadVersion(DocKindRequirements, 2)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.RenumberVersion(DocKindRequirements, 2, 3); err != nil {
		t.Fatalf("再採番に失敗: %v", err)
	}

	// 旧フォルダは消え、新フォルダに同じ内容がある。
	if _, err := os.Stat(filepath.Join(s.Root(), filepath.FromSlash(VersionDir(DocKindRequirements, 2)))); !os.IsNotExist(err) {
		t.Errorf("旧フォルダが残っている: %v", err)
	}
	after, err := s.LoadVersion(DocKindRequirements, 3)
	if err != nil {
		t.Fatalf("付け替え後の確定版を読めない: %v", err)
	}
	if len(after) != len(before) || after[0].Body != before[0].Body || after[0].FileName != before[0].FileName {
		t.Errorf("確定版の内容が変わった: %+v → %+v", before, after)
	}
	if after[0].Version != 3 {
		t.Errorf("章の版番号が付け替えられていない: %+v", after[0])
	}
	if after[0].Status != DocStatusConfirmed {
		t.Errorf("章の状態が変わった: %+v", after[0])
	}

	// 版履歴の版番号だけが変わり、確定日時・確定者・収載ソースは変わらない。
	versions, err := s.Versions(DocKindRequirements)
	if err != nil {
		t.Fatal(err)
	}
	if len(versions) != 2 || versions[0].Version != 1 || versions[1].Version != 3 {
		t.Fatalf("版履歴の版番号が違う: %+v", versions)
	}
	if versions[1].ConfirmedBy != testAuthor().AuthorID || versions[1].ConfirmedAt.IsZero() ||
		len(versions[1].SourceIDs) != 1 {
		t.Errorf("版履歴の他の項目が変わった: %+v", versions[1])
	}
	// v1 は影響を受けない。
	if _, err := s.LoadVersion(DocKindRequirements, 1); err != nil {
		t.Errorf("他の確定版が壊れた: %v", err)
	}

	// 付け替え先が既にある・存在しない版・同じ番号は理由つきで拒否する。
	if err := s.RenumberVersion(DocKindRequirements, 3, 1); err == nil {
		t.Error("既にある版番号へ付け替えられた")
	}
	if err := s.RenumberVersion(DocKindRequirements, 9, 4); err == nil {
		t.Error("存在しない版を付け替えられた")
	}
	if err := s.RenumberVersion(DocKindRequirements, 1, 1); err == nil {
		t.Error("同じ版番号への付け替えが受理された")
	}
}

// 取り込み時の衝突を、相手の版履歴と突き合わせて解消する（自分が遅い側なら付け替え）。
func TestResolveVersionCollisions(t *testing.T) {
	s := newMemberStore(t)
	confirmVersion(t, s, DocKindRequirements, "自分の v1")

	// 相手は同じ v1 を**先に**確定していた（自分が付け替える側）。
	theirs := []byte("versions:\n" +
		"  - version: 1\n" +
		"    confirmed_at: 2020-01-01T00:00:00Z\n" +
		"    confirmed_by: y.suzuki@example.co.jp\n" +
		"    source_ids: [FR-INV-002]\n")

	var recorded []VersionRenumber
	resolver := VersionRenumberResolver{Store: s, OnRenumber: func(r VersionRenumber) { recorded = append(recorded, r) }}
	applied, resolved, err := resolver.ResolveVersionCollisions(s.Root(), map[string][]byte{DocKindRequirements: theirs})
	if err != nil {
		t.Fatalf("衝突の解消に失敗: %v", err)
	}
	if applied != 1 || len(recorded) != 1 || recorded[0] != (VersionRenumber{Kind: DocKindRequirements, From: 1, To: 2}) {
		t.Fatalf("再採番の結果が違う: applied=%d recorded=%+v", applied, recorded)
	}
	// 相手の版履歴を取り込んだので、統合では自分側を採ってよい（統合済みのパスとして返る）。
	if len(resolved) != 1 || resolved[0] != VersionsFile(DocKindRequirements) {
		t.Errorf("統合済みのパスが違う: %+v", resolved)
	}
	if versions, err := s.Versions(DocKindRequirements); err != nil || len(versions) != 2 {
		t.Errorf("相手の版履歴が取り込まれていない: %+v %v", versions, err)
	}
	chapters, err := s.LoadVersion(DocKindRequirements, 2)
	if err != nil || len(chapters) == 0 || chapters[0].Body != "自分の v1" {
		t.Errorf("付け替え後に自分の内容が読めない: %+v %v", chapters, err)
	}

	// もう一度同じ相手と突き合わせても、今度は衝突せず書き換えも起きない（べき等）。
	applied, resolved, err = resolver.ResolveVersionCollisions(s.Root(), map[string][]byte{DocKindRequirements: theirs})
	if err != nil || applied != 0 || len(resolved) != 0 {
		t.Errorf("同じ突き合わせで再び付け替えた: applied=%d resolved=%v err=%v", applied, resolved, err)
	}

	// 別のプロジェクトの作業コピーを指定された場合は何もしない。
	applied, _, err = resolver.ResolveVersionCollisions(t.TempDir(), map[string][]byte{DocKindRequirements: theirs})
	if err != nil || applied != 0 {
		t.Errorf("別のプロジェクトを書き換えた: applied=%d err=%v", applied, err)
	}
}

// 自分が早い側なら付け替えない（相手が付け替える）。
func TestResolveVersionCollisionsKeepsEarlier(t *testing.T) {
	s := newMemberStore(t)
	confirmVersion(t, s, DocKindRequirements, "自分の v1")
	theirs := []byte("versions:\n" +
		"  - version: 1\n" +
		"    confirmed_at: 2099-01-01T00:00:00Z\n" +
		"    confirmed_by: y.suzuki@example.co.jp\n")

	resolver := VersionRenumberResolver{Store: s}
	applied, _, err := resolver.ResolveVersionCollisions(s.Root(), map[string][]byte{DocKindRequirements: theirs})
	if err != nil || applied != 0 {
		t.Fatalf("早い側が付け替えられた: applied=%d err=%v", applied, err)
	}
	if _, err := s.LoadVersion(DocKindRequirements, 1); err != nil {
		t.Errorf("自分の v1 が失われた: %v", err)
	}
}
