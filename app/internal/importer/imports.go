package importer

// 本ファイルは取り込み 1 件 = 1 フォルダ（imports/IMP-nnn/）の
// 作成・読出・一覧を担う。
//
// 原本 source.<拡張子> は取り込み後いかなる操作でも変更しない。そのため
// **本パッケージは原本を更新・上書きする公開 API を持たない**。
// 開発AIフィードバックの分類（classification）の保持も本パッケージの責務。
// 取り込み時の初期値を Input で受け取り、以後の付与・変更は SetClassification で行う
// （import.yaml だけを書き換え、原本・抽出テキストには触れない = classification.go）。

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/howashoji/ReqWeave/app/internal/projectstore"
)

// Kind は取り込み資料の種別。
type Kind string

const (
	KindMaterial      Kind = "material"        // 資料
	KindMinutes       Kind = "minutes"         // 議事録
	KindDevAIFeedback Kind = "dev-ai-feedback" // 開発AIフィードバック
)

// Format は原本の形式。
type Format string

const (
	FormatTxt       Format = "txt"
	FormatMD        Format = "md"
	FormatDocx      Format = "docx"
	FormatXlsx      Format = "xlsx"
	FormatPptx      Format = "pptx"
	FormatPDF       Format = "pdf"
	FormatClipboard Format = "clipboard"
)

// ExtractionStatus はテキスト抽出の結果。
type ExtractionStatus string

const (
	StatusExtracted ExtractionStatus = "extracted"
	StatusFailed    ExtractionStatus = "failed" // 抽出不能。原本は保持し extracted.md は作らない
)

// Classification は開発AIフィードバックの分類。
type Classification string

const (
	ClassRequirementsGap Classification = "requirements-gap"
	ClassDesignGap       Classification = "design-gap"
	ClassNewRequest      Classification = "new-request"
	ClassClarification   Classification = "clarification"
	ClassOther           Classification = "other"
)

// ClipboardSourceName はクリップボード貼り付けの source_name。
// 端末固有のパスを保持しないため、貼り付けは常にこの固定値になる。
const ClipboardSourceName = "clipboard"

// ファイル名（取り込み 1 件 = 1 フォルダの中身）。
const (
	MetaFile      = "import.yaml"
	ExtractedFile = "extracted.md"
	sourceStem    = "source"
)

// sourceExt は原本ファイルの拡張子。
//
// クリップボード貼り付けは元ファイルが存在しないため、内容がテキストであることに
// 合わせて source.txt として保存する（原本の名前は「source.<拡張子>」とだけ決めている）。
var sourceExt = map[Format]string{
	FormatTxt:       "txt",
	FormatMD:        "md",
	FormatDocx:      "docx",
	FormatXlsx:      "xlsx",
	FormatPptx:      "pptx",
	FormatPDF:       "pdf",
	FormatClipboard: "txt",
}

var (
	validKinds = map[Kind]bool{
		KindMaterial: true, KindMinutes: true, KindDevAIFeedback: true,
	}
	validStatuses = map[ExtractionStatus]bool{
		StatusExtracted: true, StatusFailed: true,
	}
	validClassifications = map[Classification]bool{
		ClassRequirementsGap: true, ClassDesignGap: true, ClassNewRequest: true,
		ClassClarification: true, ClassOther: true,
	}
)

// Meta は import.yaml。
type Meta struct {
	ID               string           `yaml:"id"`
	Kind             Kind             `yaml:"kind"`
	ImportedAt       time.Time        `yaml:"imported_at"`
	SourceName       string           `yaml:"source_name"`
	SourceFormat     Format           `yaml:"source_format"`
	ExtractionStatus ExtractionStatus `yaml:"extraction_status"`

	// FromTemplate はエクスポートに同梱した定型テンプレ（開発 AI からのフィードバック用）由来か。
	// kind: dev-ai-feedback のみ。未設定と false を区別するためポインタで持つ。
	FromTemplate *bool `yaml:"from_template,omitempty"`
	// Classification は担当者が付与する分類。kind: dev-ai-feedback のみ。
	Classification Classification `yaml:"classification,omitempty"`
}

