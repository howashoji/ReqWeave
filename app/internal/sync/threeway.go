package sync

// 本ファイルは取り込み時の三面マージ。
//
// **方式**: git の自動マージ機構・マージドライバを使わず、**マージベース（基準版）・相手・自分の 3 つの内容を
// 本システムが読み比べて統合**し、結果を 2 親のマージコミットとして記録する（コミットの作成は ops.go）。
// 作業ツリーへコンフリクトマーカーを書かせない（git の仕組みを利用者へ見せない）ため、統合は一時インデックス上で完結させ、
// **承認が済むまで作業ツリーへ一切書かない**。
//
// **提示単位**はファイル種別ごとに異なる:
//
//	レコード   requirements/ decisions/ open-issues/ questionnaires/ imports/（1 レコード 1 ファイル）
//	エントリ   terms.yaml members.yaml roster.yaml perspectives.yaml reservations.yaml id-ranges.yaml
//	フィールド project.yaml
//	章         documents/<種別>/draft/
//	所有権     sessions/ audit/（片側しか書かないはずのため、両側変更は異常として提示する）
//
// エントリ・フィールド単位のファイルは、**異なるキーの変更を競合にしない**（自動統合する）。
// 同一キーを両側が変えたときだけ、そのキーを 1 件の競合として提示する。
//
// **承認**: 競合があるときは ConflictResolver へ提示し、競合ごとに
// 「相手を採る / 自分を採る / 両立させる / 未決事項として起票する」の**明示の選択**を受け取る。
// 受け口が未配線・選択が欠けている・選択が空のいずれでも統合しない（**既定の選択を置かない**
// = 承認なしに統合しない）。統合しない場合は ErrMergeConflict を返し、呼び出し側が復帰点へ戻す。
//
// **後勝ち上書きの経路を持たない**: 競合を承認なしに解決する公開 API を設けない
// （構造としての検証は threeway_api_test.go）。

