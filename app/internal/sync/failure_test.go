package sync

import (
	"strings"
	"testing"
)

// git の出力から失敗種別を推定する（LC_ALL=C の英語出力）。
func TestClassifyGitError(t *testing.T) {
	cases := map[string]FailureKind{
		"! [rejected]        work/a -> work/a (fetch first)\nerror: failed to push some refs to '/share/proj.git'":                                               FailNonFastForward,
		"! [rejected]        work/a -> work/a (non-fast-forward)":                                                                                                FailNonFastForward,
		"fatal: Authentication failed for 'https://git.example.co.jp/team/proj.git/'":                                                                            FailAuth,
		"git@git.example.co.jp: Permission denied (publickey).\nfatal: Could not read from remote repository.":                                                   FailAuth,
		"fatal: could not read Username for 'https://github.com': terminal prompts disabled":                                                                     FailAuth,
		"Host key verification failed.\nfatal: Could not read from remote repository.":                                                                           FailAuth,
		"remote: Permission to org/repo.git denied to alice.\nfatal: unable to access 'https://github.com/org/repo.git/': The requested URL returned error: 403": FailForbidden,
		"remote: error: insufficient permission for adding an object to repository database ./objects":                                                           FailForbidden,
		"fatal: '/share/proj.git' does not appear to be a git repository\nfatal: Could not read from remote repository.":                                         FailNotFound,
		"remote: Repository not found.\nfatal: repository 'https://github.com/org/none.git/' not found":                                                          FailNotFound,
		"fatal: unable to access 'https://git.example.co.jp/x.git/': Could not resolve host: git.example.co.jp":                                                  FailUnreachable,
		"ssh: connect to host git.example.co.jp port 22: Connection refused\nfatal: Could not read from remote repository.":                                      FailUnreachable,
		"fatal: unable to access 'https://x/': Failed to connect to x port 443 after 10000 ms: Timeout was reached":                                              FailUnreachable,
		"fatal: something entirely different": FailInternal,
	}
	for stderr, want := range cases {
		got := classifyGitError(&gitError{args: []string{"push"}, exitCode: 128, stderr: stderr})
		if got != want {
			t.Errorf("分類が違う: got %s, want %s\n%s", got, want, stderr)
		}
	}
	if got := classifyGitError(errCredentialNotSetForTest); got != FailInternal {
		t.Errorf("git 以外のエラーの分類が違う: %s", got)
	}
}

// 同期系の文言は原因＋次の行動＋作業コピーが変わらない旨。git の語を含めない。
func TestFailureMessages(t *testing.T) {
	for _, kind := range []FailureKind{FailUnreachable, FailAuth, FailForbidden, FailNotFound, FailNonFastForward,
		FailIDRangeExhausted, FailCanceled, FailUnavailable, FailNotMember, FailAlreadyPresent, FailInternal} {
		for _, op := range []Operation{OpClone, OpIncorporate, OpPublish} {
			f := newFailure(kind, op, "detail")
			if f.Message == "" || !strings.HasSuffix(f.Message, "。") {
				t.Errorf("%s/%s: 文言が 1 文の様式でない: %q", kind, op, f.Message)
			}
			if !strings.Contains(f.Message, "変わっていません") && !strings.Contains(f.Message, "作られていません") {
				t.Errorf("%s/%s: 内容が変わらない旨が無い: %q", kind, op, f.Message)
			}
			for _, word := range []string{"commit", "branch", "push", "fetch", "merge", "rebase", "conflict marker"} {
				if strings.Contains(strings.ToLower(f.Message), word) {
					t.Errorf("%s/%s: git の語 %q を含む: %q", kind, op, word, f.Message)
				}
			}
			if f.Detail != "detail" || f.Kind != kind || f.Op != op {
				t.Errorf("%s/%s: フィールドが違う: %+v", kind, op, f)
			}
		}
	}
	if f := newFailure(FailUnreachable, OpPublish, ""); !strings.Contains(f.Message, "反映") {
		t.Errorf("操作名が文言に反映されない: %q", f.Message)
	}
	if f := newFailure(FailNonFastForward, OpPublish, ""); !strings.Contains(f.Message, "取り込") {
		t.Errorf("非 fast-forward で取り込みへの導線が無い: %q", f.Message)
	}
	if f := newFailure(FailAuth, OpIncorporate, ""); !strings.Contains(f.Message, "設定") {
		t.Errorf("認証失敗で設定への導線が無い: %q", f.Message)
	}
}

func TestFailureFromWrapsGitAndContextErrors(t *testing.T) {
	f := failureFrom(OpPublish, &gitError{args: []string{"push"}, exitCode: 1, stderr: "! [rejected] (fetch first)"})
	if f.Kind != FailNonFastForward || !strings.Contains(f.Detail, "rejected") {
		t.Errorf("git エラーの変換が違う: %+v", f)
	}
	if f := failureFrom(OpIncorporate, errGitUnavailable); f.Kind != FailUnavailable {
		t.Errorf("git 不在の変換が違う: %+v", f)
	}
	existing := newFailure(FailNotMember, OpClone, "")
	if f := failureFrom(OpClone, existing); f != existing {
		t.Error("既存の Failure が包み直された")
	}
}
