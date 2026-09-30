//go:build e2e

// e2e（配布物の実機確認）: make dist が出力した配布用書庫を、利用者と同じ手順で
// 自分の領域へ展開して起動し、起動時間・universal binary・管理者権限なしでの配置と起動・
// 配布物の構成（OS ごとに 1 個）・OS の署名評価を確認する。
//
// 単体・結合テストでは意味を持たない項目（起動時間・universal binary・管理者権限なしでの配置と起動・
// OS の署名評価）を、実際の配布物と実機の GUI セッション上で見る。
//
// 実行: make -C app test-e2e（先に make -C app dist が要る）

package e2e

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// startupBudget は起動時間の基準（**2 回目以降**の起動が 5 秒以内）。
const startupBudget = 5 * time.Second

// firstStartupBudget は初回起動（インストール・更新の直後の 1 回）の基準。
//
// 初回は **OS がアプリ本体を走査する**ため遅い。同梱した Codex（約 400MB）でその分が伸びた
// （2026-09-14 実測: 同梱前 1.77 秒 → 同梱後 6.937 秒。2 回目以降は 0.27 秒で変わらない）。
// **上限を置かない扱いにはしない**（置くと初回起動について何も確かめられなくなる）。
const firstStartupBudget = 15 * time.Second

// startupRuns は起動時間の計測回数。
const startupRuns = 5

// deployDistribution は配布用書庫を利用者領域へ展開し、アプリ本体のパスと展開先を返す。
func deployDistribution(t *testing.T) (appBundle, deployDir string) {
	t.Helper()
	deployDir = userAreaDir(t, ".reqweave-e2e")
	extractDist(t, distArchive(t), deployDir)
	appBundle = filepath.Join(deployDir, "ReqWeave.app")
	if st, err := os.Stat(appBundle); err != nil || !st.IsDir() {
		t.Fatalf("展開先に ReqWeave.app がありません（%s）: %v", appBundle, err)
	}
	return appBundle, deployDir
}

// TestDistributionIsSingleAppWithGuide は配布物の中身が「アプリ本体 1 個 + 初回起動の手順書」で
// あることを、実際に展開した結果で確認する。
func TestDistributionIsSingleAppWithGuide(t *testing.T) {
	_, deployDir := deployDistribution(t)
	entries, err := os.ReadDir(deployDir)
	if err != nil {
		t.Fatal(err)
	}
	var apps, guides, others []string
	for _, e := range entries {
		switch {
		case e.Name() == "__MACOSX":
			// 解凍時に付く作業用の入れ物。利用者には見えないため対象外。
		case strings.HasSuffix(e.Name(), ".app"):
			apps = append(apps, e.Name())
		case strings.HasSuffix(e.Name(), ".md"):
			guides = append(guides, e.Name())
		default:
			others = append(others, e.Name())
		}
	}
	if len(apps) != 1 {
		t.Errorf("アプリ本体が 1 個ではありません: %v", apps)
	}
	if len(guides) != 1 {
		t.Errorf("初回起動の手順書が 1 個ではありません: %v", guides)
	}
	if len(others) != 0 {
		t.Errorf("想定外の同梱物があります: %v", others)
	}
	t.Logf("展開結果: アプリ %v / 手順書 %v", apps, guides)
}

// TestDistributedBinaryIsUniversal は配布物が Apple Silicon・Intel の双方で動く形で
// 作られていることを、実機の lipo で確認する。
func TestDistributedBinaryIsUniversal(t *testing.T) {
	appBundle, _ := deployDistribution(t)
	bin := filepath.Join(appBundle, "Contents", "MacOS", "ReqWeave")
	out, err := exec.Command("lipo", "-archs", bin).CombinedOutput()
	if err != nil {
		t.Fatalf("lipo を実行できません: %v\n%s", err, out)
	}
	archs := strings.Fields(string(out))
	t.Logf("lipo -archs: %s", strings.TrimSpace(string(out)))
	for _, want := range []string{"x86_64", "arm64"} {
		found := false
		for _, a := range archs {
			if a == want {
				found = true
			}
		}
		if !found {
			t.Errorf("配布物が %s に対応していません（実際: %v）", want, archs)
		}
	}
}

