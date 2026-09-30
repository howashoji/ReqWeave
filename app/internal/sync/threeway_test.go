package sync

// 三面マージの単体検証。実際の git と共有フォルダ相当の bare リポジトリで、
// 2 端末（A / B）の作業コピーを取り込ませて確かめる。
//
// 確かめること:
//   - 基準版 / 相手 / 自分の 3 つを、レコード・エントリ・フィールド・章の単位で提示する
//   - 承認しない限り統合せず、復帰点へ戻す（後勝ち上書き 0 件）
//   - コンフリクトマーカーを作業コピーへ書かない
//   - 解決を変更履歴（merge-applied）へ記録する
//   - 取り込み時の他ブランチのマージ順: 同一の入力に対して結果が一意

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/howashoji/ReqWeave/app/internal/projectstore"
	"gopkg.in/yaml.v3"
)

// ---- テスト用の承認の受け口 --------------------------------------------------

// stubResolver は提示された競合を記録し、決めておいた解決を返す。
type stubResolver struct {
	// decide は競合 1 件に対する解決を返す。nil を返すと「選択なし」（承認されていない）。
	decide func(c Conflict) *Resolution
	// err を返すと利用者の中止に相当する。
	err error

	seen []Conflict
	// calls は提示の回数（承認を経ずに統合していないことの確認に使う）。
	calls int
}

func (s *stubResolver) ResolveConflicts(_ context.Context, conflicts []Conflict) (map[string]Resolution, error) {
	s.calls++
	s.seen = append(s.seen, conflicts...)
	if s.err != nil {
		return nil, s.err
	}
	out := map[string]Resolution{}
	for _, c := range conflicts {
		if r := s.decide(c); r != nil {
			out[c.ID] = *r
		}
	}
	return out, nil
}

// always は全件に同じ解決を返す受け口。
func always(r Resolution) *stubResolver {
	return &stubResolver{decide: func(Conflict) *Resolution { return &r }}
}

// recordingAuditor は merge-applied の記録先。
type recordingAuditor struct {
	roots []string
	items []Conflict
	res   []Resolution
}

func (a *recordingAuditor) RecordMergeApplied(root string, c Conflict, r Resolution) {
	a.roots = append(a.roots, root)
	a.items = append(a.items, c)
	a.res = append(a.res, r)
}

// newClientWith は承認の受け口・記録先を組み込んだクライアントを作る。
func newClientWith(t *testing.T, author projectstore.Author, resolver ConflictResolver, auditor MergeAuditor) *Client {
	t.Helper()
	c := newClient(t, author)
	c.merger = ThreeWayMerger{Resolver: resolver, Auditor: auditor}
	return c
}

// theirConflict は seen のうち path が一致する最初の競合を返す。
func findConflict(t *testing.T, seen []Conflict, path, key string) Conflict {
	t.Helper()
	for _, c := range seen {
		if c.Path == path && c.Key == key {
			return c
		}
	}
	t.Fatalf("競合が提示されていない（path=%s key=%s）: %+v", path, key, seen)
	return Conflict{}
}

// ---- エントリ単位（terms.yaml 等） --------------------------------------------

