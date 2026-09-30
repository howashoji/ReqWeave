//go:build integration

package keymanager_test

import (
	"fmt"
	"os"
	"testing"

	"github.com/howashoji/ReqWeave/app/internal/keymanager/keytest"
)

// TestMain は、前回までの中断で消し損ねた OS セキュアストレージの項目を先に片づける。
//
// 各テストは t.Cleanup で自分の項目を消すが、実行が中断されると Cleanup は走らない。
// 利用者の実環境（login キーチェーン）へ残骸を残さないため、開始時にも掃除する。
func TestMain(m *testing.M) {
	if n := keytest.SweepStale(); n > 0 {
		fmt.Printf("keytest: 前回の中断で残っていたテスト用の項目を %d 件片づけました\n", n)
	}
	os.Exit(m.Run())
}