import (
	"context"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// ---- 提示単位と競合 ---------------------------------------------------------

// Unit は競合の提示単位。
type Unit string

const (
	// UnitRecord は 1 レコード 1 ファイルの成果物（要件項目・決定事項・未決事項ほか）。
	UnitRecord Unit = "record"
	// UnitEntry はコレクションファイルの 1 エントリ（用語・メンバー・予約ほか）。
	UnitEntry Unit = "entry"
	// UnitField は設定ファイルの 1 フィールド（project.yaml）。
	UnitField Unit = "field"
	// UnitChapter は成果物ドラフトの 1 章。
	UnitChapter Unit = "chapter"
	// UnitOwnership は「片側しか書かない」前提が破れた対象（対話セッション・監査記録）。
	UnitOwnership Unit = "ownership"
)

// UnitLabel は提示単位の表示名（生のコード値を画面へ出さない）。
var UnitLabel = map[Unit]string{
	UnitRecord:    "レコード",
	UnitEntry:     "エントリ",
	UnitField:     "項目",
	UnitChapter:   "章",
	UnitOwnership: "本来は片側だけが変更する対象",
}

// Conflict は承認を求める競合 1 件（三面マージの画面の三面表示 1 つ分）。
//
// Base / Theirs / Ours は提示単位の内容（レコード・章はファイル全体、エントリ・項目はその部分）。
// 対象が存在しないことは空文字で表す（片側が削除した場合）。
type Conflict struct {
	// ID は競合の識別子（解決の対応づけに使う。同一の入力に対して一意に定まる）。
	ID string `json:"id"`
	// Unit は提示単位。
	Unit Unit `json:"unit"`
	// Label は利用者向けの対象名（「用語「在庫」」等。git の語・生のパスを主役にしない）。
	Label string `json:"label"`
	// Category は変更の区分（Summary と同じ値集合。画面の並び替え用）。
	Category string `json:"category"`
	// Path は作業コピーからの相対パス（詳細表示用）。
	Path string `json:"path"`
	// Key はエントリ・項目のキー（レコード・章では空）。
	Key string `json:"key,omitempty"`
	// TheirsAuthor は相手の作業者（author_id）。
	TheirsAuthor string `json:"theirsAuthor"`
	// TheirsBranch は相手の同期上の位置（詳細表示用。画面には出さない）。
	TheirsBranch string `json:"-"`

	Base   string `json:"base"`
	Theirs string `json:"theirs"`
	Ours   string `json:"ours"`
}

// Target は変更履歴へ記録する対象の識別（`merge-applied` の target）。
//
// レコード・章はその ID・パス、エントリ・項目はファイルごとの識別（用語名 / `member:<author_id>` /
// `STK-nnn` / `PRS-nnn` / 予約対象 ID / `project:<項目名>` / `id-range:<種別#作業者#下限>`）。
func (c Conflict) Target() string {
	if c.Key == "" {
		if c.Unit == UnitRecord {
			return baseName(c.Path)
		}
		return c.Path
	}
	switch c.Path {
	case "members.yaml":
		return "member:" + c.Key
	case "project.yaml":
		return "project:" + c.Key
	case "id-ranges.yaml":
		return "id-range:" + c.Key
	}
	return c.Key
}

// Choice は競合の解決の選択（4 択）。**既定値を持たない**（空 = 未承認）。
type Choice string

const (
	ChoiceTheirs    Choice = "theirs"
	ChoiceOurs      Choice = "ours"
	ChoiceBoth      Choice = "both"
	ChoiceOpenIssue Choice = "open-issue"
)

// ChoiceLabel は選択の表示名（変更履歴・画面用）。
var ChoiceLabel = map[Choice]string{
	ChoiceTheirs:    "相手を採る",
	ChoiceOurs:      "自分を採る",
	ChoiceBoth:      "両立させる",
	ChoiceOpenIssue: "未決事項として起票する",
}

// Resolution は 1 件の競合に対する本人の承認結果。
type Resolution struct {
	// Choice は選択。空・未知の値は「承認されていない」として扱い、統合しない。
	Choice Choice `json:"choice"`
	// Merged は ChoiceBoth のときの統合後の内容（利用者が両方を踏まえて確定させた内容）。
	// 提示単位の内容として解釈する（レコード・章はファイル全体、エントリ・項目はその部分）。
	Merged string `json:"merged,omitempty"`
	// Adopt は ChoiceOpenIssue のときに暫定採用する側（ChoiceTheirs / ChoiceOurs）。
	Adopt Choice `json:"adopt,omitempty"`
	// OpenIssueID は ChoiceOpenIssue のときに起票した未決事項の ID（変更履歴の evidence に残す）。
	OpenIssueID string `json:"openIssueId,omitempty"`
}

// ConflictResolver は競合を利用者へ提示して**本人の承認**を得る受け口（実装は画面側）。
//
// 戻り値は競合 ID → 解決。**すべての競合に明示の選択が要る**（欠けていれば統合しない）。
// 利用者が中止した場合はエラーを返す（取り込みは復帰点へ戻る）。
type ConflictResolver interface {
	ResolveConflicts(ctx context.Context, conflicts []Conflict) (map[string]Resolution, error)
}

// MergeAuditor は解決した競合を変更履歴へ記録する受け口（`merge-applied`。実装はバインディング層）。
//
// 記録の失敗で取り込みを止めない（監査記録の失敗で作業を止めない方針に合わせ、戻り値を持たない）。
type MergeAuditor interface {
	RecordMergeApplied(root string, c Conflict, r Resolution)
}

// ---- 三面マージ -------------------------------------------------------------

// ThreeWayMerger は三面マージ。Resolver が nil なら競合があるときに統合せず中止する。
type ThreeWayMerger struct {
	Resolver ConflictResolver
	Auditor  MergeAuditor
}

// Merge は基準版・相手・自分の 3 つを読み比べて統合したツリーを作る（git の自動マージを使わない）。
func (m ThreeWayMerger) Merge(ctx context.Context, repo *Repo, in MergeInput) (*MergeResult, error) {
	ours, err := diffTree(ctx, repo, in.Base, in.Ours)
	if err != nil {
		return nil, err
	}
	theirs, err := diffTree(ctx, repo, in.Base, in.Theirs)
	if err != nil {
		return nil, err
	}
	resolved := map[string]bool{}
	for _, p := range in.Resolved {
		resolved[p] = true
	}

	// 相手側だけが変えたパスはそのまま適用する。両側が変えたパスは種別ごとの規則で読み比べる。
	apply := map[string]treeChange{}
	var merges []*pathMerge
	paths := make([]string, 0, len(theirs))
	for p := range theirs {
		paths = append(paths, p)
	}
	sort.Strings(paths)
	for _, p := range paths {
		if resolved[p] {
			// 統合の前に解決済み（版番号の再採番）。自分側を採るため相手の変更を適用しない。
			continue
		}
		mine, both := ours[p]
		if !both {
			apply[p] = theirs[p]
			continue
		}
		if mine.SHA == theirs[p].SHA && mine.Mode == theirs[p].Mode {
			continue // 双方が同じ結果へ変えた（競合ではない）
		}
		pm, err := m.readPathMerge(ctx, repo, in, p, mine, theirs[p])
		if err != nil {
			return nil, err
		}
		merges = append(merges, pm)
	}

	var conflicts []Conflict
	for _, pm := range merges {
		conflicts = append(conflicts, pm.conflicts...)
	}
	if len(conflicts) > 0 {
		resolutions, err := m.approve(ctx, conflicts)
		if err != nil {
			return nil, err
		}
		for _, pm := range merges {
			if err := pm.applyResolutions(resolutions); err != nil {
				return nil, err
			}
		}
	}
	for _, pm := range merges {
		change, err := pm.change(ctx, repo)
		if err != nil {
			return nil, err
		}
		apply[pm.path] = change
	}

	tree, err := applyChanges(ctx, repo, in.Ours, apply)
	if err != nil {
		return nil, err
	}
	if m.Auditor != nil {
		for _, pm := range merges {
			for _, c := range pm.conflicts {
				m.Auditor.RecordMergeApplied(repo.Root(), c, pm.resolutions[c.ID])
			}
		}
	}
	return &MergeResult{Tree: tree, Conflicts: len(conflicts)}, nil
}

// approve は競合を利用者へ提示し、全件に明示の選択があることを確かめる。
//
// 受け口が未配線・中止・選択の欠落・未知の選択のいずれでも統合しない（既定の選択を置かない）。
func (m ThreeWayMerger) approve(ctx context.Context, conflicts []Conflict) (map[string]Resolution, error) {
	paths := conflictPaths(conflicts)
	if m.Resolver == nil {
		return nil, &ConflictError{Paths: paths}
	}
	resolutions, err := m.Resolver.ResolveConflicts(ctx, conflicts)
	if err != nil {
		return nil, err
	}
	for _, c := range conflicts {
		r, ok := resolutions[c.ID]
		if !ok || !r.valid() {
			return nil, &ConflictError{Paths: paths}
		}
	}
	return resolutions, nil
}

// valid は解決が承認として成立しているかを返す（既定値・未知の値を承認として扱わない）。
func (r Resolution) valid() bool {
	switch r.Choice {
	case ChoiceTheirs, ChoiceOurs:
		return true
	case ChoiceBoth:
		// 「両立させる」は統合後の内容を伴う（空の内容で対象を消す経路にしない）。
		return strings.TrimSpace(r.Merged) != ""
	case ChoiceOpenIssue:
		// 起票した未決事項の ID と、暫定採用する側の両方が要る。
		return r.OpenIssueID != "" && (r.Adopt == ChoiceTheirs || r.Adopt == ChoiceOurs)
	}
	return false
}

// content は解決後の提示単位の内容を返す（空文字 = 対象が無い状態）。
func (r Resolution) content(c Conflict) string {
	switch r.Choice {
	case ChoiceTheirs:
		return c.Theirs
	case ChoiceOurs:
		return c.Ours
	case ChoiceBoth:
		return r.Merged
	case ChoiceOpenIssue:
		if r.Adopt == ChoiceTheirs {
			return c.Theirs
		}
		return c.Ours
	}
	return c.Ours
}

func conflictPaths(conflicts []Conflict) []string {
	seen := map[string]bool{}
	var paths []string
	for _, c := range conflicts {
		if !seen[c.Path] {
			seen[c.Path] = true
			paths = append(paths, c.Path)
		}
	}
	sort.Strings(paths)
	return paths
}

// ---- パス 1 本分の統合 -------------------------------------------------------

// pathMerge は両側が変更した 1 パスの統合状態。
type pathMerge struct {
	path string
	rule rule
	mode string // 統合後のファイルモード

	// 構造化ファイル（エントリ・項目単位）の統合結果。keys は並び、values はキー → 内容。
	keys   []string
	values map[string]string
	// structured は構造化ファイルか（false = ファイル全体が 1 単位）。
	structured bool

	// whole はファイル全体が 1 単位のときの統合後の内容（競合が無ければ使わない）。
	whole string

	conflicts   []Conflict
	resolutions map[string]Resolution
}

// readPathMerge は 1 パスの三面を読み、規則に従って統合できる部分を統合し、残りを競合にする。
func (m ThreeWayMerger) readPathMerge(ctx context.Context, repo *Repo, in MergeInput,
	p string, mine, their treeChange) (*pathMerge, error) {

	r := ruleFor(p)
	base, err := blobAt(ctx, repo, in.Base, p)
	if err != nil {
		return nil, err
	}
	oursBody, err := blobAt(ctx, repo, in.Ours, p)
	if err != nil {
		return nil, err
	}
	theirsBody, err := blobAt(ctx, repo, in.Theirs, p)
	if err != nil {
		return nil, err
	}
	mode := mine.Mode
	if mode == "" || mode == "000000" {
		mode = their.Mode
	}
	pm := &pathMerge{path: p, rule: r, mode: mode, resolutions: map[string]Resolution{}}

	split := r.split
	if split == nil {
		// ファイル全体が 1 単位（レコード・章・所有権）。両側が変えている時点で競合。
		pm.whole = oursBody
		pm.conflicts = append(pm.conflicts, Conflict{
			ID: p, Unit: r.unit, Label: r.label(p, ""), Category: categoryOf(p), Path: p,
			TheirsAuthor: in.TheirsAuthor, TheirsBranch: in.TheirsBranch,
			Base: base, Theirs: theirsBody, Ours: oursBody,
		})
		return pm, nil
	}

	baseParts, _, err := splitOrFail(split, p, base)
	if err != nil {
		return nil, err
	}
	oursParts, oursOrder, err := splitOrFail(split, p, oursBody)
	if err != nil {
		return nil, err
	}
	theirsParts, theirsOrder, err := splitOrFail(split, p, theirsBody)
	if err != nil {
		return nil, err
	}
	pm.structured = true
	pm.values = map[string]string{}
	pm.keys = mergeOrder(oursOrder, theirsOrder)
	for _, key := range pm.keys {
		b, o, t := baseParts[key], oursParts[key], theirsParts[key]
		switch {
		case o == t: // 双方が同じ結果（変更なし・同一の変更・双方の削除）
			pm.values[key] = o
		case o == b: // 自分は変えていない → 相手を採る
			pm.values[key] = t
		case t == b: // 相手は変えていない → 自分を採る
			pm.values[key] = o
		default:
			pm.conflicts = append(pm.conflicts, Conflict{
				ID: p + "#" + key, Unit: r.unit, Label: r.label(p, key), Category: categoryOf(p),
				Path: p, Key: key, TheirsAuthor: in.TheirsAuthor, TheirsBranch: in.TheirsBranch,
				Base: b, Theirs: t, Ours: o,
			})
		}
	}
	return pm, nil
}

// splitOrFail は構造化ファイルを分割する。解釈できないときは統合を中止する
// （壊れた内容を黙って片側で上書きしない）。
func splitOrFail(split splitFunc, p, data string) (map[string]string, []string, error) {
	if strings.TrimSpace(data) == "" {
		return map[string]string{}, nil, nil
	}
	parts, order, err := split(data)
	if err != nil {
		return nil, nil, fmt.Errorf("%s の内容を解釈できないため統合できません: %w", p, err)
	}
	return parts, order, nil
}

// mergeOrder は自分側の並びを保ち、相手側にしかないキーを相手の並びで後ろに足す（決定的）。
func mergeOrder(ours, theirs []string) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(ours)+len(theirs))
	for _, k := range ours {
		if !seen[k] {
			seen[k] = true
			out = append(out, k)
		}
	}
	for _, k := range theirs {
		if !seen[k] {
			seen[k] = true
			out = append(out, k)
		}
	}
	return out
}

