//go:build integration

// 結合テスト（実ファイル I/O）: AI 対話回答の対話ログの保存・復元・返送への同梱。
//
//   - 確定前の対話ログは暗号化して回答作業領域へ置き、**平文の発話はどこにも残らない**
//   - 中断して開き直すと復元される（入力済み回答の中断再開と同じ扱い）
//   - 確定後の返送ファイルに sessions/S-0001.md が入り、担当者側が取り込める（再採番して保存）
//   - 対話をしなければ対話ログのファイルも返送の sessions/ も作られない
//
// 実行: make -C app test-integration

package exchange_test

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/howashoji/ReqWeave/app/internal/exchange"
	"github.com/howashoji/ReqWeave/app/internal/projectstore"
)

const (
	respondUtteranceUser  = "棚卸しの頻度をどう答えるべきか迷っています。"
	respondUtteranceAgent = "現状の運用に合わせて「月次」を選び、実施日を自由記述に書くとよいでしょう。"
)

// talk は AI 対話回答の 1 往復を足す。
func talk(t *testing.T, sess *exchange.RespondSession) {
	t.Helper()
	if err := sess.AppendUtterance(projectstore.SpeakerUser, respondUtteranceUser); err != nil {
		t.Fatalf("発話を保存できない: %v", err)
	}
	if err := sess.AppendUtterance(projectstore.SpeakerAgent, respondUtteranceAgent); err != nil {
		t.Fatalf("発話を保存できない: %v", err)
	}
}

func TestDialogueLogIsStoredEncrypted(t *testing.T) {
	f := newRespondFixture(t)
	sess, err := f.workspace.Open(f.issueFile, f.passcode)
	if err != nil {
		t.Fatalf("開けない: %v", err)
	}
	talk(t, sess)

	root := filepath.Join(f.appBase, "respondent")
	var files []string
	err = filepath.Walk(root, func(p string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return err
		}
		files = append(files, filepath.Base(p))
		body, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		for _, needle := range []string{respondUtteranceUser, respondUtteranceAgent} {
			if bytes.Contains(body, []byte(needle)) {
				t.Fatalf("回答作業領域 %s に発話が平文で現れました: %q", p, needle)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	// 走査が空振りしていないこと（対話ログのファイルが実在する）。
	if !contains(files, "sessions.enc") {
		t.Fatalf("対話ログのファイルが作られていない: %v", files)
	}
}

func TestDialogueLogIsRestoredAfterReopen(t *testing.T) {
	f := newRespondFixture(t)
	sess, err := f.workspace.Open(f.issueFile, f.passcode)
	if err != nil {
		t.Fatalf("開けない: %v", err)
	}
	talk(t, sess)
	if len(sess.Utterances) != 2 {
		t.Fatalf("発話が保持されていない: %d 件", len(sess.Utterances))
	}

	reopened, err := f.workspace.Open(f.issueFile, f.passcode)
	if err != nil {
		t.Fatalf("開き直せない: %v", err)
	}
	if len(reopened.Utterances) != 2 {
		t.Fatalf("開き直しで対話が復元されていない: %d 件", len(reopened.Utterances))
	}
	if reopened.Utterances[0].Body != respondUtteranceUser ||
		reopened.Utterances[1].Body != respondUtteranceAgent {
		t.Errorf("復元した発話の中身が違う: %+v", reopened.Utterances)
	}
	if reopened.Utterances[0].Speaker != projectstore.SpeakerUser ||
		reopened.Utterances[1].Speaker != projectstore.SpeakerAgent {
		t.Errorf("復元した発話の話者が違う: %+v", reopened.Utterances)
	}
}

func TestFinalizeCarriesDialogueSessionIntoReturn(t *testing.T) {
	f := newRespondFixture(t)
	sess, err := f.workspace.Open(f.issueFile, f.passcode)
	if err != nil {
		t.Fatalf("開けない: %v", err)
	}
	talk(t, sess)
	answerAll(t, sess)

	dst := filepath.Join(t.TempDir(), "return.rwva")
	if err := sess.Finalize(dst); err != nil {
		t.Fatalf("確定に失敗: %v", err)
	}

	// 担当者側が取り込む経路で読む（返送の中身は暗号化されている）。
	review, err := exchange.ValidateReturn(f.store, dst)
	if err != nil {
		t.Fatalf("返送ファイルを検査できない: %v", err)
	}
	if len(review.Sessions) != 1 {
		t.Fatalf("返送にステークホルダーセッションが入っていない: %d 件（%v）", len(review.Sessions), keysOf(review.Sessions))
	}
	var body string
	for _, data := range review.Sessions {
		body = string(data)
	}
	for _, want := range []string{
		"type: stakeholder", respondUtteranceUser, respondUtteranceAgent,
		"utt-00001", "utt-00002",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("返送のセッションに %q が無い:\n%s", want, body)
		}
	}
	// 担当者側のプロジェクトへ取り込めること（再採番）。
	sessionRec, err := f.store.ImportStakeholderSession([]byte(body))
	if err != nil {
		t.Fatalf("ステークホルダーセッションを取り込めない: %v", err)
	}
	if sessionRec.Type != projectstore.SessionStakeholder {
		t.Errorf("取り込んだセッションの種別が違う: %q", sessionRec.Type)
	}
}

func TestFinalizeWithoutDialogueCarriesNoSession(t *testing.T) {
	f := newRespondFixture(t)
	sess, err := f.workspace.Open(f.issueFile, f.passcode)
	if err != nil {
		t.Fatalf("開けない: %v", err)
	}
	answerAll(t, sess)

	dst := filepath.Join(t.TempDir(), "return.rwva")
	if err := sess.Finalize(dst); err != nil {
		t.Fatalf("確定に失敗: %v", err)
	}
	review, err := exchange.ValidateReturn(f.store, dst)
	if err != nil {
		t.Fatalf("返送ファイルを検査できない: %v", err)
	}
	if len(review.Sessions) != 0 {
		t.Errorf("対話をしていないのにセッションが入っている: %d 件", len(review.Sessions))
	}
	// 走査が空振りしていないこと（回答は入っている）。
	if len(review.Answers.Answers) == 0 {
		t.Fatal("返送に回答が入っていない（テストが成立していない）")
	}
	if _, err := os.Stat(filepath.Join(f.appBase, "respondent")); err != nil {
		t.Fatalf("回答作業領域が無い: %v", err)
	}
}

// keysOf は失敗時の手掛かりとしてコンテナ内パスを並べる。
func keysOf(m map[string][]byte) []string {
	var out []string
	for k := range m {
		out = append(out, k)
	}
	return out
}

func contains(list []string, want string) bool {
	for _, v := range list {
		if v == want {
			return true
		}
	}
	return false
}
