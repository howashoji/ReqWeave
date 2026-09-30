package dialogue

// 本ファイルは対話ループの状態機械を担う。
//
// 保存値は英語識別子（セッションファイルの dialogue_state）。画面表示の日本語ラベルは UI 側が持つ。

import "fmt"

// 対話状態（状態機械の状態）。
const (
	StateQuestioning      = "questioning"       // 質問生成中
	StateAwaitingAnswer   = "awaiting-answer"   // 回答待ち
	StateExtracting       = "extracting"        // 抽出中
	StateAwaitingApproval = "awaiting-approval" // 承認待ち
	StateApplying         = "applying"          // 反映中
	StateSuspended        = "suspended"         // 中断
	StateFailed           = "failed"            // 障害停止
)

// allowedTransitions は状態機械の遷移表。
//
// 「反映中」は AI 呼び出しを伴わないローカル処理のため障害停止へ遷移しない。
// 反映に失敗したとき（競合・記録できない候補）は候補を保全したまま承認待ちへ戻す。
// 戻れないと、競合を確認して押し直す・入力を直して押し直す、のどちらもできなくなる。
var allowedTransitions = map[string][]string{
	StateQuestioning:      {StateAwaitingAnswer, StateSuspended, StateFailed},
	StateAwaitingAnswer:   {StateExtracting, StateSuspended},
	StateExtracting:       {StateAwaitingApproval, StateSuspended, StateFailed},
	StateAwaitingApproval: {StateApplying, StateQuestioning, StateSuspended},
	StateApplying:         {StateQuestioning, StateAwaitingApproval},
	StateFailed:           {StateSuspended},
	StateSuspended:        {StateQuestioning},
}

// CanTransition は遷移が状態機械で許されるかを返す。
func CanTransition(from, to string) bool {
	for _, allowed := range allowedTransitions[from] {
		if allowed == to {
			return true
		}
	}
	return false
}

// ValidateTransition は許されない遷移をエラーにする（状態機械の外側の遷移を作らない）。
func ValidateTransition(from, to string) error {
	if from == to {
		return nil
	}
	if !CanTransition(from, to) {
		return fmt.Errorf("対話状態を %s から %s へ移せません", from, to)
	}
	return nil
}

// IsKnownState は保存されている状態が既知かを返す（未知の値は再開時に中断として扱う）。
func IsKnownState(s string) bool {
	switch s {
	case StateQuestioning, StateAwaitingAnswer, StateExtracting,
		StateAwaitingApproval, StateApplying, StateSuspended, StateFailed:
		return true
	default:
		return false
	}
}
