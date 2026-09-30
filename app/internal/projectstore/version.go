package projectstore

import (
	"fmt"
	"strconv"
	"strings"
)

// CurrentFormatVersion は本アプリが書き出すデータ形式バージョン。
// 初版 "1.0" → "1.1"（project.yaml へ summary を追加）
// → "1.2"（要件項目へ acceptance_criteria を追加）
// → "1.3"（project.yaml へ ambiguous_terms を追加）
// → "1.4"（project.yaml の sync / reservations.yaml / id-ranges.yaml /
// versions.md の confirmed_by・confirmed_at を追加）。
// いずれも minor 増分（任意フィールドと追加ファイルのみで、旧版のアプリでも読める）。
const CurrentFormatVersion = "1.4"

// FormatVersion は `<major>.<minor>` のデータ形式バージョン。
type FormatVersion struct {
	Major int
	Minor int
}

// ParseFormatVersion は "1.0" 形式の文字列を解釈する。
func ParseFormatVersion(s string) (FormatVersion, error) {
	major, minor, ok := strings.Cut(s, ".")
	if !ok {
		return FormatVersion{}, fmt.Errorf("形式バージョンが `<major>.<minor>` ではありません: %q", s)
	}
	ma, err := strconv.Atoi(major)
	if err != nil || ma < 0 {
		return FormatVersion{}, fmt.Errorf("形式バージョンのメジャーが数値ではありません: %q", s)
	}
	mi, err := strconv.Atoi(minor)
	if err != nil || mi < 0 {
		return FormatVersion{}, fmt.Errorf("形式バージョンのマイナーが数値ではありません: %q", s)
	}
	return FormatVersion{Major: ma, Minor: mi}, nil
}

func (v FormatVersion) String() string { return fmt.Sprintf("%d.%d", v.Major, v.Minor) }

// Compatibility は読み込んだデータ形式と自版との関係。
type Compatibility int

const (
	// CompatSame は自版と同一。通常どおり読み書きする。
	CompatSame Compatibility = iota
	// CompatNeedsMigration は自版より古い。移行前に自動退避を 1 世代作成してから自版へ移行する。
	CompatNeedsMigration
	// CompatNewerMinor は自版より新しいマイナー。未知フィールドを保持したまま読み書きする（前方互換）。
	CompatNewerMinor
	// CompatTooNew は自版より新しいメジャー。いかなるファイルにも書き込まない（前方互換の破壊防止）。
	CompatTooNew
)

// Classify は読み込んだ形式バージョンと自版（CurrentFormatVersion）との関係を判定する。
func Classify(file FormatVersion) Compatibility {
	cur, err := ParseFormatVersion(CurrentFormatVersion)
	if err != nil {
		// CurrentFormatVersion は定数であり、解釈できないのは実装の誤り。
		panic("projectstore: CurrentFormatVersion が不正です: " + CurrentFormatVersion)
	}
	switch {
	case file.Major > cur.Major:
		return CompatTooNew
	case file.Major < cur.Major, file.Major == cur.Major && file.Minor < cur.Minor:
		return CompatNeedsMigration
	case file.Major == cur.Major && file.Minor > cur.Minor:
		return CompatNewerMinor
	default:
		return CompatSame
	}
}

// ErrNeedsMigration は自版より古い形式のプロジェクトを開いたときのエラー。
//
// 移行は「自動退避を 1 世代作成 → 自版へ移行 → migrated_from を記録」の順で行う（Migrate）。
// Open は黙って形式を変えず、本エラーで移行の実行を促す。
// 現行の形式バージョンは初版 "1.0" のため、正規の経路で作られたデータでは発生しない。
type ErrNeedsMigration struct {
	Found   FormatVersion
	Current FormatVersion
}

func (e *ErrNeedsMigration) Error() string {
	return fmt.Sprintf("このプロジェクトは古いデータ形式です（データ形式 %s / 本アプリ %s）。形式の移行が必要です", e.Found, e.Current)
}

// ErrTooNew は自版より新しいメジャー形式のプロジェクトを開いたときのエラー。
// 表示文言（原因と次の行動の 1 文）は呼び出し側（バインディング層）が組み立てる。
type ErrTooNew struct {
	Found   FormatVersion
	Current FormatVersion
}

func (e *ErrTooNew) Error() string {
	return fmt.Sprintf("このプロジェクトは新しい版のアプリで作られています（データ形式 %s / 本アプリ %s）", e.Found, e.Current)
}
