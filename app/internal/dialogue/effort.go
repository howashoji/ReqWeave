package dialogue

// 本ファイルはエフォート段階ごとの対話側の制御値を担う。
//
// 追問深さの**正本は本表**であり、AIプロバイダ抽象化層のエフォート写像は
// 追問深さを写像しない。制御値はシステムプロンプトへ実値として注入し、
// 追問上限の超過はアプリ側でも往復数を数えて次論点への移行を強制する（AI の遵守に依存しない）。

import "github.com/howashoji/ReqWeave/app/internal/aiprovider"

// EffortControls は 1 段階ぶんの制御値。
type EffortControls struct {
	// FollowUpLimit は 1 論点あたりの追問上限（往復数）。
	FollowUpLimit int
	// CriteriaProbe は受け入れ条件の具体化追問の回数。
	CriteriaProbe int
	// OpenIssueProbe は未決事項候補への深掘りの回数。
	OpenIssueProbe int
	// ReflectionScope は要件項目反映案の生成範囲（プロンプトへ入れる説明文）。
	ReflectionScope string
	// CriteriaRule / OpenIssueRule は上記回数に対応する規律の説明文。
	CriteriaRule  string
	OpenIssueRule string
}

// controlsTable は段階ごとの制御値の表（低 / 標準 / 高）。
var controlsTable = map[aiprovider.Effort]EffortControls{
	aiprovider.EffortLow: {
		FollowUpLimit:   1,
		CriteriaProbe:   0,
		OpenIssueProbe:  0,
		ReflectionScope: "回答に明示された内容のみ",
		CriteriaRule:    "受け入れ条件の具体化追問は行わない（担当者の編集に委ねる）",
		OpenIssueRule:   "未決事項候補は記録のみとし、深掘りしない",
	},
	aiprovider.EffortStandard: {
		FollowUpLimit:   3,
		CriteriaProbe:   1,
		OpenIssueProbe:  1,
		ReflectionScope: "明示内容に加え、回答から直接導かれる受け入れ条件案まで",
		CriteriaRule:    "反映案の受け入れ条件に測定可能な数値・条件がない場合のみ 1 回だけ具体化を尋ねる",
		OpenIssueRule:   "未決事項候補には「決める人・期限」を 1 回だけ確認する",
	},
	aiprovider.EffortHigh: {
		FollowUpLimit:   5,
		CriteriaProbe:   2,
		OpenIssueProbe:  2,
		ReflectionScope: "明示内容・受け入れ条件案に加え、関連する他章観点への波及案まで（いずれも承認必須）",
		CriteriaRule:    "受け入れ条件の具体化を 1 回尋ね、さらに例外ケース・境界値の確認を 1 回行う",
		OpenIssueRule:   "未決事項候補には「決める人・期限」を確認し、さらに決着案を 1 回提示する",
	},
}

// ControlsFor はエフォート段階の対話側制御値を返す（未知の段階は既定の標準）。
func ControlsFor(e aiprovider.Effort) EffortControls {
	c, ok := controlsTable[e]
	if !ok {
		return controlsTable[aiprovider.EffortStandard]
	}
	return c
}
