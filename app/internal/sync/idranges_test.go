package sync

// ID の番号帯が同期の一連で確保・共有されることを、実際の git と共有フォルダ相当の bare リポジトリで検証する
// 番号帯の実体は projectstore（id-ranges.yaml）。

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/howashoji/ReqWeave/app/internal/projectstore"
)

// clientWithRanges は番号帯の確保を配線した Client を返す（アプリ本体の配線と同じ構成）。
func clientWithRanges(t *testing.T, author projectstore.Author, store *projectstore.Store, width int) *Client {
	t.Helper()
	c, err := New(Options{
		Author:    author,
		ConfigDir: filepath.Join(t.TempDir(), "sync"),
		Ranges:    projectstore.IDRangeReserver{Store: store, Width: width},
	})
	if err != nil {
		t.Fatal(err)
	}
	if av := c.Availability(); !av.Available {
		t.Fatalf("この環境に git が無いため検証できない: %s", av.Reason)
	}
	return c
}

func openStore(t *testing.T, root string, author projectstore.Author) *projectstore.Store {
	t.Helper()
	store, err := projectstore.Open(root, author)
	if err != nil {
		t.Fatalf("作業コピーを開けない: %v", err)
	}
	t.Cleanup(func() { store.Close() })
	return store
}

func createRequirement(t *testing.T, store *projectstore.Store) string {
	t.Helper()
	r, err := store.CreateRequirement("INV", projectstore.Requirement{Title: "在庫引当",
		Chapter: "functional-requirements", Kind: projectstore.RequirementFunctional,
		Priority: projectstore.PriorityMust, Status: projectstore.RequirementDraft,
		Body: "受注確定時に在庫を引き当てること。", Evidence: []string{"S-0001#utt-00001"}})
	if err != nil {
		t.Fatalf("要件項目を作成できない: %v", err)
	}
	return r.ID
}

