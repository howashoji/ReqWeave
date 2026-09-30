//go:build integration

// 結合テスト（トークン上限設定のバインディング）。

package binding

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/howashoji/ReqWeave/app/internal/auditlog"
	"github.com/howashoji/ReqWeave/app/internal/projectstore"
)

func newUsageLimitAPI(t *testing.T) (*API, string) {
	t.Helper()
	a, root := newDialogueAPI(t, &streamingStub{})
	if _, err := a.OpenDialogueProject(root); err != nil {
		t.Fatalf("プロジェクトを開けない: %v", err)
	}
	t.Cleanup(func() { _ = a.CloseDialogueProject() })
	return a, root
}

// demoteToEditor は自分を編集権限へ落とす（オーナーは別のメンバーが持つ）。
func demoteToEditor(t *testing.T, root, authorID string) {
	t.Helper()
	path := filepath.Join(root, projectstore.FileMembers)
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	members, err := projectstore.UnmarshalMembers(data)
	if err != nil {
		t.Fatal(err)
	}
	for i := range members.Members {
		if members.Members[i].AuthorID == authorID {
			members.Members[i].Role = projectstore.RoleEditor
		}
	}
	members.Members = append(members.Members, projectstore.Member{
		AuthorID: "owner@example.co.jp", DisplayName: "オーナー",
		Role: projectstore.RoleOwner, AddedAt: time.Now().UTC(), AddedBy: authorID,
	})
	out, err := members.Marshal()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, out, 0o644); err != nil {
		t.Fatal(err)
	}
}

// オーナーが設定・変更・解除でき、変更履歴へ作業者名つきで記録される。
func TestUsageLimitBindingLifecycleAndHistory(t *testing.T) {
	a, root := newUsageLimitAPI(t)

	status, err := a.SetUsageLimit(UsageLimitRequest{TokensMax: 1000})
	if err != nil {
		t.Fatalf("上限を設定できない: %v", err)
	}
	if status.LimitTokens == nil || *status.LimitTokens != 1000 || status.WarnRatio != 0.8 {
		t.Errorf("設定後の状態が違う: %+v", status)
	}
	rec := changeOf(t, root, "project", auditlog.ChangeUsageLimitChanged)
	if rec == nil {
		t.Fatal("変更履歴に usage-limit-changed が無い")
	}
	if rec.Author != "k.sato@example.co.jp" {
		t.Errorf("作業者名が記録されていない: %+v", rec)
	}
	if rec.Before != "未設定" || !strings.Contains(rec.After, "上限 1000 トークン") {
		t.Errorf("変更前後の値が記録されていない: before=%q after=%q", rec.Before, rec.After)
	}

	// 変更（警告閾値も変える）。
	ratio := 0.5
	if _, err := a.SetUsageLimit(UsageLimitRequest{TokensMax: 2000, WarnRatio: &ratio}); err != nil {
		t.Fatalf("上限を変更できない: %v", err)
	}
	changes := usageLimitChanges(t, root)
	if len(changes) != 2 {
		t.Fatalf("変更履歴の件数が違う: %d", len(changes))
	}
	if !strings.Contains(changes[1].Before, "上限 1000") || !strings.Contains(changes[1].After, "上限 2000") {
		t.Errorf("変更前後の値が違う: %+v", changes[1])
	}
	if !strings.Contains(changes[1].After, "警告 50%") {
		t.Errorf("警告閾値の変更が記録されていない: %+v", changes[1])
	}

	// 同値の再設定では履歴が増えない。
	if _, err := a.SetUsageLimit(UsageLimitRequest{TokensMax: 2000, WarnRatio: &ratio}); err != nil {
		t.Fatalf("同値の設定でエラー: %v", err)
	}
	if got := usageLimitChanges(t, root); len(got) != 2 {
		t.Errorf("値が変わっていないのに履歴が増えた: %d 件", len(got))
	}

	// 解除。
	status, err = a.ClearUsageLimit()
	if err != nil {
		t.Fatalf("上限を解除できない: %v", err)
	}
	if status.LimitTokens != nil || status.Level != UsageLevelNone {
		t.Errorf("解除後の状態が違う: %+v", status)
	}
	changes = usageLimitChanges(t, root)
	if len(changes) != 3 || changes[2].After != "未設定" {
		t.Errorf("解除が「未設定へ」として記録されていない: %+v", changes)
	}

	// 未設定のまま解除しても履歴は増えない。
	if _, err := a.ClearUsageLimit(); err != nil {
		t.Fatalf("未設定での解除でエラー: %v", err)
	}
	if got := usageLimitChanges(t, root); len(got) != 3 {
		t.Errorf("未設定の解除で履歴が増えた: %d 件", len(got))
	}
}

