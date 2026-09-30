//go:build integration

// 結合テスト（実ファイル I/O）。モックを使わず実際のフォルダ・ファイルを対象にする。
// 実行: make -C app test-integration

package projectstore

import (
	"encoding/json"
	"fmt"
	"os"
	"os/user"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
	"unicode/utf8"
)

func testAuthor() Author {
	return Author{AuthorID: "k.sato@example.co.jp", DisplayName: "佐藤"}
}

func createTestProject(t *testing.T) *Store {
	t.Helper()
	root := filepath.Join(t.TempDir(), "在庫管理システム")
	s, err := CreateProject(root, CreateOptions{
		TargetSystemName: "在庫管理システム",
		DomainPresets:    []string{"inventory"},
		Author:           testAuthor(),
	})
	if err != nil {
		t.Fatalf("プロジェクトを作成できません: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

// 単一フォルダ構成のディレクトリ・ファイルが揃うこと。
func TestCreateProjectBuildsLayout(t *testing.T) {
	s := createTestProject(t)

	for _, d := range projectDirs {
		p := filepath.Join(s.Root(), filepath.FromSlash(d))
		st, err := os.Stat(p)
		if err != nil {
			t.Errorf("ディレクトリがありません: %s: %v", d, err)
			continue
		}
		if !st.IsDir() {
			t.Errorf("ディレクトリではありません: %s", d)
		}
	}
	for _, f := range []string{FileProject, FileMembers, FileRoster, FileTerms, FilePerspectives, FileReservations} {
		if _, err := os.Stat(filepath.Join(s.Root(), f)); err != nil {
			t.Errorf("ファイルがありません: %s: %v", f, err)
		}
	}
	if !IsProjectFolder(s.Root()) {
		t.Error("プロジェクトフォルダと判定されません")
	}

	// 作成直後は現在フェーズが「要件定義」
	p := s.Project()
	if p.Phase != PhaseRequirements {
		t.Errorf("作成直後の phase が requirements ではない: %q", p.Phase)
	}
	if p.FormatVersion != CurrentFormatVersion {
		t.Errorf("format_version: %q", p.FormatVersion)
	}
	if p.TargetSystemName != "在庫管理システム" {
		t.Errorf("target_system_name: %q", p.TargetSystemName)
	}
	if len(p.DomainPresets) != 1 || p.DomainPresets[0] != "inventory" {
		t.Errorf("domain_presets: %v", p.DomainPresets)
	}
	if p.CreatedAt.Location() != time.UTC {
		t.Errorf("created_at が UTC ではない: %v", p.CreatedAt)
	}

	// 作成者はオーナーとして初期登録される
	m, err := s.LoadMembers()
	if err != nil {
		t.Fatalf("メンバー一覧を読めません: %v", err)
	}
	owner, ok := m.Find("k.sato@example.co.jp")
	if !ok {
		t.Fatalf("作成者がメンバーに登録されていません: %+v", m)
	}
	if owner.Role != RoleOwner {
		t.Errorf("作成者の権限が owner ではない: %q", owner.Role)
	}
}

func TestCreateProjectRejectsNonEmptyDir(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "既存.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if s, err := CreateProject(root, CreateOptions{TargetSystemName: "X", Author: testAuthor()}); err == nil {
		_ = s.Close()
		t.Error("空でないフォルダに作成できてしまった（既存データの破壊防止）")
	}
}

func TestCreateProjectValidatesInput(t *testing.T) {
	cases := map[string]CreateOptions{
		"対象システム名が空":  {TargetSystemName: "", Author: testAuthor()},
		"利用者 ID が不正": {TargetSystemName: "X", Author: Author{AuthorID: "ksato", DisplayName: "佐藤"}},
		"表示名が空":      {TargetSystemName: "X", Author: Author{AuthorID: "k.sato@example.co.jp"}},
	}
	for name, opts := range cases {
		t.Run(name, func(t *testing.T) {
			root := filepath.Join(t.TempDir(), "p")
			s, err := CreateProject(root, opts)
			if err == nil {
				_ = s.Close()
				t.Fatal("不正な入力で作成できてしまった")
			}
			if _, statErr := os.Stat(root); statErr == nil {
				t.Error("失敗したのにフォルダが作られている")
			}
		})
	}
}

// UTF-8（BOM なし）・改行 LF。
func TestWrittenFilesAreUTF8LFWithoutBOM(t *testing.T) {
	s := createTestProject(t)
	for _, f := range []string{FileProject, FileMembers, FileRoster, FileTerms, FilePerspectives, FileReservations} {
		b, err := os.ReadFile(filepath.Join(s.Root(), f))
		if err != nil {
			t.Fatalf("%s: %v", f, err)
		}
		if !utf8.Valid(b) {
			t.Errorf("%s: UTF-8 として不正", f)
		}
		if strings.HasPrefix(string(b), "\uFEFF") {
			t.Errorf("%s: BOM が付いている", f)
		}
		if strings.Contains(string(b), "\r\n") {
			t.Errorf("%s: CRLF が含まれる", f)
		}
	}
}

// プロジェクトデータに絶対パス・OS ユーザー名等の端末固有情報を書き込まない。
func TestProjectDataHasNoTerminalSpecificInfo(t *testing.T) {
	s := createTestProject(t)
	u, err := user.Current()
	if err != nil {
		t.Fatalf("OS ユーザーを取得できません: %v", err)
	}
	forbidden := []string{s.Root(), u.Username}
	for _, f := range []string{FileProject, FileMembers} {
		b, err := os.ReadFile(filepath.Join(s.Root(), f))
		if err != nil {
			t.Fatalf("%s: %v", f, err)
		}
		for _, bad := range forbidden {
			if bad == "" {
				continue
			}
			if strings.Contains(string(b), bad) {
				t.Errorf("%s に端末固有情報 %q が含まれる:\n%s", f, bad, b)
			}
		}
	}
}

// 一時ファイル → rename。書き込み後に一時ファイルを残さない。
func TestWriteFileAtomicReplacesAndLeavesNoTemp(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "data.yaml")
	if err := WriteFileAtomic(path, []byte("first\n")); err != nil {
		t.Fatalf("1 回目の書き込みに失敗: %v", err)
	}
	if err := WriteFileAtomic(path, []byte("second\n")); err != nil {
		t.Fatalf("2 回目の書き込みに失敗: %v", err)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "second\n" {
		t.Errorf("内容が置き換わっていない: %q", got)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		names := make([]string, 0, len(entries))
		for _, e := range entries {
			names = append(names, e.Name())
		}
		t.Errorf("一時ファイルが残っている: %v", names)
	}
}

// 差し替えは rename で行う（対象ファイルへの直接上書きではない）。
// 直接上書き実装（os.WriteFile 等）ではファイル同一性が変わらないため、ここで検出できる。
func TestWriteFileAtomicSwapsFileByRename(t *testing.T) {
	path := filepath.Join(t.TempDir(), "data.yaml")
	if err := WriteFileAtomic(path, []byte("first\n")); err != nil {
		t.Fatal(err)
	}
	before, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := WriteFileAtomic(path, []byte("second\n")); err != nil {
		t.Fatal(err)
	}
	after, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if os.SameFile(before, after) {
		t.Error("対象ファイルを直接上書きしている（一時ファイル → rename になっていない）")
	}
}

// 書き込みに失敗しても、対象ファイルの旧内容が残ること。
func TestWriteFileAtomicKeepsOldContentOnFailure(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "data.yaml")
	if err := WriteFileAtomic(path, []byte("original\n")); err != nil {
		t.Fatal(err)
	}
	// rename 先をディレクトリにして差し替えを失敗させる
	blocked := filepath.Join(dir, "blocked.yaml")
	if err := os.Mkdir(blocked, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := WriteFileAtomic(blocked, []byte("new\n")); err == nil {
		t.Fatal("ディレクトリを対象にした書き込みが成功してしまった")
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "original\n" {
		t.Errorf("失敗した書き込みが別ファイルへ影響した: %q", got)
	}
	// 失敗時も一時ファイルを残さない
	for _, e := range mustReadDir(t, dir) {
		if strings.HasPrefix(e, ".") {
			t.Errorf("失敗時に一時ファイルが残っている: %s", e)
		}
	}
}

func mustReadDir(t *testing.T, dir string) []string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		names = append(names, e.Name())
	}
	return names
}

// プロジェクトへの全書き込みを単一の保存キューで直列化する。
func TestWritesAreSerialized(t *testing.T) {
	s := createTestProject(t)

	const n = 20
	var inFlight atomic.Int32
	var overlapped atomic.Bool
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			err := s.UpdateProject(func(p *Project) error {
				if inFlight.Add(1) > 1 {
					overlapped.Store(true)
				}
				defer inFlight.Add(-1)
				time.Sleep(time.Millisecond)
				p.DomainPresets = append(p.DomainPresets, "x")
				return nil
			})
			if err != nil {
				t.Errorf("更新に失敗: %v", err)
			}
		}()
	}
	wg.Wait()

	if overlapped.Load() {
		t.Error("書き込みが並行実行された（直列化されていない）")
	}
	// 逐次に読み直して追記されるため、全件が反映される（後勝ちの取りこぼしがない）
	p := s.Project()
	if len(p.DomainPresets) != 1+n {
		t.Errorf("更新が取りこぼされた: %d 件（期待 %d 件）", len(p.DomainPresets), 1+n)
	}
	reopened, err := Open(s.Root(), testAuthor())
	if err != nil {
		t.Fatalf("再オープンに失敗: %v", err)
	}
	defer reopened.Close()
	if len(reopened.Project().DomainPresets) != 1+n {
		t.Errorf("ディスク上の内容が違う: %d 件", len(reopened.Project().DomainPresets))
	}
}

