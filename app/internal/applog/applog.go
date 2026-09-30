// Package applog は動作ログ。
//
// エラー・警告・主要動作イベントを、アプリ設定領域配下の `logs/` へ JSON Lines で追記する。
// 1 ファイル 10MB を超えたら世代を送り、保持は 5 ファイルまで（超過分は自動削除）。
//
// # 端末外へ出さない
//
// 本パッケージは書き出し以外の経路を持たない（送信・アップロードの関数を持たない。通信先を限り、
// ログを外部へ送る機能を設けない方針）。障害調査は利用者がファイルを手動で提供する運用。
//
// # 記録しない情報
//
// シークレットキー本体・キーへの参照名・同期先の認証情報・OS ユーザー名/ホスト名等の端末固有情報・
// 発話本文は記録しない。これは呼び出し側が渡さないこと（第一防衛）で担保し、
// 取りこぼしを本パッケージの出力層で masking.Mask に通して伏せる（第二防衛）。
// マスキングは出力層の 1 か所（write）で、JSON へ整形する前のメッセージ・付随項目の全体へ掛けるため、
// どの経路（メッセージ・付随項目・パニックのスタック）から入っても必ず通る。
package applog

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime/debug"
	"strings"
	"sync"
	"time"

	"github.com/howashoji/ReqWeave/app/internal/masking"
	"github.com/howashoji/ReqWeave/app/internal/projectstore"
)

const (
	// MaxFileBytes は 1 ファイルの上限（10MB）。
	MaxFileBytes int64 = 10 << 20
	// MaxFiles は保持するファイル数（現行 1 + 世代 4）。
	MaxFiles = 5

	// baseName は現行ファイルの名前。世代は app.1.log 〜 app.<MaxFiles-1>.log。
	baseName = "app.log"

	dirMode  = 0o700
	fileMode = 0o600
)

// アプリの生存期間を表すイベント名。**この 2 つの対で「前回が正常に終了したか」を判定する**
// （EventAppStart に対応する EventAppStop が無く、かつその**プロセスが既に居ない**なら異常終了
// = PreviousRunIncomplete）。
// Go の fatal error（concurrent map writes・stack overflow・cgo 側のシグナル等）は recover できず、
// 終了時のフックも走らないため、クラッシュの検知はこの欠落によるほかない（実測）。
//
// **対応づけはプロセス識別子（pid）で行う**。本システムは単一インスタンス化をしない
// （2 つ目の起動を止めない方針）ため、
//   - 多重起動（2 つ目のウィンドウ。Windows では受け渡しファイルの関連付けから日常的に起きる）
//   - 自動更新の自己再起動（**新版を起動してから旧版が終了する**）
//
// の双方で「最後の記録が app.start」という状態が正常に発生する。行の並びだけで判定すると
// **設計上あり得る動作を毎回異常終了と報告する**（2026-09-04 に利用者の実機で発生）。
const (
	// EventAppStart は起動の記録。
	EventAppStart = "app.start"
	// EventAppStop は正常終了の記録。
	EventAppStop = "app.stop"
	// EventPreviousRunIncomplete は「前回が正常に終了していない」ことの記録。
	EventPreviousRunIncomplete = "app.previous_run_incomplete"
	// EventAppPanic はパニックの記録。
	EventAppPanic = "app.panic"
	// EventOpenFileReceived は OS からファイルを受け取った記録。
	//
	// **パス・ファイル名は記録しない**（OS ユーザー名や案件名が混じるため）。種別だけを残す。
	// これが無いと「受け取ったが画面へ届かなかった」のか「そもそも受け取っていない」のかを
	// 切り分けられない（2026-09-08 に実機で切り分けに詰まった）。
	EventOpenFileReceived = "openfile.received"

	// FieldPID は生存期間のイベントへ自動で添えるプロセス識別子の項目名。
	// **呼び出し側は指定しない**（write が必ず付ける = 付け忘れを構造で防ぐ）。
	// pid は端末や利用者を特定しない値であり、「記録しない情報」に当たらない。
	FieldPID = "pid"
)

// Level は記録の水準（エラー・警告・主要動作イベント）。
type Level string

