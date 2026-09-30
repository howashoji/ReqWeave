package main

import (
	"bufio"
	"fmt"
	"regexp"
	"sort"
	"strings"
)

// developerIDPrefix は Mac App Store の外へ配る .app に使う証明書の名前の頭。
// 指定が無いときは、この種類の証明書だけを候補にする（開発用・インストーラ用の証明書では公証を通らない）。
const developerIDPrefix = "Developer ID Application:"

// identityLine は `security find-identity -v -p codesigning` の 1 件の行。
// 行頭から「1)」のような番号・SHA-1（16 進数 40 桁）・引用符で囲んだ名前の順に並ぶ。-v を付けない出力では名前の後ろに理由が付くことがあるので、
// 名前の後ろは読まない。
var identityLine = regexp.MustCompile(`^\s*\d+\)\s+([0-9A-Fa-f]{40})\s+"([^"]*)"`)

var sha1Pattern = regexp.MustCompile(`^[0-9A-Fa-f]{40}$`)

// identity はキーチェーンにある署名用の証明書 1 枚。
type identity struct {
	SHA1 string // 大文字にそろえた SHA-1
	Name string
}

// parseIdentities は find-identity の出力から証明書を取り出す。
// 同じ証明書が複数の行に出ることがある（同じ SHA-1 の行が 2 回出る）ので、SHA-1 で 1 つにまとめる。
// 別々の証明書が同じ名前を持つことはある（作り直した証明書など）ので、名前ではまとめない。
func parseIdentities(listing string) []identity {
	seen := map[string]bool{}
	var out []identity
	sc := bufio.NewScanner(strings.NewReader(listing))
	for sc.Scan() {
		m := identityLine.FindStringSubmatch(sc.Text())
		if m == nil {
			continue
		}
		sha := strings.ToUpper(m[1])
		if seen[sha] {
			continue
		}
		seen[sha] = true
		out = append(out, identity{SHA1: sha, Name: m[2]})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].SHA1 < out[j].SHA1 })
	return out
}

// resolve は署名に使う証明書を 1 つに決める。
//
//   - explicit が SHA-1（16 進数 40 桁）なら、キーチェーンにあるその証明書を使う。
//   - explicit が名前なら、その名前の証明書が 1 枚だけのときに使う。
//   - explicit が空なら、Developer ID Application の証明書が 1 枚だけのときに使う。
//
// 候補が 2 枚以上あるときは選ばずに止める。1 枚目を黙って使うと、別の製品の証明書や
// 期限切れ間近の古い証明書で署名してしまうおそれがあるため、利用者に SHA-1 で選ばせる。
func resolve(listing, explicit string) (identity, error) {
	all := parseIdentities(listing)
	explicit = strings.TrimSpace(explicit)

	if sha1Pattern.MatchString(explicit) {
		want := strings.ToUpper(explicit)
		for _, id := range all {
			if id.SHA1 == want {
				return id, nil
			}
		}
		return identity{}, fmt.Errorf("✘ 指定した SHA-1 の証明書がキーチェーンの有効な署名 ID にありません: %s\n%s", want, listCandidates(all))
	}

	var candidates []identity
	for _, id := range all {
		if explicit != "" && id.Name == explicit {
			candidates = append(candidates, id)
		}
		if explicit == "" && strings.HasPrefix(id.Name, developerIDPrefix) {
			candidates = append(candidates, id)
		}
	}

	switch {
	case len(candidates) == 1:
		return candidates[0], nil
	case len(candidates) == 0 && explicit != "":
		return identity{}, fmt.Errorf("✘ 指定した名前の証明書がキーチェーンの有効な署名 ID にありません: %s\n%s", explicit, listCandidates(all))
	case len(candidates) == 0:
		return identity{}, fmt.Errorf("✘ Developer ID Application の署名 ID がキーチェーンにありません。証明書をキーチェーンへ入れてから再実行してください")
	default:
		what := "Developer ID Application の証明書"
		if explicit != "" {
			what = "指定した名前の証明書"
		}
		return identity{}, fmt.Errorf("✘ %sが %d 枚あるため、どれで署名するかを決められません。\n%s  使う証明書の SHA-1 を MAC_SIGN_IDENTITY=<SHA-1> で指定してください。\n  見分け方: キーチェーンアクセスで証明書を開き、有効期限と「指紋」の SHA-1 を見比べます",
			what, len(candidates), listCandidates(candidates))
	}
}

// listCandidates は候補を「SHA-1 と名前」の行に並べる（利用者が SHA-1 を写して指定できるように）。
func listCandidates(ids []identity) string {
	if len(ids) == 0 {
		return "  （有効な署名 ID はありません。security find-identity -v -p codesigning で確かめられます）\n"
	}
	var b strings.Builder
	b.WriteString("  候補:\n")
	for _, id := range ids {
		fmt.Fprintf(&b, "    %s  %q\n", id.SHA1, id.Name)
	}
	return b.String()
}
