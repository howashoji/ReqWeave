package binding

// 受け渡しファイルを OS のファイル関連付け・アプリへのドラッグ・2 個目の起動から
// 受け取る経路。
//
// macOS の Finder のダブルクリックは**起動引数ではなく** open-file イベント
//（application:openFile:）でパスを届けるため、引数だけに依存しない受け口を持つ。
// 起動中のアプリがある場合、macOS は新しいプロセスを起こさず同じインスタンスへ
// ファイルを渡す。Windows は 2 個目のプロセスが起動し、引数でパスを受け取る
//（単一インスタンス化はしない。自動更新の自己再起動は新版を起動してから旧版が終わるため、
// 単一インスタンスの仕組みと競合して更新後にアプリが起動しなくなる）。
//
// 受け取ったファイルは拡張子で区分し、開けない種類は受け渡し系のエラー様式
//（原因＋次に取る行動）で知らせる。アプリは終了しない。

import (
	"os"
	"path/filepath"
	"strings"
	"sync"

	wailsruntime "github.com/wailsapp/wails/v2/pkg/runtime"

	"github.com/howashoji/ReqWeave/app/internal/applog"
	"github.com/howashoji/ReqWeave/app/internal/exchange"
	"github.com/howashoji/ReqWeave/app/internal/projectstore"
)

// EventOpenFile は受け渡しファイルを受け取ったことを画面へ伝えるイベント名。
const EventOpenFile = "openfile:event"

// 受け取ったファイルの区分（受け渡しファイルの 2 拡張子・プロジェクト・それ以外）。
const (
	OpenFileRespond     = "respond"     // .rwvq = 回答モードで開く
	OpenFileImport      = "import"      // .rwva = 担当者側の取込導線へ
	OpenFileProject     = "project"     // .reqweave = そのプロジェクトを開く
	OpenFileUnsupported = "unsupported" // それ以外 = 開けない
)

// unsupportedOpenFileMessage は開けない種類を渡されたときの利用者向け文言。
// 生の拡張子・パスを出さず、原因と次に取る行動だけを示す（受け渡し系のエラー様式）。
const unsupportedOpenFileMessage = "このファイルは ReqWeave で開ける種類ではありません。質問票ファイル（.rwvq）または返送ファイル（.rwva）を開いてください。"

// notAProjectOpenFileMessage は、名前は合っているが中身がプロジェクトでないときの文言。
// 「開ける種類ではない」と言うと、名前が合っているだけに利用者が納得できない。
const notAProjectOpenFileMessage = "このプロジェクトを開けません。中身が ReqWeave のプロジェクトデータではありません。プロジェクト一覧から開いてください。"

// OpenFileEvent は受け取ったファイルの区分と、画面が次に開く対象。
type OpenFileEvent struct {
	// Kind は respond / import / project / unsupported。空文字は「受け取っていない」。
	Kind string `json:"kind"`
	// FilePath は受け取ったファイル（respond / import のときのみ）。
	FilePath string `json:"filePath,omitempty"`
	// FileName は画面表示用のファイル名（パス全体を出さない）。
	FileName string `json:"fileName,omitempty"`
	// Message は開けない場合の利用者向け文言（unsupported のときのみ）。
	Message string `json:"message,omitempty"`
}

// openFileState は受け取ったファイルを 1 件保持する。
//
// 起動直後は画面がまだ無く（Wails の ctx も未設定）イベントを送れないため、
// ここへ保留して StartupMode / PendingOpenFile が拾う。
type openFileState struct {
	mu      sync.Mutex
	pending *OpenFileEvent
}

// classifyOpenFile は拡張子から区分を決める（大文字小文字を区別しない）。
func classifyOpenFile(path string) OpenFileEvent {
	name := filepath.Base(path)
	switch strings.ToLower(filepath.Ext(path)) {
	case exchange.ExtIssue:
		return OpenFileEvent{Kind: OpenFileRespond, FilePath: path, FileName: name}
	case exchange.ExtReturn:
		return OpenFileEvent{Kind: OpenFileImport, FilePath: path, FileName: name}
	case projectstore.ProjectFolderExt:
		// プロジェクトは拡張子付きの**ディレクトリ**。macOS ではパッケージの
		// ダブルクリックでここへ届く。中身がプロジェクトでなければ受け付けない
		// （拡張子だけを見て開こうとすると、開いた先で分かりにくく失敗する）。
		if projectstore.IsProjectFolder(path) {
			return OpenFileEvent{Kind: OpenFileProject, FilePath: path, FileName: name}
		}
		return OpenFileEvent{Kind: OpenFileUnsupported, FileName: name, Message: notAProjectOpenFileMessage}
	}
	return OpenFileEvent{Kind: OpenFileUnsupported, FileName: name, Message: unsupportedOpenFileMessage}
}

// fileArg は引数列から最初のファイル引数を返す（実行ファイル自身とフラグを除く）。
func fileArg(args []string) (string, bool) {
	for i, arg := range args {
		if i == 0 || arg == "" || strings.HasPrefix(arg, "-") {
			continue
		}
		return arg, true
	}
	return "", false
}

// HandleOpenFile は OS からファイルを受け取る唯一の入口（macOS の open-file イベント）。
//
// 画面が既にあればイベントで知らせ、無ければ保留する。どちらの経路でも同じ区分になる。
func (a *API) HandleOpenFile(path string) {
	ev := classifyOpenFile(path)
	// 受け取った事実を動作ログへ残す。**パス・ファイル名は残さない**（種別のみ）。
	// これが無いと「受け取ったが画面へ届かなかった」のか「そもそも受け取っていない」のかを
	// 切り分けられない。
	if a.log != nil {
		a.log.Info(applog.EventOpenFileReceived, "OS からファイルを受け取りました",
			applog.F("kind", ev.Kind))
	}
	a.openFile.mu.Lock()
	a.openFile.pending = &ev
	a.openFile.mu.Unlock()

	if a.ctx == nil {
		// 起動直後（画面が出る前）。StartupMode / PendingOpenFile が保留分を拾う。
		return
	}
	wailsruntime.EventsEmit(a.ctx, EventOpenFile, ev)
	// 既に起動しているウィンドウを前面へ出す（新しい窓を開かない = 二重起動しない）。
	wailsruntime.WindowShow(a.ctx)
	wailsruntime.WindowUnminimise(a.ctx)
}

// PendingOpenFile は起動時に受け取ったファイルを返す（画面の初回描画で拾う）。
// 受け取っていない場合は Kind が空の値を返す。
//
// Windows の関連付け起動と、バイナリへのドラッグ・コマンドラインからの起動は
// 起動引数で届くため、保留が無ければ引数からも判定する。
func (a *API) PendingOpenFile() OpenFileEvent {
	if ev := a.peekOpenFile(); ev != nil {
		return *ev
	}
	if path, ok := fileArg(os.Args); ok {
		return classifyOpenFile(path)
	}
	return OpenFileEvent{}
}

// peekOpenFile は open-file イベントで受け取った保留分を消費せずに返す。
func (a *API) peekOpenFile() *OpenFileEvent {
	a.openFile.mu.Lock()
	defer a.openFile.mu.Unlock()
	if a.openFile.pending == nil {
		return nil
	}
	ev := *a.openFile.pending
	return &ev
}
