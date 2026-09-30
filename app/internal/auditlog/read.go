package auditlog

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// ReadChanges は期間内の変更履歴を全作業者ファイルからマージして日時順で返す。
// from / to は境界を含む。ゼロ値は無制限。
func ReadChanges(root string, from, to time.Time) ([]ChangeRecord, error) {
	var out []ChangeRecord
	err := readNDJSON(root, DirHistory, from, to, func(line []byte) error {
		var rec ChangeRecord
		if err := json.Unmarshal(line, &rec); err != nil {
			return err
		}
		if inPeriod(rec.At, from, to) {
			out = append(out, rec)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].At.Before(out[j].At) })
	return out, nil
}

// ReadSyncRecords は期間内の同期の記録を全作業者ファイルからマージして日時順で返す。
//
// 閲覧権限でも参照できる（ほかの監査情報の参照と同じ基準。権限判定は呼び出し側）。
func ReadSyncRecords(root string, from, to time.Time) ([]SyncRecord, error) {
	var out []SyncRecord
	err := readNDJSON(root, DirSyncLog, from, to, func(line []byte) error {
		var rec SyncRecord
		if err := json.Unmarshal(line, &rec); err != nil {
			return err
		}
		if inPeriod(rec.At, from, to) {
			out = append(out, rec)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].At.Before(out[j].At) })
	return out, nil
}

// LastIncorporation は最後に成功した取り込みの日時を返す（無ければゼロ値）。
//
// 集計の範囲表示に使う（共同プロジェクトの集計は「最後に取り込んだ時点まで」）。
func LastIncorporation(root string) (time.Time, error) {
	records, err := ReadSyncRecords(root, time.Time{}, time.Time{})
	if err != nil {
		return time.Time{}, err
	}
	var last time.Time
	for _, rec := range records {
		if rec.Op == SyncOpIncorporate && rec.Result == SyncResultOK && rec.At.After(last) {
			last = rec.At
		}
	}
	return last, nil
}

// LastSyncAt は最後に成功した同期（取得・取り込み・反映）の日時を返す（同期の状態表示）。
//
// プロジェクト一覧が作業コピーを開かずに「最後に同期した日時」を出すために使う。
// 記録が無ければゼロ値を返す。
func LastSyncAt(root string) (time.Time, error) {
	records, err := ReadSyncRecords(root, time.Time{}, time.Time{})
	if err != nil {
		return time.Time{}, err
	}
	var last time.Time
	for _, rec := range records {
		if rec.Result == SyncResultOK && rec.At.After(last) {
			last = rec.At
		}
	}
	return last, nil
}

// ReadAISends は期間内の AI 送信記録を全作業者ファイルからマージして日時順で返す。
//
// 1 送信は送信行 + 実績行の 2 行で記録されるため、送信 ID をキーに両者をマージして
// 1 レコードとして返す。実績行の無い送信はトークン実績なし（欠測）のまま返す（推定値で埋めない）。
// 実績行が翌月に追記された場合を取りこぼさないよう、読むファイルは期間の前後 1 か月まで広げる
// （返すレコードの絞り込みは送信行の at で行う）。
func ReadAISends(root string, from, to time.Time) ([]AISendRecord, error) {
	scanFrom, scanTo := from, to
	if !scanFrom.IsZero() {
		scanFrom = scanFrom.AddDate(0, -1, 0)
	}
	if !scanTo.IsZero() {
		scanTo = scanTo.AddDate(0, 1, 0)
	}

	var sends []AISendRecord
	usage := map[string]AISendRecord{}
	err := readNDJSON(root, DirAILog, scanFrom, scanTo, func(line []byte) error {
		var rec AISendRecord
		if err := json.Unmarshal(line, &rec); err != nil {
			return err
		}
		if rec.isUsageLine() {
			usage[rec.ID] = rec
			return nil
		}
		if inPeriod(rec.At, from, to) {
			sends = append(sends, rec)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	for i := range sends {
		u, ok := usage[sends[i].ID]
		if !ok || sends[i].ID == "" {
			continue
		}
		sends[i].TokensIn, sends[i].TokensOut, sends[i].TokensReasoning = u.TokensIn, u.TokensOut, u.TokensReasoning
	}
	sort.SliceStable(sends, func(i, j int) bool { return sends[i].At.Before(sends[j].At) })
	return sends, nil
}

// readNDJSON は対象ディレクトリの月別 × 作業者別ファイルを走査し、1 行ずつ handle へ渡す。
// JSON として解釈できない行（追記途中で壊れた行）は読み飛ばす（行単位で自己完結しているため）。
func readNDJSON(root, dir string, from, to time.Time, handle func(line []byte) error) error {
	full := filepath.Join(root, filepath.FromSlash(dir))
	entries, err := os.ReadDir(full)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("監査データを走査できません（%s）: %w", dir, err)
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), fileExt) {
			continue
		}
		if !monthInPeriod(e.Name(), from, to) {
			continue
		}
		names = append(names, e.Name())
	}
	sort.Strings(names)
	for _, name := range names {
		if err := readLines(filepath.Join(full, name), handle); err != nil {
			return err
		}
	}
	return nil
}

func readLines(path string, handle func(line []byte) error) error {
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("監査データを読み込めません（%s）: %w", filepath.Base(path), err)
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	// 送信本文（prompt）を含む行は長くなるため、既定の 64KB では足りない
	sc.Buffer(make([]byte, 0, 64*1024), 16*1024*1024)
	for sc.Scan() {
		line := sc.Bytes()
		if len(strings.TrimSpace(string(line))) == 0 {
			continue
		}
		// 壊れた行は読み飛ばす（追記途中の切断・部分書き込み）
		if err := handle(line); err != nil {
			continue
		}
	}
	if err := sc.Err(); err != nil {
		return fmt.Errorf("監査データの読み出しに失敗（%s）: %w", filepath.Base(path), err)
	}
	return nil
}

// monthInPeriod は "YYYY-MM.<author>.ndjson" の月が期間に重なるかを返す
// （読むファイルを先に限定する）。
func monthInPeriod(name string, from, to time.Time) bool {
	month, _, ok := strings.Cut(name, ".")
	if !ok {
		return false
	}
	start, err := time.Parse("2006-01", month)
	if err != nil {
		return false
	}
	end := start.AddDate(0, 1, 0).Add(-time.Nanosecond)
	if !from.IsZero() && end.Before(from) {
		return false
	}
	if !to.IsZero() && start.After(to) {
		return false
	}
	return true
}

func inPeriod(at, from, to time.Time) bool {
	if !from.IsZero() && at.Before(from) {
		return false
	}
	if !to.IsZero() && at.After(to) {
		return false
	}
	return true
}

// ParseChangeLines は変更履歴の NDJSON バイト列を解釈する（1 ファイル分）。
//
// 取り込みで作業コピーへ入った変更履歴を組み立てるために使う（取り込みの結果に、誰が何を変えたかを示すため）。
// 解釈できない行は**黙って捨てず**にエラーを返す（壊れた履歴を「変更なし」と誤って提示しない）。
func ParseChangeLines(data []byte) ([]ChangeRecord, error) {
	var out []ChangeRecord
	for i, line := range strings.Split(string(data), "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		var rec ChangeRecord
		if err := json.Unmarshal([]byte(line), &rec); err != nil {
			return nil, fmt.Errorf("変更履歴の %d 行目を解釈できません: %w", i+1, err)
		}
		out = append(out, rec)
	}
	return out, nil
}
