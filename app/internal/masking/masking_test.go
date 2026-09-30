package masking

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// 既知接頭辞・URL 資格情報部・トークン接頭辞・SSH 鍵ブロックを伏せる。
func TestMaskReplacesKnownSecrets(t *testing.T) {
	cases := []struct {
		name   string
		in     string
		secret string // 出力に残ってはいけない部分
		keep   string // 出力に残るべき部分（空なら検査しない）
	}{
		{"Anthropic キー", "key sk-ant-api03-abcdefghijklmnop failed", "sk-ant-api03-abcdefghijklmnop", "failed"},
		{"OpenAI キー", "sk-proj-abcdefghijklmnop", "sk-proj-abcdefghijklmnop", ""},
		{"Google キー", "AIzaSyA-abcdefghijklmnopqrstuvwxyz012345", "AIzaSyA-abcdefghijklmnopqrstuvwxyz012345", ""},
		{"GitHub PAT", "ghp_abcdefghijklmnopqrstuvwxyz0123456789", "ghp_abcdefghijklmnopqrstuvwxyz0123456789", ""},
		{"GitHub fine-grained PAT", "github_pat_11ABCDEFG0abcdefghijklmnopqrstuvwxyz", "github_pat_11ABCDEFG0abcdefghijklmnopqrstuvwxyz", ""},
		{"GitLab PAT", "glpat-abcdefghijklmnopqrst", "glpat-abcdefghijklmnopqrst", ""},
		{"Atlassian トークン", "ATATT3xFfGF0abcdefghijklmnopqrstuvwxyz", "ATATT3xFfGF0abcdefghijklmnopqrstuvwxyz", ""},
		{"URL の user:pass", "fetch https://alice:s3cretPass@git.example.co.jp/team/proj.git failed", "s3cretPass", "https://" + Masked + "@git.example.co.jp/team/proj.git"},
		{"URL の利用者名部に置いたトークン", "https://abcdefghijklmnopqrstuvwxyz@github.com/x/y.git", "abcdefghijklmnopqrstuvwxyz", "https://" + Masked + "@github.com/x/y.git"},
		{"Authorization ヘッダ", "Authorization: Basic YWxpY2U6czNjcmV0", "YWxpY2U6czNjcmV0", "Authorization: Basic " + Masked},
		{"資格情報プロトコル", "protocol=https\nhost=github.com\npassword=hunter2xyz\n", "hunter2xyz", "host=github.com"},
		{"SSH 鍵ブロック", "before\n-----BEGIN OPENSSH PRIVATE KEY-----\nb3BlbnNzaC1rZXktdjEAAAAA\n-----END OPENSSH PRIVATE KEY-----\nafter", "b3BlbnNzaC1rZXktdjEAAAAA", "before\n" + Masked + "\nafter"},
		{"終端の無い SSH 鍵ブロック", "x -----BEGIN RSA PRIVATE KEY-----\nMIIEow", "MIIEow", "x " + Masked},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := Mask(c.in)
			if strings.Contains(got, c.secret) {
				t.Errorf("秘密情報が残っている:\n%s", got)
			}
			if !strings.Contains(got, Masked) {
				t.Errorf("マスク表記が無い: %q", got)
			}
			if c.keep != "" && !strings.Contains(got, c.keep) {
				t.Errorf("残すべき部分が壊れた: got %q, want to contain %q", got, c.keep)
			}
		})
	}
}

// 秘密情報を含まない文字列は変えない（誤検知で記録を壊さない）。
func TestMaskLeavesOrdinaryTextIntact(t *testing.T) {
	cases := []string{
		"",
		"同期先に接続できません。接続が回復してから、もう一度取り込んでください。",
		"ssh://git@git.example.co.jp/team/proj.git",
		"git@github.com:howashoji/proj.git",
		"https://github.com/howashoji/proj.git",
		"/Volumes/share/reqweave/proj.git",
		`\\nas\share\reqweave\proj.git`,
		"tokens_in=120 tokens_out=30",
		"sk-1",             // 接頭辞だけの短い文字列は伏せない
		"desk-top-machine", // 語中の sk- は伏せない
	}
	for _, in := range cases {
		if got := Mask(in); got != in {
			t.Errorf("秘密情報の無い文字列が書き換えられた: %q → %q", in, got)
		}
	}
}

