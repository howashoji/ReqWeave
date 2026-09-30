package exchange

// 本ファイルは受け渡しファイルのコンテナと版照合を担う。
//
// 構成: 平文の manifest.yaml ＋ 暗号化ペイロード payload.enc（内容物一式の zip を暗号化したもの）。
// 質問文・宛先氏名等の業務情報を平文メタデータに含めない（パスコードなしでは業務情報を読めないように）。

import (
	"archive/zip"
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/howashoji/ReqWeave/app/internal/projectstore"
)

// 受け渡しファイルの拡張子。
const (
	ExtIssue  = ".rwvq" // 発行用（担当者 → ステークホルダー）
	ExtReturn = ".rwva" // 返送用（ステークホルダー → 担当者）
)

// コンテナの種別（manifest の kind）。
const (
	KindIssue  = "issue"
	KindReturn = "return"
)

// CurrentFormatVersion は本アプリが書き出す受け渡し形式の版。
// 規則はデータ形式の版と同一（minor 増分 = 後方互換の追加 / major 増分 = 非互換変更）。
const CurrentFormatVersion = "1.0"

// コンテナ内のエントリ名。
const (
	manifestEntry = "manifest.yaml"
	payloadEntry  = "payload.enc"
)

// ErrTooNewFormat は自版より新しいメジャー形式のファイル（読み書きしない）。
var ErrTooNewFormat = errors.New("このファイルは新しい版の本システムで作られています。本システムを更新してから開いてください")

// Manifest は受け渡しファイルの平文メタデータ。
//
// 業務情報（質問文・背景説明・宛先氏名・所属）を持たない。
type Manifest struct {
	ExchangeFormatVersion string    `yaml:"exchange_format_version"`
	Kind                  string    `yaml:"kind"`
	ProjectID             string    `yaml:"project_id"`
	QuestionnaireID       string    `yaml:"questionnaire_id"`
	IssuedAt              time.Time `yaml:"issued_at"`
	// KDF は発行用のみ（返送用はプロジェクト交換鍵で暗号化するため持たない）。
	KDF *KDFParams `yaml:"kdf,omitempty"`

	// unknown は自版が知らないフィールド（minor 差での前方互換）。
	unknown map[string]*yaml.Node
}

var knownManifestFields = map[string]bool{
	"exchange_format_version": true, "kind": true, "project_id": true,
	"questionnaire_id": true, "issued_at": true, "kdf": true,
}

// Validate は manifest の必須項目を検証する。
func (m *Manifest) Validate() error {
	if _, err := projectstore.ParseFormatVersion(m.ExchangeFormatVersion); err != nil {
		return fmt.Errorf("受け渡しファイルの形式バージョンが不正です: %w", err)
	}
	switch m.Kind {
	case KindIssue, KindReturn:
	default:
		return fmt.Errorf("受け渡しファイルの種別が不正です: %q", m.Kind)
	}
	if strings.TrimSpace(m.ProjectID) == "" {
		return fmt.Errorf("受け渡しファイルにプロジェクト ID がありません")
	}
	if _, ok := projectstore.IDQuestionnaire.Parse(m.QuestionnaireID); !ok {
		return fmt.Errorf("受け渡しファイルの質問票 ID が不正です: %q", m.QuestionnaireID)
	}
	if m.IssuedAt.IsZero() {
		return fmt.Errorf("受け渡しファイルに発行日時がありません")
	}
	if m.Kind == KindIssue {
		if m.KDF == nil {
			return fmt.Errorf("発行用ファイルに暗号設定がありません")
		}
		if err := m.KDF.Validate(); err != nil {
			return err
		}
	}
	return nil
}

// Payload はコンテナに入れる内容物（コンテナ内パス → 内容）。
type Payload map[string][]byte