// applyResolutions は承認結果を統合結果へ反映する。
func (pm *pathMerge) applyResolutions(resolutions map[string]Resolution) error {
	for _, c := range pm.conflicts {
		r := resolutions[c.ID]
		pm.resolutions[c.ID] = r
		content := r.content(c)
		if pm.structured {
			pm.values[c.Key] = content
			continue
		}
		pm.whole = content
	}
	return nil
}

// change は統合後の内容を git のオブジェクトとして書き、ツリーへの変更を返す。
func (pm *pathMerge) change(ctx context.Context, repo *Repo) (treeChange, error) {
	var content string
	if pm.structured {
		out, err := pm.rule.join(pm.keys, pm.values)
		if err != nil {
			return treeChange{}, fmt.Errorf("%s を組み立てられません: %w", pm.path, err)
		}
		content = out
	} else {
		content = pm.whole
	}
	if content == "" {
		// 双方の承認の結果、対象が無くなった（削除を採った）。
		return treeChange{Mode: "000000", Status: 'D', Path: pm.path}, nil
	}
	sha, err := repo.GitInput(ctx, []byte(content), "hash-object", "-w", "-t", "blob", "--stdin")
	if err != nil {
		return treeChange{}, err
	}
	return treeChange{Mode: pm.mode, SHA: strings.TrimSpace(sha), Status: 'M', Path: pm.path}, nil
}