// 異なるエントリの変更は承認を求めずに統合される。
func TestEntryMergeIntegratesDifferentEntriesWithoutApproval(t *testing.T) {
	a, _, rootA, rootB, remote := setupShared(t)
	resolver := always(Resolution{Choice: ChoiceTheirs})
	b := newClientWith(t, authorB, resolver, nil)

	writeProjectFile(t, rootA, "terms.yaml", "terms:\n  - name: 在庫\n    name_en: stock\n    definition: A の定義\n")
	writeProjectFile(t, rootA, "roster.yaml", "stakeholders:\n  - id: STK-001\n    name: 佐藤\n    org: 情報システム部\n")
	mustPublish(t, a, rootA, remote, false)

	writeProjectFile(t, rootB, "terms.yaml", "terms:\n  - name: 発注\n    name_en: order\n    definition: B の定義\n")
	writeProjectFile(t, rootB, "roster.yaml", "stakeholders:\n  - id: STK-101\n    name: 鈴木\n    org: 購買部\n")
	res := mustIncorporate(t, b, rootB, remote)

	if resolver.calls != 0 {
		t.Errorf("競合でないのに承認を求めた: %d 回", resolver.calls)
	}
	if res.Conflicts != 0 {
		t.Errorf("競合件数が違う: %d", res.Conflicts)
	}
	terms := loadTermNames(t, readProjectFile(t, rootB, "terms.yaml"))
	if len(terms) != 2 || !contains(terms, "在庫") || !contains(terms, "発注") {
		t.Errorf("双方の用語が残っていない: %v", terms)
	}
	roster := readProjectFile(t, rootB, "roster.yaml")
	if !strings.Contains(roster, "STK-001") || !strings.Contains(roster, "STK-101") {
		t.Errorf("双方のステークホルダーが残っていない:\n%s", roster)
	}
	// 統合結果が各スキーマとして読める（形を壊していない）
	if _, err := projectstore.UnmarshalMembers([]byte(readProjectFile(t, rootB, "members.yaml"))); err != nil {
		t.Errorf("members.yaml が壊れた: %v", err)
	}
}

// 同一エントリの相反する変更は、そのエントリ 1 件だけを三面で提示する。
func TestEntryConflictIsPresentedPerEntryWithThreeSides(t *testing.T) {
	a, _, rootA, rootB, remote := setupShared(t)
	auditor := &recordingAuditor{}
	resolver := always(Resolution{Choice: ChoiceTheirs})
	b := newClientWith(t, authorB, resolver, auditor)

	// 基準版（双方が見ている状態）を作る
	base := "terms:\n  - name: 在庫\n    name_en: stock\n    definition: 基準の定義\n"
	writeProjectFile(t, rootA, "terms.yaml", base)
	mustPublish(t, a, rootA, remote, false)
	mustIncorporate(t, b, rootB, remote)

	writeProjectFile(t, rootA, "terms.yaml",
		"terms:\n  - name: 在庫\n    name_en: stock\n    definition: A の定義\n  - name: 発注\n    name_en: order\n    definition: A の追加\n")
	mustPublish(t, a, rootA, remote, false)
	writeProjectFile(t, rootB, "terms.yaml", "terms:\n  - name: 在庫\n    name_en: stock\n    definition: B の定義\n")

	res := mustIncorporate(t, b, rootB, remote)
	if resolver.calls != 1 {
		t.Errorf("承認の提示回数が違う: %d", resolver.calls)
	}
	if len(resolver.seen) != 1 {
		t.Fatalf("提示単位がエントリになっていない（%d 件）: %+v", len(resolver.seen), resolver.seen)
	}
	c := resolver.seen[0]
	if c.Unit != UnitEntry || c.Key != "在庫" || c.Path != "terms.yaml" {
		t.Errorf("提示単位が違う: %+v", c)
	}
	if c.Label != "用語「在庫」" || c.Category != CatTerms {
		t.Errorf("利用者向けの対象名が違う: %q / %q", c.Label, c.Category)
	}
	if c.TheirsAuthor != authorA.AuthorID {
		t.Errorf("相手の作業者が違う: %q", c.TheirsAuthor)
	}
	// 三面がそろっている（基準版 / 相手 / 自分）
	if !strings.Contains(c.Base, "基準の定義") || !strings.Contains(c.Theirs, "A の定義") || !strings.Contains(c.Ours, "B の定義") {
		t.Errorf("三面の内容が違う:\nbase=%q\ntheirs=%q\nours=%q", c.Base, c.Theirs, c.Ours)
	}
	if res.Conflicts != 1 {
		t.Errorf("解決した競合の件数が違う: %d", res.Conflicts)
	}

	// 相手を採った結果が入り、競合していない追加（発注）も統合されている
	got := readProjectFile(t, rootB, "terms.yaml")
	if !strings.Contains(got, "A の定義") || strings.Contains(got, "B の定義") {
		t.Errorf("「相手を採る」が反映されていない:\n%s", got)
	}
	if names := loadTermNames(t, got); len(names) != 2 || !contains(names, "発注") {
		t.Errorf("競合していない追加が失われた: %v", names)
	}
	// 変更履歴への記録
	if len(auditor.items) != 1 || auditor.items[0].Key != "在庫" || auditor.res[0].Choice != ChoiceTheirs {
		t.Errorf("merge-applied の記録が違う: %+v / %+v", auditor.items, auditor.res)
	}
	if len(auditor.roots) != 1 || auditor.roots[0] != rootB {
		t.Errorf("記録先の作業コピーが違う: %v", auditor.roots)
	}
}

