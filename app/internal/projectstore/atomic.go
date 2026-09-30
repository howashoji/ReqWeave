package projectstore

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const (
	// dataFileMode / dataDirMode はプロジェクトデータの権限。
	// 共有フォルダ上で複数メンバーが編集する運用を想定し、
	// 記憶媒体上の保護は共有フォルダの ACL 併用に委ねる（アプリ側の権限管理ではファイルの直接操作を防げない）。
	dataFileMode os.FileMode = 0o644
	dataDirMode  os.FileMode = 0o755
)

// WriteFileAtomic は原子的書き込みを行う。
//
// 同一ディレクトリ内の一時ファイルへ全文を書き、fsync してから対象名へ rename する。
// 途中で失敗しても対象ファイルは旧内容のまま残り、半端な内容が見えることはない。
func WriteFileAtomic(path string, data []byte) error {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, "."+filepath.Base(path)+".tmp")
	if err != nil {
		return fmt.Errorf("一時ファイルを作成できません（%s）: %w", dir, err)
	}
	tmpName := tmp.Name()
	committed := false
	defer func() {
		if !committed {
			_ = tmp.Close()
			_ = os.Remove(tmpName)
		}
	}()

	if _, err := tmp.Write(data); err != nil {
		return fmt.Errorf("一時ファイルへ書き込めません（%s）: %w", tmpName, err)
	}
	if err := tmp.Sync(); err != nil {
		return fmt.Errorf("一時ファイルを同期できません（%s）: %w", tmpName, err)
	}
	if err := tmp.Chmod(dataFileMode); err != nil {
		return fmt.Errorf("一時ファイルの権限を設定できません（%s）: %w", tmpName, err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("一時ファイルを閉じられません（%s）: %w", tmpName, err)
	}
	if err := os.Rename(tmpName, path); err != nil {
		return fmt.Errorf("保存先へ差し替えられません（%s）: %w", path, err)
	}
	committed = true

	// rename 自体の永続化。ここで失敗しても対象ファイルの内容は新旧いずれかで完結している。
	return syncDir(dir)
}

// syncDir はディレクトリエントリの変更（rename・作成）を永続化する。
func syncDir(dir string) error {
	d, err := os.Open(dir)
	if err != nil {
		return fmt.Errorf("ディレクトリを開けません（%s）: %w", dir, err)
	}
	defer d.Close()
	if err := d.Sync(); err != nil {
		return fmt.Errorf("ディレクトリを同期できません（%s）: %w", dir, err)
	}
	return nil
}

// staleTempAge は「残骸」と見なす一時ファイルの古さ。
//
// 原子的置換の一時ファイルは作られてから rename までミリ秒で消える。
// これより古いものは、書き込み中に落ちたプロセスが残した残骸である。
//
// **なぜ即時に消さないか**: アプリは単一インスタンスに制限しないため、
// 同じプロジェクトを別プロセスが開いていることがある。無条件に消すと
// **相手が書き込み中の一時ファイルを巻き添えで消してしまう**。
const staleTempAge = 10 * time.Minute

// sweepStaleTempFiles はプロジェクトフォルダに残った一時ファイルの残骸を消す。
//
// `WriteFileAtomic` は後片づけを defer で行うため、**SIGKILL では走らない**。
// 残骸を放置すると、共同プロジェクトでは作業ツリーそのものが同期の対象であるため
// （同期から除くのは `locks/` のみ）、次の反映で同期先へ載り他メンバーへ配られる。
//
// 消せなかったものは無視する（掃除の失敗でプロジェクトを開けなくしない）。
// 戻り値は消した件数（呼び出し側の記録用）。
func sweepStaleTempFiles(root string) int {
	removed := 0
	cutoff := time.Now().Add(-staleTempAge)
	_ = filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil // 読めない場所は飛ばす（掃除は最善努力）
		}
		if d.IsDir() {
			if d.Name() == gitDirName {
				return filepath.SkipDir // git の内部は触らない
			}
			return nil
		}
		if !isAtomicTempName(d.Name()) {
			return nil
		}
		info, ierr := d.Info()
		if ierr != nil || info.ModTime().After(cutoff) {
			return nil // 別プロセスが書き込み中かもしれない
		}
		if os.Remove(p) == nil {
			removed++
		}
		return nil
	})
	return removed
}

// isAtomicTempName は WriteFileAtomic が作る一時ファイルの名前かを判定する。
//
// 生成側は `os.CreateTemp(dir, "."+base+".tmp")` であり、
// 名前は「`.` + 対象ファイル名 + `.tmp` + 数字」になる。**この形以外は消さない**
// （利用者が置いた `.tmp` で終わるファイルを巻き添えにしないため、末尾の数字まで見る）。
func isAtomicTempName(name string) bool {
	if !strings.HasPrefix(name, ".") {
		return false
	}
	i := strings.LastIndex(name, ".tmp")
	if i < 0 {
		return false
	}
	digits := name[i+len(".tmp"):]
	if digits == "" {
		return false
	}
	for _, r := range digits {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}
