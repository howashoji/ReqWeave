package projectstore

// 本ファイルは確定版の版番号の仮採番と再採番。
//
// 共同プロジェクトでは、確定操作の版番号 N は「その作業コピーで見えている最大版 + 1」の**仮採番**であり、
// 同期先へ到達できない状態でも確定できる。取り込みで同一種別の同一 `v<N>` が双方に存在した場合は
// **内容を変えずに版番号だけを付け替える**（確定版の上書き・削除をしない）。
//
// 決定手順（一意に定まる）:
//  1. `confirmed_at` が早い方が N を保持する。
//  2. 同時刻なら `confirmed_by`（利用者 ID）の辞書順で先の方が N を保持する。
//  3. 保持しない側を、その種別の**未使用の最小版番号**へ付け替える。
//
// 版番号は `documents/` と `versions.md` にのみ現れ、要件項目からの参照は章→要件の片方向のため、
// **参照の張り替えは発生しない**。

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
)

// VersionRenumber は再採番 1 件（旧番号 → 新番号）。
type VersionRenumber struct {
	Kind string
	From int
	To   int
}

// PlanVersionRenumber は自分の版履歴 mine と相手の版履歴 theirs から、**自分側**に必要な再採番を返す
// （上の決定手順）。同じ入力に対して結果が一意に定まる。
//
// 相手側の再採番は相手の端末で同じ手順により決まる（双方が同じ規則で判断するため、結果は一致する）。
func PlanVersionRenumber(kind string, mine, theirs []DocumentVersion) []VersionRenumber {
	byNumberTheirs := map[int]DocumentVersion{}
	for _, v := range theirs {
		byNumberTheirs[v.Version] = v
	}
	// 付け替え先の候補から除く番号（自分・相手の双方で使われている番号）。
	taken := map[int]bool{}
	for _, v := range mine {
		taken[v.Version] = true
	}
	for _, v := range theirs {
		taken[v.Version] = true
	}

	sorted := append([]DocumentVersion(nil), mine...)
	sort.SliceStable(sorted, func(i, j int) bool { return sorted[i].Version < sorted[j].Version })

	var plan []VersionRenumber
	for _, v := range sorted {
		other, collides := byNumberTheirs[v.Version]
		if !collides || keepsNumber(v, other) {
			continue
		}
		to := 1
		for taken[to] {
			to++
		}
		taken[to] = true
		plan = append(plan, VersionRenumber{Kind: kind, From: v.Version, To: to})
	}
	return plan
}

// keepsNumber は衝突した 2 つの確定版のうち mine が版番号を保持するかを返す（決定手順の 1・2）。
func keepsNumber(mine, theirs DocumentVersion) bool {
	if !mine.ConfirmedAt.Equal(theirs.ConfirmedAt) {
		return mine.ConfirmedAt.Before(theirs.ConfirmedAt)
	}
	// 同時刻は利用者 ID の辞書順。確定者が記録されていない（データ形式 1.4 より前に確定した）側は後ろに置く。
	if mine.ConfirmedBy == theirs.ConfirmedBy {
		return true // 同一作業者の同時刻。付け替えても意味が無いため保持する。
	}
	if mine.ConfirmedBy == "" {
		return false
	}
	if theirs.ConfirmedBy == "" {
		return true
	}
	return mine.ConfirmedBy < theirs.ConfirmedBy
}

// RenumberVersion は確定版の版番号を付け替える。
//
// `documents/<種別>/v<from>/` を `v<to>/` へ改名し、`versions.md` の版番号と各章の版番号を書き換える。
// **章本文には触れない**（確定版の内容を変えない）。付け替え先が既にある場合は何もしない。
func (s *Store) RenumberVersion(kind string, from, to int) error {
	if err := validateDocKind(kind); err != nil {
		return err
	}
	if from <= 0 || to <= 0 || from == to {
		return fmt.Errorf("版番号の付け替えが不正です: v%d → v%d", from, to)
	}
	src := filepath.Join(s.root, filepath.FromSlash(VersionDir(kind, from)))
	dst := filepath.Join(s.root, filepath.FromSlash(VersionDir(kind, to)))
	if _, err := os.Stat(src); err != nil {
		return fmt.Errorf("付け替える確定版がありません（v%d）: %w", from, err)
	}
	if _, err := os.Stat(dst); err == nil {
		return fmt.Errorf("付け替え先の確定版が既にあります（v%d）", to)
	}

	versions, err := s.Versions(kind)
	if err != nil {
		return err
	}
	found := false
	for i := range versions {
		if versions[i].Version == from {
			versions[i].Version = to
			found = true
		}
	}
	if !found {
		return fmt.Errorf("版履歴に v%d がありません", from)
	}

	if err := s.write(func() error { return os.Rename(src, dst) }); err != nil {
		return fmt.Errorf("確定版のフォルダを付け替えられません（v%d → v%d）: %w", from, to, err)
	}
	// 章ファイルの版番号（フロントマターの version）はフォルダの版番号と同じ値であり、
	// 付け替え後に古い番号が残ると版履歴と食い違う。本文は変更しない。
	if err := s.renumberChapters(kind, to); err != nil {
		return err
	}
	return s.writeVersions(kind, versions)
}