// 4 つの解決（相手 / 自分 / 両立 / 未決事項として起票）がそれぞれ反映される。
func TestResolutionChoicesAreApplied(t *testing.T) {
	cases := []struct {
		name       string
		resolution Resolution
		want       string
		notWant    string
	}{
		{"相手を採る", Resolution{Choice: ChoiceTheirs}, "A の定義", "B の定義"},
		{"自分を採る", Resolution{Choice: ChoiceOurs}, "B の定義", "A の定義"},
		{"両立させる", Resolution{Choice: ChoiceBoth,
			Merged: "name: 在庫\nname_en: stock\ndefinition: A と B を踏まえた定義\n"}, "A と B を踏まえた定義", "A の定義"},
		{"未決事項として起票する", Resolution{Choice: ChoiceOpenIssue, Adopt: ChoiceOurs, OpenIssueID: "ISS-101"},
			"B の定義", "A の定義"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			a, _, rootA, rootB, remote := setupShared(t)
			auditor := &recordingAuditor{}
			b := newClientWith(t, authorB, always(tc.resolution), auditor)

			writeProjectFile(t, rootA, "terms.yaml", "terms:\n  - name: 在庫\n    definition: A の定義\n")
			mustPublish(t, a, rootA, remote, false)
			writeProjectFile(t, rootB, "terms.yaml", "terms:\n  - name: 在庫\n    definition: B の定義\n")

			mustIncorporate(t, b, rootB, remote)
			got := readProjectFile(t, rootB, "terms.yaml")
			if !strings.Contains(got, tc.want) || strings.Contains(got, tc.notWant) {
				t.Errorf("解決が反映されていない:\n%s", got)
			}
			if names := loadTermNames(t, got); len(names) != 1 || names[0] != "在庫" {
				t.Errorf("統合後の内容が用語一覧として読めない: %v", names)
			}
			if len(auditor.res) != 1 || auditor.res[0].Choice != tc.resolution.Choice {
				t.Errorf("記録した解決が違う: %+v", auditor.res)
			}
			if tc.resolution.Choice == ChoiceOpenIssue && auditor.res[0].OpenIssueID != "ISS-101" {
				t.Errorf("起票した未決事項の対応づけが記録されていない: %+v", auditor.res[0])
			}
		})
	}
}