const (
	LevelInfo  Level = "info"
	LevelWarn  Level = "warn"
	LevelError Level = "error"
)

// Field は記録に添える項目。値は JSON へ出せる形へ正規化して保持する。
type Field struct {
	Key   string
	Value any
}

// F は Field を作る。
func F(key string, value any) Field { return Field{Key: key, Value: normalize(value)} }

// Logger は動作ログの書き出し口。
//
// ゼロ値および nil は「記録しない」ロガーとして安全に使える（動作ログを開けない端末でも
// アプリの機能を止めないため。動作ログはアプリを使うための前提ではない）。
type Logger struct {
	mu   sync.Mutex
	dir  string
	file *os.File
	size int64

	// maxBytes / now はテストが差し替える（既定は MaxFileBytes / time.Now）。
	maxBytes int64
	now      func() time.Time

	// prevIncomplete は「開いた時点で、前回の起動に対応する終了の記録が無かった」ことを表す。
	// 書き出しを始める**前**に判定するため、開いた直後に確定して以後は変わらない。
	prevIncomplete bool

	// pid は生存期間のイベントへ添える自プロセスの識別子（テストで差し替える）。
	pid int
}

// record は 1 行の構造（JSON Lines）。
type record struct {
	At      string         `json:"at"`
	Level   string         `json:"level"`
	Event   string         `json:"event"`
	Message string         `json:"message,omitempty"`
	Fields  map[string]any `json:"fields,omitempty"`
}

// New はアプリ設定領域配下の `logs/` へ書くロガーを開く。
//
// 動作ログの保存先を知るのは本パッケージだけ（他の層は LogsDir を参照しない = depcheck
// 規則 logs-writer）。
func New(paths projectstore.AppPaths) (*Logger, error) {
	if paths.Base == "" {
		return nil, fmt.Errorf("アプリ設定の保存先が定まっていないため動作ログを開けません")
	}
	return open(paths.LogsDir())
}

// open は指定ディレクトリへロガーを開く。
func open(dir string) (*Logger, error) {
	if err := os.MkdirAll(dir, dirMode); err != nil {
		return nil, fmt.Errorf("動作ログの保存先を作成できません: %w", err)
	}
	// 追記を始める**前**に、前回が正常終了しているかを見る（自分の app.start を数えないため）。
	prev := scanPreviousRun(filepath.Join(dir, baseName), aliveCheck)
	f, err := os.OpenFile(filepath.Join(dir, baseName), os.O_CREATE|os.O_WRONLY|os.O_APPEND, fileMode)
	if err != nil {
		return nil, fmt.Errorf("動作ログを開けません: %w", err)
	}
	info, err := f.Stat()
	if err != nil {
		f.Close()
		return nil, fmt.Errorf("動作ログの状態を取得できません: %w", err)
	}
	return &Logger{
		dir:            dir,
		file:           f,
		size:           info.Size(),
		maxBytes:       MaxFileBytes,
		now:            time.Now,
		prevIncomplete: prev,
		pid:            os.Getpid(),
	}, nil
}

// PreviousRunIncomplete は「前回の起動が正常に終了していない」かを返す。
//
// 判定は**開いた時点の記録**による（自分が書いた app.start は含まれない）。
// 現行ファイルに生存期間の記録が 1 件も無い場合（初回起動・世代送り直後）は false を返す
// ＝「分からない」を「異常終了した」と言わない。
// 同じ理由で、**まだ動いているプロセスの app.start** と、**pid を持たない古い版の記録**も
// 異常終了と見なさない。
func (l *Logger) PreviousRunIncomplete() bool {
	if l == nil {
		return false
	}
	return l.prevIncomplete
}