// Validate は各項目の値集合と、種別に依存する項目の適用範囲を検証する。
//
// 列挙値の検証は**書き込み前**に行う（不正値をファイルへ残さない）。
func (m *Meta) Validate() error {
	if _, ok := projectstore.IDImport.Parse(m.ID); !ok {
		return fmt.Errorf("取り込み ID が %s-nnn の形式ではありません: %q", projectstore.IDImport.Prefix, m.ID)
	}
	if !validKinds[m.Kind] {
		return fmt.Errorf("取り込みの種別が不正です（%s / %s / %s のいずれか）: %q",
			KindMaterial, KindMinutes, KindDevAIFeedback, m.Kind)
	}
	if _, ok := sourceExt[m.SourceFormat]; !ok {
		return fmt.Errorf("原本の形式が不正です（txt / md / docx / xlsx / pptx / pdf / clipboard のいずれか）: %q", m.SourceFormat)
	}
	if !validStatuses[m.ExtractionStatus] {
		return fmt.Errorf("抽出状態が不正です（%s / %s のいずれか）: %q",
			StatusExtracted, StatusFailed, m.ExtractionStatus)
	}
	if m.ImportedAt.IsZero() {
		return fmt.Errorf("%s の取り込み日時がありません", m.ID)
	}
	if err := validateSourceName(m.SourceName, m.SourceFormat); err != nil {
		return err
	}
	if m.Kind != KindDevAIFeedback {
		if m.FromTemplate != nil {
			return fmt.Errorf("from_template は開発AIフィードバックにのみ設定できます（%s の種別は %s）", m.ID, m.Kind)
		}
		if m.Classification != "" {
			return fmt.Errorf("classification は開発AIフィードバックにのみ設定できます（%s の種別は %s）", m.ID, m.Kind)
		}
	}
	if m.Classification != "" && !validClassifications[m.Classification] {
		return fmt.Errorf("分類が不正です（requirements-gap / design-gap / new-request / clarification / other のいずれか）: %q", m.Classification)
	}
	return nil
}

