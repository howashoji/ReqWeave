package projectstore

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/google/uuid"
)

// プロジェクトフォルダ直下のファイル名。
const (
	FileProject      = "project.yaml"
	FileMembers      = "members.yaml"
	FileRoster       = "roster.yaml"
	FileTerms        = "terms.yaml"
	FilePerspectives = "perspectives.yaml"
	FileExchangeKeys = "exchange-keys.yaml"
	FileReservations = "reservations.yaml" // 予約と作業状況（同期対象）
)

// projectDirs はプロジェクトフォルダの物理構成のディレクトリ（作成時に用意するもの）。
// documents/ の確定版（v1, v2 …）は確定操作時に作る。
var projectDirs = []string{
	"sessions",
	"requirements",
	"decisions",
	"open-issues",
	"questionnaires",
	"imports",
	"documents/requirements/draft",
	"documents/basic-design/draft",
	"audit/ai-log",
	"audit/history",
	"locks",
}

// 空のコレクションファイルの初期内容（各スキーマの最上位キー）。
var emptyCollections = map[string][]byte{
	FileRoster:       []byte("stakeholders: []\n"),
	FileTerms:        []byte("terms: []\n"),
	FilePerspectives: []byte("perspectives: []\n"),
	FileReservations: []byte("reservations: []\n"),
}

// Author はプロジェクトを操作する作業者。
type Author struct {
	AuthorID    string // 利用者 ID（正規化済み = NormalizeAuthorID）
	DisplayName string // 表示名
}

// CreateOptions はプロジェクト作成の入力（プロジェクト一覧の新規作成）。
type CreateOptions struct {
	TargetSystemName string   // 対象システム名（必須）
	Summary          string   // 対象システムの概要（任意）
	DomainPresets    []string // 業務領域（任意・複数可）
	Author           Author   // 作成者。オーナーとして初期登録する

	// ExchangeKeys は質問票返送の復号鍵。
	// アプリ経由の作成では必ず渡す（プロジェクト作成時に生成する）。
	// 未指定の場合は最初の受け渡し操作の前に遅延生成する
	// （交換鍵を持たない古いプロジェクトと同じ経路 = CreateExchangeKeysIfAbsent）。
	ExchangeKeys *ExchangeKeys
}

// CreateProject は単一フォルダ構成のプロジェクトを root に作り、開いた Store を返す。
// root は存在しないか空フォルダであること（既存データを上書きしない）。
func CreateProject(root string, opts CreateOptions) (*Store, error) {
	if opts.TargetSystemName == "" {
		return nil, fmt.Errorf("対象システム名を入力してください")
	}
	authorID, err := NormalizeAuthorID(opts.Author.AuthorID)
	if err != nil {
		return nil, err
	}
	if opts.Author.DisplayName == "" {
		return nil, fmt.Errorf("作成者の表示名がありません")
	}
	if err := ensureEmptyDir(root); err != nil {
		return nil, err
	}
	// macOS では 1 個のファイルとして見せる（package_darwin.go）。
	// 実体はディレクトリのままであり、失敗しても作成は続ける（見せ方の問題で作業を止めない）。
	_ = markAsPackage(root)

	now := time.Now().UTC().Truncate(time.Second)
	project := &Project{
		FormatVersion:    CurrentFormatVersion,
		ProjectID:        newProjectID(),
		TargetSystemName: opts.TargetSystemName,
		Summary:          strings.TrimSpace(opts.Summary),
		Phase:            PhaseRequirements,
		DomainPresets:    opts.DomainPresets,
		CreatedAt:        now,
	}
	members := &Members{Members: []Member{{
		AuthorID:    authorID,
		DisplayName: opts.Author.DisplayName,
		Role:        RoleOwner,
		AddedAt:     now,
		AddedBy:     authorID,
	}}}

	projectYAML, err := project.Marshal()
	if err != nil {
		return nil, err
	}
	membersYAML, err := members.Marshal()
	if err != nil {
		return nil, err
	}

	if err := os.MkdirAll(root, dataDirMode); err != nil {
		return nil, fmt.Errorf("プロジェクトフォルダを作成できません（%s）: %w", root, err)
	}
	for _, d := range projectDirs {
		p := filepath.Join(root, filepath.FromSlash(d))
		if err := os.MkdirAll(p, dataDirMode); err != nil {
			return nil, fmt.Errorf("フォルダを作成できません（%s）: %w", p, err)
		}
	}
	files := map[string][]byte{FileProject: projectYAML, FileMembers: membersYAML}
	if opts.ExchangeKeys != nil {
		keysYAML, err := opts.ExchangeKeys.Marshal()
		if err != nil {
			return nil, err
		}
		files[FileExchangeKeys] = keysYAML
	}
	for name, content := range emptyCollections {
		files[name] = content
	}
	for _, name := range sortedKeys(files) {
		if err := WriteFileAtomic(filepath.Join(root, name), files[name]); err != nil {
			return nil, err
		}
	}
	return Open(root, Author{AuthorID: authorID, DisplayName: opts.Author.DisplayName})
}

// newProjectID は新しいプロジェクト ID（UUIDv4）を返す。
func newProjectID() string { return uuid.NewString() }

// ensureEmptyDir は root が存在しないか空であることを確認する。
func ensureEmptyDir(root string) error {
	entries, err := os.ReadDir(root)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("フォルダを確認できません（%s）: %w", root, err)
	}
	if len(entries) > 0 {
		return fmt.Errorf("フォルダが空ではありません（%s）。別のフォルダを指定してください", root)
	}
	return nil
}

// IsProjectFolder は root がプロジェクトフォルダ（project.yaml を持つ）かを返す。
func IsProjectFolder(root string) bool {
	st, err := os.Stat(filepath.Join(root, FileProject))
	return err == nil && !st.IsDir()
}
