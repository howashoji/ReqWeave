package projectstore

import (
	"fmt"
	"strings"
)

// NormalizeAuthorID は利用者 ID（組織メール / UPN）を正規化する。
// 前後空白を除去し小文字化した文字列を返す。プロジェクトデータ・アプリ設定に書き込む
// `author_id` は必ず本関数を通した値とする（端末に依存しない識別子にするため）。
func NormalizeAuthorID(s string) (string, error) {
	v := strings.ToLower(strings.TrimSpace(s))
	if v == "" {
		return "", fmt.Errorf("メールアドレス（利用者 ID）が入力されていません")
	}
	local, domain, ok := strings.Cut(v, "@")
	if !ok {
		return "", fmt.Errorf("メールアドレス（利用者 ID）の形式が正しくありません。会社で使っているメールアドレスを入力してください: %q", s)
	}
	if local == "" || domain == "" {
		return "", fmt.Errorf("メールアドレスの @ の前後が空です: %q", s)
	}
	if strings.Contains(domain, "@") {
		return "", fmt.Errorf("メールアドレスに @ が複数あります: %q", s)
	}
	if !strings.Contains(domain, ".") {
		return "", fmt.Errorf("メールアドレスの @ より後ろにドットがありません: %q", s)
	}
	if strings.ContainsAny(v, " \t\r\n") {
		return "", fmt.Errorf("メールアドレスに空白を含めることはできません: %q", s)
	}
	return v, nil
}
