package projectstore

// 本ファイルは成果物ドキュメント（documents/）と
// 版の保存構造を担う。
//
// 確定版（vN）へ書き込む経路は draft からの複製のみで、編集・削除の API を持たない
//（確定した版は後から変わらない）。ドラフトの置換は一時フォルダ + リネームで原子性を確保する。

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// 成果物の種別（doc_kind）。
const (
	DocKindRequirements = "requirements"
	DocKindBasicDesign  = "basic-design"
)

// 章ファイルの状態（status）。
const (
	DocStatusGenerated = "generated"
	DocStatusConfirmed = "confirmed"
)

// dirDocuments は成果物の配置。
const dirDocuments = "documents"

// draftDirName はドラフトのディレクトリ名（版番号を持たない。確定したときに初めて版番号が付く）。
const draftDirName = "draft"

// versionsFileName は版履歴。
const versionsFileName = "versions.md"

// DocumentChapter は章ファイル 1 件（フロントマター + 本文）。
type DocumentChapter struct {
	DocKind     string    `yaml:"doc_kind"`
	Chapter     string    `yaml:"chapter"`
	Version     int       `yaml:"version,omitempty"`
	Status      string    `yaml:"status"`
	GeneratedAt time.Time `yaml:"generated_at"`
	// Covers は本章が収載する要件項目・決定事項・未決事項の ID（章から要件へ辿る連鎖）。
	Covers []string `yaml:"covers"`

	// FileName は章ファイル名（例: 06-functional-requirements.md）。
	FileName string `yaml:"-"`
	// Body は章本文（Markdown）。
	Body string `yaml:"-"`
}

