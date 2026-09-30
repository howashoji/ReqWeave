package sync

// 確定版の版番号の衝突が、取り込みで内容を変えずに解消されることを、実際の git と
// 共有フォルダ相当の bare リポジトリで検証する。

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/howashoji/ReqWeave/app/internal/projectstore"
)

// clientWithVersions は版番号の衝突解消を配線した Client を返す（アプリ本体の配線と同じ構成）。
func clientWithVersions(t *testing.T, author projectstore.Author, store *projectstore.Store,
	onRenumber func(projectstore.VersionRenumber)) *Client {
	t.Helper()
	c, err := New(Options{
		Author:    author,
		ConfigDir: filepath.Join(t.TempDir(), "sync"),
		Versions:  projectstore.VersionRenumberResolver{Store: store, OnRenumber: onRenumber},
	})
	if err != nil {
		t.Fatal(err)
	}
	if av := c.Availability(); !av.Available {
		t.Fatalf("この環境に git が無いため検証できない: %s", av.Reason)
	}
	return c
}

// putDraft はドラフトを置く（章 1 つ）。生成日時を固定するため、双方の作業コピーで同じ内容になる。
func putDraft(t *testing.T, s *projectstore.Store, body string) {
	t.Helper()
	chapters := []projectstore.DocumentChapter{{
		DocKind: projectstore.DocKindRequirements, Chapter: "functional-requirements",
		Status: projectstore.DocStatusGenerated, GeneratedAt: time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC),
		Covers: []string{"FR-INV-001"}, FileName: "06-functional-requirements.md", Body: body,
	}}
	if err := s.ReplaceDraft(projectstore.DocKindRequirements, chapters); err != nil {
		t.Fatalf("ドラフトを置けない: %v", err)
	}
}

// confirmDraft はドラフトを確定する（仮採番）。
func confirmDraft(t *testing.T, s *projectstore.Store) *projectstore.DocumentVersion {
	t.Helper()
	v, err := s.ConfirmDraft(projectstore.DocKindRequirements, []string{"FR-INV-001"},
		map[string][]string{"functional-requirements": {"FR-INV-001"}})
	if err != nil {
		t.Fatalf("確定できない: %v", err)
	}
	return v
}

