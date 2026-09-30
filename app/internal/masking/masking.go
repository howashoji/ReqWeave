// Package masking は秘密情報のマスキングフィルタ（シークレットキーを外へ出さないための第二防衛）。
//
// ログ・エラーメッセージ・同期の記録へ書き出す直前に Mask を通し、シークレットキーの既知接頭辞・
// 同期先 URL の資格情報部・既知のトークン接頭辞・SSH 秘密鍵ブロックに一致する連続文字列を
// ***MASKED*** へ置換する。第一防衛（キー本体を受け取る型・引数を持たない = 構造的非出力）の
// 取りこぼしを拾う役割であり、本パッケージがあることを理由に値を流してよいわけではない。
//
// 動作ログのロガーは internal/applog。その書き出し（write）が本フィルタの
// 唯一の通過点で、メッセージ・付随項目・パニックのスタックのいずれもここを通る。
// **JSON へ整形する前の値へ掛けること**（整形後の行へ掛けると値の末尾に続く区切りまで
// 置換に飲まれて行が壊れる）。
package masking

import (
	"os"
	"regexp"
	"strings"
	"sync"
)

// Masked は置換後の表記。
const Masked = "***MASKED***"

// longUserinfo は `scheme://<userinfo>@` の userinfo に ':'（パスワード区切り）が無くても
// 資格情報とみなす長さ。GitHub 等はトークンを利用者名部に置く形式（`https://<token>@host`）を
// 受け付けるため、利用者名として不自然に長いものは伏せる（`git@` のような短い利用者名は残す）。
const longUserinfo = 20

var (
	// SSH 秘密鍵ブロック（終端が欠けていても末尾まで伏せる）。
	sshKeyBlock     = regexp.MustCompile(`(?s)-----BEGIN [A-Z0-9 ]*PRIVATE KEY-----.*?-----END [A-Z0-9 ]*PRIVATE KEY-----`)
	sshKeyBlockOpen = regexp.MustCompile(`(?s)-----BEGIN [A-Z0-9 ]*PRIVATE KEY-----.*`)

	// URL の資格情報部（`scheme://userinfo@`）。
	urlUserinfo = regexp.MustCompile(`([A-Za-z][A-Za-z0-9+.-]*://)([^/\s@]+)@`)

	// HTTP 認証ヘッダ。
	authHeader = regexp.MustCompile(`(?i)(authorization\s*:\s*(?:basic|bearer|token)\s+)\S+`)

	// key=value / key: value 形式の秘密情報（git の資格情報プロトコル `password=` を含む）。
	// "tokens_in=" のような複合語に当たらないよう、キー名の直後を区切りに限定する。
	secretPair = regexp.MustCompile(`(?i)\b(password|passwd|passphrase|secret|token|private_key|api[_-]?key)(\s*[=:]\s*)\S+`)

	// 既知の接頭辞（コードの中の一覧で保守する）。
	//   sk-ant- / sk- : Anthropic / OpenAI のシークレットキー
	//   AIza          : Google API キー
	//   ghp_ gho_ ghu_ ghs_ ghr_ github_pat_ : GitHub のトークン
	//   gl*-          : GitLab のトークン（glpat- / gldt- / glrt- / glcbt- / glptt- / glsoat- / glffct- / glimt-）
	//   ATATT / ATBB  : Atlassian API トークン / Bitbucket アプリパスワード
	//   xox[abprs]-   : Slack のトークン
	// 利用者のホームディレクトリ配下のパス（OS ユーザー名を含むため `~` へ落とす）。
	// 先頭の境界（行頭・空白・引用符・括弧・等号）を捕まえることで、URL のパス
	// （`https://host/Users/...`）を巻き込まない。
	homePath = regexp.MustCompile(`(?m)(^|[\s"'(\[=])((?:[A-Za-z]:)?[\\/](?:Users|home)[\\/][^\\/\s"']+)`)

	knownPrefix = regexp.MustCompile(`\b(?:sk-ant-[A-Za-z0-9_-]{8,}|sk-[A-Za-z0-9_-]{8,}|AIza[0-9A-Za-z_-]{20,}|gh[pousr]_[A-Za-z0-9]{16,}|github_pat_[A-Za-z0-9_]{20,}|gl(?:pat|dt|rt|cbt|ptt|soat|ffct|imt)-[A-Za-z0-9_-]{16,}|ATATT[A-Za-z0-9_-]{20,}|ATBB[A-Za-z0-9_-]{16,}|xox[abprs]-[A-Za-z0-9-]{10,})`)
)

// Mask は s に含まれる秘密情報らしい連続文字列を Masked へ置換して返す。
// 秘密情報を含まない文字列はそのまま返す（置換は冪等）。
func Mask(s string) string {
	if s == "" {
		return s
	}
	s = sshKeyBlock.ReplaceAllString(s, Masked)
	s = sshKeyBlockOpen.ReplaceAllString(s, Masked)
	s = urlUserinfo.ReplaceAllStringFunc(s, func(m string) string {
		sub := urlUserinfo.FindStringSubmatch(m)
		if !isCredentialUserinfo(sub[2]) {
			return m
		}
		return sub[1] + Masked + "@"
	})
	s = authHeader.ReplaceAllString(s, "${1}"+Masked)
	s = secretPair.ReplaceAllString(s, "${1}${2}"+Masked)
	s = knownPrefix.ReplaceAllString(s, Masked)
	s = maskHomePaths(s)
	return s
}

// homeOnce / homeDir は実際のホームディレクトリ（1 度だけ解決する）。
var (
	homeOnce sync.Once
	homeDir  string
)

// maskHomePaths はパス中の利用者のホームディレクトリを `~` へ置き換える。
//
// 目的は **OS ユーザー名（端末を特定できる情報で、ログに記録しないもの）を落とすこと**であり、
// パス全体を伏せることではない。案件フォルダ名などの残りの相対パスは障害調査に要るため残す。
// エラー文・パニックの文言には `open /Users/<OS ユーザー名>/Documents/<案件名>/x.yaml` の形で
// 混じるため、動作ログへ書く時点でここを通す。
func maskHomePaths(s string) string {
	if home := userHome(); home != "" {
		s = strings.ReplaceAll(s, home, "~")
	}
	// 実行中の利用者以外のホーム（共有端末・別アカウントのパス）も落とす。
	return homePath.ReplaceAllString(s, "${1}~")
}

// userHome は実行中の利用者のホームディレクトリを返す（取得できなければ空）。
func userHome() string {
	homeOnce.Do(func() {
		h, err := os.UserHomeDir()
		if err != nil {
			return
		}
		homeDir = strings.TrimRight(h, `/\`)
	})
	return homeDir
}

// StripURLCredentials は同期先の所在（URL）から資格情報部（`user:pass@`）を除去して返す
// （同期の記録・画面に出す所在に、同期先の資格情報を残さないため）。
// 資格情報でない短い利用者名（`ssh://git@host/...` の `git@`）は残す。URL でない所在
// （共有フォルダのパス・scp 形式）はそのまま返す。
func StripURLCredentials(location string) string {
	return urlUserinfo.ReplaceAllStringFunc(location, func(m string) string {
		sub := urlUserinfo.FindStringSubmatch(m)
		if !isCredentialUserinfo(sub[2]) {
			return m
		}
		return sub[1]
	})
}

// isCredentialUserinfo は userinfo が資格情報（パスワード付き、または長いトークン）かを返す。
func isCredentialUserinfo(userinfo string) bool {
	return strings.Contains(userinfo, ":") || len(userinfo) >= longUserinfo
}