// blobAt は commit におけるパスの内容を返す（無ければ空文字）。
func blobAt(ctx context.Context, repo *Repo, commit, p string) (string, error) {
	if commit == "" {
		commit = emptyTree
	}
	out, err := repo.Git(ctx, "cat-file", "blob", commit+":"+p)
	if err != nil {
		var ge *gitError
		if asGitError(err, &ge) && ge.exitCode == 128 {
			return "", nil // そのコミットに当該パスが無い
		}
		return "", err
	}
	return out, nil
}

// ---- ファイル種別ごとの規則 -------------------------------------------------

// splitFunc はファイル内容を「キー → 内容」と並びへ分ける。
type splitFunc func(data string) (map[string]string, []string, error)

// joinFunc はキーの並びと内容からファイル内容を組み立てる。
type joinFunc func(keys []string, values map[string]string) (string, error)

// rule は 1 種別のマージ規則。split が nil ならファイル全体が 1 単位。
type rule struct {
	unit  Unit
	split splitFunc
	join  joinFunc
	// labelOf は利用者向けの対象名（key はエントリ・項目のキー。ファイル全体では空）。
	labelOf func(p, key string) string
}

func (r rule) label(p, key string) string {
	if r.labelOf != nil {
		return r.labelOf(p, key)
	}
	return p
}

