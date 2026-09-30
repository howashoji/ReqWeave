package binding

// 本ファイルはディレクトリ指定の一括取り込み。
//
// **2 段構成**にする。ScanImportDirectory が対象の一覧と件数を返し、利用者の確認操作を経て
// ImportDirectory が実行する（黙って大量に取り込まない）。
//
//   - サブディレクトリを再帰的にたどる
//   - 1 回の件数上限は設けない（件数は確認操作で利用者が判断する）
//   - 種別は 1 回につき 1 つを一括適用する（混在は利用者が分けて取り込む）
//   - 隠しファイルと `.git` 等の管理用ディレクトリは除外する
//   - 対象外の形式・読めなかったものは件数と理由を示し、取り込めたものだけ登録する
//   - **AI 分析は行わない**。原本の登録までで終える（分析は資料ごとに同意を経る）
//   - 取り込むとプロジェクトの総量の上限を超える場合は実行しない
//
// 保持する元ファイル名は**ベース名だけ**であり、フォルダからの相対パスは確認画面の表示にのみ使う
// （端末固有のパスをプロジェクトデータへ残さない。validateSourceName を参照）。

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	wailsruntime "github.com/wailsapp/wails/v2/pkg/runtime"

	"github.com/howashoji/ReqWeave/app/internal/importer"
	"github.com/howashoji/ReqWeave/app/internal/projectstore"
)

// importScanListLimit は確認画面へ返す一覧の最大件数。
//
// 件数自体は全件を数えるが、数千件の明細をそのまま画面へ渡すと描画が実用的でなくなる。
// 一覧は先頭のこの件数までとし、残りは件数で示す。
const importScanListLimit = 200

// ImportScanEntry は一括取り込みの対象 1 件（確認画面の表示用）。
type ImportScanEntry struct {
	// Name はフォルダからの相対パス（表示用。プロジェクトデータへは保存しない）。
	Name string `json:"name"`
	// Size は原本のバイト数。
	Size int64 `json:"size"`
}

// ImportSkip は取り込まなかった 1 件と、その理由（利用者向けの 1 文）。
type ImportSkip struct {
	Name   string `json:"name"`
	Reason string `json:"reason"`
}

// ImportScanView はフォルダを調べた結果（確認操作の材料）。
type ImportScanView struct {
	// Dir は選んだフォルダ（画面には末尾の名前だけを出せばよい）。
	Dir string `json:"dir"`
	// Count は取り込み対象の総数、TotalBytes はその合計バイト数。
	Count      int   `json:"count"`
	TotalBytes int64 `json:"totalBytes"`
	// TotalSizeLabel は合計の大きさの表示用文字列（画面で数値を組み立て直さない）。
	TotalSizeLabel string `json:"totalSizeLabel"`
	// Files は対象の一覧（先頭 importScanListLimit 件まで）。
	Files []ImportScanEntry `json:"files"`
	// Skipped は対象外の一覧（先頭 importScanListLimit 件まで）、SkippedCount はその総数。
	Skipped      []ImportSkip `json:"skipped"`
	SkippedCount int          `json:"skippedCount"`
	// Blocked は実行できない理由（空なら実行できる）。総量の上限を超える場合に入る。
	Blocked string `json:"blocked,omitempty"`
	// Notice は実行前に伝える注意（上限に近いなど）。無ければ空。
	Notice string `json:"notice,omitempty"`
}

// ImportBatchView は一括取り込みの結果（1 件の失敗で全体を止めない）。
type ImportBatchView struct {
	// Imported は取り込めたもの、ImportedCount はその件数。
	Imported      []ImportView `json:"imported"`
	ImportedCount int          `json:"importedCount"`
	// Failed は取り込めなかったものと理由、FailedCount はその件数。
	Failed      []ImportSkip `json:"failed"`
	FailedCount int          `json:"failedCount"`
	// SkippedCount は対象外として最初から除いた件数。
	SkippedCount int `json:"skippedCount"`
	// Notice は結果の要約（画面はこの 1 文を出す）。
	Notice string `json:"notice"`
}

// ChooseImportDirectory は一括取り込みするフォルダを選ぶ（OS の選択ダイアログ）。
func (a *API) ChooseImportDirectory() (string, error) {
	if a.ctx == nil {
		return "", fmt.Errorf("フォルダの選択を開けません。アプリを再起動してください。")
	}
	return wailsruntime.OpenDirectoryDialog(a.ctx, wailsruntime.OpenDialogOptions{
		Title: "取り込むフォルダを選ぶ",
	})
}