// validateSourceName は source_name が端末固有のパスを含まないことを確かめる。
//
// 保持してよいのは元の**ファイル名だけ**であり、絶対パス・相対パス・ドライブレターは
// いずれも「どの端末のどこにあったか」を漏らすため受け付けない。
func validateSourceName(name string, format Format) error {
	if format == FormatClipboard {
		if name != ClipboardSourceName {
			return fmt.Errorf("クリップボード貼り付けの source_name は %q です: %q", ClipboardSourceName, name)
		}
		return nil
	}
	if strings.TrimSpace(name) == "" {
		return fmt.Errorf("元ファイル名がありません")
	}
	if name != strings.TrimSpace(name) {
		return fmt.Errorf("元ファイル名の前後に空白が含まれています: %q", name)
	}
	if strings.ContainsAny(name, `/\`) {
		return fmt.Errorf("元ファイル名にパス区切りを含められません（端末固有のパスを保持しない）: %q", name)
	}
	if strings.Contains(name, ":") {
		return fmt.Errorf("元ファイル名にドライブレター・ストリーム指定を含められません: %q", name)
	}
	if name == "." || name == ".." {
		return fmt.Errorf("元ファイル名が不正です: %q", name)
	}
	return nil
}

// Input は取り込み 1 件の入力。
type Input struct {
	Kind         Kind
	SourceName   string // 元ファイル名（ベース名のみ）。クリップボードは空でよい
	SourceFormat Format
	Content      []byte // 原本のバイト列。取り込み後は変更しない

	// ExtractionStatus は抽出の結果。抽出処理そのものは Extract（ImportWithExtraction）が担い、
	// Import は結果を受け取って保存する。
	ExtractionStatus ExtractionStatus
	// ExtractedText は抽出テキスト（ExtractionStatus が extracted のときのみ保存する）。
	ExtractedText string

	FromTemplate   *bool          // kind: dev-ai-feedback のみ
	Classification Classification // kind: dev-ai-feedback のみ。取り込み時点で分かっている場合
}

// Importer は取り込みモジュール。
type Importer struct {
	store *projectstore.Store
}

// New は取り込みモジュールを作る。
func New(store *projectstore.Store) *Importer { return &Importer{store: store} }

// Dir は取り込み 1 件のフォルダ（プロジェクトルートからの相対・スラッシュ区切り）。
func Dir(id string) string { return "imports/" + id }

// MetaPath は import.yaml の相対パス。
func MetaPath(id string) string { return Dir(id) + "/" + MetaFile }

// ExtractedPath は extracted.md の相対パス。
func ExtractedPath(id string) string { return Dir(id) + "/" + ExtractedFile }

// SourcePath は原本の相対パス。
func SourcePath(id string, format Format) string {
	return Dir(id) + "/" + sourceStem + "." + sourceExt[format]
}

// Import は資料を 1 件取り込む。
//
// IMP-nnn は既存の最大連番 + 1 で採番する（欠番は再利用しない。消した ID が別の資料を指さないように）。
// 書き込みはいずれも保存キューと原子的書き込みを経由する。
// 既存資料に独立の件数上限は設けない（プロジェクトの総量 500MB の内数）。
func (im *Importer) Import(in Input) (*Meta, error) {
	if in.SourceFormat == FormatClipboard && in.SourceName == "" {
		in.SourceName = ClipboardSourceName
	}
	meta := &Meta{
		Kind:             in.Kind,
		ImportedAt:       time.Now().UTC().Truncate(time.Second),
		SourceName:       in.SourceName,
		SourceFormat:     in.SourceFormat,
		ExtractionStatus: in.ExtractionStatus,
		FromTemplate:     in.FromTemplate,
		Classification:   in.Classification,
	}
	// ID 以外を先に検証する（採番してから弾くと欠番が増えるため）。
	meta.ID = projectstore.IDImport.Format(1)
	if err := meta.Validate(); err != nil {
		return nil, err
	}

	id, err := im.store.AllocateID(projectstore.IDImport, func(id string) error {
		meta.ID = id
		data, err := yaml.Marshal(meta)
		if err != nil {
			return fmt.Errorf("取り込みのメタデータを組み立てられません: %w", err)
		}
		// 原本を先に書く（メタデータだけが残る状態を作らない）。
		if err := im.store.WriteFile(SourcePath(id, meta.SourceFormat), in.Content); err != nil {
			return err
		}
		if meta.ExtractionStatus == StatusExtracted {
			if err := im.store.WriteFile(ExtractedPath(id), []byte(in.ExtractedText)); err != nil {
				return err
			}
		}
		return im.store.WriteFile(MetaPath(id), data)
	})
	if err != nil {
		return nil, err
	}
	return im.Load(id)
}

// ImportWithExtraction は原本からテキストを抽出したうえで取り込む（通常の取り込み経路）。
//
// 抽出できない資料は extraction_status: failed として原本のみを保持する
// （利用者向けの通知文言は ErrNotExtractable）。
// 呼び出し側で Extract を呼び忘れて「抽出済みなのに本文が空」という状態を作らないため、
// 通常はこちらを使い、Import は抽出結果が既に手元にある場合の下位経路とする。
func (im *Importer) ImportWithExtraction(in Input) (*Meta, error) {
	text, err := Extract(in.SourceFormat, in.Content)
	if err != nil {
		if !errors.Is(err, ErrNotExtractable) {
			return nil, err
		}
		in.ExtractionStatus = StatusFailed
		in.ExtractedText = ""
	} else {
		in.ExtractionStatus = StatusExtracted
		in.ExtractedText = text
	}
	return im.Import(in)
}

// Load は取り込み 1 件のメタデータを読む。
func (im *Importer) Load(id string) (*Meta, error) {
	data, err := os.ReadFile(im.abs(MetaPath(id)))
	if os.IsNotExist(err) {
		return nil, fmt.Errorf("取り込んだ資料が見つかりません（%s）", id)
	}
	if err != nil {
		return nil, fmt.Errorf("取り込みのメタデータを読み込めません（%s）: %w", id, err)
	}
	var m Meta
	if err := yaml.Unmarshal(data, &m); err != nil {
		return nil, fmt.Errorf("取り込みのメタデータを解釈できません（%s）: %w", id, err)
	}
	if err := m.Validate(); err != nil {
		return nil, err
	}
	return &m, nil
}

// ReadSource は原本のバイト列を返す（読出のみ。書き換える API は設けない）。
func (im *Importer) ReadSource(id string) ([]byte, error) {
	m, err := im.Load(id)
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(im.abs(SourcePath(id, m.SourceFormat)))
	if err != nil {
		return nil, fmt.Errorf("原本を読み込めません（%s）: %w", id, err)
	}
	return data, nil
}

// ReadExtracted は抽出テキストを返す。抽出不能（failed）の資料は extracted.md を持たない。
func (im *Importer) ReadExtracted(id string) (string, error) {
	m, err := im.Load(id)
	if err != nil {
		return "", err
	}
	if m.ExtractionStatus != StatusExtracted {
		return "", fmt.Errorf("この資料はテキストを抽出できていません（%s）", id)
	}
	data, err := os.ReadFile(im.abs(ExtractedPath(id)))
	if err != nil {
		return "", fmt.Errorf("抽出テキストを読み込めません（%s）: %w", id, err)
	}
	return string(data), nil
}

// List は取り込み済みの資料を ID 昇順（= 取り込み順）で返す。
//
// 一覧が返すのは種別・取り込み日時・抽出状態を含むメタデータ全体である。
func (im *Importer) List() ([]Meta, error) {
	entries, err := os.ReadDir(im.abs("imports"))
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("取り込みフォルダを走査できません: %w", err)
	}
	var out []Meta
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		if _, ok := projectstore.IDImport.Parse(e.Name()); !ok {
			continue
		}
		m, err := im.Load(e.Name())
		if err != nil {
			return nil, err
		}
		out = append(out, *m)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}

// abs はプロジェクトルートからの相対パスを絶対パスへ直す。
func (im *Importer) abs(rel string) string {
	return filepath.Join(im.store.Root(), filepath.FromSlash(rel))
}