// TestDistributionRunsFromUserAreaWithoutElevation は、配布物を利用者が自分で置ける場所へ
// 置くだけで起動できること（管理者権限も開発ツールも要らないこと）を確認する。
func TestDistributionRunsFromUserAreaWithoutElevation(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Fatal("root で実行している。管理者権限なしの確認にならないため、通常の利用者で実行すること")
	}
	appBundle, deployDir := deployDistribution(t)

	// 置き場所も中身も利用者自身が書き換えられること（= 昇格なしで自動更新できる前提）。
	for _, p := range []string{deployDir, appBundle} {
		if err := os.Chmod(p, 0o755); err != nil {
			t.Fatalf("利用者の権限で %s を変更できません: %v", p, err)
		}
	}
	probe := filepath.Join(deployDir, ".write-probe")
	if err := os.WriteFile(probe, []byte("x"), 0o600); err != nil {
		t.Fatalf("置き場所へ書き込めません（自動更新が成立しない）: %v", err)
	}
	_ = os.Remove(probe)

	home := userAreaDir(t, ".reqweave-e2e-home")
	elapsed, line := launchAndWaitReady(t, launchOptions{
		bin:     filepath.Join(appBundle, "Contents", "MacOS", "ReqWeave"),
		home:    home,
		timeout: 60 * time.Second,
	})
	t.Logf("配置先 %s から起動: %v / %s", deployDir, elapsed.Round(time.Millisecond), line)
}

// TestStartupTimeWithinBudget は起動時間を実機で計測する。
//
// 1 回目は設定・データが無い状態（初回起動 = 備考の「初期設定前」）、2 回目以降は同じ場所を
// 使い回した状態で計測する。利用者の実データへ触れないよう $HOME を隔離した場所へ向ける。
func TestStartupTimeWithinBudget(t *testing.T) {
	appBundle, _ := deployDistribution(t)
	bin := filepath.Join(appBundle, "Contents", "MacOS", "ReqWeave")
	home := userAreaDir(t, ".reqweave-e2e-home")

	measurements := make([]time.Duration, 0, startupRuns)
	for i := 1; i <= startupRuns; i++ {
		elapsed, _ := launchAndWaitReady(t, launchOptions{bin: bin, home: home, timeout: 60 * time.Second})
		measurements = append(measurements, elapsed)
		label, budget := "2 回目以降（初期設定後）", startupBudget
		if i == 1 {
			label, budget = "1 回目（初期設定前 = 初回起動。OS の走査を含む）", firstStartupBudget
		}
		t.Logf("起動 %d/%d %s: %v", i, startupRuns, label, elapsed.Round(time.Millisecond))
		if elapsed > budget {
			t.Errorf("起動 %d 回目が基準を超過: %v（基準 %v 以内）", i, elapsed, budget)
		}
	}
	if len(measurements) != startupRuns {
		t.Fatalf("計測回数が足りません: %d 回（期待 %d 回）", len(measurements), startupRuns)
	}
	var worst time.Duration
	for _, m := range measurements[1:] {
		if m > worst {
			worst = m
		}
	}
	t.Logf("初回: %v（基準 %v）/ 2 回目以降の最大値: %v（基準 %v）",
		measurements[0].Round(time.Millisecond), firstStartupBudget,
		worst.Round(time.Millisecond), startupBudget)
}

// TestUnsignedDistributionIsRejectedByGatekeeper は、**make dist の出力（開発・検証ゲート用）が
// 未署名であり、OS に拒否される**ことを macOS 自身の評価で確認する。
//
// 署名・公証は **make dist-release でだけ**行う（dist に混ぜると
// 検証ゲートが毎回ネットワークと数分を要求するようになる）。本テストが固定するのは
// 「署名しなければ OS は拒否する」という**署名が実際に効いている理由**そのものであり、
// リリース物の署名・公証は `distcheck -require-signature` が検査する。
//
// **警告ダイアログの文言と GUI 操作は人手確認**であり本テストの範囲外。
func TestUnsignedDistributionIsRejectedByGatekeeper(t *testing.T) {
	appBundle, _ := deployDistribution(t)

	// 利用者がダウンロードした状態を再現する（隔離属性が付く）。
	if out, err := exec.Command("xattr", "-w", "com.apple.quarantine",
		"0081;00000000;Safari;", appBundle).CombinedOutput(); err != nil {
		t.Fatalf("隔離属性を付けられません: %v\n%s", err, out)
	}

	out, _ := exec.Command("spctl", "--assess", "--type", "execute", "-vvv", appBundle).CombinedOutput()
	assessment := strings.TrimSpace(string(out))
	t.Logf("spctl --assess の実出力:\n%s", assessment)
	if !strings.Contains(assessment, "rejected") {
		t.Errorf("make dist の出力は未署名のはずが OS に受理された（署名の段が dist へ混入していないか）: %s", assessment)
	}

	sig, _ := exec.Command("codesign", "--display", "--verbose=2", appBundle).CombinedOutput()
	t.Logf("codesign --display の実出力:\n%s", strings.TrimSpace(string(sig)))
	if strings.Contains(string(sig), "Authority=Developer ID Application") {
		t.Errorf("make dist の出力に Developer ID 署名が付いている。署名・公証は make dist-release の役目: %s", sig)
	}
}