func usageLimitChanges(t *testing.T, root string) []auditlog.ChangeRecord {
	t.Helper()
	all, err := auditlog.ReadChanges(root, time.Time{}, time.Time{})
	if err != nil {
		t.Fatalf("変更履歴を読めない: %v", err)
	}
	var out []auditlog.ChangeRecord
	for _, c := range all {
		if c.Change == auditlog.ChangeUsageLimitChanged {
			out = append(out, c)
		}
	}
	return out
}

// 編集・閲覧権限では変更できず、オーナーの操作である旨が返る。
func TestUsageLimitBindingDeniesNonOwner(t *testing.T) {
	a, root := newUsageLimitAPI(t)
	if _, err := a.SetUsageLimit(UsageLimitRequest{TokensMax: 1000}); err != nil {
		t.Fatalf("前提の設定に失敗: %v", err)
	}
	demoteToEditor(t, root, "k.sato@example.co.jp")

	_, err := a.SetUsageLimit(UsageLimitRequest{TokensMax: 5000})
	if err == nil {
		t.Fatal("編集権限で上限を変更できてしまった")
	}
	if !strings.Contains(err.Error(), "オーナー権限が必要です") {
		t.Errorf("オーナーの操作である旨が示されていない: %v", err)
	}
	if _, err := a.ClearUsageLimit(); err == nil {
		t.Fatal("編集権限で上限を解除できてしまった")
	}

	// 参照はできる（AI 利用量ダッシュボードの参照はすべての権限でできる）。
	status, err := a.UsageStatusNow()
	if err != nil {
		t.Fatalf("編集権限で利用量を参照できない: %v", err)
	}
	if status.LimitTokens == nil || *status.LimitTokens != 1000 {
		t.Errorf("参照した上限が違う: %+v", status)
	}

	// 画面が判定をやり直さずに済むよう、可否と理由が権限情報に載ること。
	perm, err := a.CurrentPermission()
	if err != nil {
		t.Fatalf("権限情報を取得できない: %v", err)
	}
	if perm.CanManageUsageLimit {
		t.Error("編集権限なのに上限設定が可能と返った")
	}
	if !strings.Contains(perm.UsageLimitReason, "オーナー権限が必要です") {
		t.Errorf("上限設定ができない理由が示されていない: %q", perm.UsageLimitReason)
	}
}

// オーナーには可否が true で返る（理由は空）。
func TestUsageLimitPermissionForOwner(t *testing.T) {
	a, _ := newUsageLimitAPI(t)
	perm, err := a.CurrentPermission()
	if err != nil {
		t.Fatalf("権限情報を取得できない: %v", err)
	}
	if !perm.CanManageUsageLimit || perm.UsageLimitReason != "" {
		t.Errorf("オーナーなのに上限設定が不可と返った: %+v", perm)
	}
}

// 現在の上限設定・累計消費・消費率を画面が取得できる（未設定と 0 を区別する）。
func TestUsageStatusExposesConsumptionRatio(t *testing.T) {
	a, root := newUsageLimitAPI(t)
	appendConsumption(t, root, "k.sato@example.co.jp", time.Now().UTC(), 250, "c-1")

	status, err := a.UsageStatusNow()
	if err != nil {
		t.Fatalf("利用量を取得できない: %v", err)
	}
	if status.ConsumedTokens != 250 {
		t.Errorf("累計消費が違う: %+v", status)
	}
	if status.LimitTokens != nil || status.ConsumptionRatio != nil {
		t.Errorf("上限未設定なのに上限・消費率が入っている: %+v", status)
	}

	if _, err := a.SetUsageLimit(UsageLimitRequest{TokensMax: 1000}); err != nil {
		t.Fatalf("上限を設定できない: %v", err)
	}
	status, err = a.UsageStatusNow()
	if err != nil {
		t.Fatal(err)
	}
	if status.ConsumptionRatio == nil || *status.ConsumptionRatio != 0.25 {
		t.Errorf("消費率が違う: %+v", status.ConsumptionRatio)
	}
	if status.RemainingTokens == nil || *status.RemainingTokens != 750 {
		t.Errorf("残りトークン数が違う: %+v", status.RemainingTokens)
	}
}