// Validate は必須項目・値集合を検証する。
func (c *DocumentChapter) Validate() error {
	switch c.DocKind {
	case DocKindRequirements, DocKindBasicDesign:
	default:
		return fmt.Errorf("成果物の種別が不正です: %q", c.DocKind)
	}
	if strings.TrimSpace(c.Chapter) == "" {
		return fmt.Errorf("章識別子がありません（%s）", c.FileName)
	}
	switch c.Status {
	case DocStatusGenerated, DocStatusConfirmed:
	default:
		return fmt.Errorf("章ファイルの状態が不正です: %q", c.Status)
	}
	if c.GeneratedAt.IsZero() {
		return fmt.Errorf("生成日時がありません（%s）", c.FileName)
	}
	if strings.TrimSpace(c.FileName) == "" || !strings.HasSuffix(c.FileName, ".md") {
		return fmt.Errorf("章ファイル名が不正です: %q", c.FileName)
	}
	if strings.ContainsAny(c.FileName, `/\`) {
		return fmt.Errorf("章ファイル名にパス区切りを含められません: %q", c.FileName)
	}
	return nil
}

// DocumentVersion は版履歴の 1 件（versions.md）。
type DocumentVersion struct {
	Version     int       `yaml:"version"`
	ConfirmedAt time.Time `yaml:"confirmed_at"`
	// ConfirmedBy は確定した作業者の利用者 ID（取り込みで版番号が衝突したときの再採番の決定手順の入力）。
	ConfirmedBy string `yaml:"confirmed_by,omitempty"`
	// SourceIDs は確定時のソースレコード ID 集合（依存マップの入力）。
	SourceIDs []string `yaml:"source_ids"`
	// Chapters は章 → 収載ソース ID の依存マップ。
	Chapters map[string][]string `yaml:"chapters,omitempty"`
}

// documentDir は documents/<種別>/<draft|vN> の相対パスを返す。
func documentDir(kind, version string) string {
	return dirDocuments + "/" + kind + "/" + version
}

// DraftDir はドラフトの相対パスを返す。
func DraftDir(kind string) string { return documentDir(kind, draftDirName) }

// VersionDir は確定版の相対パスを返す。
func VersionDir(kind string, n int) string {
	return documentDir(kind, "v"+strconv.Itoa(n))
}

// VersionsFile は版履歴の相対パスを返す。
func VersionsFile(kind string) string { return dirDocuments + "/" + kind + "/" + versionsFileName }

// ReplaceDraft はドラフトを丸ごと置き換える（文書生成の結果の原子的置換）。
//
// 一時フォルダへ全章を書き出してからリネームするため、途中で失敗しても旧ドラフトが残る。
func (s *Store) ReplaceDraft(kind string, chapters []DocumentChapter) error {
	if err := validateDocKind(kind); err != nil {
		return err
	}
	if len(chapters) == 0 {
		return fmt.Errorf("生成された章がありません")
	}
	for i := range chapters {
		chapters[i].DocKind = kind
		chapters[i].Status = DocStatusGenerated
		chapters[i].Version = 0
		if err := chapters[i].Validate(); err != nil {
			return err
		}
	}

	target := filepath.Join(s.root, filepath.FromSlash(DraftDir(kind)))
	return s.write(func() error {
		parent := filepath.Dir(target)
		if err := os.MkdirAll(parent, dataDirMode); err != nil {
			return fmt.Errorf("成果物の保存先を作成できません: %w", err)
		}
		tmp, err := os.MkdirTemp(parent, ".draft-*")
		if err != nil {
			return fmt.Errorf("一時フォルダを作成できません: %w", err)
		}
		defer os.RemoveAll(tmp) // 成功時は rename 済みで存在しない

		for _, c := range chapters {
			data, err := marshalDocument(&c, c.Body)
			if err != nil {
				return err
			}
			if err := os.WriteFile(filepath.Join(tmp, c.FileName), data, dataFileMode); err != nil {
				return fmt.Errorf("章ファイルを書き出せません（%s）: %w", c.FileName, err)
			}
		}

		// 旧ドラフトを退避してから差し替える（差し替え途中で失敗しても復旧できる）。
		backup := target + ".old"
		_ = os.RemoveAll(backup)
		if _, err := os.Stat(target); err == nil {
			if err := os.Rename(target, backup); err != nil {
				return fmt.Errorf("旧ドラフトを退避できません: %w", err)
			}
		}
		if err := os.Rename(tmp, target); err != nil {
			_ = os.Rename(backup, target) // 復旧
			return fmt.Errorf("ドラフトを差し替えられません: %w", err)
		}
		_ = os.RemoveAll(backup)
		return syncDir(parent)
	})
}

// LoadDraft はドラフトの全章を章ファイル名の昇順で返す（未生成なら空）。
func (s *Store) LoadDraft(kind string) ([]DocumentChapter, error) {
	return s.loadChapters(kind, DraftDir(kind))
}

// LoadVersion は確定版の全章を返す。
func (s *Store) LoadVersion(kind string, n int) ([]DocumentChapter, error) {
	return s.loadChapters(kind, VersionDir(kind, n))
}

func (s *Store) loadChapters(kind, dir string) ([]DocumentChapter, error) {
	if err := validateDocKind(kind); err != nil {
		return nil, err
	}
	full := filepath.Join(s.root, filepath.FromSlash(dir))
	entries, err := os.ReadDir(full)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("成果物を走査できません（%s）: %w", dir, err)
	}
	var out []DocumentChapter
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".md") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(full, e.Name()))
		if err != nil {
			return nil, fmt.Errorf("章ファイルを読み込めません（%s）: %w", e.Name(), err)
		}
		var c DocumentChapter
		body, err := parseDocument(data, &c)
		if err != nil {
			return nil, fmt.Errorf("章ファイルを解釈できません（%s）: %w", e.Name(), err)
		}
		c.FileName, c.Body = e.Name(), body
		if err := c.Validate(); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].FileName < out[j].FileName })
	return out, nil
}

// ConfirmDraft はドラフトを次の確定版へ複製し、版履歴へ記録する。
//
// 確定版のディレクトリへはこの複製処理でのみ書き込む。
func (s *Store) ConfirmDraft(kind string, sourceIDs []string, chapterMap map[string][]string) (*DocumentVersion, error) {
	chapters, err := s.LoadDraft(kind)
	if err != nil {
		return nil, err
	}
	if len(chapters) == 0 {
		return nil, fmt.Errorf("確定できるドラフトがありません")
	}
	versions, err := s.Versions(kind)
	if err != nil {
		return nil, err
	}
	next := 1
	if n := len(versions); n > 0 {
		next = versions[n-1].Version + 1
	}
	// 共同プロジェクトでは版番号は**仮採番**（その作業コピーで見えている最大版 + 1）。取り込みで
	// 衝突したら内容を変えずに再採番する。
	record := DocumentVersion{
		Version: next, ConfirmedAt: time.Now().UTC(), ConfirmedBy: s.author.AuthorID,
		SourceIDs: sourceIDs, Chapters: chapterMap,
	}

	target := filepath.Join(s.root, filepath.FromSlash(VersionDir(kind, next)))
	err = s.write(func() error {
		if _, err := os.Stat(target); err == nil {
			return fmt.Errorf("確定版 v%d は既に存在します（上書きしません）", next)
		}
		if err := os.MkdirAll(target, dataDirMode); err != nil {
			return fmt.Errorf("確定版の保存先を作成できません: %w", err)
		}
		for _, c := range chapters {
			c.Status = DocStatusConfirmed
			c.Version = next
			data, err := marshalDocument(&c, c.Body)
			if err != nil {
				return err
			}
			if err := os.WriteFile(filepath.Join(target, c.FileName), data, dataFileMode); err != nil {
				return fmt.Errorf("確定版を書き出せません（%s）: %w", c.FileName, err)
			}
		}
		return syncDir(target)
	})
	if err != nil {
		return nil, err
	}
	if err := s.appendVersion(kind, record); err != nil {
		return nil, err
	}
	return &record, nil
}

// Versions は版履歴を版番号の昇順で返す。
func (s *Store) Versions(kind string) ([]DocumentVersion, error) {
	if err := validateDocKind(kind); err != nil {
		return nil, err
	}
	data, err := os.ReadFile(filepath.Join(s.root, filepath.FromSlash(VersionsFile(kind))))
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("版履歴を読み込めません: %w", err)
	}
	return ParseVersions(data)
}

// versionsFile は versions.md の内容（YAML フロントマター相当の構造化データ）。
type versionsFile struct {
	Versions []DocumentVersion `yaml:"versions"`
}

// ParseVersions は versions.md の内容を版番号の昇順で解釈する（取り込み時の突き合わせに使う）。
func ParseVersions(data []byte) ([]DocumentVersion, error) {
	var file versionsFile
	if err := yaml.Unmarshal(data, &file); err != nil {
		return nil, fmt.Errorf("版履歴を解釈できません: %w", err)
	}
	sort.Slice(file.Versions, func(i, j int) bool { return file.Versions[i].Version < file.Versions[j].Version })
	return file.Versions, nil
}

// appendVersion は版履歴へ 1 件追記する（原子的書き込み）。
func (s *Store) appendVersion(kind string, v DocumentVersion) error {
	versions, err := s.Versions(kind)
	if err != nil {
		return err
	}
	versions = append(versions, v)
	return s.writeVersions(kind, versions)
}

// writeVersions は版履歴を版番号の昇順で書き出す（原子的書き込み）。
func (s *Store) writeVersions(kind string, versions []DocumentVersion) error {
	sort.Slice(versions, func(i, j int) bool { return versions[i].Version < versions[j].Version })
	data, err := yaml.Marshal(versionsFile{Versions: versions})
	if err != nil {
		return fmt.Errorf("版履歴を組み立てられません: %w", err)
	}
	var b strings.Builder
	b.WriteString("---\n")
	b.Write(data)
	b.WriteString("---\n\n")
	b.WriteString("# 版履歴\n\n")
	for _, r := range versions {
		fmt.Fprintf(&b, "- v%d（%s）: 収載ソース %d 件\n",
			r.Version, r.ConfirmedAt.Format(time.RFC3339), len(r.SourceIDs))
	}
	return s.WriteFile(VersionsFile(kind), []byte(b.String()))
}

// LatestVersion は最新の確定版番号を返す（0 = 確定版なし）。
func (s *Store) LatestVersion(kind string) (int, error) {
	versions, err := s.Versions(kind)
	if err != nil {
		return 0, err
	}
	if len(versions) == 0 {
		return 0, nil
	}
	return versions[len(versions)-1].Version, nil
}

func validateDocKind(kind string) error {
	switch kind {
	case DocKindRequirements, DocKindBasicDesign:
		return nil
	default:
		return fmt.Errorf("成果物の種別が不正です: %q", kind)
	}
}
