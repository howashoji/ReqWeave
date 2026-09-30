// crashwriter は、強制終了されても保存済みのデータが失われず壊れないことの検証用に、**外から強制終了される側**の
// プロセスとして保存処理を回し続ける。
//
// なぜ別プロセスが要るか: 同一プロセスの中で Store を捨てて開き直すだけでは
// OS のページキャッシュが応えてしまい、fsync を外しても振る舞いが変わらない
// （故障注入の検証で実測）。**実際に SIGKILL で落とす**ことで、
// 「直前まで保存できていたものが残るか」「置換の途中が見えないか」を振る舞いとして確かめる。
//
// 本プログラムは検証専用であり配布物には入らない（`tools/` 配下は wails のビルド対象外）。
//
// 使い方:
//
//	crashwriter -mode append -root <プロジェクトフォルダ> -session S-0001
//	crashwriter -mode atomic -root <プロジェクトフォルダ> -target <相対パス> -size 262144
//
// 標準出力に 1 行 1 イベントで進捗を出す（親が読んで、殺す時機と期待値を決める）:
//
//	ready                 起動して書き込みを始められる
//	writing <n>           n 回目の保存を**始めた**
//	committed <n> <id>    n 回目の保存が**返った**（= ここまでは残っているはず）
//
// `writing` の後に対応する `committed` が出ないまま死んでいれば、
// **保存処理の最中に落ちた**ことの実測になる（空振りの試行を成功に数えないため）。
package main

import (
	"bufio"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/howashoji/ReqWeave/app/internal/projectstore"
)

func main() {
	var (
		mode    = flag.String("mode", "", "append（発話の追記）または atomic（原子的置換）")
		root    = flag.String("root", "", "プロジェクトフォルダ")
		session = flag.String("session", "", "append のときの対話セッション ID")
		target  = flag.String("target", "", "atomic のときの保存先（プロジェクトフォルダからの相対パス）")
		size    = flag.Int("size", 256*1024, "atomic のときの 1 回あたりの本文の大きさ（バイト）")
		author  = flag.String("author", "k.sato@example.co.jp", "作業者の利用者 ID")
	)
	flag.Parse()

	if err := run(*mode, *root, *session, *target, *size, *author); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(mode, root, session, target string, size int, authorID string) error {
	if root == "" {
		return fmt.Errorf("-root を指定してください")
	}
	out := bufio.NewWriter(os.Stdout)
	// 進捗は 1 行ごとに流す（親が読めないまま殺すと時機を決められない）。
	emit := func(format string, args ...any) {
		fmt.Fprintf(out, format+"\n", args...)
		_ = out.Flush()
	}

	switch mode {
	case "append":
		if session == "" {
			return fmt.Errorf("-session を指定してください")
		}
		s, err := projectstore.Open(root, projectstore.Author{AuthorID: authorID, DisplayName: "佐藤"})
		if err != nil {
			return fmt.Errorf("プロジェクトを開けません: %w", err)
		}
		emit("ready")
		for n := 1; ; n++ {
			emit("writing %d", n)
			id, err := s.AppendUtterance(session, projectstore.Utterance{
				Speaker: projectstore.SpeakerUser,
				Body:    fmt.Sprintf("%d 件目の発話です。%s", n, strings.Repeat("あ", 200)),
			})
			if err != nil {
				return fmt.Errorf("発話 %d の追記に失敗: %w", n, err)
			}
			emit("committed %d %s", n, id)
		}
	case "atomic":
		if target == "" {
			return fmt.Errorf("-target を指定してください")
		}
		path := filepath.Join(root, filepath.FromSlash(target))
		emit("ready")
		for n := 1; ; n++ {
			emit("writing %d", n)
			// 版ごとに中身をすべて変える。半端な内容が見えたら「どの版でもない」ことで検出できる。
			body := []byte(fmt.Sprintf("version=%d\n", n) + strings.Repeat(fmt.Sprintf("%d", n%10), size))
			if err := projectstore.WriteFileAtomic(path, body); err != nil {
				return fmt.Errorf("原子的置換 %d に失敗: %w", n, err)
			}
			emit("committed %d v%d", n, n)
		}
	default:
		return fmt.Errorf("-mode は append か atomic を指定してください（指定: %q）", mode)
	}
}
