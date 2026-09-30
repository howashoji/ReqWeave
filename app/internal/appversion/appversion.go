// Package appversion はアプリの版番号の正本を保持する。
//
// 正本は本パッケージ内の VERSION ファイル 1 つだけである。Go の実行時値は
// 埋め込み（go:embed）で得るため、ビルドフラグ（-ldflags -X）の指定有無に
// かかわらず常に正本と一致する。wails.json の productVersion との一致は
// tools/versioncheck が lint 段で検査する。
//
// 版番号は更新確認で配布中の最新版と比べる「現行版」として使う。
package appversion

import (
	_ "embed"
	"fmt"
	"strconv"
	"strings"
)

//go:embed VERSION
var raw string

// Version は正本の版番号（major.minor.patch）。
func Version() string { return strings.TrimSpace(raw) }

// Semver は major.minor.patch に分解した版番号。
type Semver struct {
	Major int
	Minor int
	Patch int
}

// String は major.minor.patch 形式へ戻す。
func (s Semver) String() string { return fmt.Sprintf("%d.%d.%d", s.Major, s.Minor, s.Patch) }

// Compare は s と other の順序を返す（s < other なら負、等しければ 0、s > other なら正）。
// 数値順で比較するため 0.10.0 は 0.9.0 より新しいと判定される。
func (s Semver) Compare(other Semver) int {
	switch {
	case s.Major != other.Major:
		return s.Major - other.Major
	case s.Minor != other.Minor:
		return s.Minor - other.Minor
	default:
		return s.Patch - other.Patch
	}
}

// ParseSemver は major.minor.patch 形式の文字列を解釈する。
// 前後の空白は許容するが、接頭辞 v・プレリリース・ビルドメタデータは受け付けない
// （更新確認の版比較を数値順に限定するため）。
func ParseSemver(s string) (Semver, error) {
	trimmed := strings.TrimSpace(s)
	if trimmed == "" {
		return Semver{}, fmt.Errorf("版番号が空です")
	}
	parts := strings.Split(trimmed, ".")
	if len(parts) != 3 {
		return Semver{}, fmt.Errorf("版番号 %q は major.minor.patch 形式ではありません", trimmed)
	}
	out := Semver{}
	dst := []*int{&out.Major, &out.Minor, &out.Patch}
	for i, p := range parts {
		if p == "" || strings.TrimLeft(p, "0123456789") != "" {
			return Semver{}, fmt.Errorf("版番号 %q の第 %d 要素 %q が数字ではありません", trimmed, i+1, p)
		}
		if len(p) > 1 && p[0] == '0' {
			return Semver{}, fmt.Errorf("版番号 %q の第 %d 要素 %q に先頭 0 があります", trimmed, i+1, p)
		}
		n, err := strconv.Atoi(p)
		if err != nil {
			return Semver{}, fmt.Errorf("版番号 %q の第 %d 要素 %q を数値にできません", trimmed, i+1, p)
		}
		*dst[i] = n
	}
	return out, nil
}

// Current は正本の版番号を解釈して返す。正本が不正な形式なら誤りを返す。
func Current() (Semver, error) { return ParseSemver(Version()) }
