//go:build integration

package keytest

// 記録ファイル（pending.jsonl）は**複数のテストバイナリが同時に**読み書きする
// （`go test -tags integration ./...` は keymanager / binding / sync… を並行に走らせ、
// いずれも keytest を使う）。読んで書き戻す形の更新をプロセスをまたいで直列化しないと、
// 後から書いた側が相手の追記を消してしまい（lost update）、
//
//   - 片づけ待ちの記録が消える → **中断で残った項目が二度と片づかない**（本パッケージの目的が崩れる）
//   - 記録を根拠に掃除するテストが不定期に赤くなる（実際に観測した）
//
// が起きる。本ファイルはそれを**再現**して直したことを確かめる。

import (
	"os"
	"os/exec"
	"strconv"
	"testing"

	"github.com/google/uuid"
	"github.com/howashoji/ReqWeave/app/internal/keymanager"
)

// hammerEnv は補助プロセスとして動くときの目印（TestMain が見る）。
const hammerEnv = "REQWEAVE_KEYTEST_HAMMER"

// 別のテストバイナリが記録を読み書きしている間も、こちらの記録が失われないこと。
//
// 記録が残っていれば SweepStale が残骸を片づけられる。失われると片づかない。
func TestRegistrySurvivesConcurrentProcesses(t *testing.T) {
	m := keymanager.New()
	ref := keymanager.Ref{ProviderID: "anthropic", Label: Marker + uuid.NewString()}

	// 「中断で残った項目」= 終了したプロセスが書いた記録。
	add(t, entry{Kind: "key", Ref: ref.String()})
	overwritePIDForTest(t, ref.String(), exitedPID(t))
	if err := m.Register(ref, dummySecret); err != nil {
		t.Fatalf("OS セキュアストレージへ登録できません: %v", err)
	}
	t.Cleanup(func() { _ = m.Delete(ref) })

	// 並行して動く別のテストバイナリを模した補助プロセス（記録の追記と削除を繰り返す）。
	const helpers = 3
	done := make(chan error, helpers)
	for i := 0; i < helpers; i++ {
		go func(i int) {
			cmd := exec.Command(os.Args[0])
			cmd.Env = append(os.Environ(), hammerEnv+"="+strconv.Itoa(i))
			cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
			done <- cmd.Run()
		}(i)
	}
	for i := 0; i < helpers; i++ {
		if err := <-done; err != nil {
			t.Fatalf("補助プロセスが失敗した: %v", err)
		}
	}

	// 記録が残っていること（これが SweepStale の唯一の根拠）。
	found := false
	for _, e := range read(registryPath()) {
		if e.Ref == ref.String() {
			found = true
		}
	}
	if !found {
		t.Error("並行して動く別のテストバイナリの書き込みで、片づけ待ちの記録が消えた（中断で残った項目が片づかなくなる）")
	}

	SweepStale()

	ok, err := m.Exists(ref)
	if err != nil {
		t.Fatalf("存在確認に失敗: %v", err)
	}
	if ok {
		t.Error("中断で残った項目が片づけられていない")
	}
}

// runHammer は補助プロセスの本体（記録の追記と削除を繰り返す）。
//
// **OS セキュアストレージには触れない**（利用者のキーチェーンを汚さないため。
// 確かめたいのは記録ファイルの読み書きの競合であり、鍵の保管ではない）。
func runHammer(id string) {
	for i := 0; i < 300; i++ {
		e := entry{Kind: "key", Ref: "anthropic/" + Marker + "hammer-" + id + "-" + strconv.Itoa(i)}
		addEntry(e)
		remove(e)
	}
}

// TestMain は、目印の環境変数があるときだけ補助プロセスとして動く（それ以外は通常のテスト）。
func TestMain(m *testing.M) {
	if id := os.Getenv(hammerEnv); id != "" {
		runHammer(id)
		os.Exit(0)
	}
	os.Exit(m.Run())
}
