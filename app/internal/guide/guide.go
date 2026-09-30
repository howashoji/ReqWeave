// Package guide は「いま要件定義のどの工程にいて、次に何をするか」を
// プロジェクトの状態から導く。
//
// 本パッケージは、要件定義フェーズの業務の流れ（資料取込 → 対話 ⇄ 質問票 → 生成 → 指摘 → 確定）と、
// その途中で「次に何をするか」が分かれる条件を、そのままコードに写したものである。
//
// **判断をここに集める**理由: 「次に何をするか」は画面をまたぐ業務判断であり、
// 画面ごとに書くと同じ判断が分散して食い違う。画面側は本パッケージの結果を表示するだけにする。
// 画面の一時状態（選択中の資料・同意チェックなど）はここでは扱わない（画面側の責務）。
package guide

import "strconv"

// Stage は要件定義フェーズの工程（業務の流れの 1 段）。
//
// 工程の説明（Purpose）と、その工程で使う画面（Screen / Target）も持つ。
// **工程の説明の正本はここ 1 か所**であり、画面側で別の言い回しを作らない。
type Stage struct {
	ID    string `json:"id"`
	Label string `json:"label"`
	// Purpose はその工程で何をするかの 1 文（利用者の言葉。内部用語を出さない）。
	Purpose string `json:"purpose"`
	// Screen はその工程で使う画面の名前（**画面上の表記のまま**）。
	// Target はそこへ移る行き先（画面側の view 名と同じ集合）。
	Screen string `json:"screen"`
	Target string `json:"target"`
}

// Stages は工程の並び。**利用者に見せる順序**であり、実際の進行はループする
// （対話 ⇄ 質問票、生成 → 指摘 → 対話）。どの工程からでも始められるよう、順序を強制はしない。
//
// 「要件定義を確定する」は専用の画面を持たず、要件定義書の画面で確定する。
var Stages = []Stage{
	{ID: "imports", Label: "資料を取り込む",
		Purpose: "手元にある既存の資料を読み込ませ、そこから決定事項や要件の候補を出します。",
		Screen:  "資料取込", Target: "imports"},
	{ID: "dialogue", Label: "対話で要件を詰める",
		Purpose: "AI の質問に答えていき、決定事項・未決事項・要件項目を書き出していきます。",
		Screen:  "対話", Target: "dialogue"},
	{ID: "questionnaires", Label: "質問票で関係者に聞く",
		Purpose: "自分では決められない未決事項を質問票にして、関係者へファイルで渡します。",
		Screen:  "質問票", Target: "questionnaires"},
	{ID: "import-answers", Label: "回答を取り込む",
		Purpose: "関係者から返ってきた回答を読み込み、内容を確かめて要件へ反映します。",
		Screen:  "回答取込", Target: "import-answers"},
	{ID: "documents", Label: "要件定義書を作る",
		Purpose: "整理した内容から要件定義書を生成し、本文を読んで直します。",
		Screen:  "要件定義書", Target: "documents"},
	{ID: "confirm", Label: "要件定義を確定する",
		Purpose: "確定前の確認結果を見て、内容に合意できたら版として確定します。",
		Screen:  "要件定義書", Target: "documents"},
}

// State は導出に使うプロジェクトの状態。**永続している事実だけ**を持つ
// （画面の一時状態は持たない）。
type State struct {
	// UnanalyzedImports は取り込み済みで、まだ分析していない資料の件数
	// （テキストを取り出せなかった資料は数えない＝分析できないため）。
	UnanalyzedImports int
	// Requirements は要件項目の総数。DraftRequirements はうち未合意の件数。
	Requirements      int
	DraftRequirements int
	// BlockingIssues は要件項目をブロックしている未決事項の件数。
	BlockingIssues int
	// OpenIssues は未決状態の未決事項の総数（質問票の発行元になりうる）。
	OpenIssues int
	// Completeness は章観点の充足率の平均（0〜100）。
	Completeness int
	// IssuedQuestionnaires は発行済みで回答待ちの質問票。
	// AnsweredQuestionnaires は回答が返ってきて、まだ反映していない質問票。
	IssuedQuestionnaires   int
	AnsweredQuestionnaires int
	// HasDraftDocument は要件定義書のドラフトが生成済みか。
	// ConfirmedDocument は確定版があるか。
	HasDraftDocument  bool
	ConfirmedDocument bool
	// Confirmable は確定できる状態か（dialogue.Confirmable の結果）。
	Confirmable bool
}

// Guide は画面へ渡す導出結果。
type Guide struct {
	StageID    string `json:"stageId"`
	StageLabel string `json:"stageLabel"`
	// StageIndex は 1 始まり。StageTotal は工程の総数。
	StageIndex int `json:"stageIndex"`
	StageTotal int `json:"stageTotal"`
	// Next は次にやることの 1 文（利用者の言葉。内部用語を出さない）。
	Next string `json:"next"`
	// Target は次にやることを行う画面。Button はそこへ移る操作の文言。
	// 現在地の画面で完結する場合も、どの画面の話かが分かるよう必ず入れる。
	Target string `json:"target"`
	Button string `json:"button"`
	// Note は「ほかにも取れる道」の 1 文（任意）。NoteTarget / NoteButton は同じ形。
	Note       string `json:"note,omitempty"`
	NoteTarget string `json:"noteTarget,omitempty"`
	NoteButton string `json:"noteButton,omitempty"`
}