// 反映のたびに自分の番号帯が確保されて同期先へ載り、
// 2 名が同期先へ到達できない状態で並行に作った ID が重複せず、取り込みで変化しない。
//
// 確保は**反映の直前だけ**に行い、反映の成功をもって確定する（取り込みでは確保しない）。
func TestIDRangesReservedAndSharedThroughSync(t *testing.T) {
	// オーナー A のプロジェクトを作り、B をメンバーに登録して反映する。
	rootA := filepath.Join(t.TempDir(), "A", "proj")
	storeA := newProject(t, rootA, authorA)
	if _, err := storeA.AddMember(authorB.AuthorID, authorB.DisplayName, projectstore.RoleEditor); err != nil {
		t.Fatal(err)
	}
	a := clientWithRanges(t, authorA, storeA, 100)
	remote := folderRemote(t)

	// 反映の直前に番号帯が確保され、同じ反映に含まれる。
	if storeA.UsesIDRanges() {
		t.Fatal("反映前から番号帯モードになっている")
	}
	mustPublish(t, a, rootA, remote, true)
	if !storeA.UsesIDRanges() {
		t.Fatal("反映後も番号帯が確保されていない")
	}
	rangesA, err := storeA.LoadIDRanges()
	if err != nil {
		t.Fatal(err)
	}
	mineA := rangesA.For(projectstore.RangeRequirement, authorA.AuthorID)
	if len(mineA) != 1 || mineA[0].From != 1 || mineA[0].To != 100 {
		t.Fatalf("A の番号帯が違う: %+v", mineA)
	}

	// B が取得すると A の番号帯も一緒に来る（同期対象）。
	rootB := filepath.Join(t.TempDir(), "B", "proj")
	b := clientWithRanges(t, authorB, nil, 100)
	if _, err := b.Clone(context.Background(), CloneOptions{Remote: remote, Dest: rootB}); err != nil {
		t.Fatalf("取得に失敗: %v", err)
	}
	if got := readProjectFile(t, rootB, projectstore.FileIDRanges); !strings.Contains(got, authorA.AuthorID) {
		t.Fatalf("取得した作業コピーに A の番号帯が無い:\n%s", got)
	}
	storeB := openStore(t, rootB, authorB)
	b = clientWithRanges(t, authorB, storeB, 100)

	// 取り込みでは確保しない（同期先へ載らない区間を作らない）。
	mustIncorporate(t, b, rootB, remote)
	rangesB, err := storeB.LoadIDRanges()
	if err != nil {
		t.Fatal(err)
	}
	if mine := rangesB.For(projectstore.RangeRequirement, authorB.AuthorID); len(mine) != 0 {
		t.Fatalf("取り込みで番号帯が確保された（未反映の区間ができる）: %+v", mine)
	}

	// B は反映で自分の番号帯（A の次）を確保する。
	mustPublish(t, b, rootB, remote, false)
	rangesB, err = storeB.LoadIDRanges()
	if err != nil {
		t.Fatal(err)
	}
	mineB := rangesB.For(projectstore.RangeRequirement, authorB.AuthorID)
	if len(mineB) != 1 || mineB[0].From != 101 || mineB[0].To != 200 {
		t.Fatalf("B の番号帯が違う: %+v", mineB)
	}

	// 双方が同期先へ触れずに 3 件ずつ作る（オフライン並行）。
	var idsA, idsB []string
	for i := 0; i < 3; i++ {
		idsA = append(idsA, createRequirement(t, storeA))
		idsB = append(idsB, createRequirement(t, storeB))
	}
	seen := map[string]bool{}
	for _, id := range append(append([]string{}, idsA...), idsB...) {
		if seen[id] {
			t.Errorf("ID が重複した: %s", id)
		}
		seen[id] = true
	}
	if len(seen) != 6 {
		t.Fatalf("採番数が違う: %d（%v / %v）", len(seen), idsA, idsB)
	}

	// B が反映 → A が取り込む。双方の要件項目が揃い、ID は変わらない（参照の張り替えが要らない）。
	mustPublish(t, b, rootB, remote, false)
	mustIncorporate(t, a, rootA, remote)
	for _, id := range append(append([]string{}, idsA...), idsB...) {
		if _, err := storeA.LoadRequirement(id); err != nil {
			t.Errorf("取り込み後に %s が読めない: %v", id, err)
		}
	}
	// 取り込み後の A の採番は自分の区間の続き（相手の区間を侵さない）。
	next := createRequirement(t, storeA)
	if _, _, n, ok := projectstore.ParseRequirementID(next); !ok || n < 1 || n > 100 {
		t.Errorf("取り込み後の採番が自分の区間の外: %s", next)
	}

	// ここまでの確保（反映のたびに 1 回）: A 1-100（初回反映）→ B 101-200（B の初回反映）→
	// B 101-300（B の 2 回目の反映で自分の末尾に連続確保）。取り込みでは増えない。
	rangesA, _ = storeA.LoadIDRanges()
	mineA = rangesA.For(projectstore.RangeRequirement, authorA.AuthorID)
	if len(mineA) != 1 || mineA[0].From != 1 || mineA[0].To != 100 {
		t.Fatalf("取り込みで A の番号帯が増えた（未反映の区間ができる）: %+v", mineA)
	}
	// 反映で確保する。同期先で見えている上限（B の 300）を跨ぐ。
	mustPublish(t, a, rootA, remote, false)
	rangesA, _ = storeA.LoadIDRanges()
	mineA = rangesA.For(projectstore.RangeRequirement, authorA.AuthorID)
	if len(mineA) != 2 || mineA[1].From != 301 || mineA[1].To != 400 {
		t.Fatalf("反映で確保した A の番号帯が違う: %+v", mineA)
	}
	// 他のメンバーの区間は失われず、重ならない。
	others := rangesA.For(projectstore.RangeRequirement, authorB.AuthorID)
	if len(others) != 1 || others[0].From != 101 || others[0].To != 300 {
		t.Fatalf("B の番号帯が失われた・変わった: %+v", others)
	}
	if err := rangesA.Validate(); err != nil {
		t.Errorf("番号帯が重なった: %v", err)
	}
}