// scanPreviousRun は「終了の記録が無く、かつそのプロセスが既に居ない起動」が
// 現行ファイルにあるかを返す。
//
// 行の並びではなく **pid の対応**で判定する。単一インスタンス化をしないため、
// 多重起動と自動更新の自己再起動では「最後の記録が app.start」が正常に起きるからである。
//
// alive は pid のプロセスがまだ動いているかを返す関数（テストで差し替える）。
// ファイルが無い・読めない・生存期間の記録が無い場合は false（分からないときは異常終了と言わない）。
func scanPreviousRun(path string, alive func(int) bool) bool {
	f, err := os.Open(path)
	if err != nil {
		return false
	}
	defer f.Close()

	sc := bufio.NewScanner(f)
	// パニックのスタックを含む行は既定の上限（64KB）を超えうる。
	sc.Buffer(make([]byte, 0, 64<<10), 1<<20)

	// 終了の記録が来ていない起動の pid。出現順を保つ必要は無いため集合で持つ。
	unfinished := map[int]bool{}
	for sc.Scan() {
		line := sc.Text()
		// まず安価な部分一致で生存期間の行だけに絞る（1 行が壊れていても走査を続けるため、
		// 全行の JSON 解析はしない）。
		var start bool
		switch {
		case strings.Contains(line, `"event":"`+EventAppStart+`"`):
			start = true
		case strings.Contains(line, `"event":"`+EventAppStop+`"`):
			start = false
		default:
			continue
		}
		pid, ok := pidOf(line)
		if !ok {
			// pid を持たない記録（本修正より前の版が書いた行・壊れた行）は
			// 対応づけられないため数えない。**古い記録で誤検知しない**ことを優先する。
			continue
		}
		if start {
			unfinished[pid] = true
		} else {
			delete(unfinished, pid)
		}
	}

	for pid := range unfinished {
		// **まだ動いているプロセスは異常終了ではない**（多重起動・自動更新の旧版）。
		if !alive(pid) {
			return true
		}
	}
	return false
}

// aliveCheck は生存判定の入口（テストで差し替える。既定は OS ごとの processAlive）。
var aliveCheck = processAlive

// pidOf は 1 行の JSON から fields.pid を取り出す。
// 解析できない・項目が無い場合は ok=false（推測しない）。
func pidOf(line string) (int, bool) {
	var rec struct {
		Fields struct {
			PID *float64 `json:"pid"`
		} `json:"fields"`
	}
	if err := json.Unmarshal([]byte(line), &rec); err != nil {
		return 0, false
	}
	if rec.Fields.PID == nil {
		return 0, false
	}
	return int(*rec.Fields.PID), true
}

// Dir は書き出し先のディレクトリを返す（テスト・診断用）。
func (l *Logger) Dir() string {
	if l == nil {
		return ""
	}
	return l.dir
}

// Info は主要動作イベントを記録する。
func (l *Logger) Info(event, message string, fields ...Field) {
	l.write(LevelInfo, event, message, fields)
}

// Warn は警告を記録する。
func (l *Logger) Warn(event, message string, fields ...Field) {
	l.write(LevelWarn, event, message, fields)
}

// Error はエラーを記録する。
func (l *Logger) Error(event, message string, fields ...Field) {
	l.write(LevelError, event, message, fields)
}

// RecoverPanic は defer で使い、パニックを記録してから再送出する（クラッシュ時の
// パニックの記録も同じマスキングを通す）。
//
//	defer logger.RecoverPanic("app.panic")
//
// 記録後に再送出するため、パニックそのものを握り潰さない（異常終了は次回起動時の
// 復元が受ける）。
func (l *Logger) RecoverPanic(event string) {
	r := recover()
	if r == nil {
		return
	}
	l.recordPanic(event, r)
	panic(r)
}

// RecordPanic は既に recover 済みのパニックを記録する（再送出しない）。
//
// パニックを握り潰して別の形（利用者向けのエラー）へ倒す場所で使う。倒し先の文言へは
// パニックの内容を載せず、**内容とスタックは動作ログにだけ残す**（画面・送信記録・受け渡しデータへ
// 出さない。動作ログは出力層でマスキングを通る）。
func (l *Logger) RecordPanic(event string, r any) {
	if r == nil {
		return
	}
	l.recordPanic(event, r)
}

// recordPanic はパニックの内容とスタックを記録する（再送出はしない）。
// スタックは deferred 関数の中で採るため、巻き戻し中のパニック発生元の呼び出し列を含む。
func (l *Logger) recordPanic(event string, r any) {
	l.Error(event, fmt.Sprint(r), F("stack", string(debug.Stack())))
}