// ruleFor はパスに対応する規則を返す。
//
// 表に無いパスはファイル全体を 1 単位のレコードとして扱う（自動統合しない側に倒す）。
func ruleFor(p string) rule {
	p = strings.ReplaceAll(p, "\\", "/")
	if r, ok := fileRules[p]; ok {
		return r
	}
	top, rest, _ := strings.Cut(p, "/")
	switch top {
	case "sessions", "audit":
		return rule{unit: UnitOwnership, labelOf: func(p, _ string) string {
			return CategoryLabel(categoryOf(p)) + " " + baseName(p)
		}}
	case "documents":
		if strings.Contains(rest, "/draft/") {
			return rule{unit: UnitChapter, labelOf: func(p, _ string) string {
				return docKindOf(p) + "ドラフト " + baseName(p) + " 章"
			}}
		}
	}
	return rule{unit: UnitRecord, labelOf: func(p, _ string) string {
		return CategoryLabel(categoryOf(p)) + " " + baseName(p)
	}}
}

// fileRules はプロジェクトフォルダ直下の構造化ファイル。
//
// パス名は projectstore の定数と一致していること（drift の検出は threeway_test.go）。
var fileRules = map[string]rule{
	"terms.yaml":        entryRule(UnitEntry, "terms", "name", "用語"),
	"members.yaml":      entryRule(UnitEntry, "members", "author_id", "メンバー"),
	"roster.yaml":       entryRule(UnitEntry, "stakeholders", "id", "ステークホルダー"),
	"perspectives.yaml": entryRule(UnitEntry, "perspectives", "id", "観点"),
	"reservations.yaml": entryRule(UnitEntry, "reservations", "target", "予約"),
	"id-ranges.yaml":    idRangeRule(),
	"project.yaml":      fieldRule(),
}

func baseName(p string) string {
	if i := strings.LastIndex(p, "/"); i >= 0 {
		p = p[i+1:]
	}
	return strings.TrimSuffix(p, ".md")
}

// docKindOf は documents/<種別>/... の種別の表示名。
func docKindOf(p string) string {
	parts := strings.Split(p, "/")
	if len(parts) < 2 {
		return "成果物"
	}
	switch parts[1] {
	case "requirements":
		return "要件定義書"
	case "basic-design":
		return "基本設計書"
	}
	return "成果物"
}

// ---- エントリ単位（コレクションファイル） -----------------------------------