// 反映が失敗したら確保を巻き戻す（同期先へ載っていない区間を残さない）。
// 利用者の作業には触れない。
//
// 失敗は**確保のあと**に起こる経路で確かめる（非 fast-forward = 同一利用者が別端末から先に反映した状態。
// 到達できない場合は確保の前に落ちるため、巻き戻しの経路を通らない）。
func TestReservationIsRolledBackWhenPublishFails(t *testing.T) {
	ctx := context.Background()
	remote := folderRemote(t)

	// 端末 1: プロジェクトを作って初回反映（1-100 を確保）
	root1 := filepath.Join(t.TempDir(), "A1", "proj")
	store1 := newProject(t, root1, authorA)
	a1 := clientWithRanges(t, authorA, store1, 100)
	mustPublish(t, a1, root1, remote, true)

	// 端末 2: 同じ利用者が取得する
	root2 := filepath.Join(t.TempDir(), "A2", "proj")
	tmp := clientWithRanges(t, authorA, nil, 100)
	if _, err := tmp.Clone(ctx, CloneOptions{Remote: remote, Dest: root2}); err != nil {
		t.Fatalf("別端末での取得に失敗: %v", err)
	}
	store2 := openStore(t, root2, authorA)
	a2 := clientWithRanges(t, authorA, store2, 100)
	before := readProjectFile(t, root2, projectstore.FileIDRanges)

	// 端末 1 が先に反映して同期先を進める → 端末 2 の反映は非 fast-forward で拒否される
	writeProjectFile(t, root1, "requirements/FR-INV-001.md", "from A1\n")
	mustPublish(t, a1, root1, remote, false)

	writeProjectFile(t, root2, "decisions/DEC-001.md", "---\nid: DEC-001\n---\n決定\n")
	_, err := a2.Publish(ctx, root2, remote, "", PublishOptions{})
	expectFailure(t, err, FailNonFastForward)

	// 確保が巻き戻っている（作業ツリーと履歴の両方）
	if got := readProjectFile(t, root2, projectstore.FileIDRanges); got != before {
		t.Errorf("失敗した反映で確保が作業ツリーに残った:\n%s", got)
	}
	inHead, err := a2.Repo(root2).Git(ctx, "cat-file", "blob", "HEAD:"+projectstore.FileIDRanges)
	if err != nil {
		t.Fatal(err)
	}
	if inHead != before {
		t.Errorf("確保だけのコミットが残っている:\n%s", inHead)
	}
	ranges, err := store2.LoadIDRanges()
	if err != nil {
		t.Fatal(err)
	}
	if mine := ranges.For(projectstore.RangeRequirement, authorA.AuthorID); len(mine) != 1 || mine[0].To != 100 {
		t.Errorf("確保が巻き戻っていない: %+v", mine)
	}
	// 利用者の変更は失われず、次の反映対象として残る
	if readProjectFile(t, root2, "decisions/DEC-001.md") == "" {
		t.Fatal("巻き戻しで利用者の変更が失われた")
	}
	tracked, _ := a2.Repo(root2).Git(ctx, "ls-files", "--", "decisions")
	if !strings.Contains(tracked, "DEC-001.md") {
		t.Errorf("利用者の変更が作業コピーから消えた: %q", tracked)
	}

	// 取り込んでから再反映すると、そこで初めて次の区間が確保されて載る。
	// 端末 1 の 2 回目の反映で 1-200 まで進んでいるため、その次（201-300）が加わって 1-300 にまとまる。
	mustIncorporate(t, a2, root2, remote)
	mustPublish(t, a2, root2, remote, false)
	ranges, _ = store2.LoadIDRanges()
	mine := ranges.For(projectstore.RangeRequirement, authorA.AuthorID)
	if len(mine) != 1 || mine[0].From != 1 || mine[0].To != 300 {
		t.Errorf("再反映で確保されていない（連続する区間はまとめる）: %+v", mine)
	}
}