// 置換は冪等（二重に通しても壊れない）。
func TestMaskIsIdempotent(t *testing.T) {
	in := "https://alice:pw@host/x ghp_abcdefghijklmnopqrstuvwxyz0123456789"
	once := Mask(in)
	if twice := Mask(once); twice != once {
		t.Errorf("二重適用で結果が変わった: %q → %q", once, twice)
	}
}

// 同期の記録・画面に出す所在から資格情報部を除去する。
func TestStripURLCredentials(t *testing.T) {
	cases := map[string]string{
		"https://alice:s3cret@git.example.co.jp/team/proj.git":  "https://git.example.co.jp/team/proj.git",
		"https://abcdefghijklmnopqrstuvwxyz@github.com/x/y.git": "https://github.com/x/y.git",
		"https://github.com/x/y.git":                            "https://github.com/x/y.git",
		"ssh://git@git.example.co.jp/team/proj.git":             "ssh://git@git.example.co.jp/team/proj.git",
		"git@github.com:howashoji/proj.git":                     "git@github.com:howashoji/proj.git",
		"/Volumes/share/reqweave/proj.git":                      "/Volumes/share/reqweave/proj.git",
		"":                                                      "",
	}
	for in, want := range cases {
		if got := StripURLCredentials(in); got != want {
			t.Errorf("StripURLCredentials(%q) = %q, want %q", in, got, want)
		}
	}
}

// パス中のホームディレクトリを `~` へ落とすこと。
// 目的は OS ユーザー名（端末を特定できる情報）を残さないこと。
func TestMaskHomePaths(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{
			name: "macOS のパス（案件名は残す）",
			in:   "open /Users/tanaka/Documents/案件A/project.yaml: permission denied",
			want: "open ~/Documents/案件A/project.yaml: permission denied",
		},
		{
			name: "Windows のパス",
			in:   `open C:\Users\tanaka\Documents\案件A\project.yaml: アクセスが拒否されました`,
			want: `open ~\Documents\案件A\project.yaml: アクセスが拒否されました`,
		},
		{
			name: "Linux のパス",
			in:   "read /home/tanaka/.config/ReqWeave/settings.json",
			want: "read ~/.config/ReqWeave/settings.json",
		},
		{
			name: "行頭のパス",
			in:   "/Users/tanaka/x",
			want: "~/x",
		},
		{
			name: "URL のパスは巻き込まない",
			in:   "https://example.com/Users/tanaka/repo.git へ到達できません",
			want: "https://example.com/Users/tanaka/repo.git へ到達できません",
		},
		{
			name: "ホーム配下でないパスはそのまま",
			in:   "/Volumes/share/reqweave/repo.git",
			want: "/Volumes/share/reqweave/repo.git",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := Mask(tt.in)
			if got != tt.want {
				t.Errorf("Mask(%q) = %q（期待 %q）", tt.in, got, tt.want)
			}
			// 冪等であること（出力層で二重に通っても壊れない）。
			if again := Mask(got); again != got {
				t.Errorf("冪等でない: %q → %q", got, again)
			}
		})
	}
}

// 実行中の利用者のホームディレクトリも落ちること（既定の場所でない場合を含む）。
func TestMaskActualHomeDir(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatalf("ホームディレクトリを取得できない: %v", err)
	}
	in := "cannot write " + filepath.Join(home, "reqweave", "app.log")
	got := Mask(in)
	if strings.Contains(got, home) {
		t.Errorf("ホームディレクトリが残っている: %q", got)
	}
	if !strings.Contains(got, "~") {
		t.Errorf("`~` へ置き換わっていない: %q", got)
	}
}