// entryRule は「最上位キーの下に並ぶエントリ」を keyField で識別して統合する規則。
func entryRule(unit Unit, listKey, keyField, label string) rule {
	return rule{
		unit: unit,
		split: func(data string) (map[string]string, []string, error) {
			return splitEntries(data, listKey, keyField)
		},
		join: func(keys []string, values map[string]string) (string, error) {
			return joinEntries(listKey, keys, values)
		},
		labelOf: func(_, key string) string {
			if key == "" {
				return label
			}
			return label + "「" + key + "」"
		},
	}
}

// splitEntries は listKey の並びをキー（keyField の値）→ エントリの YAML へ分ける。
func splitEntries(data, listKey, keyField string) (map[string]string, []string, error) {
	var doc yaml.Node
	if err := yaml.Unmarshal([]byte(data), &doc); err != nil {
		return nil, nil, err
	}
	seq := seqUnder(&doc, listKey)
	parts := map[string]string{}
	var order []string
	for i, item := range seq {
		key := scalarField(item, keyField)
		if key == "" {
			return nil, nil, fmt.Errorf("%d 件目に %s がありません", i+1, keyField)
		}
		if _, dup := parts[key]; dup {
			return nil, nil, fmt.Errorf("%s が重複しています: %s", keyField, key)
		}
		out, err := marshalNode(item)
		if err != nil {
			return nil, nil, err
		}
		parts[key] = out
		order = append(order, key)
	}
	return parts, order, nil
}

// joinEntries はエントリを listKey の並びへ戻す。
func joinEntries(listKey string, keys []string, values map[string]string) (string, error) {
	seq := &yaml.Node{Kind: yaml.SequenceNode, Tag: "!!seq"}
	for _, key := range keys {
		body := values[key]
		if strings.TrimSpace(body) == "" {
			continue // 削除されたエントリ
		}
		node, err := parseNode(body)
		if err != nil {
			return "", fmt.Errorf("%s の内容を解釈できません: %w", key, err)
		}
		seq.Content = append(seq.Content, node)
	}
	if len(seq.Content) == 0 {
		return listKey + ": []\n", nil
	}
	root := &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map", Content: []*yaml.Node{
		{Kind: yaml.ScalarNode, Tag: "!!str", Value: listKey}, seq,
	}}
	return marshalNode(root)
}

// ---- 項目単位（project.yaml） -----------------------------------------------

// fieldRule は最上位のキーごとに統合する規則。
func fieldRule() rule {
	return rule{
		unit:  UnitField,
		split: splitFields,
		join:  joinFields,
		labelOf: func(p, key string) string {
			if key == "" {
				return CategoryLabel(categoryOf(p))
			}
			return CategoryLabel(categoryOf(p)) + "の " + key
		},
	}
}

func splitFields(data string) (map[string]string, []string, error) {
	var doc yaml.Node
	if err := yaml.Unmarshal([]byte(data), &doc); err != nil {
		return nil, nil, err
	}
	m := mappingRoot(&doc)
	parts := map[string]string{}
	var order []string
	for i := 0; i+1 < len(m); i += 2 {
		key := m[i].Value
		out, err := marshalNode(m[i+1])
		if err != nil {
			return nil, nil, err
		}
		parts[key] = out
		order = append(order, key)
	}
	return parts, order, nil
}

func joinFields(keys []string, values map[string]string) (string, error) {
	root := &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
	for _, key := range keys {
		body := values[key]
		if strings.TrimSpace(body) == "" {
			continue // 削除された項目
		}
		node, err := parseNode(body)
		if err != nil {
			return "", fmt.Errorf("%s の内容を解釈できません: %w", key, err)
		}
		root.Content = append(root.Content,
			&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: key}, node)
	}
	if len(root.Content) == 0 {
		return "{}\n", nil
	}
	return marshalNode(root)
}

// ---- 番号帯（id-ranges.yaml） -----------------------------------------------

// idRangeRule は確保済み区間 1 件を単位として統合する規則。
//
// 区間は「対象種別 × 作業者 × 下限」で一意であり、**自分の区間を書くのは自分だけ**であるため、
// 別々の作業者が確保した区間は競合せずに合流する（作業者ごとの区画のため実質競合しない）。
func idRangeRule() rule {
	return rule{
		unit:  UnitEntry,
		split: splitIDRanges,
		join:  joinIDRanges,
		labelOf: func(_, key string) string {
			kind, rest, _ := strings.Cut(key, "#")
			author, _, _ := strings.Cut(rest, "#")
			if author == "" {
				return "番号帯"
			}
			return "番号帯（" + kind + " / " + author + "）"
		},
	}
}