// 承認が無い・不完全なときは統合せず、復帰点へ戻す（既定の選択を置かない）。
func TestUnapprovedResolutionsCancelIncorporate(t *testing.T) {
	cases := []struct {
		name     string
		resolver *stubResolver
	}{
		{"選択なし（承認画面を閉じた）", &stubResolver{decide: func(Conflict) *Resolution { return nil }}},
		{"空の選択", always(Resolution{})},
		{"未知の選択", always(Resolution{Choice: Choice("take-latest")})},
		{"両立させるが内容が空", always(Resolution{Choice: ChoiceBoth, Merged: "  \n"})},
		{"起票したが暫定採用が無い", always(Resolution{Choice: ChoiceOpenIssue, OpenIssueID: "ISS-101"})},
		{"起票の未決事項 ID が無い", always(Resolution{Choice: ChoiceOpenIssue, Adopt: ChoiceOurs})},
		{"利用者が中止した", &stubResolver{err: errors.New("利用者が中止しました")}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			a, _, rootA, rootB, remote := setupShared(t)
			auditor := &recordingAuditor{}
			b := newClientWith(t, authorB, tc.resolver, auditor)
			ctx := context.Background()

			writeProjectFile(t, rootA, "terms.yaml", "terms:\n  - name: 在庫\n    definition: A の定義\n")
			mustPublish(t, a, rootA, remote, false)
			mine := "terms:\n  - name: 在庫\n    definition: B の定義\n"
			writeProjectFile(t, rootB, "terms.yaml", mine)

			_, err := b.Incorporate(ctx, rootB, remote, "")
			if err == nil {
				t.Fatal("承認が無いのに取り込みが成功した")
			}
			if got := readProjectFile(t, rootB, "terms.yaml"); got != mine {
				t.Errorf("取り込み前の内容に戻っていない（後勝ち上書き）:\n%s", got)
			}
			if len(auditor.items) != 0 {
				t.Errorf("統合していないのに記録された: %+v", auditor.items)
			}
			status, _ := b.Repo(rootB).Git(ctx, "status", "--porcelain")
			if strings.TrimSpace(status) != "" {
				t.Errorf("中止後の作業ツリーが復帰点と一致しない:\n%s", status)
			}
			head, _ := b.Repo(rootB).Git(ctx, "rev-list", "--parents", "-n", "1", "HEAD")
			if len(strings.Fields(head)) != 2 {
				t.Errorf("中止したのにマージコミットが残っている: %q", head)
			}
		})
	}
}

// ---- レコード・章・所有権・フィールド単位 -----------------------------------

// 提示単位がファイル種別ごとに変わる。
func TestConflictUnitsByFileKind(t *testing.T) {
	cases := []struct {
		name      string
		path      string
		theirs    string
		ours      string
		wantUnit  Unit
		wantLabel string
	}{
		{"要件項目はレコード単位", "requirements/FR-INV-001.md",
			"---\nid: FR-INV-001\n---\nA の本文\n", "---\nid: FR-INV-001\n---\nB の本文\n",
			UnitRecord, "要件項目 FR-INV-001"},
		{"ドラフトは章単位", "documents/requirements/draft/03-scope.md",
			"# スコープ\nA の章\n", "# スコープ\nB の章\n",
			UnitChapter, "要件定義書ドラフト 03-scope 章"},
		{"対話セッションは所有権の破れ", "sessions/S-0001.md",
			"A の発話\n", "B の発話\n",
			UnitOwnership, "対話セッション S-0001"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			a, _, rootA, rootB, remote := setupShared(t)
			resolver := always(Resolution{Choice: ChoiceOurs})
			b := newClientWith(t, authorB, resolver, nil)

			writeProjectFile(t, rootA, tc.path, tc.theirs)
			mustPublish(t, a, rootA, remote, false)
			writeProjectFile(t, rootB, tc.path, tc.ours)

			mustIncorporate(t, b, rootB, remote)
			c := findConflict(t, resolver.seen, tc.path, "")
			if c.Unit != tc.wantUnit || c.Label != tc.wantLabel {
				t.Errorf("提示単位・対象名が違う: %+v", c)
			}
			// レコード・章はファイル全体が三面になる
			if c.Theirs != tc.theirs || c.Ours != tc.ours {
				t.Errorf("三面の内容が違う: theirs=%q ours=%q", c.Theirs, c.Ours)
			}
			if got := readProjectFile(t, rootB, tc.path); got != tc.ours {
				t.Errorf("「自分を採る」が反映されていない:\n%s", got)
			}
		})
	}
}