// stageIndex は Stages 内の位置（1 始まり）。未知の ID は 0 を返す。
func stageIndex(id string) int {
	for i, s := range Stages {
		if s.ID == id {
			return i + 1
		}
	}
	return 0
}

// at は工程 ID から Guide の骨組みを作る。
func at(id, next, target, button string) Guide {
	g := Guide{StageID: id, StageIndex: stageIndex(id), StageTotal: len(Stages),
		Next: next, Target: target, Button: button}
	for _, s := range Stages {
		if s.ID == id {
			g.StageLabel = s.Label
		}
	}
	return g
}

// Derive は状態から「いまの工程」と「次にやること」を導く。
//
// 上から順に見て、最初に当てはまったものを返す（**利用者を待たせているもの**から先に出す）。
// 分岐は要件定義フェーズの業務の流れに沿う:
//
//  1. 返送された回答が届いている  … 相手を待たせた分、反映が最優先
//  2. 分析していない資料がある    … 取り込みは「原本の登録」までなので、分析しないと何も増えない
//  3. 要件項目が 1 件も無い       … まず対話で要件を出す
//  4. 章観点が埋まっていない      … 対話を続ける
//  5. 要件をブロックする未決がある … 決着させないと確定できない
//  6. 未合意の要件項目がある      … 合意にする
//  7. 成果物が未生成             … 生成してレビューへ
//  8. 確定できる                 … 確定する
//  9. 確定済み                   … 次のフェーズへ
func Derive(s State) Guide {
	// 1. 回答が返ってきている。
	if s.AnsweredQuestionnaires > 0 {
		return at("import-answers",
			"関係者から返ってきた回答を取り込んで、要件へ反映します。",
			"import-answers", "回答取込を開く")
	}
	// 2. 取り込んだだけで分析していない資料がある（取り込みは原本の登録までで、分析は別の操作）。
	if s.UnanalyzedImports > 0 {
		return at("imports",
			plural(s.UnanalyzedImports)+"の資料がまだ分析されていません。分析して決定事項や要件の候補を出します。",
			"imports", "資料取込を開く")
	}
	// 3. 要件項目がまだ無い。
	if s.Requirements == 0 {
		g := at("dialogue",
			"対話で質問に答えて、要件を書き出していきます。",
			"dialogue", "対話へ移る")
		return withQuestionnaireNote(g, s)
	}
	// 4. 章観点が埋まっていない。
	if s.Completeness < 100 {
		g := at("dialogue",
			"対話を続けて、まだ埋まっていない章を詰めます。",
			"dialogue", "対話へ移る")
		return withQuestionnaireNote(g, s)
	}
	// 5. 要件をブロックしている未決事項がある。
	if s.BlockingIssues > 0 {
		g := at("dialogue",
			plural(s.BlockingIssues)+"の未決事項が要件をブロックしています。決着させると確定へ進めます。",
			"records", "決定・未決を開く")
		return withQuestionnaireNote(g, s)
	}
	// 6. 未合意の要件項目がある。
	if s.DraftRequirements > 0 {
		return at("dialogue",
			plural(s.DraftRequirements)+"の要件項目がまだ合意済みになっていません。内容を確認して合意にします。",
			"requirements", "要件項目を開く")
	}
	// 7. 成果物が未生成。
	if !s.HasDraftDocument {
		return at("documents",
			"要件がそろいました。要件定義書を生成してレビューします。",
			"documents", "要件定義書を開く")
	}
	// 8. 確定できる。
	if s.Confirmable {
		return at("confirm",
			"要件定義書を確認し、内容に合意できたら確定します。",
			"documents", "要件定義書を開く")
	}
	// 9. 確定済み。
	if s.ConfirmedDocument {
		return at("confirm",
			"要件定義は確定済みです。基本設計へ進むか、開発AI向けにエクスポートできます。",
			"documents", "要件定義書を開く")
	}
	// ここに来るのは「生成済みだが確定できない」状態。理由は成果物画面の確定前チェックが示す。
	return at("documents",
		"要件定義書を確認します。確定できない理由は成果物の画面に出ます。",
		"documents", "要件定義書を開く")
}

// withQuestionnaireNote は、担当者だけでは決められない論点を関係者へ聞く道を添える
// （担当者だけで決められない論点は関係者へ聞く、という業務の流れの分岐。未決事項がある間だけ出す）。
func withQuestionnaireNote(g Guide, s State) Guide {
	switch {
	case s.IssuedQuestionnaires > 0:
		g.Note = plural(s.IssuedQuestionnaires) + "の質問票が回答待ちです。"
		g.NoteTarget = "questionnaires"
		g.NoteButton = "質問票を開く"
	case s.OpenIssues > 0:
		g.Note = "自分では決められない未決事項は、質問票にして関係者へ聞けます。"
		g.NoteTarget = "questionnaires"
		g.NoteButton = "質問票を開く"
	}
	return g
}

// plural は件数の日本語表記（「3 件」）。表示は画面で組み立てず、文の一部としてここで作る。
func plural(n int) string {
	return strconv.Itoa(n) + " 件"
}
