package dialogue

// 本ファイルは新規の要件項目の ID（FR-<グループ>-nnn / NFR-<グループ>-nnn）の
// **グループと種別をアプリが決める**ことを担う（利用者が ID の決まりを知らなくても一貫した ID になるように）。
//
// 利用者に入力させない。AI が抽出時に提案した id_group を形式で検証して採り、
// 不正・未提案なら章観点の既定（metamodel.yaml の id_group）へ倒す。
// 種別（機能 / 非機能）は章観点から決める（AI の出力に委ねない）。

import (
	"regexp"
	"strings"

	"github.com/howashoji/ReqWeave/app/internal/projectstore"
)

// requirementGroupFormat は ID グループの形式（英大文字と数字 2〜8 文字）。
//
// 上限を置くのは、ID が読めなくなる長さの略号を弾くため。
var requirementGroupFormat = regexp.MustCompile(`^[A-Z0-9]{2,8}$`)

// fallbackIDGroup は章観点が分からないときの既定グループ。
const fallbackIDGroup = "GEN"

// nonFunctionalChapter は非機能要件の章観点 ID（metamodel.yaml と対）。
const nonFunctionalChapter = "non-functional-requirements"

// NormalizeRequirementGroup は提案された ID グループを記録に使える形へ整える。
//
// 英字は大文字へそろえ、形式に合わなければ空を返す（呼び出し側が既定へ倒す）。
func NormalizeRequirementGroup(proposed string) string {
	group := strings.ToUpper(strings.TrimSpace(proposed))
	if !requirementGroupFormat.MatchString(group) {
		return ""
	}
	return group
}

// ChapterIDGroup は章観点の既定の ID グループを返す（metamodel.yaml の id_group）。
//
// 未知の章観点・メタモデルを読めない場合は fallbackIDGroup を返す
// （記録できない ID を作らないことを優先する）。
func ChapterIDGroup(chapterID string) string {
	meta, err := LoadMetaModel()
	if err != nil {
		return fallbackIDGroup
	}
	for _, phase := range meta.Phases {
		for _, c := range phase.Chapters {
			if c.ID != chapterID {
				continue
			}
			if group := NormalizeRequirementGroup(c.IDGroup); group != "" {
				return group
			}
			return fallbackIDGroup
		}
	}
	return fallbackIDGroup
}

// ChapterRequirementKind は章観点に対応する要件項目の種別を返す。
//
// 非機能要件の章だけが non-functional（ID 接頭辞 NFR-）。それ以外は functional。
func ChapterRequirementKind(chapterID string) string {
	if chapterID == nonFunctionalChapter {
		return projectstore.RequirementNonFunctional
	}
	return projectstore.RequirementFunctional
}

// ResolveRequirementIDs は新規の要件項目候補の ID グループ・種別を確定する（承認画面へ出す前）。
//
// 画面はここで決まった値を**表示するだけ**にする（入力させない）。
// known は既に使っているグループ（出現順）。提案が無いとき、同じ章観点で既に使っている
// グループがあればそれを使い、ID のまとまりが散らばらないようにする。
func (e *Extraction) ResolveRequirementIDs(known map[string]string) {
	for i := range e.RequirementUpdates {
		c := &e.RequirementUpdates[i]
		if c.Operation != OperationCreate {
			continue
		}
		c.Kind = ChapterRequirementKind(c.Chapter)
		group := NormalizeRequirementGroup(c.IDGroup)
		if group == "" {
			group = NormalizeRequirementGroup(known[c.Chapter])
		}
		if group == "" {
			group = ChapterIDGroup(c.Chapter)
		}
		c.IDGroup = group
	}
}

// RequirementGroupsByChapter は既存の要件項目から「章観点 → 使っているグループ」を作る。
//
// 同じ章観点に複数のグループがある場合は、**件数が最も多いもの**を採る（後から入った
// 一度きりのグループへ引きずられないため）。同数なら ID の並び順で先のものを採る。
func RequirementGroupsByChapter(reqs []projectstore.Requirement) map[string]string {
	type count struct {
		group string
		n     int
	}
	counts := map[string]map[string]int{}
	for _, r := range reqs {
		_, group, _, ok := projectstore.ParseRequirementID(r.ID)
		if !ok || r.Chapter == "" {
			continue
		}
		if counts[r.Chapter] == nil {
			counts[r.Chapter] = map[string]int{}
		}
		counts[r.Chapter][group]++
	}
	out := map[string]string{}
	for chapter, groups := range counts {
		best := count{}
		for group, n := range groups {
			if n > best.n || (n == best.n && best.group != "" && group < best.group) {
				best = count{group: group, n: n}
			}
		}
		if best.group != "" {
			out[chapter] = best.group
		}
	}
	return out
}
