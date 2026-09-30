package updater

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/howashoji/ReqWeave/app/internal/appversion"
)

// SchemaID は更新マニフェストの形式識別子。形式を変えるときは版を上げる
// （古いアプリが新形式を「壊れたマニフェスト」ではなく「未対応の形式」として扱えるようにする）。
const SchemaID = "reqweave-update-manifest/1"

// malformedManifestMessage は更新情報が形式として読めないときの利用者向け 1 文。
// 内訳（どのフィールドが不正か）は診断用の cause 側にだけ置く（利用者向けの文言に内部情報を出さない）。
const malformedManifestMessage = "更新情報の内容が正しくないため、現行版のまま更新を中止しました。時間をおいて再実行してください"

// Manifest は更新マニフェスト（更新の流れの (1) で取得し、署名を検証する）。
//
// 署名の対象は本構造体の JSON バイト列ではなく SigningPayload が組み立てる
// 正準テキストである（JSON のキー順・空白の違いで署名が壊れないようにするため）。
type Manifest struct {
	Schema     string      `json:"schema"`
	Version    string      `json:"version"`
	Assets     []Asset     `json:"assets"`
	Signatures []Signature `json:"signatures"`
}

// Asset は配布物 1 件（OS・アーキテクチャごと）。
type Asset struct {
	OS     string `json:"os"`
	Arch   string `json:"arch"`
	URL    string `json:"url"`
	SHA256 string `json:"sha256"`
	Size   int64  `json:"size"`
}

// Signature はマニフェストへの ed25519 署名。鍵ローテーション中は
// 新旧 2 鍵ぶんが並ぶ（旧鍵を埋め込んだ版も更新できるよう、2 版前まで二重署名を維持する）。
type Signature struct {
	KeyID string `json:"key_id"`
	Sig   string `json:"sig"` // base64（標準・パディングあり）
}

// ID は配布物の識別（os/arch）。
func (a Asset) ID() string { return a.OS + "/" + a.Arch }

// SigningPayload は署名対象の正準テキストを組み立てる。
//
// 署名ツールと検証側が同じ規則で組み立てられるよう、
// JSON のシリアライズには依存しない行指向の形式にしてある。
// 配布物の行は ID（os/arch）の昇順に並べる。
func SigningPayload(m Manifest) []byte {
	assets := make([]Asset, len(m.Assets))
	copy(assets, m.Assets)
	sort.Slice(assets, func(i, j int) bool { return assets[i].ID() < assets[j].ID() })

	var b strings.Builder
	b.WriteString(m.Schema)
	b.WriteString("\n")
	b.WriteString("version=")
	b.WriteString(m.Version)
	b.WriteString("\n")
	for _, a := range assets {
		fmt.Fprintf(&b, "asset=%s url=%s sha256=%s size=%d\n", a.ID(), a.URL, a.SHA256, a.Size)
	}
	return []byte(b.String())
}

// ParseManifest は JSON を読み、形式として成立しているかを検査する。
// 署名の検証は行わない（Verify が行う）。
//
// 未知のフィールドは無視する（将来の形式追加で古いアプリが壊れないようにする）が、
// 既知の必須フィールドの欠落・不正は誤りとして返す。
func ParseManifest(raw []byte) (Manifest, error) {
	var m Manifest
	if err := json.Unmarshal(raw, &m); err != nil {
		return Manifest{}, &Error{Kind: KindMalformed, msg: "更新情報を読み取れないため、現行版のまま更新を中止しました。時間をおいて再実行してください", cause: err}
	}
	if m.Schema != SchemaID {
		return Manifest{}, &Error{Kind: KindUnsupportedSchema,
			msg:   "更新情報の形式に対応していないため、現行版のまま更新を中止しました。配布元から最新版を入手してください",
			cause: fmt.Errorf("unsupported schema %q", m.Schema)}
	}
	if _, err := appversion.ParseSemver(m.Version); err != nil {
		return Manifest{}, &Error{Kind: KindMalformed, msg: "更新情報の内容が正しくないため、現行版のまま更新を中止しました。時間をおいて再実行してください", cause: err}
	}
	if len(m.Assets) == 0 {
		return Manifest{}, &Error{Kind: KindMalformed, msg: "更新情報の内容が正しくないため、現行版のまま更新を中止しました。時間をおいて再実行してください"}
	}
	seen := map[string]bool{}
	for i, a := range m.Assets {
		if err := validateAsset(a); err != nil {
			return Manifest{}, &Error{Kind: KindMalformed,
				msg: malformedManifestMessage, cause: fmt.Errorf("asset %d: %w", i+1, err)}
		}
		if seen[a.ID()] {
			return Manifest{}, &Error{Kind: KindMalformed,
				msg: malformedManifestMessage, cause: fmt.Errorf("duplicate asset %s", a.ID())}
		}
		seen[a.ID()] = true
	}
	if len(m.Signatures) == 0 {
		return Manifest{}, &Error{Kind: KindMalformed, msg: "更新情報の内容が正しくないため、現行版のまま更新を中止しました。時間をおいて再実行してください"}
	}
	for i, s := range m.Signatures {
		if strings.TrimSpace(s.KeyID) == "" || strings.TrimSpace(s.Sig) == "" {
			return Manifest{}, &Error{Kind: KindMalformed,
				msg: malformedManifestMessage, cause: fmt.Errorf("signature %d is incomplete", i+1)}
		}
	}
	return m, nil
}

// Asset は os / arch に一致する配布物を返す。
func (m Manifest) Asset(goos, goarch string) (Asset, bool) {
	// 完全一致を優先し、無ければ universal（両アーキテクチャ対応の配布物）を探す。
	for _, a := range m.Assets {
		if a.OS == goos && a.Arch == goarch {
			return a, true
		}
	}
	for _, a := range m.Assets {
		if a.OS == goos && a.Arch == ArchUniversal {
			return a, true
		}
	}
	return Asset{}, false
}

// ArchUniversal は両アーキテクチャに対応する配布物（macOS の Universal Binary）。
const ArchUniversal = "universal"

func validateAsset(a Asset) error {
	if a.OS == "" || a.Arch == "" {
		return fmt.Errorf("os / arch が空です")
	}
	if !strings.HasPrefix(a.URL, "https://") {
		// 取得は HTTPS のみ（通信を暗号化する）。http:// を書けてしまう経路を残さない。
		return fmt.Errorf("配布物 URL が https:// で始まっていません")
	}
	if err := validateSHA256Hex(a.SHA256); err != nil {
		return err
	}
	if a.Size <= 0 {
		return fmt.Errorf("配布物のサイズが 0 以下です")
	}
	return nil
}

func validateSHA256Hex(s string) error {
	if len(s) != 64 {
		return fmt.Errorf("SHA-256 が 64 桁ではありません")
	}
	for _, r := range s {
		if !(r >= '0' && r <= '9') && !(r >= 'a' && r <= 'f') {
			return fmt.Errorf("SHA-256 に 16 進小文字以外の文字があります")
		}
	}
	return nil
}
