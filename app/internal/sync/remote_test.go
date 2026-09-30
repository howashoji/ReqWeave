package sync

import (
	"testing"

	"github.com/howashoji/ReqWeave/app/internal/keymanager"
	"github.com/howashoji/ReqWeave/app/internal/projectstore"
)

// 所在の形式。認証情報を含む URL・暗号化されない接続は拒否する。
func TestRemoteValidate(t *testing.T) {
	ok := []Remote{
		{RemoteFolder, "/Volumes/share/reqweave/proj.git"},
		{RemoteFolder, `\\nas\share\reqweave\proj.git`},
		{RemoteGitInternal, "https://git.example.co.jp/team/proj.git"},
		{RemoteGitInternal, "ssh://git@git.example.co.jp/team/proj.git"},
		{RemoteGitExternal, "git@github.com:howashoji/proj.git"},
		{RemoteGitExternal, "https://github.com/howashoji/proj.git"},
	}
	for _, r := range ok {
		if err := r.Validate(); err != nil {
			t.Errorf("%+v が拒否された: %v", r, err)
		}
	}
	bad := []Remote{
		{RemoteFolder, ""},
		{RemoteFolder, "https://git.example.co.jp/x.git"},
		{RemoteGitInternal, "http://git.example.co.jp/x.git"},
		{RemoteGitInternal, "https://alice:token@git.example.co.jp/x.git"},
		{RemoteGitInternal, "ftp://git.example.co.jp/x.git"},
		{RemoteGitInternal, "just-a-name"},
		{"svn", "https://x/y"},
	}
	for _, r := range bad {
		if err := r.Validate(); err == nil {
			t.Errorf("%+v が受理された", r)
		}
	}
}

func TestRemoteCredentialKindAndDisplay(t *testing.T) {
	if k := (Remote{RemoteGitInternal, "https://git.example.co.jp/x.git"}).CredentialKind(); k != keymanager.CredentialToken {
		t.Errorf("https の認証方式が違う: %s", k)
	}
	if k := (Remote{RemoteGitExternal, "git@github.com:x/y.git"}).CredentialKind(); k != keymanager.CredentialSSHKey {
		t.Errorf("scp 形式の認証方式が違う: %s", k)
	}
	if (Remote{RemoteFolder, "/share/x.git"}).RequiresCredential() {
		t.Error("共有フォルダで認証情報を要求した")
	}
	if got := (Remote{RemoteGitInternal, "https://alice:pw@host/x.git"}).Display(); got != "https://host/x.git" {
		t.Errorf("表示用の所在から資格情報部が除去されていない: %q", got)
	}
	p := &projectstore.Project{Sync: &projectstore.SyncSetting{Kind: projectstore.SyncKindFolder, Location: "/share/x.git"}}
	if r, ok := RemoteFromProject(p); !ok || r.Kind != RemoteFolder || r.Location != "/share/x.git" {
		t.Errorf("project.yaml からの変換が違う: %+v %v", r, ok)
	}
	if _, ok := RemoteFromProject(&projectstore.Project{}); ok {
		t.Error("同期先未設定なのに同期先が返った")
	}
}

func TestBranchNameRoundTrip(t *testing.T) {
	for _, id := range []string{"k.sato@example.co.jp", "user-1@x.y", "abc"} {
		b := BranchName(id)
		if got, ok := AuthorIDFromBranch(b); !ok || got != id {
			t.Errorf("ブランチ名の往復が違う: %q → %q → %q", id, b, got)
		}
	}
	if _, ok := AuthorIDFromBranch("main"); ok {
		t.Error("work/ 以外のブランチが作業者として解釈された")
	}
}

func TestSummaryCategoriesAndDescribe(t *testing.T) {
	s := Summary{}
	s.add("requirements/FR-1.md", changeAdded)
	s.add("requirements/FR-2.md", changeModified)
	s.add("decisions/DEC-1.md", changeAdded)
	s.add("terms.yaml", changeModified)
	s.add("locks/x.lock", changeAdded)
	s.add("README", changeRemoved)
	if s.Total() != 6 {
		t.Errorf("合計が違う: %d", s.Total())
	}
	if got := s.Describe(); got != "用語 1 / 要件項目 2 / 決定事項 1 / その他 2" {
		t.Errorf("要約の表記が違う: %q", got)
	}
	if got := (Summary{}).Describe(); got != "変更なし" {
		t.Errorf("空の要約が違う: %q", got)
	}
	for cat := range categoryLabels {
		if CategoryLabel(cat) == "" {
			t.Errorf("区分 %q のラベルが空", cat)
		}
	}
	if CategoryLabel("unknown") != "その他" {
		t.Error("未知の区分が「その他」に倒れない")
	}
}

// 同期の記録は「未反映の変更の有無」の指標から外す（同期のたびに常時点灯させない）。
// 変更履歴・AI 送信記録など他の監査記録は指標に残す（利用者の作業だから）。
func TestIsSyncBookkeeping(t *testing.T) {
	bookkeeping := []string{
		"audit/sync-log/2026-09.k.sato%40example.co.jp.ndjson",
		"./audit/sync-log/2026-09.x.ndjson",
		`audit\sync-log\2026-09.x.ndjson`,
	}
	for _, p := range bookkeeping {
		if !IsSyncBookkeeping(p) {
			t.Errorf("同期の記録が指標から外れていない: %q", p)
		}
	}
	kept := []string{
		"audit/history/2026-09.x.ndjson",
		"audit/ai-log/2026-09.x.ndjson",
		"decisions/DEC-001.md",
		"terms.yaml",
		"audit-sync-log/x.ndjson",
	}
	for _, p := range kept {
		if IsSyncBookkeeping(p) {
			t.Errorf("利用者の変更が指標から外れた: %q", p)
		}
	}
}
