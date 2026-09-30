package projectstore

import (
	"archive/zip"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

const (
	// BackupGenerations は自動退避の保持世代数。
	BackupGenerationsMax = 10
	// backupStampLayout は自動退避のファイル名（UTC 時刻）。
	// コロンは Windows のファイル名に使えないため使わない。
	backupStampLayout = "20060102T150405Z"
	backupExt         = ".zip"
)

// Generation は自動退避の 1 世代。
type Generation struct {
	Path      string    // zip のパス
	CreatedAt time.Time // 退避時刻（UTC。ファイル名から復元）
	Size      int64
}

// gitDirName は同期の管理情報のフォルダ（自動退避では除き、手動バックアップには含める）。
const gitDirName = ".git"

// WriteManualBackupZip は手動バックアップを単一 zip として dst へ書き出す。
//
// `locks/` は含めない（ロックは端末内の一時的な状態のため持ち運ばない）。**`.git/` は含める**
// （別端末への移送・障害復旧で、取り込み・反映の関係をそのまま引き継げるようにするため）。
// 圧縮は標準 zip（Deflate）互換とし、OS 標準機能で展開・内容確認できる形式にする。
func WriteManualBackupZip(root, dst string) error {
	return writeBackupZip(root, dst, true)
}

// writeBackupZip は zip の書き出し本体。includeGit は `.git/`（同期の管理情報）を含めるか。
//
// 自動退避は false・手動バックアップは true。内部構成は同一で、
// 復元処理（RestoreZip）を共通化する。
func writeBackupZip(root, dst string, includeGit bool) error {
	if err := os.MkdirAll(filepath.Dir(dst), dataDirMode); err != nil {
		return fmt.Errorf("バックアップの保存先を作成できません: %w", err)
	}
	tmp := dst + ".tmp"
	f, err := os.Create(tmp)
	if err != nil {
		return fmt.Errorf("バックアップファイルを作成できません（%s）: %w", dst, err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = f.Close()
			_ = os.Remove(tmp)
		}
	}()

	zw := zip.NewWriter(f)
	err = filepath.WalkDir(root, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(root, p)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		if rel == "." {
			return nil
		}
		if rel == lockDir || strings.HasPrefix(rel, lockDir+"/") {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		// `.git/` は自動退避では除く（10 世代の容量が効き、退避の目的は作業コピーの内容を
		// 直前の正常な状態へ戻すことにあるため）。復元後は同期先から取り直す。
		if !includeGit && (rel == gitDirName || strings.HasPrefix(rel, gitDirName+"/")) {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		header, err := zip.FileInfoHeader(info)
		if err != nil {
			return err
		}
		header.Name = rel
		if d.IsDir() {
			header.Name += "/"
			header.Method = zip.Store
		} else {
			header.Method = zip.Deflate
		}
		w, err := zw.CreateHeader(header)
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		src, err := os.Open(p)
		if err != nil {
			return err
		}
		defer src.Close()
		_, err = io.Copy(w, src)
		return err
	})
	if err != nil {
		return fmt.Errorf("バックアップを作成できません: %w", err)
	}
	if err := zw.Close(); err != nil {
		return fmt.Errorf("バックアップを閉じられません: %w", err)
	}
	if err := f.Sync(); err != nil {
		return fmt.Errorf("バックアップを同期できません: %w", err)
	}
	if err := f.Close(); err != nil {
		return fmt.Errorf("バックアップを閉じられません: %w", err)
	}
	if err := os.Rename(tmp, dst); err != nil {
		return fmt.Errorf("バックアップを保存先へ移動できません: %w", err)
	}
	committed = true
	return syncDir(filepath.Dir(dst))
}

// AutoBackup は自動退避を 1 世代作り、保持世代数を超えた分を古い順に削除する。
// 戻り値は作成した zip のパス。
func AutoBackup(paths AppPaths, root, projectID string, now time.Time) (string, error) {
	if projectID == "" {
		return "", fmt.Errorf("自動退避の対象プロジェクトが特定できません")
	}
	dir := paths.ProjectBackupsDir(projectID)
	stamp := now.UTC().Format(backupStampLayout)
	dst := filepath.Join(dir, stamp+backupExt)
	// 同一秒に複数回退避した場合も上書きしない
	for i := 1; ; i++ {
		if _, err := os.Stat(dst); os.IsNotExist(err) {
			break
		}
		dst = filepath.Join(dir, fmt.Sprintf("%s-%d%s", stamp, i, backupExt))
	}
	// 自動退避は `.git/` を含めない（理由は writeBackupZip）
	if err := writeBackupZip(root, dst, false); err != nil {
		return "", err
	}
	if err := rotateBackups(paths, projectID, BackupGenerationsMax); err != nil {
		return dst, err
	}
	return dst, nil
}

// rotateBackups は保持世代数を超えた自動退避を古い順に削除する。
func rotateBackups(paths AppPaths, projectID string, keep int) error {
	gens, err := ListBackupGenerations(paths, projectID)
	if err != nil {
		return err
	}
	for i := keep; i < len(gens); i++ {
		if err := os.Remove(gens[i].Path); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("古い自動退避を削除できません（%s）: %w", filepath.Base(gens[i].Path), err)
		}
	}
	return nil
}

// ListBackupGenerations は自動退避の世代一覧を新しい順に返す（復元の画面用）。
func ListBackupGenerations(paths AppPaths, projectID string) ([]Generation, error) {
	dir := paths.ProjectBackupsDir(projectID)
	entries, err := os.ReadDir(dir)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("自動退避の一覧を取得できません: %w", err)
	}
	var gens []Generation
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), backupExt) {
			continue
		}
		stamp, _, _ := strings.Cut(strings.TrimSuffix(e.Name(), backupExt), "-")
		at, err := time.Parse(backupStampLayout, stamp)
		if err != nil {
			continue
		}
		info, err := e.Info()
		if err != nil {
			return nil, err
		}
		gens = append(gens, Generation{Path: filepath.Join(dir, e.Name()), CreatedAt: at.UTC(), Size: info.Size()})
	}
	sort.Slice(gens, func(i, j int) bool {
		if gens[i].CreatedAt.Equal(gens[j].CreatedAt) {
			return gens[i].Path > gens[j].Path
		}
		return gens[i].CreatedAt.After(gens[j].CreatedAt)
	})
	return gens, nil
}