// renumberChapters は確定版の各章のフロントマターの版番号を n に揃える（本文は変更しない）。
func (s *Store) renumberChapters(kind string, n int) error {
	dir := filepath.Join(s.root, filepath.FromSlash(VersionDir(kind, n)))
	entries, err := os.ReadDir(dir)
	if err != nil {
		return fmt.Errorf("確定版を読み込めません（v%d）: %w", n, err)
	}
	for _, e := range entries {
		if e.IsDir() || filepath.Ext(e.Name()) != ".md" {
			continue
		}
		path := filepath.Join(dir, e.Name())
		data, err := os.ReadFile(path)
		if err != nil {
			return fmt.Errorf("章ファイルを読み込めません（%s）: %w", e.Name(), err)
		}
		var c DocumentChapter
		body, err := parseDocument(data, &c)
		if err != nil {
			return fmt.Errorf("章ファイルを解釈できません（%s）: %w", e.Name(), err)
		}
		if c.Version == n {
			continue
		}
		c.Version, c.FileName, c.Body = n, e.Name(), body
		out, err := marshalDocument(&c, body)
		if err != nil {
			return err
		}
		if err := s.write(func() error { return WriteFileAtomic(path, out) }); err != nil {
			return err
		}
	}
	return nil
}

// VersionRenumberResolver は取り込み時の版番号の衝突を解消する受け口（sync 側から呼ばれる）。
//
// 相手側の版履歴（`versions.md` の内容）を受け取り、上の決定手順で自分側の確定版を再採番する。
// 変更履歴への記録（`version-renumbered`）は OnRenumber の呼び出し側が行う
// （projectstore は監査記録を書かない分担 = members.yaml / roster.yaml と同じ）。
type VersionRenumberResolver struct {
	Store *Store
	// OnRenumber は再採番 1 件ごとの通知（nil 可）。
	OnRenumber func(r VersionRenumber)
}

// ResolveVersionCollisions は相手側の版履歴と突き合わせ、必要な再採番を行う。
//
// theirs のキーは成果物の種別（`requirements` / `basic-design`）、値は相手の `versions.md` の内容。
// 戻り値は再採番した件数と、**自分側で統合済みにしたパス**（版履歴。相手の項目を取り込んだので、
// 以後の統合では自分側を採ってよい）。root が開いているプロジェクトと異なる場合は何もしない
// （取り違えて別のプロジェクトを書き換えない）。
func (r VersionRenumberResolver) ResolveVersionCollisions(root string, theirs map[string][]byte) (int, []string, error) {
	if r.Store == nil {
		return 0, nil, nil
	}
	if filepath.Clean(root) != filepath.Clean(r.Store.Root()) {
		return 0, nil, nil
	}
	applied := 0
	var resolved []string
	for _, kind := range []string{DocKindRequirements, DocKindBasicDesign} {
		data, ok := theirs[kind]
		if !ok || len(data) == 0 {
			continue
		}
		other, err := ParseVersions(data)
		if err != nil {
			return applied, resolved, err
		}
		mine, err := r.Store.Versions(kind)
		if err != nil {
			return applied, resolved, err
		}
		for _, plan := range PlanVersionRenumber(kind, mine, other) {
			if err := r.Store.RenumberVersion(kind, plan.From, plan.To); err != nil {
				return applied, resolved, err
			}
			applied++
			if r.OnRenumber != nil {
				r.OnRenumber(plan)
			}
		}
		merged, err := r.Store.mergeVersionHistory(kind, other)
		if err != nil {
			return applied, resolved, err
		}
		if merged {
			resolved = append(resolved, VersionsFile(kind))
		}
	}
	return applied, resolved, nil
}

// mergeVersionHistory は相手側の版履歴の項目を自分側へ取り込む（版番号の重複は再採番で解消済み）。
//
// 版履歴は「確定した事実の追記」であり、同じ版番号が重複しない限り**和集合が正しい統合結果**になる
// （同期のエントリ単位の統合と同じ考え方）。書き換えが生じたときだけ true を返す。
func (s *Store) mergeVersionHistory(kind string, theirs []DocumentVersion) (bool, error) {
	mine, err := s.Versions(kind)
	if err != nil {
		return false, err
	}
	have := map[int]bool{}
	for _, v := range mine {
		have[v.Version] = true
	}
	added := false
	for _, v := range theirs {
		if have[v.Version] {
			continue
		}
		mine = append(mine, v)
		have[v.Version] = true
		added = true
	}
	if !added {
		return false, nil
	}
	if err := s.writeVersions(kind, mine); err != nil {
		return false, err
	}
	return true, nil
}