// 変更が保存され、開き直して復元されること。
func TestUpdateProjectPersistsAcrossReopen(t *testing.T) {
	s := createTestProject(t)
	if err := s.UpdateProject(func(p *Project) error {
		p.Phase = PhaseBasicDesign
		return nil
	}); err != nil {
		t.Fatalf("更新に失敗: %v", err)
	}
	if err := s.Close(); err != nil {
		t.Fatalf("クローズに失敗: %v", err)
	}
	reopened, err := Open(s.Root(), testAuthor())
	if err != nil {
		t.Fatalf("再オープンに失敗: %v", err)
	}
	defer reopened.Close()
	if got := reopened.Project().Phase; got != PhaseBasicDesign {
		t.Errorf("フェーズが復元されない: %q", got)
	}
}

// 自版より新しいメジャー形式は開かず、いかなるファイルにも書き込まない。
func TestOpenRejectsTooNewMajor(t *testing.T) {
	s := createTestProject(t)
	root := s.Root()
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, FileProject)
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	bumped := strings.Replace(string(before), `format_version: "`+CurrentFormatVersion+`"`, `format_version: "2.0"`, 1)
	if bumped == string(before) {
		t.Fatalf("形式バージョンを差し替えられませんでした:\n%s", before)
	}
	if err := os.WriteFile(path, []byte(bumped), 0o644); err != nil {
		t.Fatal(err)
	}
	stBefore, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}

	reopened, err := Open(root, testAuthor())
	if err == nil {
		_ = reopened.Close()
		t.Fatal("新しいメジャー形式のプロジェクトが開けてしまった")
	}
	var tooNew *ErrTooNew
	if !asErrTooNew(err, &tooNew) {
		t.Fatalf("ErrTooNew ではない: %T %v", err, err)
	}
	if tooNew.Found.Major != 2 {
		t.Errorf("検出した形式バージョンが違う: %v", tooNew.Found)
	}
	stAfter, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if !stAfter.ModTime().Equal(stBefore.ModTime()) {
		t.Error("開けなかったのに project.yaml が更新された")
	}
}