// Names は内容物のパスを昇順で返す（出力の決定性と、内容の全数検査に使う）。
func (p Payload) Names() []string {
	names := make([]string, 0, len(p))
	for name := range p {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// Container は読み出した受け渡しファイル（ペイロードは暗号化されたまま保持する）。
type Container struct {
	Manifest Manifest
	sealed   []byte
}

// WriteIssueFile は発行用ファイル（.rwvq）を書き出す。
//
// ペイロードはパスコード導出鍵で暗号化する（発行方向）。
// 使用した KDF パラメータを manifest へ格納するため、manifest.KDF の指定は不要。
func WriteIssueFile(dst string, m Manifest, payload Payload, passcode string) error {
	container, err := BuildIssueContainer(m, payload, passcode)
	if err != nil {
		return err
	}
	return writeFileAtomic(dst, container)
}

// BuildIssueContainer は発行用ファイルのバイト列を組み立てる（出力と発行控えで同じ値を使う）。
func BuildIssueContainer(m Manifest, payload Payload, passcode string) ([]byte, error) {
	if m.Kind != KindIssue {
		return nil, fmt.Errorf("発行用ファイルの種別が不正です: %q", m.Kind)
	}
	packed, err := packPayload(payload)
	if err != nil {
		return nil, err
	}
	sealed, params, err := SealWithPasscode(packed, passcode)
	if err != nil {
		return nil, err
	}
	m.KDF = &params
	return buildContainer(m, sealed)
}

// WriteReturnFile は返送用ファイル（.rwva）を書き出す。
//
// ペイロードは発行ファイルに同梱された公開鍵で暗号化する（返送方向）。
// 返送用は KDF を持たない（担当者側はパスコードなしで取り込む）。
func WriteReturnFile(dst string, m Manifest, payload Payload, recipientPublic []byte) error {
	container, err := BuildReturnContainer(m, payload, recipientPublic)
	if err != nil {
		return err
	}
	return writeFileAtomic(dst, container)
}

// BuildReturnContainer は返送用ファイルのバイト列を組み立てる。
func BuildReturnContainer(m Manifest, payload Payload, recipientPublic []byte) ([]byte, error) {
	if m.Kind != KindReturn {
		return nil, fmt.Errorf("返送用ファイルの種別が不正です: %q", m.Kind)
	}
	packed, err := packPayload(payload)
	if err != nil {
		return nil, err
	}
	sealed, err := SealForRecipient(packed, recipientPublic)
	if err != nil {
		return nil, err
	}
	m.KDF = nil
	return buildContainer(m, sealed)
}

// ReadContainer は受け渡しファイルの manifest を読み、版を照合する。
//
// 自版より新しいメジャー形式のときは ErrTooNewFormat を返し、ペイロードを一切展開しない。
// ペイロードの復号は OpenIssuePayload / OpenReturnPayload で行う。
func ReadContainer(src string) (*Container, error) {
	data, err := os.ReadFile(src)
	if err != nil {
		return nil, fmt.Errorf("受け渡しファイルを読み込めません（%s）: %w", src, err)
	}
	return ParseContainer(data)
}

// ParseContainer はバイト列から受け渡しファイルを読む（ReadContainer の実体）。
func ParseContainer(data []byte) (*Container, error) {
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return nil, fmt.Errorf("受け渡しファイルの形式が違います（壊れているか、別の種類のファイルです）")
	}
	entries := map[string][]byte{}
	for _, f := range zr.File {
		if f.Name != manifestEntry && f.Name != payloadEntry {
			continue
		}
		rc, err := f.Open()
		if err != nil {
			return nil, fmt.Errorf("受け渡しファイルを読み取れません: %w", err)
		}
		body, err := io.ReadAll(rc)
		_ = rc.Close()
		if err != nil {
			return nil, fmt.Errorf("受け渡しファイルを読み取れません: %w", err)
		}
		entries[f.Name] = body
	}
	if entries[manifestEntry] == nil || entries[payloadEntry] == nil {
		return nil, fmt.Errorf("受け渡しファイルの形式が違います（必要な内容が入っていません）")
	}

	var m Manifest
	if err := yaml.Unmarshal(entries[manifestEntry], &m); err != nil {
		return nil, fmt.Errorf("受け渡しファイルのメタデータを解釈できません: %w", err)
	}
	m.unknown, err = projectstore.CollectUnknownFields(entries[manifestEntry], knownManifestFields, "受け渡しファイル")
	if err != nil {
		return nil, err
	}
	if err := m.Validate(); err != nil {
		return nil, err
	}
	if err := checkFormatVersion(m.ExchangeFormatVersion); err != nil {
		return nil, err
	}
	return &Container{Manifest: m, sealed: entries[payloadEntry]}, nil
}

// checkFormatVersion は版照合。自版より新しい major は読み書きしない。
// 自版より古い版・新しい minor は読み込む（旧版の返送ファイルの後方互換 = 09 第4.3項）。
func checkFormatVersion(v string) error {
	file, err := projectstore.ParseFormatVersion(v)
	if err != nil {
		return fmt.Errorf("受け渡しファイルの形式バージョンが不正です: %w", err)
	}
	current, err := projectstore.ParseFormatVersion(CurrentFormatVersion)
	if err != nil {
		panic("exchange: CurrentFormatVersion が不正です: " + CurrentFormatVersion)
	}
	if file.Major > current.Major {
		return ErrTooNewFormat
	}
	return nil
}

// OpenIssuePayload は発行用ファイルのペイロードをパスコードで復号して展開する（回答モード）。
func (c *Container) OpenIssuePayload(passcode string) (Payload, error) {
	if c.Manifest.Kind != KindIssue {
		return nil, fmt.Errorf("このファイルは質問票（発行用）ではありません。担当者から届いた質問票ファイルを開いてください")
	}
	if c.Manifest.KDF == nil {
		return nil, fmt.Errorf("発行用ファイルに暗号設定がありません")
	}
	packed, err := OpenWithPasscode(c.sealed, passcode, *c.Manifest.KDF)
	if err != nil {
		return nil, err
	}
	return unpackPayload(packed)
}

// OpenReturnPayload は返送用ファイルのペイロードをプロジェクト交換鍵で復号して展開する
// （取り込みの最初の手順。パスコード入力は不要）。
func (c *Container) OpenReturnPayload(kp KeyPair) (Payload, error) {
	if c.Manifest.Kind != KindReturn {
		return nil, fmt.Errorf("発行用ファイルです。ステークホルダーから返送されたファイルを選んでください")
	}
	packed, err := OpenSealed(c.sealed, kp)
	if err != nil {
		return nil, err
	}
	return unpackPayload(packed)
}

// ---- 内部 ---------------------------------------------------------------

// buildContainer は manifest（平文）と暗号化ペイロードを単一の zip コンテナへまとめる。
func buildContainer(m Manifest, sealed []byte) ([]byte, error) {
	if err := m.Validate(); err != nil {
		return nil, err
	}
	if err := checkFormatVersion(m.ExchangeFormatVersion); err != nil {
		return nil, err
	}
	manifestYAML, err := projectstore.MarshalWithUnknownFields(&m, m.unknown, "受け渡しファイル")
	if err != nil {
		return nil, err
	}

	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	if err := writeZipEntry(zw, manifestEntry, manifestYAML, zip.Deflate); err != nil {
		return nil, err
	}
	// 暗号化済みのペイロードは圧縮しない（圧縮率から内容量が漏れるのを避ける）。
	if err := writeZipEntry(zw, payloadEntry, sealed, zip.Store); err != nil {
		return nil, err
	}
	if err := zw.Close(); err != nil {
		return nil, fmt.Errorf("受け渡しファイルを組み立てられません: %w", err)
	}
	return buf.Bytes(), nil
}

func writeZipEntry(zw *zip.Writer, name string, body []byte, method uint16) error {
	w, err := zw.CreateHeader(&zip.FileHeader{Name: name, Method: method})
	if err != nil {
		return fmt.Errorf("受け渡しファイルを組み立てられません: %w", err)
	}
	if _, err := w.Write(body); err != nil {
		return fmt.Errorf("受け渡しファイルを組み立てられません: %w", err)
	}
	return nil
}

// packPayload は内容物一式を zip 化する（標準 zip = Deflate 互換）。
func packPayload(p Payload) ([]byte, error) {
	if len(p) == 0 {
		return nil, fmt.Errorf("受け渡しファイルに入れる内容がありません")
	}
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for _, name := range p.Names() {
		if err := validatePayloadName(name); err != nil {
			return nil, err
		}
		if err := writeZipEntry(zw, name, p[name], zip.Deflate); err != nil {
			return nil, err
		}
	}
	if err := zw.Close(); err != nil {
		return nil, fmt.Errorf("受け渡しファイルを組み立てられません: %w", err)
	}
	return buf.Bytes(), nil
}

// unpackPayload は復号済みの zip を展開する。
func unpackPayload(packed []byte) (Payload, error) {
	zr, err := zip.NewReader(bytes.NewReader(packed), int64(len(packed)))
	if err != nil {
		return nil, fmt.Errorf("受け渡しファイルの内容を展開できません（ファイルが壊れています）")
	}
	out := Payload{}
	for _, f := range zr.File {
		if f.FileInfo().IsDir() {
			continue
		}
		if err := validatePayloadName(f.Name); err != nil {
			return nil, err
		}
		rc, err := f.Open()
		if err != nil {
			return nil, fmt.Errorf("受け渡しファイルの内容を展開できません: %w", err)
		}
		body, err := io.ReadAll(rc)
		_ = rc.Close()
		if err != nil {
			return nil, fmt.Errorf("受け渡しファイルの内容を展開できません: %w", err)
		}
		out[f.Name] = body
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("受け渡しファイルの内容が空です")
	}
	return out, nil
}

// validatePayloadName は内容物のパスを検証する（展開先を逸脱する名前を受け付けない）。
func validatePayloadName(name string) error {
	if name == "" || strings.HasPrefix(name, "/") || strings.Contains(name, `\`) {
		return fmt.Errorf("受け渡しファイルの内容の名前が不正です: %q", name)
	}
	clean := path.Clean(name)
	if clean != name || clean == "." || strings.HasPrefix(clean, "../") {
		return fmt.Errorf("受け渡しファイルの内容の名前が不正です: %q", name)
	}
	return nil
}

// writeFileAtomic は出力先へ原子的に書き出す（プロジェクトデータの保存と同方式）。
func writeFileAtomic(dst string, data []byte) error {
	return writeFileAtomicMode(dst, data, 0o644)
}

// writeFileAtomicMode は権限を指定して原子的に書き出す。
//
// 一時ファイルは**出力先と同じフォルダ**に作る（rename の原子性は同一ボリューム内でのみ成立する）。
// ここは OS ネイティブのパスを扱うため filepath を使う。スラッシュ専用の path を使うと
// Windows のパス（区切りが `\`）でフォルダを取り違える。
func writeFileAtomicMode(dst string, data []byte, mode os.FileMode) error {
	dir := filepath.Dir(dst)
	tmp, err := os.CreateTemp(dir, ".rwv-*.tmp")
	if err != nil {
		return fmt.Errorf("出力先に書き込めません（%s）: %w", dir, err)
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
		return fmt.Errorf("出力先に書き込めません（%s）: %w", dst, err)
	}
	if err := tmp.Sync(); err != nil {
		return fmt.Errorf("出力先に書き込めません（%s）: %w", dst, err)
	}
	if err := tmp.Chmod(mode); err != nil {
		return fmt.Errorf("出力先の権限を設定できません（%s）: %w", dst, err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("出力先を閉じられません（%s）: %w", dst, err)
	}
	if err := os.Rename(tmpName, dst); err != nil {
		return fmt.Errorf("出力先へ差し替えられません（%s）: %w", dst, err)
	}
	committed = true
	return nil
}