// idRangeKey は「対象種別#作業者#下限」。
func idRangeKey(kind string, item *yaml.Node) string {
	return kind + "#" + scalarField(item, "author_id") + "#" + scalarField(item, "from")
}

func splitIDRanges(data string) (map[string]string, []string, error) {
	var doc yaml.Node
	if err := yaml.Unmarshal([]byte(data), &doc); err != nil {
		return nil, nil, err
	}
	ranges := mapUnder(&doc, "ranges")
	parts := map[string]string{}
	var order []string
	for i := 0; i+1 < len(ranges); i += 2 {
		kind := ranges[i].Value
		for _, item := range ranges[i+1].Content {
			key := idRangeKey(kind, item)
			if strings.HasSuffix(key, "#") || strings.Contains(key, "##") {
				return nil, nil, fmt.Errorf("%s の区間に作業者または下限がありません", kind)
			}
			if _, dup := parts[key]; dup {
				return nil, nil, fmt.Errorf("区間が重複しています: %s", key)
			}
			out, err := marshalNode(item)
			if err != nil {
				return nil, nil, err
			}
			parts[key] = out
			order = append(order, key)
		}
	}
	return parts, order, nil
}

func joinIDRanges(keys []string, values map[string]string) (string, error) {
	// 対象種別ごとにまとめ、重なりを解消してから下限の昇順で並べる（同一の入力に対して結果が一意）。
	byKind := map[string][]string{}
	var kinds []string
	for _, key := range keys {
		if strings.TrimSpace(values[key]) == "" {
			continue // 削除された区間
		}
		kind, _, _ := strings.Cut(key, "#")
		if _, ok := byKind[kind]; !ok {
			kinds = append(kinds, kind)
		}
		byKind[kind] = append(byKind[kind], key)
	}
	sort.Strings(kinds)
	root := &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
	ranges := &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
	for _, kind := range kinds {
		nodes := make([]*yaml.Node, 0, len(byKind[kind]))
		for _, key := range byKind[kind] {
			node, err := parseNode(values[key])
			if err != nil {
				return "", fmt.Errorf("%s の区間を解釈できません: %w", key, err)
			}
			nodes = append(nodes, node)
		}
		resolveRangeOverlaps(nodes)
		seq := &yaml.Node{Kind: yaml.SequenceNode, Tag: "!!seq", Content: nodes}
		ranges.Content = append(ranges.Content,
			&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: kind}, seq)
	}
	if len(ranges.Content) == 0 {
		return "ranges: {}\n", nil
	}
	root.Content = append(root.Content,
		&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: "ranges"}, ranges)
	return marshalNode(root)
}

// resolveRangeOverlaps は 1 対象種別の区間の重なりを決定的な規則で解消し、下限の昇順へ並べ替える
// （重なりの解消）。
//
//  1. `reserved_at` が早い方が区間を保持する。同時刻は `author_id` の辞書順で先の方が保持する。
//  2. 保持しない側を、当該種別の最大上限の次へ同じ幅で移す。
//
// 双方の作業コピーが同じ和集合を見て同じ規則で判断するため、結果は両者で一致する。
// 移す前に採番済みの ID は変更しない（区間の外にある既存 ID が生じうる）。
func resolveRangeOverlaps(nodes []*yaml.Node) {
	type interval struct{ from, to int }
	order := append([]*yaml.Node(nil), nodes...)
	// 優先順位（保持する順）: reserved_at の昇順 → author_id の辞書順
	sort.SliceStable(order, func(i, j int) bool {
		ai, aj := rangeReservedAt(order[i]), rangeReservedAt(order[j])
		if !ai.Equal(aj) {
			return ai.Before(aj)
		}
		return scalarField(order[i], "author_id") < scalarField(order[j], "author_id")
	})
	maxTo := 0
	for _, n := range nodes {
		if to, ok := intField(n, "to"); ok && to > maxTo {
			maxTo = to
		}
	}
	var kept []interval
	for _, n := range order {
		from, okFrom := intField(n, "from")
		to, okTo := intField(n, "to")
		if !okFrom || !okTo || to < from {
			continue // 形の壊れた区間は動かさない（読み込み側が形を検証する）
		}
		overlaps := false
		for _, k := range kept {
			if from <= k.to && k.from <= to {
				overlaps = true
				break
			}
		}
		if !overlaps {
			kept = append(kept, interval{from, to})
			continue
		}
		width := to - from
		newFrom := maxTo + 1
		newTo := newFrom + width
		setScalarField(n, "from", strconv.Itoa(newFrom))
		setScalarField(n, "to", strconv.Itoa(newTo))
		kept = append(kept, interval{newFrom, newTo})
		maxTo = newTo
	}
	// 出力は下限の昇順（同値は作業者の辞書順）。整列は文字列順ではなく数値で行う。
	sort.SliceStable(nodes, func(i, j int) bool {
		fi, _ := intField(nodes[i], "from")
		fj, _ := intField(nodes[j], "from")
		if fi != fj {
			return fi < fj
		}
		return scalarField(nodes[i], "author_id") < scalarField(nodes[j], "author_id")
	})
}