// 自版より古い形式は、移行（退避 → 移行 → migrated_from）なしに書き換えない。
func TestOpenRejectsOlderFormatUntilMigration(t *testing.T) {
	s := createTestProject(t)
	root := s.Root()
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, FileProject)
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	older := strings.Replace(string(before), `format_version: "`+CurrentFormatVersion+`"`, `format_version: "0.9"`, 1)
	if older == string(before) {
		t.Fatalf("形式バージョンを差し替えられませんでした:\n%s", before)
	}
	if err := os.WriteFile(path, []byte(older), 0o644); err != nil {
		t.Fatal(err)
	}

	reopened, err := Open(root, testAuthor())
	if err == nil {
		_ = reopened.Close()
		t.Fatal("古い形式のプロジェクトが移行なしで開けてしまった")
	}
	var needs *ErrNeedsMigration
	if !asErrNeedsMigration(err, &needs) {
		t.Fatalf("ErrNeedsMigration ではない: %T %v", err, err)
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != older {
		t.Error("開けなかったのに project.yaml が書き換えられた")
	}
}

func TestWriteAfterCloseFails(t *testing.T) {
	s := createTestProject(t)
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	if err := s.UpdateProject(func(p *Project) error { return nil }); err == nil {
		t.Error("閉じた後の書き込みが成功してしまった")
	}
}