// project.yaml は項目（フィールド）単位。別々の項目の変更は自動統合し、同一項目だけを提示する。
func TestProjectSettingsMergeByField(t *testing.T) {
	a, _, rootA, rootB, remote := setupShared(t)
	resolver := always(Resolution{Choice: ChoiceTheirs})
	b := newClientWith(t, authorB, resolver, nil)

	// A は概要を、B は業務領域を変える（別々の項目 → 自動統合）
	editProjectYAML(t, rootA, func(p *projectstore.Project) { p.Summary = "A が書いた概要" })
	mustPublish(t, a, rootA, remote, false)
	editProjectYAML(t, rootB, func(p *projectstore.Project) { p.DomainPresets = []string{"在庫管理"} })
	mustIncorporate(t, b, rootB, remote)

	if resolver.calls != 0 {
		t.Errorf("別々の項目なのに承認を求めた: %d 回", resolver.calls)
	}
	merged := loadProject(t, rootB)
	if merged.Summary != "A が書いた概要" || len(merged.DomainPresets) != 1 || merged.DomainPresets[0] != "在庫管理" {
		t.Errorf("項目単位の統合ができていない: %+v", merged)
	}
	if merged.ProjectID == "" || merged.TargetSystemName != "在庫管理システム" || merged.FormatVersion == "" {
		t.Errorf("統合で他の項目が失われた: %+v", merged)
	}

	// 同一項目を両側が変えたときは、その項目だけを提示する
	editProjectYAML(t, rootA, func(p *projectstore.Project) { p.Summary = "A の再修正" })
	mustPublish(t, a, rootA, remote, false)
	editProjectYAML(t, rootB, func(p *projectstore.Project) { p.Summary = "B の修正" })
	mustIncorporate(t, b, rootB, remote)

	if len(resolver.seen) != 1 {
		t.Fatalf("提示単位が項目になっていない（%d 件）: %+v", len(resolver.seen), resolver.seen)
	}
	c := resolver.seen[0]
	if c.Unit != UnitField || c.Key != "summary" || c.Label != "プロジェクト設定の summary" {
		t.Errorf("提示単位・対象名が違う: %+v", c)
	}
	if loadProject(t, rootB).Summary != "A の再修正" {
		t.Errorf("「相手を採る」が反映されていない: %+v", loadProject(t, rootB))
	}
}

// 番号帯は作業者ごとの区画のため、双方が確保しても競合しない。
func TestIDRangesMergeWithoutConflict(t *testing.T) {
	a, _, rootA, rootB, remote := setupShared(t)
	resolver := always(Resolution{Choice: ChoiceTheirs})
	b := newClientWith(t, authorB, resolver, nil)

	// 双方が自分の区間を確保した状態を作る（確保の実体は projectstore）
	reserve(t, rootA, authorA)
	mustPublish(t, a, rootA, remote, false)
	reserve(t, rootB, authorB)

	mustIncorporate(t, b, rootB, remote)
	if resolver.calls != 0 {
		t.Errorf("作業者ごとの区画なのに承認を求めた: %d 回", resolver.calls)
	}
	data := []byte(readProjectFile(t, rootB, projectstore.FileIDRanges))
	ranges, err := projectstore.UnmarshalIDRanges(data)
	if err != nil {
		t.Fatalf("統合後の番号帯を読めない: %v", err)
	}
	for _, key := range projectstore.RangeKeys() {
		if len(ranges.For(key, authorA.AuthorID)) == 0 || len(ranges.For(key, authorB.AuthorID)) == 0 {
			t.Fatalf("双方の区間が残っていない（%s）: %+v", key, ranges.Ranges[key])
		}
	}
	// 対象種別ごとに下限の昇順で並ぶ（統合結果が一意に定まる）
	for _, key := range projectstore.RangeKeys() {
		prev := 0
		for _, e := range ranges.Ranges[key] {
			if e.From < prev {
				t.Errorf("区間の並びが昇順でない（%s）: %+v", key, ranges.Ranges[key])
				break
			}
			prev = e.From
		}
	}
	// 双方が同じ位置から確保していても、統合の時点で重なりが解消される。
	// 解消規則そのものの検証は idranges_test.go の TestOverlappingRangesAreResolvedDeterministically。
	if err := ranges.Validate(); err != nil {
		t.Errorf("統合後の番号帯に重なりが残った: %v", err)
	}
}

// ---- 全体の性質 --------------------------------------------------------------

