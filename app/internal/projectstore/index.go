package projectstore

// 本ファイルは派生インデックスのうち、**共有レコードの内容ハッシュ**を担う。
//
// インデックスは**派生データ**であり、正はプロジェクトフォルダ内のファイル。
// 保存先はアプリ設定領域 `cache/<project_id>/index.json`（プロジェクトフォルダを汚さない）。
//
// **更新のタイミングは取り込みへ集約している**。作業コピーは 1 名の利用者が使うため、
// 他端末の書き込みで実体が随時変わることはない。以前の「画面表示前・書き込み前の更新時刻確認と
// 差分再読込」は廃止し（通常操作の応答を単独利用と同一にする）、
// 他メンバーの変更が入る**取り込みの完了時にだけ**、変更されたファイルを差分再構築する
// （`ApplyChangedPaths`）。
// **全再構築は不在・広範囲不整合の場合のみ**（`Rebuild`。利用者が直接編集した場合・復元を含む）。
//
// 内容ハッシュの用途は、以前の競合検知の基準版から**取り込み後の再構築の要否判定**へ
// 変わった（同期の三面マージは基準版をコミットから直接読むため、本索引に依存しない）。
//
// 本ファイルの範囲は共有レコードの索引に限る。
// 逆方向参照・全文検索索引・ID 最大値は既存の実装（追跡連鎖・採番の実体走査）を置き換えない。

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// indexFileName は派生インデックスのファイル名。
const indexFileName = "index.json"

// TermIndexPrefix は用語のインデックスキー接頭辞（用語は ID を持たないため名前で引く）。
const TermIndexPrefix = "term:"

// IndexEntry は共有レコード 1 件の索引（競合検知の基準版と同じハッシュ）。
type IndexEntry struct {
	// ID はレコード ID（FR-* / NFR-* / DEC-nnn / ISS-nnn）または `term:<用語名>`。
	ID string `json:"id"`
	// File はプロジェクトフォルダからの相対パス（スラッシュ区切り）。
	File string `json:"file"`
	// ModTime は File の更新時刻（広範囲不整合の検出に使う）。
	ModTime time.Time `json:"modTime"`
	// Hash は内容ハッシュ（SHA-256 の 16 進表記）。取り込み後の再構築の要否判定に使う。
	Hash string `json:"hash"`
}

// RecordIndex は共有レコードの索引（アプリ設定領域のキャッシュ）。
type RecordIndex struct {
	ProjectID string                `json:"projectId"`
	Entries   map[string]IndexEntry `json:"entries"`
}

// indexPath は当該プロジェクトの索引ファイルのパス。
func indexPath(paths AppPaths, projectID string) string {
	return filepath.Join(paths.CacheDir(), projectID, indexFileName)
}

// LoadRecordIndex は索引を読んで返す。
//
// **通常操作のたびにファイルを読み直さない**（更新時刻の確認と差分再読込は廃止した）。
// 索引が無い・読めない・別プロジェクトのもの・**実体と広範囲に食い違う**場合だけ全再構築する
// （正はファイル側）。結果はキャッシュへ保存する（保存に失敗しても索引の内容は返す = 派生データ）。
func LoadRecordIndex(paths AppPaths, s *Store) (*RecordIndex, error) {
	projectID := s.Project().ProjectID
	var ix *RecordIndex
	if data, err := os.ReadFile(indexPath(paths, projectID)); err == nil {
		var cached RecordIndex
		if json.Unmarshal(data, &cached) == nil && cached.ProjectID == projectID && cached.Entries != nil {
			ix = &cached
		}
	}
	if ix != nil {
		consistent, err := ix.consistent(s)
		if err != nil {
			return nil, err
		}
		if consistent {
			return ix, nil
		}
	}
	ix, err := Rebuild(s)
	if err != nil {
		return nil, err
	}
	_ = ix.save(paths)
	return ix, nil
}

// Rebuild は実体を全走査して索引を作り直す（全再構築）。
func Rebuild(s *Store) (*RecordIndex, error) {
	ix := &RecordIndex{ProjectID: s.Project().ProjectID, Entries: map[string]IndexEntry{}}
	if err := ix.refresh(s); err != nil {
		return nil, err
	}
	return ix, nil
}

