package sync

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/howashoji/ReqWeave/app/internal/masking"
	"github.com/howashoji/ReqWeave/app/internal/projectstore"
)

// git 実装（**外部 `git` コマンド**）。
//
// 選定理由: 認証情報の受け渡し手順（`GIT_SSH_COMMAND` / 資格情報ヘルパ）が
// 外部コマンドを前提にしている。同梱はせず、不在時は Availability で
// 「同期を利用できない理由と対処」を返し、他機能を止めない（git の無い端末でも同期以外は使える）。
//
// 利用者の git 設定に依存しないよう、システム設定を無効化し（GIT_CONFIG_NOSYSTEM）、
// グローバル設定を本システムが管理するファイルへ差し替える（GIT_CONFIG_GLOBAL）。
// 共有フォルダ上のリポジトリは所有者が別アカウントになり得るため `safe.directory = *` を置く。

// gitConfigFileName は本システムが管理する git のグローバル設定ファイル名（ConfigDir 配下）。
const gitConfigFileName = "gitconfig"

// managedGitConfig は GIT_CONFIG_GLOBAL として git へ渡す設定。利用者の ~/.gitconfig は読まない。
const managedGitConfig = `# ReqWeave が管理する git 設定（同期に使う）。手で編集しても次回起動時に上書きされる。
[safe]
	directory = *
[core]
	autocrlf = false
	quotepath = false
	longpaths = true
	fsmonitor = false
[gc]
	auto = 0
[credential]
	helper =
[advice]
	detachedHead = false
[init]
	defaultBranch = work
`

// gitRunner は外部 git コマンドの実行器。
type gitRunner struct {
	exe        string // 解決した git の実行ファイル。空 = 見つからない
	version    string // "2.50.1" 等
	lookupErr  error
	configFile string
	identity   []string // 全コマンドに付ける -c user.name / user.email
}

// Availability は同期機能の利用可否（git が無いときは理由と対処を提示する）。
type Availability struct {
	Available bool   `json:"available"`
	Version   string `json:"version,omitempty"`
	// Reason は利用できない理由と次の行動（利用者向けの日本語 1 文）。
	Reason string `json:"reason,omitempty"`
}

var versionPattern = regexp.MustCompile(`git version (\d+\.\d+(?:\.\d+)?)`)

// newGitRunner は git を探し、管理下の設定ファイルを用意する。
func newGitRunner(configDir string, author projectstore.Author) (*gitRunner, error) {
	if strings.TrimSpace(configDir) == "" {
		return nil, fmt.Errorf("同期モジュールの設定ファイルの置き場所がありません")
	}
	if err := os.MkdirAll(configDir, 0o700); err != nil {
		return nil, fmt.Errorf("同期モジュールの設定フォルダを作成できません: %w", err)
	}
	configFile := filepath.Join(configDir, gitConfigFileName)
	if err := projectstore.WriteFileAtomic(configFile, []byte(managedGitConfig)); err != nil {
		return nil, fmt.Errorf("同期モジュールの設定ファイルを書き込めません: %w", err)
	}
	r := &gitRunner{
		configFile: configFile,
		identity: []string{
			"-c", "user.name=" + author.DisplayName,
			"-c", "user.email=" + author.AuthorID,
		},
	}
	exe, err := exec.LookPath("git")
	if err != nil {
		r.lookupErr = err
		return r, nil
	}
	r.exe = exe
	out, err := exec.Command(exe, "--version").Output()
	if err != nil {
		r.exe = ""
		r.lookupErr = err
		return r, nil
	}
	if m := versionPattern.FindStringSubmatch(string(out)); m != nil {
		r.version = m[1]
	}
	return r, nil
}

// availability は git の有無を利用者向けの表現で返す。
func (r *gitRunner) availability() Availability {
	if r.exe == "" {
		return Availability{
			Available: false,
			Reason:    "同期に必要なプログラム（git）がこの端末にありません。git を導入してから、もう一度実行してください。同期以外の機能はそのまま使えます。",
		}
	}
	return Availability{Available: true, Version: r.version}
}

// gitError は git コマンドの失敗。stderr はマスキング済み。
type gitError struct {
	args     []string
	exitCode int
	stderr   string
}

func (e *gitError) Error() string {
	// 引数に認証情報は載せない設計だが、第二防衛として引数側もマスクして組み立てる
	return masking.Mask(fmt.Sprintf("git %s: exit %d: %s", strings.Join(e.args, " "), e.exitCode, strings.TrimSpace(e.stderr)))
}

// errGitUnavailable は git が無い環境での実行要求。
var errGitUnavailable = errors.New("git がありません")

// run は git を実行する。dir が空ならカレントディレクトリに依存しない引数のみを許す。
//
//   - h（認証の受け渡し資材）が nil なら認証なしで実行する。
//   - extraEnv は一時インデックス（GIT_INDEX_FILE）等の追加環境変数。認証情報の平文を入れない。
//   - 出力は LC_ALL=C で英語に固定し、失敗分類（failure.go）を安定させる。
func (r *gitRunner) run(ctx context.Context, dir string, h *Handoff, extraEnv []string, args ...string) (string, error) {
	return r.runInput(ctx, dir, h, extraEnv, nil, args...)
}

// runInput は標準入力へ stdin を流して git を実行する（オブジェクトの書き込み = hash-object 等）。
// stdin が nil なら標準入力を与えない。認証情報を stdin へ載せる用途には使わない（認証情報の
// 受け渡しは Handoff 側で完結する）。
func (r *gitRunner) runInput(ctx context.Context, dir string, h *Handoff, extraEnv []string, stdin []byte, args ...string) (string, error) {
	if r.exe == "" {
		return "", errGitUnavailable
	}
	full := append([]string{}, r.identity...)
	if h != nil {
		full = append(full, h.ConfigArgs()...)
	}
	full = append(full, args...)
	cmd := exec.CommandContext(ctx, r.exe, full...)
	cmd.Dir = dir
	if stdin != nil {
		cmd.Stdin = bytes.NewReader(stdin)
	}
	env := append(os.Environ(),
		"GIT_CONFIG_NOSYSTEM=1",
		"GIT_CONFIG_GLOBAL="+r.configFile,
		"GIT_TERMINAL_PROMPT=0",
		"GIT_OPTIONAL_LOCKS=0",
		"LC_ALL=C",
		"LANG=C",
	)
	if h != nil {
		env = append(env, h.Env()...)
	}
	env = append(env, extraEnv...)
	cmd.Env = env
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &limitedWriter{w: &stderr, limit: 64 * 1024}
	err := cmd.Run()
	if err != nil {
		if ctx.Err() != nil {
			return "", ctx.Err()
		}
		code := -1
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			code = exitErr.ExitCode()
		}
		return "", &gitError{args: args, exitCode: code, stderr: masking.Mask(stderr.String())}
	}
	return stdout.String(), nil
}

// limitedWriter は最大 limit バイトまで書き、以降は捨てる（暴走した出力でメモリを使い切らない）。
type limitedWriter struct {
	w     *bytes.Buffer
	limit int
}

func (l *limitedWriter) Write(p []byte) (int, error) {
	if remain := l.limit - l.w.Len(); remain > 0 {
		if len(p) > remain {
			l.w.Write(p[:remain])
		} else {
			l.w.Write(p)
		}
	}
	return len(p), nil
}