// 統合後の作業コピーにコンフリクトマーカーを残さない。
func TestNoConflictMarkersAfterResolution(t *testing.T) {
	a, _, rootA, rootB, remote := setupShared(t)
	b := newClientWith(t, authorB, always(Resolution{Choice: ChoiceTheirs}), nil)
	ctx := context.Background()

	writeProjectFile(t, rootA, "terms.yaml", "terms:\n  - name: 在庫\n    definition: A の定義\n")
	writeProjectFile(t, rootA, "requirements/FR-INV-001.md", "A の本文\n")
	mustPublish(t, a, rootA, remote, false)
	writeProjectFile(t, rootB, "terms.yaml", "terms:\n  - name: 在庫\n    definition: B の定義\n")
	writeProjectFile(t, rootB, "requirements/FR-INV-001.md", "B の本文\n")

	res := mustIncorporate(t, b, rootB, remote)
	if res.Conflicts != 2 {
		t.Errorf("解決した競合の件数が違う: %d", res.Conflicts)
	}
	out, _ := b.Repo(rootB).Git(ctx, "grep", "-l", "-e", "<<<<<<<", "-e", "=======", "-e", ">>>>>>>", "--", ".")
	if strings.TrimSpace(out) != "" {
		t.Errorf("コンフリクトマーカーが残っている: %s", out)
	}
	status, _ := b.Repo(rootB).Git(ctx, "status", "--porcelain")
	if strings.TrimSpace(status) != "" {
		t.Errorf("統合後に未コミットの残骸がある:\n%s", status)
	}
}

// 同一の入力に対して統合結果が一意に定まる（他ブランチのマージ順が決まっている）。
func TestMergeIsDeterministic(t *testing.T) {
	trees := make([]string, 0, 2)
	for i := 0; i < 2; i++ {
		a, _, rootA, rootB, remote := setupShared(t)
		b := newClientWith(t, authorB, always(Resolution{Choice: ChoiceTheirs}), nil)
		writeProjectFile(t, rootA, "terms.yaml",
			"terms:\n  - name: 在庫\n    definition: A の定義\n  - name: 発注\n    definition: A の追加\n")
		mustPublish(t, a, rootA, remote, false)
		writeProjectFile(t, rootB, "terms.yaml",
			"terms:\n  - name: 在庫\n    definition: B の定義\n  - name: 受注\n    definition: B の追加\n")
		mustIncorporate(t, b, rootB, remote)
		trees = append(trees, readProjectFile(t, rootB, "terms.yaml"))
	}
	if trees[0] != trees[1] {
		t.Errorf("同一の入力で統合結果が変わった:\n%s\n----\n%s", trees[0], trees[1])
	}
}

// 対象ファイルの並びが projectstore の定数と一致していること（片方だけ変わると規則が効かなくなる）。
func TestMergeRuleFilesMatchProjectStore(t *testing.T) {
	for _, name := range []string{
		projectstore.FileTerms, projectstore.FileMembers, projectstore.FileRoster,
		projectstore.FilePerspectives, projectstore.FileReservations,
		projectstore.FileIDRanges, projectstore.FileProject,
	} {
		if _, ok := fileRules[name]; !ok {
			t.Errorf("構造化ファイルの規則が無い: %s", name)
		}
	}
	if len(fileRules) != 7 {
		t.Errorf("規則の件数が想定と違う（対象を増減したら本テストも直す）: %d", len(fileRules))
	}
	// レコード・章のパス規約（projectstore の生成規則と同じ前提で分類している）
	if !strings.HasPrefix(projectstore.RequirementFile("FR-INV-001"), "requirements/") ||
		!strings.HasPrefix(projectstore.DecisionFile("DEC-001"), "decisions/") ||
		!strings.HasPrefix(projectstore.OpenIssueFile("ISS-001"), "open-issues/") {
		t.Error("レコードのパス規約が変わった")
	}
	if !strings.Contains(projectstore.DraftDir(projectstore.DocKindRequirements), "/draft") {
		t.Error("ドラフトのパス規約が変わった")
	}
	if got := ruleFor(projectstore.DraftDir(projectstore.DocKindRequirements) + "/03-scope.md"); got.unit != UnitChapter {
		t.Errorf("ドラフトが章単位に分類されない: %v", got.unit)
	}
	if got := ruleFor(projectstore.RequirementFile("FR-INV-001")); got.unit != UnitRecord {
		t.Errorf("要件項目がレコード単位に分類されない: %v", got.unit)
	}
}