// ApplyChangedPaths は取り込みで変更されたパスだけを索引へ反映する（取り込みの完了時に同期モジュールが呼ぶ）。
//
// paths は作業コピーからの相対パス（スラッシュ区切り）。索引の対象外のパスは無視する。
// `terms.yaml` が含まれるときは用語の索引を作り直す（1 ファイルに同居するため）。
// 反映した結果はキャッシュへ保存する（保存の失敗は致命ではない = 派生データ）。
func (ix *RecordIndex) ApplyChangedPaths(paths AppPaths, s *Store, changed []string) error {
	if ix.Entries == nil {
		ix.Entries = map[string]IndexEntry{}
	}
	terms := false
	for _, rel := range changed {
		rel = strings.TrimPrefix(strings.ReplaceAll(rel, "\\", "/"), "./")
		if rel == FileTerms {
			terms = true
			continue
		}
		dir, name, ok := strings.Cut(rel, "/")
		if !ok || !isRecordDir(dir) || !strings.HasSuffix(name, ".md") {
			continue
		}
		id := strings.TrimSuffix(name, ".md")
		info, err := os.Stat(filepath.Join(s.root, filepath.FromSlash(rel)))
		if os.IsNotExist(err) {
			delete(ix.Entries, id) // 取り込みで消えたレコード
			continue
		}
		if err != nil {
			return fmt.Errorf("%s の情報を取得できません: %w", rel, err)
		}
		data, err := os.ReadFile(filepath.Join(s.root, filepath.FromSlash(rel)))
		if err != nil {
			return fmt.Errorf("%s を読み込めません: %w", rel, err)
		}
		ix.Entries[id] = IndexEntry{ID: id, File: rel, ModTime: info.ModTime(), Hash: contentHash(data)}
	}
	if terms {
		for id := range ix.Entries {
			if strings.HasPrefix(id, TermIndexPrefix) {
				delete(ix.Entries, id)
			}
		}
		if err := ix.refreshTerms(s, map[string]bool{}); err != nil {
			return err
		}
	}
	_ = ix.save(paths)
	return nil
}

// isRecordDir は 1 レコード 1 ファイルの置き場所かを返す。
func isRecordDir(dir string) bool {
	return dir == dirRequirements || dir == dirDecisions || dir == dirOpenIssues
}

// consistent は索引が実体と食い違っていないかを返す（不整合検出）。
//
// **内容は読まない**（stat のみ。通常操作の応答を単独利用と同一に保つ）。
// 索引に無いレコード・消えたレコード・更新時刻の不一致のいずれかがあれば偽を返し、呼び出し側が
// **全再構築**する（利用者がエディタで直接編集した場合・復元を含む）。
// 以前の「食い違ったファイルだけを読み直す」差分再読込は行わない。
func (ix *RecordIndex) consistent(s *Store) (bool, error) {
	seen := map[string]bool{}
	for _, dir := range []string{dirRequirements, dirDecisions, dirOpenIssues} {
		entries, err := os.ReadDir(filepath.Join(s.root, dir))
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return false, fmt.Errorf("%s を走査できません: %w", dir, err)
		}
		for _, e := range entries {
			if e.IsDir() || !strings.HasSuffix(e.Name(), ".md") {
				continue
			}
			id := strings.TrimSuffix(e.Name(), ".md")
			seen[id] = true
			cached, ok := ix.Entries[id]
			if !ok {
				return false, nil
			}
			info, err := e.Info()
			if err != nil {
				return false, fmt.Errorf("%s の情報を取得できません: %w", dir+"/"+e.Name(), err)
			}
			if !cached.ModTime.Equal(info.ModTime()) {
				return false, nil
			}
		}
	}
	info, err := os.Stat(filepath.Join(s.root, FileTerms))
	if err != nil && !os.IsNotExist(err) {
		return false, fmt.Errorf("%s の情報を取得できません: %w", FileTerms, err)
	}
	for id, e := range ix.Entries {
		if strings.HasPrefix(id, TermIndexPrefix) {
			if err != nil || !e.ModTime.Equal(info.ModTime()) {
				return false, nil
			}
			seen[id] = true
			continue
		}
		if !seen[id] {
			return false, nil // 実体が消えたレコードが索引に残っている
		}
	}
	// 用語ファイルはあるが索引に用語が 1 件も無い場合（未索引）も再構築する
	if err == nil && !hasTermEntries(ix) {
		terms, terr := s.LoadTerms()
		if terr != nil {
			return false, terr
		}
		if len(terms.Terms) > 0 {
			return false, nil
		}
	}
	return true, nil
}

func hasTermEntries(ix *RecordIndex) bool {
	for id := range ix.Entries {
		if strings.HasPrefix(id, TermIndexPrefix) {
			return true
		}
	}
	return false
}

