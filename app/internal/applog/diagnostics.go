package applog

// 診断情報の書き出し（利用者が問い合わせのときに提供するファイル）。
//
// 動作ログのファイルを開いてよいのは本パッケージだけ（depcheck 規則
// logs-writer）なので、書き出しも本パッケージが担う。
//
// **動作ログはそのまま束ねる**。秘密情報のマスキングと端末固有情報（ホームディレクトリの
// 接頭辞）の除去は記録の時点で済んでいるため、ここで別の加工を掛けない
// （同じ規則を 2 か所で保守しない）。

import (
	"archive/zip"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// previewMaxBytes は書き出し前に画面へ出す本文の上限。
//
// 書き出す前に中身を全文見せるのが原則だが、動作ログは 1 ファイル 10MB×5 世代まで育ちうる。
// 上限を超える場合は**新しい側**を残して切り、切ったことを Truncated で伝える
// （直近の異常が調査対象であるため。書き出す書庫には全量が入る）。
const previewMaxBytes = 2 << 20

// environmentName は書庫に入れる実行環境の説明ファイルの名前。
const environmentName = "environment.txt"

// truncationNotice は本文を切ったときに先頭へ置く印。
const truncationNotice = "（表示は上限を超えたため古い部分を省略しています。書き出すファイルには全量が入ります）\n"

// DiagnosticEnvironment は診断情報に添える実行環境。
//
// 端末を特定できる値を入れてはならない（ホスト名・OS ユーザー名等）。
type DiagnosticEnvironment struct {
	AppName    string
	AppVersion string
	OS         string
	OSVersion  string
	Arch       string
}

// Text は書庫へ入れる形（人が読める 1 ファイル）に整える。
func (e DiagnosticEnvironment) Text() string {
	var b strings.Builder
	fmt.Fprintf(&b, "アプリ: %s %s\n", e.AppName, e.AppVersion)
	if e.OSVersion == "" {
		fmt.Fprintf(&b, "OS: %s（版は取得できませんでした）\n", e.OS)
	} else {
		fmt.Fprintf(&b, "OS: %s %s\n", e.OS, e.OSVersion)
	}
	fmt.Fprintf(&b, "アーキテクチャ: %s\n", e.Arch)
	return b.String()
}

// DiagnosticItem は書庫に入る 1 件（画面での確認用）。
type DiagnosticItem struct {
	Name  string `json:"name"`
	Bytes int64  `json:"bytes"`
}

// Diagnostic は書き出す内容（書き出す前に利用者が中身を確かめるために用いる）。
type Diagnostic struct {
	// Environment は実行環境の説明（書庫にも同じ内容が入る）。
	Environment string `json:"environment"`
	// Items は書庫に入るファイルの一覧。
	Items []DiagnosticItem `json:"items"`
	// LogText は動作ログの本文（古い世代から順に連結。上限で切ることがある）。
	LogText string `json:"logText"`
	// Truncated は LogText を切ったかどうか。
	Truncated bool `json:"truncated"`
	// TotalBytes は動作ログ全体の大きさ。
	TotalBytes int64 `json:"totalBytes"`
}

// Diagnostics は書き出す内容を組み立てて返す（書き出しはしない）。
//
// nil ロガー（動作ログを開けない端末）では、動作ログのない診断情報を返す
// （実行環境だけでも渡せるほうが調査の役に立つため）。
func (l *Logger) Diagnostics(env DiagnosticEnvironment) (Diagnostic, error) {
	d := Diagnostic{Environment: env.Text()}
	d.Items = append(d.Items, DiagnosticItem{Name: environmentName, Bytes: int64(len(d.Environment))})
	if l == nil {
		return d, nil
	}

	l.mu.Lock()
	defer l.mu.Unlock()

	var body strings.Builder
	for _, path := range l.logFilesLocked() {
		info, err := os.Stat(path)
		if err != nil {
			continue
		}
		d.Items = append(d.Items, DiagnosticItem{Name: filepath.Base(path), Bytes: info.Size()})
		d.TotalBytes += info.Size()
		data, err := os.ReadFile(path)
		if err != nil {
			return Diagnostic{}, fmt.Errorf("動作ログを読み取れません: %w", err)
		}
		body.Write(data)
	}
	text := body.String()
	if len(text) > previewMaxBytes {
		text = truncationNotice + text[len(text)-previewMaxBytes:]
		d.Truncated = true
	}
	d.LogText = text
	return d, nil
}

// WriteDiagnostics は診断情報を単一の書庫として path へ書き出す。
//
// 外部へは送らない（本パッケージは送信の経路を持たない）。
func (l *Logger) WriteDiagnostics(path string, env DiagnosticEnvironment) error {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, fileMode)
	if err != nil {
		return fmt.Errorf("診断情報の保存先を作成できません: %w", err)
	}
	defer f.Close()

	zw := zip.NewWriter(f)
	now := time.Now()
	if err := writeEntry(zw, environmentName, []byte(env.Text()), now); err != nil {
		return err
	}
	if l != nil {
		l.mu.Lock()
		paths := l.logFilesLocked()
		l.mu.Unlock()
		for _, p := range paths {
			data, err := os.ReadFile(p)
			if err != nil {
				continue // 世代送りと重なって消えた場合は飛ばす（書き出し自体は止めない）
			}
			if err := writeEntry(zw, filepath.Base(p), data, now); err != nil {
				return err
			}
		}
	}
	if err := zw.Close(); err != nil {
		return fmt.Errorf("診断情報を書き出せません: %w", err)
	}
	return nil
}

// writeEntry は書庫へ 1 ファイル入れる。
func writeEntry(zw *zip.Writer, name string, data []byte, at time.Time) error {
	h := &zip.FileHeader{Name: name, Method: zip.Deflate, Modified: at}
	w, err := zw.CreateHeader(h)
	if err != nil {
		return fmt.Errorf("診断情報を書き出せません: %w", err)
	}
	if _, err := w.Write(data); err != nil {
		return fmt.Errorf("診断情報を書き出せません: %w", err)
	}
	return nil
}

// logFilesLocked は存在する動作ログのファイルを**古い順**に返す（呼び出し側が mu を保持する）。
func (l *Logger) logFilesLocked() []string {
	var gens []int
	for i := 1; i < MaxFiles; i++ {
		if _, err := os.Stat(l.generation(i)); err == nil {
			gens = append(gens, i)
		}
	}
	// 世代番号が大きいほど古い。
	sort.Sort(sort.Reverse(sort.IntSlice(gens)))
	paths := make([]string, 0, len(gens)+1)
	for _, i := range gens {
		paths = append(paths, l.generation(i))
	}
	current := filepath.Join(l.dir, baseName)
	if _, err := os.Stat(current); err == nil {
		paths = append(paths, current)
	}
	return paths
}