// 双方が同じ版番号で確定していた場合、取り込みで**内容を変えずに**版番号だけを
// 再採番し、再採番の事実（旧番号・新番号・対象種別）を呼び出し側へ通知する。
func TestVersionCollisionIsRenumberedOnIncorporate(t *testing.T) {
	// A のプロジェクトを作り B をメンバーに登録して反映 → B が取得。
	rootA := filepath.Join(t.TempDir(), "A", "proj")
	storeA := newProject(t, rootA, authorA)
	if _, err := storeA.AddMember(authorB.AuthorID, authorB.DisplayName, projectstore.RoleEditor); err != nil {
		t.Fatal(err)
	}
	a := clientWithVersions(t, authorA, storeA, nil)
	remote := folderRemote(t)
	// ドラフトまでは共有された状態にする（双方のドラフトが同一なので、確定操作だけが衝突する）。
	putDraft(t, storeA, "共有されたドラフト本文")
	mustPublish(t, a, rootA, remote, true)

	rootB := filepath.Join(t.TempDir(), "B", "proj")
	bClone := newClient(t, authorB)
	if _, err := bClone.Clone(context.Background(), CloneOptions{Remote: remote, Dest: rootB}); err != nil {
		t.Fatalf("取得に失敗: %v", err)
	}
	storeB := openStore(t, rootB, authorB)

	// 双方が同期先へ触れずに同じドラフトを確定する（仮採番）。**B を先に確定**するので B の confirmed_at が早い。
	vB := confirmDraft(t, storeB)
	vA := confirmDraft(t, storeA)
	if vA.Version != 1 || vB.Version != 1 {
		t.Fatalf("双方が v1 を仮採番していない: A=%+v B=%+v", vA, vB)
	}
	if !vB.ConfirmedAt.Before(vA.ConfirmedAt) {
		t.Fatalf("確定日時の前後が想定と違う: A=%v B=%v", vA.ConfirmedAt, vB.ConfirmedAt)
	}

	// B が反映 → A が取り込む。A は自分の v1 を v2 へ付け替える（B の方が早いため）。
	b := clientWithVersions(t, authorB, storeB, nil)
	mustPublish(t, b, rootB, remote, false)

	var renumbered []projectstore.VersionRenumber
	a = clientWithVersions(t, authorA, storeA, func(r projectstore.VersionRenumber) {
		renumbered = append(renumbered, r)
	})
	mustIncorporate(t, a, rootA, remote)

	if len(renumbered) != 1 || renumbered[0].From != 1 || renumbered[0].To != 2 ||
		renumbered[0].Kind != projectstore.DocKindRequirements {
		t.Fatalf("再採番の通知が違う: %+v", renumbered)
	}
	versions, err := storeA.Versions(projectstore.DocKindRequirements)
	if err != nil {
		t.Fatal(err)
	}
	if len(versions) != 2 {
		t.Fatalf("取り込み後の版履歴が違う: %+v", versions)
	}
	// v1 = B の確定（早い方が番号を保持）、v2 = A の確定（内容は変わらない）。
	if versions[0].ConfirmedBy != authorB.AuthorID || versions[1].ConfirmedBy != authorA.AuthorID {
		t.Fatalf("版番号の保持者が違う: %+v", versions)
	}
	if !versions[0].ConfirmedAt.Equal(vB.ConfirmedAt) || !versions[1].ConfirmedAt.Equal(vA.ConfirmedAt) {
		t.Errorf("確定日時が変わった: %+v", versions)
	}
	v1, err := storeA.LoadVersion(projectstore.DocKindRequirements, 1)
	if err != nil || len(v1) == 0 || v1[0].Body != "共有されたドラフト本文" {
		t.Errorf("v1 の内容が読めない: %+v %v", v1, err)
	}
	v2, err := storeA.LoadVersion(projectstore.DocKindRequirements, 2)
	if err != nil || len(v2) == 0 || v2[0].Body != "共有されたドラフト本文" {
		t.Errorf("v2 の内容が変わった: %+v %v", v2, err)
	}
	if v2[0].Version != 2 {
		t.Errorf("章の版番号が付け替えられていない: %+v", v2[0])
	}

	// A が反映すると、B から見ても同じ結果になる（双方が同じ規則で判断するため矛盾しない）。
	mustPublish(t, a, rootA, remote, false)
	b = clientWithVersions(t, authorB, storeB, func(r projectstore.VersionRenumber) {
		t.Errorf("早い側が付け替えられた: %+v", r)
	})
	mustIncorporate(t, b, rootB, remote)
	versionsB, err := storeB.Versions(projectstore.DocKindRequirements)
	if err != nil {
		t.Fatal(err)
	}
	if len(versionsB) != 2 || versionsB[0].Version != 1 || versionsB[1].Version != 2 {
		t.Fatalf("B 側の版履歴が違う: %+v", versionsB)
	}
	if versionsB[0].ConfirmedBy != authorB.AuthorID || versionsB[1].ConfirmedBy != authorA.AuthorID {
		t.Errorf("B 側から見た保持者が A 側と食い違う: %+v", versionsB)
	}
	if _, err := storeB.LoadVersion(projectstore.DocKindRequirements, 1); err != nil {
		t.Errorf("B 側の v1 が失われた: %v", err)
	}
	if _, err := storeB.LoadVersion(projectstore.DocKindRequirements, 2); err != nil {
		t.Errorf("B 側に A の確定版が入っていない: %v", err)
	}
}

// 衝突が無い取り込みでは再採番しない（確定版に触れない）。
func TestNoVersionCollisionLeavesVersionsUntouched(t *testing.T) {
	rootA := filepath.Join(t.TempDir(), "A", "proj")
	storeA := newProject(t, rootA, authorA)
	if _, err := storeA.AddMember(authorB.AuthorID, authorB.DisplayName, projectstore.RoleEditor); err != nil {
		t.Fatal(err)
	}
	a := clientWithVersions(t, authorA, storeA, nil)
	remote := folderRemote(t)
	putDraft(t, storeA, "A の確定内容")
	confirmDraft(t, storeA)
	mustPublish(t, a, rootA, remote, true)

	rootB := filepath.Join(t.TempDir(), "B", "proj")
	bClone := newClient(t, authorB)
	if _, err := bClone.Clone(context.Background(), CloneOptions{Remote: remote, Dest: rootB}); err != nil {
		t.Fatalf("取得に失敗: %v", err)
	}
	storeB := openStore(t, rootB, authorB)
	// B は確定せず、別のファイルだけ変更して反映する。
	writeProjectFile(t, rootB, "decisions/DEC-001.md", "---\nid: DEC-001\n---\n決定内容\n")
	b := clientWithVersions(t, authorB, storeB, nil)
	mustPublish(t, b, rootB, remote, false)

	a = clientWithVersions(t, authorA, storeA, func(r projectstore.VersionRenumber) {
		t.Errorf("衝突が無いのに再採番した: %+v", r)
	})
	mustIncorporate(t, a, rootA, remote)
	if v, err := storeA.LoadVersion(projectstore.DocKindRequirements, 1); err != nil ||
		len(v) == 0 || v[0].Body != "A の確定内容" {
		t.Errorf("確定版が変わった: %+v %v", v, err)
	}
}