// rangeReservedAt は区間の確保日時（読めなければゼロ値 = 最優先）。
func rangeReservedAt(node *yaml.Node) time.Time {
	v := scalarField(node, "reserved_at")
	for _, layout := range []string{time.RFC3339Nano, "2006-01-02T15:04:05", "2006-01-02 15:04:05"} {
		if t, err := time.Parse(layout, v); err == nil {
			return t.UTC()
		}
	}
	return time.Time{}
}

// intField はマッピングノードの数値フィールド。
func intField(node *yaml.Node, field string) (int, bool) {
	n, err := strconv.Atoi(strings.TrimSpace(scalarField(node, field)))
	if err != nil {
		return 0, false
	}
	return n, true
}

// setScalarField はマッピングノードのスカラーフィールドを書き換える（無ければ何もしない）。
func setScalarField(node *yaml.Node, field, value string) {
	if node == nil || node.Kind != yaml.MappingNode {
		return
	}
	for i := 0; i+1 < len(node.Content); i += 2 {
		if node.Content[i].Value == field {
			node.Content[i+1].Value = value
			node.Content[i+1].Tag = "!!int"
			node.Content[i+1].Style = 0
			return
		}
	}
}

// ---- YAML ノードの補助 -------------------------------------------------------

// mappingRoot は文書の最上位マッピングの Content（キー・値の交互列）を返す。
func mappingRoot(doc *yaml.Node) []*yaml.Node {
	if doc.Kind == yaml.DocumentNode && len(doc.Content) > 0 {
		doc = doc.Content[0]
	}
	if doc.Kind != yaml.MappingNode {
		return nil
	}
	return doc.Content
}

// seqUnder は最上位マッピングの key に対応する並びの要素を返す。
func seqUnder(doc *yaml.Node, key string) []*yaml.Node {
	m := mappingRoot(doc)
	for i := 0; i+1 < len(m); i += 2 {
		if m[i].Value == key && m[i+1].Kind == yaml.SequenceNode {
			return m[i+1].Content
		}
	}
	return nil
}

// mapUnder は最上位マッピングの key に対応するマッピングの Content を返す。
func mapUnder(doc *yaml.Node, key string) []*yaml.Node {
	m := mappingRoot(doc)
	for i := 0; i+1 < len(m); i += 2 {
		if m[i].Value == key && m[i+1].Kind == yaml.MappingNode {
			return m[i+1].Content
		}
	}
	return nil
}

// scalarField はマッピングノードの field の値（スカラー）を返す。
func scalarField(node *yaml.Node, field string) string {
	if node == nil || node.Kind != yaml.MappingNode {
		return ""
	}
	for i := 0; i+1 < len(node.Content); i += 2 {
		if node.Content[i].Value == field {
			return node.Content[i+1].Value
		}
	}
	return ""
}

// marshalNode はノードを YAML テキストにする（提示・保持の単位）。
func marshalNode(node *yaml.Node) (string, error) {
	var b strings.Builder
	enc := yaml.NewEncoder(&b)
	enc.SetIndent(2)
	if err := enc.Encode(node); err != nil {
		return "", err
	}
	if err := enc.Close(); err != nil {
		return "", err
	}
	return b.String(), nil
}

// parseNode は YAML テキストをノードへ戻す。
func parseNode(text string) (*yaml.Node, error) {
	var doc yaml.Node
	if err := yaml.Unmarshal([]byte(text), &doc); err != nil {
		return nil, err
	}
	if doc.Kind == yaml.DocumentNode && len(doc.Content) > 0 {
		return doc.Content[0], nil
	}
	return nil, fmt.Errorf("内容が空です")
}