// PeekBackupProjectID はバックアップ zip を展開せずに project_id と対象システム名を読む
// （復元前の重複判定・確認表示のため）。
func PeekBackupProjectID(src string) (*Project, error) {
	r, err := zip.OpenReader(src)
	if err != nil {
		return nil, fmt.Errorf("バックアップファイルを開けません（%s）: %w", filepath.Base(src), err)
	}
	defer r.Close()
	for _, f := range r.File {
		if f.Name != FileProject {
			continue
		}
		rc, err := f.Open()
		if err != nil {
			return nil, fmt.Errorf("バックアップの内容を読めません: %w", err)
		}
		defer rc.Close()
		data, err := io.ReadAll(rc)
		if err != nil {
			return nil, fmt.Errorf("バックアップの内容を読めません: %w", err)
		}
		return UnmarshalProject(data)
	}
	return nil, fmt.Errorf("バックアップにプロジェクトデータ（%s）がありません", FileProject)
}

// RestoreZip はバックアップ zip を dst へ展開する。
//
// dst は存在しないか空であること（既存フォルダを上書きしない = 既存データの破壊防止）。
func RestoreZip(src, dst string) (*Project, error) {
	if err := ensureEmptyDir(dst); err != nil {
		return nil, err
	}
	r, err := zip.OpenReader(src)
	if err != nil {
		return nil, fmt.Errorf("バックアップファイルを開けません（%s）: %w", filepath.Base(src), err)
	}
	defer r.Close()

	if err := os.MkdirAll(dst, dataDirMode); err != nil {
		return nil, fmt.Errorf("復元先を作成できません（%s）: %w", dst, err)
	}
	for _, f := range r.File {
		target, err := safeJoin(dst, f.Name)
		if err != nil {
			return nil, err
		}
		if strings.HasSuffix(f.Name, "/") {
			if err := os.MkdirAll(target, dataDirMode); err != nil {
				return nil, fmt.Errorf("復元先のフォルダを作成できません: %w", err)
			}
			continue
		}
		if err := os.MkdirAll(filepath.Dir(target), dataDirMode); err != nil {
			return nil, fmt.Errorf("復元先のフォルダを作成できません: %w", err)
		}
		rc, err := f.Open()
		if err != nil {
			return nil, fmt.Errorf("バックアップの内容を読めません（%s）: %w", f.Name, err)
		}
		data, err := io.ReadAll(rc)
		rc.Close()
		if err != nil {
			return nil, fmt.Errorf("バックアップの内容を読めません（%s）: %w", f.Name, err)
		}
		if err := WriteFileAtomic(target, data); err != nil {
			return nil, err
		}
	}
	data, err := os.ReadFile(filepath.Join(dst, FileProject))
	if err != nil {
		return nil, fmt.Errorf("復元したデータにプロジェクトデータがありません: %w", err)
	}
	return UnmarshalProject(data)
}

// safeJoin は zip 内のパスが復元先の外へ出ないことを確認して結合する。
func safeJoin(base, name string) (string, error) {
	clean := filepath.Clean(filepath.FromSlash(name))
	if filepath.IsAbs(clean) || clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("バックアップに不正なパスが含まれています: %q", name)
	}
	return filepath.Join(base, clean), nil
}

// ReassignProjectID は復元したプロジェクトへ新しい project_id を割り当てる。
//
// 同一 project_id のプロジェクトを「複製として保持」する場合に用いる（復元の選択肢の 1 つ）。
// プロジェクトの同一性は project_id で判定するため、複製側には別の ID を与える。
func ReassignProjectID(root string, author Author) (string, error) {
	s, err := Open(root, author)
	if err != nil {
		return "", err
	}
	defer s.Close()
	var newID string
	err = s.UpdateProject(func(p *Project) error {
		p.ProjectID = newProjectID()
		newID = p.ProjectID
		return nil
	})
	if err != nil {
		return "", err
	}
	return newID, nil
}

// CloseAndBackup はプロジェクトを閉じ、自動退避を 1 世代作る（自動退避はプロジェクトを閉じるたびに作る）。
func (s *Store) CloseAndBackup(paths AppPaths) (string, error) {
	projectID := s.Project().ProjectID
	root := s.Root()
	if err := s.Close(); err != nil {
		return "", err
	}
	return AutoBackup(paths, root, projectID, time.Now())
}