// ---- テスト補助 --------------------------------------------------------------

// loadTermNames は terms.yaml の用語名を返す（統合後の内容がスキーマとして読めることも兼ねる）。
func loadTermNames(t *testing.T, data string) []string {
	t.Helper()
	var doc struct {
		Terms []struct {
			Name string `yaml:"name"`
		} `yaml:"terms"`
	}
	if err := yaml.Unmarshal([]byte(data), &doc); err != nil {
		t.Fatalf("terms.yaml を解釈できない: %v\n%s", err, data)
	}
	var names []string
	for _, term := range doc.Terms {
		names = append(names, term.Name)
	}
	return names
}

func loadProject(t *testing.T, root string) *projectstore.Project {
	t.Helper()
	p, err := projectstore.UnmarshalProject([]byte(readProjectFile(t, root, projectstore.FileProject)))
	if err != nil {
		t.Fatalf("project.yaml を解釈できない: %v", err)
	}
	return p
}

func editProjectYAML(t *testing.T, root string, mutate func(*projectstore.Project)) {
	t.Helper()
	p := loadProject(t, root)
	mutate(p)
	out, err := p.Marshal()
	if err != nil {
		t.Fatalf("project.yaml を組み立てられない: %v", err)
	}
	writeProjectFile(t, root, projectstore.FileProject, string(out))
}

// reserve は作業コピーの番号帯を 1 回確保する（確保の実装は projectstore 側）。
func reserve(t *testing.T, root string, author projectstore.Author) {
	t.Helper()
	store, err := projectstore.Open(root, author)
	if err != nil {
		t.Fatalf("作業コピーを開けない: %v", err)
	}
	defer store.Close()
	if _, err := store.ReserveIDRanges(0, nil); err != nil {
		t.Fatalf("番号帯を確保できない: %v", err)
	}
}

// 変更履歴（merge-applied）の対象識別が、ファイル種別ごとに変更履歴の target の形式になる。
func TestConflictTarget(t *testing.T) {
	cases := []struct {
		conflict Conflict
		want     string
	}{
		{Conflict{Unit: UnitRecord, Path: "requirements/FR-INV-001.md"}, "FR-INV-001"},
		{Conflict{Unit: UnitRecord, Path: "decisions/DEC-001.md"}, "DEC-001"},
		{Conflict{Unit: UnitRecord, Path: "open-issues/ISS-001.md"}, "ISS-001"},
		{Conflict{Unit: UnitChapter, Path: "documents/requirements/draft/03-scope.md"},
			"documents/requirements/draft/03-scope.md"},
		{Conflict{Unit: UnitOwnership, Path: "sessions/S-0001.md"}, "sessions/S-0001.md"},
		{Conflict{Unit: UnitEntry, Path: "terms.yaml", Key: "在庫"}, "在庫"},
		{Conflict{Unit: UnitEntry, Path: "members.yaml", Key: "a.sato@example.co.jp"}, "member:a.sato@example.co.jp"},
		{Conflict{Unit: UnitEntry, Path: "roster.yaml", Key: "STK-001"}, "STK-001"},
		{Conflict{Unit: UnitEntry, Path: "perspectives.yaml", Key: "PRS-001"}, "PRS-001"},
		{Conflict{Unit: UnitEntry, Path: "reservations.yaml", Key: "requirement:FR-INV-001"}, "requirement:FR-INV-001"},
		{Conflict{Unit: UnitField, Path: "project.yaml", Key: "summary"}, "project:summary"},
		{Conflict{Unit: UnitEntry, Path: "id-ranges.yaml", Key: "decision#a.sato@example.co.jp#1"},
			"id-range:decision#a.sato@example.co.jp#1"},
	}
	for _, tc := range cases {
		if got := tc.conflict.Target(); got != tc.want {
			t.Errorf("%s の対象識別が違う: got %q, want %q", tc.conflict.Path, got, tc.want)
		}
	}
}