// Hash は基準版ハッシュを返す（索引に無ければ ok = false = 新規作成）。
func (ix *RecordIndex) Hash(id string) (string, bool) {
	e, ok := ix.Entries[id]
	if !ok {
		return "", false
	}
	return e.Hash, true
}

// IDs は索引にあるレコード ID を昇順で返す（表示・検証用）。
func (ix *RecordIndex) IDs() []string {
	out := make([]string, 0, len(ix.Entries))
	for id := range ix.Entries {
		out = append(out, id)
	}
	sort.Strings(out)
	return out
}

// TermIndexID は用語のインデックスキーを返す。
func TermIndexID(name string) string { return TermIndexPrefix + name }

// refresh は実体を全走査して索引を作り直す（全再構築の実体）。
//
// 実体が消えたレコードは索引から落とす（正はファイル側）。
func (ix *RecordIndex) refresh(s *Store) error {
	if ix.Entries == nil {
		ix.Entries = map[string]IndexEntry{}
	}
	seen := map[string]bool{}

	for _, dir := range []string{dirRequirements, dirDecisions, dirOpenIssues} {
		entries, err := os.ReadDir(filepath.Join(s.root, dir))
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return fmt.Errorf("%s を走査できません: %w", dir, err)
		}
		for _, e := range entries {
			if e.IsDir() || !strings.HasSuffix(e.Name(), ".md") {
				continue
			}
			id := strings.TrimSuffix(e.Name(), ".md")
			rel := dir + "/" + e.Name()
			seen[id] = true
			if err := ix.refreshFileEntry(s, id, rel, e); err != nil {
				return err
			}
		}
	}

	if err := ix.refreshTerms(s, seen); err != nil {
		return err
	}

	for id := range ix.Entries {
		if !seen[id] {
			delete(ix.Entries, id)
		}
	}
	return nil
}

// refreshFileEntry は 1 ファイル = 1 レコードの索引を更新する（更新時刻が同じなら読み直さない）。
func (ix *RecordIndex) refreshFileEntry(s *Store, id, rel string, e os.DirEntry) error {
	info, err := e.Info()
	if err != nil {
		return fmt.Errorf("%s の情報を取得できません: %w", rel, err)
	}
	if cached, ok := ix.Entries[id]; ok && cached.File == rel && cached.ModTime.Equal(info.ModTime()) {
		return nil
	}
	data, err := os.ReadFile(filepath.Join(s.root, filepath.FromSlash(rel)))
	if err != nil {
		return fmt.Errorf("%s を読み込めません: %w", rel, err)
	}
	ix.Entries[id] = IndexEntry{ID: id, File: rel, ModTime: info.ModTime(), Hash: contentHash(data)}
	return nil
}

// refreshTerms は用語の索引を更新する（用語は 1 ファイルに同居するため、更新時に全件を計算し直す）。
func (ix *RecordIndex) refreshTerms(s *Store, seen map[string]bool) error {
	path := filepath.Join(s.root, FileTerms)
	info, err := os.Stat(path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("%s の情報を取得できません: %w", FileTerms, err)
	}

	// 用語の索引が更新時刻と一致していれば読み直さない。
	fresh := false
	for id, e := range ix.Entries {
		if strings.HasPrefix(id, TermIndexPrefix) {
			fresh = e.ModTime.Equal(info.ModTime())
			break
		}
	}
	if fresh {
		for id := range ix.Entries {
			if strings.HasPrefix(id, TermIndexPrefix) {
				seen[id] = true
			}
		}
		return nil
	}

	terms, err := s.LoadTerms()
	if err != nil {
		return err
	}
	for _, t := range terms.Terms {
		id := TermIndexID(t.Name)
		seen[id] = true
		ix.Entries[id] = IndexEntry{
			ID: id, File: FileTerms, ModTime: info.ModTime(), Hash: contentHash(termContent(t)),
		}
	}
	return nil
}

// termContent は用語 1 件のハッシュ対象（同一内容で同一値になる正規形）。
func termContent(t Term) []byte {
	return []byte(strings.Join(append([]string{t.Name, t.NameEn, t.Definition}, t.Forbidden...), "\x1f"))
}

// contentHash は内容ハッシュ（SHA-256 の 16 進表記）。
func contentHash(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

// save は索引をアプリ設定領域へ書き出す（派生データのため失敗しても致命ではない）。
func (ix *RecordIndex) save(paths AppPaths) error {
	path := indexPath(paths, ix.ProjectID)
	if err := os.MkdirAll(filepath.Dir(path), dataDirMode); err != nil {
		return err
	}
	data, err := json.Marshal(ix)
	if err != nil {
		return err
	}
	return WriteFileAtomic(path, data)
}