// 同時反映などで区間が重なった場合、取り込み時に決定的な規則で解消する
// （reserved_at の早い方が保持し、遅い方を最大上限の次へ移す）。双方の作業コピーで結果が一致する。
func TestOverlappingRangesAreResolvedDeterministically(t *testing.T) {
	// A（早い）と B（遅い）が同じ 1-100 を確保してしまった状態を作る
	rangesOf := func(author string, at string) string {
		return "ranges:\n  requirement:\n    - author_id: " + author +
			"\n      from: 1\n      to: 100\n      reserved_at: " + at + "\n"
	}
	early, late := "2026-09-02T09:00:00Z", "2026-09-02T09:00:05Z"

	// (1) B 側で取り込む（相手 = A が早い → A が保持し、B が移る）
	a, _, rootA, rootB, remote := setupShared(t)
	b := newClientWith(t, authorB, always(Resolution{Choice: ChoiceOurs}), nil)
	writeProjectFile(t, rootA, projectstore.FileIDRanges, rangesOf(authorA.AuthorID, early))
	mustPublish(t, a, rootA, remote, false)
	writeProjectFile(t, rootB, projectstore.FileIDRanges, rangesOf(authorB.AuthorID, late))
	mustIncorporate(t, b, rootB, remote)
	fromB := readProjectFile(t, rootB, projectstore.FileIDRanges)

	// (2) A 側で取り込む（自分が早い → 自分が保持し、相手 = B が移る）。同じ結果になること。
	a2, _, rootA2, rootB2, remote2 := setupShared(t)
	a2m := newClientWith(t, authorA, always(Resolution{Choice: ChoiceOurs}), nil)
	b2 := newClientWith(t, authorB, always(Resolution{Choice: ChoiceOurs}), nil)
	writeProjectFile(t, rootB2, projectstore.FileIDRanges, rangesOf(authorB.AuthorID, late))
	mustPublish(t, b2, rootB2, remote2, false)
	writeProjectFile(t, rootA2, projectstore.FileIDRanges, rangesOf(authorA.AuthorID, early))
	mustIncorporate(t, a2m, rootA2, remote2)
	fromA := readProjectFile(t, rootA2, projectstore.FileIDRanges)
	_ = a2

	if fromA != fromB {
		t.Errorf("双方の作業コピーで解消結果が違う:\nB 側:\n%s\nA 側:\n%s", fromB, fromA)
	}
	ranges, err := projectstore.UnmarshalIDRanges([]byte(fromB))
	if err != nil {
		t.Fatalf("解消後の番号帯を読めない: %v\n%s", err, fromB)
	}
	if err := ranges.Validate(); err != nil {
		t.Errorf("重なりが解消されていない: %v\n%s", err, fromB)
	}
	mineA := ranges.For(projectstore.RangeRequirement, authorA.AuthorID)
	mineB := ranges.For(projectstore.RangeRequirement, authorB.AuthorID)
	if len(mineA) != 1 || mineA[0].From != 1 || mineA[0].To != 100 {
		t.Errorf("早い方（A）が区間を保持していない: %+v", mineA)
	}
	if len(mineB) != 1 || mineB[0].From != 101 || mineB[0].To != 200 {
		t.Errorf("遅い方（B）が最大上限の次へ同じ幅で移っていない: %+v", mineB)
	}
	if mineB[0].ReservedAt.IsZero() {
		t.Error("移動で確保日時が失われた")
	}
}

// 番号帯の確保を配線しない場合（単独利用）は id-ranges.yaml を作らない。
func TestSyncWithoutRangeReserverKeepsSoloNumbering(t *testing.T) {
	rootA := filepath.Join(t.TempDir(), "A", "proj")
	storeA := newProject(t, rootA, authorA)
	a := newClient(t, authorA)
	remote := folderRemote(t)

	mustPublish(t, a, rootA, remote, true)
	if storeA.UsesIDRanges() {
		t.Error("受け口が未配線なのに番号帯が作られた")
	}
	if id := createRequirement(t, storeA); id != "FR-INV-001" {
		t.Errorf("単独利用の採番が変わった: %s", id)
	}
}

// 重なった区間で既に採番していた場合、同一 ID のレコードは後勝ちで消えず、
// レコード単位の競合として三面マージで提示される。
func TestDuplicateIDFromOverlappedRangeIsPresentedAsRecordConflict(t *testing.T) {
	a, _, rootA, rootB, remote := setupShared(t)
	resolver := always(Resolution{Choice: ChoiceOurs})
	b := newClientWith(t, authorB, resolver, nil)

	// 重なった区間から双方が同じ番号を採番してしまった状態
	const dup = "requirements/FR-INV-101.md"
	writeProjectFile(t, rootA, dup, "---\nid: FR-INV-101\n---\nA が作った要件\n")
	mustPublish(t, a, rootA, remote, false)
	mine := "---\nid: FR-INV-101\n---\nB が作った要件\n"
	writeProjectFile(t, rootB, dup, mine)

	mustIncorporate(t, b, rootB, remote)
	c := findConflict(t, resolver.seen, dup, "")
	if c.Unit != UnitRecord || c.Label != "要件項目 FR-INV-101" {
		t.Errorf("レコード単位で提示されていない: %+v", c)
	}
	if !strings.Contains(c.Theirs, "A が作った要件") || !strings.Contains(c.Ours, "B が作った要件") {
		t.Errorf("双方の内容が提示されていない: theirs=%q ours=%q", c.Theirs, c.Ours)
	}
	if got := readProjectFile(t, rootB, dup); got != mine {
		t.Errorf("承認した解決が反映されていない:\n%s", got)
	}
}
