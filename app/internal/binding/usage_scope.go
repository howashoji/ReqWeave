package binding

// 本ファイルは AI 利用量の**集計範囲の併記**。
//
// 共同プロジェクトでは `audit/` も同期の対象であり、他のメンバーの消費は**取り込んだぶんだけ**
// 作業コピーに存在する。したがって集計は「最後に取り込んだ時点までの全メンバーの記録」になり、
// 未取り込みの消費は含まれない。**どの取り込み時点までを反映しているかを表示に併記する**
// （上限に対する判定も同じ範囲で行うため、上限超過が取り込みで初めて判明しうることを示す）。
//
// 単独利用（同期先が未設定）のプロジェクトでは範囲の限定が起きないため、併記しない。

import (
	"fmt"

	"github.com/howashoji/ReqWeave/app/internal/auditlog"
	"github.com/howashoji/ReqWeave/app/internal/projectstore"
)

// usageScope は集計範囲の併記（同期先が未設定なら空）。
type usageScope struct {
	// Notice は利用者向けの 1 文。
	Notice string
	// LastIncorporation は最後に成功した取り込みの日時（表示用。未取り込みなら空）。
	LastIncorporation string
}

// usageScopeOf は作業コピーの同期の記録から集計範囲を組み立てる。
//
// 同期の記録を読めない場合は併記しない（集計そのものを止めない = 監査記録の失敗で作業を止めない方針）。
func usageScopeOf(store *projectstore.Store) usageScope {
	if store == nil || store.Project() == nil || store.Project().Sync == nil {
		return usageScope{} // 単独利用（同期先が未設定）
	}
	last, err := auditlog.LastIncorporation(store.Root())
	if err != nil {
		return usageScope{}
	}
	if last.IsZero() {
		return usageScope{Notice: "まだ取り込みを行っていないため、他のメンバーの利用量は含まれていません。" +
			"取り込むと、その時点までの全メンバーの利用量が集計に入ります。"}
	}
	at := last.Local().Format("2006-01-02 15:04")
	return usageScope{
		LastIncorporation: at,
		Notice: fmt.Sprintf("%s に取り込んだ時点までの、全メンバーの利用量を集計しています。"+
			"それ以降に他のメンバーが消費したぶんは、次に取り込むまで含まれません。", at),
	}
}