// ScanImportDirectory はフォルダを再帰的に調べ、取り込む対象と除外したものを返す。
//
// **ここでは何も取り込まない**。件数と一覧を提示して確認操作を促すための読み取りだけを行う。
func (a *API) ScanImportDirectory(dir string) (ImportScanView, error) {
	s, err := a.current()
	if err != nil {
		return ImportScanView{}, err
	}
	if err := a.requireRole(s.store, projectstore.RoleEditor, "資料の取り込み"); err != nil {
		return ImportScanView{}, err
	}
	files, skipped, err := scanImportDirectory(dir)
	if err != nil {
		return ImportScanView{}, err
	}

	view := ImportScanView{
		Dir: dir, Count: len(files), SkippedCount: len(skipped),
		Files: make([]ImportScanEntry, 0, min(len(files), importScanListLimit)),
	}
	for _, f := range files {
		view.TotalBytes += f.size
		if len(view.Files) < importScanListLimit {
			view.Files = append(view.Files, ImportScanEntry{Name: f.rel, Size: f.size})
		}
	}
	view.TotalSizeLabel = scaleAmount(projectstore.ScaleTotalBytes, view.TotalBytes)
	if len(skipped) > importScanListLimit {
		view.Skipped = skipped[:importScanListLimit]
	} else {
		view.Skipped = skipped
	}

	if len(files) == 0 {
		view.Blocked = "このフォルダに取り込める資料がありません。txt / md / docx / xlsx / pptx / pdf のいずれかを含むフォルダを選んでください。"
		return view, nil
	}
	blocked, notice, err := a.scaleCheckForBatch(s, view.TotalBytes)
	if err != nil {
		return ImportScanView{}, err
	}
	view.Blocked, view.Notice = blocked, notice
	return view, nil
}

// ImportDirectory はフォルダ内の対象を一括で取り込む（種別は 1 つを一括適用する）。
//
// 直前の ScanImportDirectory の結果を受け取らず、**実行時に調べ直す**
// （確認から実行までの間にフォルダが変わっていても、実際に取り込むものと結果が一致する）。
func (a *API) ImportDirectory(dir, kind string) (ImportBatchView, error) {
	s, err := a.current()
	if err != nil {
		return ImportBatchView{}, err
	}
	if err := a.requireRole(s.store, projectstore.RoleEditor, "資料の取り込み"); err != nil {
		return ImportBatchView{}, err
	}
	files, skipped, err := scanImportDirectory(dir)
	if err != nil {
		return ImportBatchView{}, err
	}
	if len(files) == 0 {
		return ImportBatchView{}, fmt.Errorf(
			"このフォルダに取り込める資料がありません。txt / md / docx / xlsx / pptx / pdf のいずれかを含むフォルダを選んでください。")
	}
	var total int64
	for _, f := range files {
		total += f.size
	}
	blocked, _, err := a.scaleCheckForBatch(s, total)
	if err != nil {
		return ImportBatchView{}, err
	}
	if blocked != "" {
		return ImportBatchView{}, fmt.Errorf("%s", blocked)
	}

	out := ImportBatchView{SkippedCount: len(skipped)}
	for _, f := range files {
		format, ferr := importFormatOf(f.abs)
		if ferr != nil {
			// 走査で形式を確かめた後なので通常は起きない（拡張子が変わった場合のみ）。
			out.Failed = append(out.Failed, ImportSkip{Name: f.rel, Reason: ferr.Error()})
			continue
		}
		content, rerr := os.ReadFile(f.abs)
		if rerr != nil {
			out.Failed = append(out.Failed, ImportSkip{Name: f.rel,
				Reason: "ファイルを読み込めませんでした。ファイルの場所と権限を確認してください。"})
			continue
		}
		added, ierr := a.importInput(s, importer.Input{
			Kind: importer.Kind(kind), SourceName: filepath.Base(f.abs),
			SourceFormat: format, Content: content,
		})
		if ierr != nil {
			out.Failed = append(out.Failed, ImportSkip{Name: f.rel, Reason: ierr.Error()})
			continue
		}
		out.Imported = append(out.Imported, added)
	}
	out.ImportedCount, out.FailedCount = len(out.Imported), len(out.Failed)
	out.Notice = importBatchNotice(out)
	return out, nil
}

// importBatchNotice は結果の 1 文を組み立てる（件数と次の行動を含める）。
func importBatchNotice(v ImportBatchView) string {
	var b strings.Builder
	fmt.Fprintf(&b, "%d 件を取り込みました。", v.ImportedCount)
	if v.SkippedCount > 0 {
		fmt.Fprintf(&b, "対象外の形式など %d 件は取り込みませんでした。", v.SkippedCount)
	}
	if v.FailedCount > 0 {
		fmt.Fprintf(&b, "%d 件は取り込めませんでした（理由は一覧を確認してください）。", v.FailedCount)
	}
	// 一括では AI へ送らない（分析は資料ごとに送信内容を確認してから行う）。次の行動を明示する。
	b.WriteString("分析は行っていません。分析する資料を一覧から選び、送信内容を確認してから実行してください。")
	return b.String()
}