// 保存キューは**原子的置換と追記の両方**を 1 本で直列化する。
//
// 既存の TestWritesAreSerialized は `UpdateProject`（一時ファイル + rename）だけを見ており、
// `AppendFile`（O_APPEND 追記。監査データが使う経路）が現れない。
// **片方の経路だけがキューを迂回する実装変更**（性能のために追記を直接 I/O にする等）を
// 検知できないため、両者を混在させた並行実行を固定する。
//
// 重なりの検知方法: 置換側のクロージャがキュー内で動いている**その最中に**追記先ファイルの
// サイズを 2 回測り、変化していたら「追記が割り込んだ」とみなす。
// 追記がキューを通っていれば置換の実行中に走ることはあり得ないため、変化は 0 でなければならない。
//
// **追記は試験の全区間にわたって発生させる**。追記 goroutine が 1 回ずつ追記して終わる作りだと、
// 追記が置換より先に全部終わってしまい、割り込みが起きうる窓が短くなる。
// 追記を小刻みに繰り返して、置換がキューを流れている間ずっと追記が走る状態を作ることで、
// 検知の余裕を確保する（故障注入で 20 回の置換のうち 7 回が割り込みを観測する程度の余裕がある）。
func TestAppendAndAtomicReplaceShareTheSameQueue(t *testing.T) {
	s := createTestProject(t)

	const (
		rel = "audit/history/serialization-probe.ndjson"
		// 置換の件数。1 件あたり replaceHold だけキューを占有する。
		replaces = 20
		// 置換 1 件がキューを握る時間。この間に追記が割り込んだかを見る。
		replaceHold = 2 * time.Millisecond
		// 追記の並列数と 1 並列あたりの回数。置換の総所要（replaces × replaceHold）を
		// またぐように小刻みに追記する。
		appendWorkers = 4
		appendsEach   = 15
		appendGap     = time.Millisecond
	)
	totalAppends := appendWorkers * appendsEach
	probe := filepath.Join(s.Root(), filepath.FromSlash(rel))

	// 追記先を作っておく（置換側が最初からサイズを測れるように）。
	if err := s.AppendFile(rel, []byte("{\"worker\":-1,\"seq\":-1}\n")); err != nil {
		t.Fatalf("追記の下準備に失敗: %v", err)
	}

	sizeOf := func() int64 {
		fi, err := os.Stat(probe)
		if err != nil {
			return -1
		}
		return fi.Size()
	}

	var inFlight atomic.Int32
	var overlapped atomic.Bool    // 置換どうしの重なり
	var appendRaced atomic.Bool   // 置換の実行中に追記が割り込んだ
	var racedWindows atomic.Int32 // 割り込みを観測した置換の回数（診断用）
	var replaceRuns atomic.Int32
	var wg sync.WaitGroup

	for i := 0; i < replaces; i++ {
		wg.Add(1)
		go func() { // 原子的置換の側
			defer wg.Done()
			err := s.UpdateProject(func(p *Project) error {
				if inFlight.Add(1) > 1 {
					overlapped.Store(true)
				}
				defer inFlight.Add(-1)
				replaceRuns.Add(1)
				before := sizeOf()
				time.Sleep(replaceHold)
				if after := sizeOf(); after != before {
					appendRaced.Store(true)
					racedWindows.Add(1)
				}
				p.DomainPresets = append(p.DomainPresets, "x")
				return nil
			})
			if err != nil {
				t.Errorf("更新に失敗: %v", err)
			}
		}()
	}
	for w := 0; w < appendWorkers; w++ {
		wg.Add(1)
		go func(worker int) { // 追記の側（全区間にわたって小刻みに）
			defer wg.Done()
			for k := 0; k < appendsEach; k++ {
				line := []byte(fmt.Sprintf("{\"worker\":%d,\"seq\":%d}\n", worker, k))
				if err := s.AppendFile(rel, line); err != nil {
					t.Errorf("追記に失敗: %v", err)
					return
				}
				time.Sleep(appendGap)
			}
		}(w)
	}
	wg.Wait()

	// 検知の前提が崩れていないこと（置換が実際にキュー内で走ったか）。
	if got := replaceRuns.Load(); int(got) != replaces {
		t.Fatalf("置換のクロージャが %d 回しか走っていない（期待 %d 回）", got, replaces)
	}
	if overlapped.Load() {
		t.Error("置換どうしが並行実行された（直列化されていない）")
	}
	if appendRaced.Load() {
		t.Errorf("原子的置換の実行中に追記が割り込んだ（追記が保存キューを迂回している）: %d/%d 回の置換で観測",
			racedWindows.Load(), replaces)
	}

	// 置換側: 全件が反映される（後勝ちの取りこぼしが無い）。
	if got := len(s.Project().DomainPresets); got != 1+replaces {
		t.Errorf("更新が取りこぼされた: %d 件（期待 %d 件）", got, 1+replaces)
	}

	// 追記側: 1 行も欠けず、行が途中で混ざっていない。
	//
	// 並行実行なので**行の並び順は不定**（どの goroutine が先にキューへ入るかは決まらない）。
	// 固定するのは「全行が揃っていること」と「各行が 1 行として自己完結していること」。
	// 行が交錯して壊れると JSON として読めなくなるため、後者はその検査で見る。
	raw, err := os.ReadFile(probe)
	if err != nil {
		t.Fatalf("追記先を読めません: %v", err)
	}
	lines := strings.Split(strings.TrimSuffix(string(raw), "\n"), "\n")
	if len(lines) != 1+totalAppends {
		t.Fatalf("追記の行数が違う: %d 行（期待 %d 行）", len(lines), 1+totalAppends)
	}
	type key struct{ worker, seq int }
	seen := map[key]bool{}
	for i, line := range lines {
		var rec struct {
			Worker *int `json:"worker"`
			Seq    *int `json:"seq"`
		}
		if err := json.Unmarshal([]byte(line), &rec); err != nil {
			t.Fatalf("%d 行目が JSON として読めない（行が交錯している）: %q", i+1, line)
		}
		if rec.Worker == nil || rec.Seq == nil {
			t.Fatalf("%d 行目の項目が欠けている: %q", i+1, line)
		}
		k := key{*rec.Worker, *rec.Seq}
		if seen[k] {
			t.Errorf("同じ行が重複している: %+v", k)
		}
		seen[k] = true
	}
	for w := 0; w < appendWorkers; w++ {
		for k := 0; k < appendsEach; k++ {
			if !seen[key{w, k}] {
				t.Errorf("追記が欠落している: worker %d / seq %d", w, k)
			}
		}
	}
	t.Logf("置換 %d 件 / 追記 %d 行を混在させて直列化を確認した（割り込み観測 %d 回）",
		replaces, len(lines), racedWindows.Load())
}