// Close は書き出し口を閉じる。
func (l *Logger) Close() error {
	if l == nil {
		return nil
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.file == nil {
		return nil
	}
	err := l.file.Close()
	l.file = nil
	return err
}

// write は 1 行を組み立てて追記する（出力層。ここが唯一のマスキング通過点）。
func (l *Logger) write(level Level, event, message string, fields []Field) {
	if l == nil {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.file == nil {
		return
	}
	// **すべての記録がここでマスキングを通る**。
	// 掛ける対象は JSON へ整形する**前**の文字列。整形後の行へ掛けると、値の末尾に続く
	// JSON の区切り（`"}}`）まで置換に飲まれて行が壊れる。
	rec := record{
		At:      l.now().UTC().Format(time.RFC3339),
		Level:   string(level),
		Event:   event,
		Message: masking.Mask(message),
	}
	if len(fields) > 0 {
		rec.Fields = make(map[string]any, len(fields))
		for _, f := range fields {
			if f.Key == "" {
				continue
			}
			rec.Fields[masking.Mask(f.Key)] = maskValue(normalize(f.Value))
		}
	}
	// 生存期間のイベントには**必ず** pid を付ける（呼び出し側の付け忘れを構造で防ぐ）。
	// 付いていないと scanPreviousRun が起動と終了を対応づけられない。
	if event == EventAppStart || event == EventAppStop {
		if rec.Fields == nil {
			rec.Fields = make(map[string]any, 1)
		}
		rec.Fields[FieldPID] = l.pid
	}
	encoded, err := json.Marshal(rec)
	if err != nil {
		// normalize 済みのため通常は起こらない。落とさずに最小限の行を残す。
		encoded = []byte(fmt.Sprintf(`{"at":%q,"level":%q,"event":%q,"message":"記録の整形に失敗しました"}`,
			rec.At, rec.Level, rec.Event))
	}
	line := string(encoded) + "\n"
	l.rotateIfNeeded(int64(len(line)))
	if l.file == nil {
		return
	}
	n, err := l.file.WriteString(line)
	l.size += int64(n)
	if err != nil {
		// 書けないこと自体は記録できない（記録先が同じであるため）。機能は止めない。
		return
	}
}

// rotateIfNeeded は追記後に上限を超えるなら世代を送る。
func (l *Logger) rotateIfNeeded(next int64) {
	if l.size == 0 || l.size+next <= l.maxBytes {
		return
	}
	if l.file != nil {
		l.file.Close()
		l.file = nil
	}
	// 超過分の削除（保持数を減らしたときの取り残しも掃除する）。
	for i := MaxFiles - 1; ; i++ {
		p := l.generation(i)
		if _, err := os.Stat(p); err != nil {
			break
		}
		os.Remove(p)
	}
	// app.<n-1>.log → app.<n>.log（新しい順に押し出す）。
	for i := MaxFiles - 2; i >= 1; i-- {
		os.Rename(l.generation(i), l.generation(i+1))
	}
	os.Rename(filepath.Join(l.dir, baseName), l.generation(1))

	f, err := os.OpenFile(filepath.Join(l.dir, baseName), os.O_CREATE|os.O_WRONLY|os.O_APPEND, fileMode)
	if err != nil {
		return
	}
	l.file = f
	l.size = 0
}

// generation は世代ファイルのパス（i >= 1）。
func (l *Logger) generation(i int) string {
	return filepath.Join(l.dir, fmt.Sprintf("app.%d.log", i))
}

// maskValue は文字列の値をマスキングする（数値・真偽値は対象外＝秘密情報の器にならない）。
func maskValue(v any) any {
	if s, ok := v.(string); ok {
		return masking.Mask(s)
	}
	return v
}

// normalize は JSON へ確実に出せる形へ落とす（marshal 失敗で記録を失わないため）。
func normalize(v any) any {
	switch t := v.(type) {
	case nil:
		return nil
	case string, bool,
		int, int8, int16, int32, int64,
		uint, uint8, uint16, uint32, uint64,
		float32, float64:
		return t
	case error:
		return t.Error()
	case fmt.Stringer: // time.Duration・独自の列挙型などはここで文字列になる
		return t.String()
	default:
		return fmt.Sprint(t)
	}
}