// scaleCheckForBatch は取り込み後の総量がプロジェクトの規模の上限を超えるかを見る。
//
// 個別の取り込みは上限を超えても止めない（scale_guard.go の方針）。**一括取り込みだけは
// 事前に増加量が分かる**ため、超える場合は実行しない。
func (a *API) scaleCheckForBatch(s *dialogueSession, addBytes int64) (blocked, notice string, err error) {
	usages, err := s.store.ScaleUsageAll(scaleWarnRatio)
	if err != nil {
		return "", "", err
	}
	for _, u := range usages {
		if u.Kind != projectstore.ScaleTotalBytes {
			continue
		}
		after := u.Current + addBytes
		if u.Limit > 0 && after >= u.Limit {
			return fmt.Sprintf(
				"この内容を取り込むと%sが上限の目安（%s）を超えます（取り込み後 %s の見込み）。"+
					"フォルダを分けるか、不要な資料を整理してから実行してください。",
				u.Label, scaleAmount(u.Kind, u.Limit), scaleAmount(u.Kind, after)), "", nil
		}
		if u.Limit > 0 && float64(after)/float64(u.Limit) >= projectstore.ScaleWarnRatioOrDefault(scaleWarnRatio) {
			return "", fmt.Sprintf(
				"取り込むと%sが上限の目安（%s）に近づきます（取り込み後 %s の見込み）。このまま取り込めます。",
				u.Label, scaleAmount(u.Kind, u.Limit), scaleAmount(u.Kind, after)), nil
		}
	}
	return "", "", nil
}

// scanFile は走査で見つけた取り込み対象 1 件。
type scanFile struct {
	abs  string
	rel  string
	size int64
}

// scanImportDirectory はフォルダを再帰的に走査し、対象と除外を返す。
//
// 隠しファイル・隠しディレクトリ（`.git` を含む）は**理由を出さずに除外**する
// （利用者が置いた資料ではないため）。対象外の形式・読めなかったものは理由つきで返す。
func scanImportDirectory(dir string) (files []scanFile, skipped []ImportSkip, err error) {
	if strings.TrimSpace(dir) == "" {
		return nil, nil, fmt.Errorf("取り込むフォルダを選んでください。")
	}
	info, err := os.Stat(dir)
	if err != nil || !info.IsDir() {
		return nil, nil, fmt.Errorf("フォルダを開けません（%s）。場所と権限を確認してください。", filepath.Base(dir))
	}

	relOf := func(path string) string {
		r, rerr := filepath.Rel(dir, path)
		if rerr != nil {
			return filepath.Base(path)
		}
		return filepath.ToSlash(r)
	}

	walkErr := filepath.WalkDir(dir, func(path string, d fs.DirEntry, werr error) error {
		if werr != nil {
			// 読めない場所があっても走査を続ける（1 件の失敗で全体を止めない）。
			skipped = append(skipped, ImportSkip{Name: relOf(path),
				Reason: "読み取れませんでした。場所と権限を確認してください。"})
			if d != nil && d.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		if path == dir {
			return nil
		}
		if strings.HasPrefix(d.Name(), ".") {
			if d.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		if d.IsDir() {
			return nil
		}
		if !d.Type().IsRegular() {
			// シンボリックリンク等は原本の同一性を保証できないため対象にしない。
			skipped = append(skipped, ImportSkip{Name: relOf(path),
				Reason: "通常のファイルではありません。"})
			return nil
		}
		if _, ferr := importFormatOf(path); ferr != nil {
			skipped = append(skipped, ImportSkip{Name: relOf(path),
				Reason: "対象の形式ではありません（txt / md / docx / xlsx / pptx / pdf）。"})
			return nil
		}
		fi, ierr := d.Info()
		if ierr != nil {
			skipped = append(skipped, ImportSkip{Name: relOf(path),
				Reason: "読み取れませんでした。場所と権限を確認してください。"})
			return nil
		}
		files = append(files, scanFile{abs: path, rel: relOf(path), size: fi.Size()})
		return nil
	})
	if walkErr != nil {
		return nil, nil, fmt.Errorf("フォルダを読み取れません（%s）。場所と権限を確認してください。", filepath.Base(dir))
	}

	// 表示順・取り込み順を決める（同じフォルダなら毎回同じ順になる）。
	sort.Slice(files, func(i, j int) bool { return files[i].rel < files[j].rel })
	sort.Slice(skipped, func(i, j int) bool { return skipped[i].Name < skipped[j].Name })
	return files, skipped, nil
}
