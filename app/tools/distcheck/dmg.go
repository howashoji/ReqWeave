package main

import (
	"debug/macho"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// dmg の検査。
//
// macOS の配布物は dmg（Windows は zip のまま）。zip と違って中身を直接読めないため、
// **読み取り専用で一時的な場所へマウントして**中身を確かめ、必ず切り離す。
//
// マウント先は `/Volumes` ではなく一時ディレクトリを指定する。
// `/Volumes/ReqWeave` は同名の別ボリュームがあると `ReqWeave 1` へずれるため、
// 「どこを見ているか」が曖昧にならないようにする。

// checkDMG は dmg をマウントして中身を検査する。問題の一覧と、記録に残す事実
// （Universal Binary の判定・署名と公証の状態）を返す。
//
// requireSignature は署名・公証を必須とするか（make dist-release = true / make dist = false）。
func checkDMG(dmgPath, appName, legalDir string, requireSignature bool) (problems []string, notes []string, err error) {
	mountPoint, err := os.MkdirTemp("", "distcheck-dmg-")
	if err != nil {
		return nil, nil, fmt.Errorf("マウント先を用意できません: %w", err)
	}
	defer os.RemoveAll(mountPoint)

	// 前回の実行が残したマウントを先に片づける。
	// 同じ dmg が既にマウントされたまま attach すると衝突し、失敗が自己増殖する。
	// **何を切り離したかは必ず記録する**（黙って消さない）。
	sweepStaleMounts(dmgPath, func(msg string) {
		fmt.Fprintf(os.Stderr, "distcheck: %s\n", msg)
	})

	if aerr := attachRetry(dmgPath, mountPoint); aerr != nil {
		return nil, nil, aerr
	}
	defer func() {
		// 切り離しに失敗するとマウントが残り、次回の検査が別の場所を見る。
		// 検査自体が成功していても失敗として扱う。
		if derr := detachRetry(mountPoint, dmgPath); derr != nil {
			if err == nil {
				err = derr
			}
		}
	}()

	problems = checkMountedContents(mountPoint, appName)
	problems = append(problems, checkLegal(func(rel string) ([]byte, error) {
		return os.ReadFile(filepath.Join(mountPoint, filepath.FromSlash(rel)))
	}, "darwin", appName, legalDir)...)

	// ビルド環境の情報の混入。**署名の有無によらず常に検査する**
	// （ゲート用の配布物でも混入は起きるため）。
	leaks, leakNotes, lerr := checkLeaks(os.DirFS(mountPoint))
	if lerr != nil {
		return nil, nil, lerr
	}
	problems = append(problems, leaks...)
	notes = append(notes, leakNotes...)

	execPath := filepath.Join(mountPoint, appName, "Contents", "MacOS", "ReqWeave")
	switch ok, d, uerr := universalCheckFile(execPath); {
	case uerr != nil:
		problems = append(problems, "実行ファイルが見つからず、対応アーキテクチャを確認できません")
	case !ok:
		problems = append(problems, "Apple Silicon・Intel の双方に対応していません（"+d+
			"。make dist は -platform darwin/universal で作ること）")
	default:
		notes = append(notes, d)
	}

	// 同梱した Codex。アプリ本体の中を見る。
	if _, statErr := os.Stat(filepath.Join(mountPoint, appName)); statErr == nil {
		codexProblems, codexNotes := checkBundledCodex(filepath.Join(mountPoint, appName), requireSignature)
		problems = append(problems, codexProblems...)
		notes = append(notes, codexNotes...)
	}

	// 署名・公証。マウント中の .app を対象にする
	//（取り出してからでは、コピーの仕方によっては署名が壊れて判定が変わる）。
	// アプリ本体が無い場合は上で問題として挙がっているため、署名は見ない。
	appPath := filepath.Join(mountPoint, appName)
	if _, statErr := os.Stat(appPath); statErr == nil {
		sigProblems, sigNotes, serr := checkSignature(appPath, dmgPath, requireSignature)
		if serr != nil {
			return nil, nil, serr
		}
		problems = append(problems, sigProblems...)
		notes = append(notes, sigNotes...)
	}
	return problems, notes, nil
}

// checkMountedContents はマウントした dmg の最上位を検査する（zip 版と同じ基準）。
func checkMountedContents(mountPoint, appName string) []string {
	var problems []string
	entries, err := os.ReadDir(mountPoint)
	if err != nil {
		return []string{fmt.Sprintf("配布物の中身を読めません: %v", err)}
	}

	allowed := map[string]bool{appName: true}
	for _, doc := range requiredDocs {
		allowed[doc] = true
	}
	found := map[string]bool{}
	for _, e := range entries {
		name := e.Name()
		// ボリュームに OS が作る隠しメタデータは配布物の中身ではない。
		if name == ".Trashes" || name == ".fseventsd" || name == ".DS_Store" {
			continue
		}
		info, lerr := os.Lstat(filepath.Join(mountPoint, name))
		if lerr == nil && info.Mode()&os.ModeSymlink != 0 {
			// **/Applications へのリンクを置かない**。ホームフォルダ配下に
			// 置かないと自動更新ができないため、同梱の手順書と矛盾する誘導になる。
			problems = append(problems, fmt.Sprintf("シンボリックリンクが含まれる: %q（/Applications へ誘導しない）", name))
			continue
		}
		found[name] = true
		if !allowed[name] {
			problems = append(problems, fmt.Sprintf("配布物に想定外の項目がある: %q", name))
		}
	}

	if !found[appName] {
		problems = append(problems, fmt.Sprintf("アプリ本体 %q が無い（1 件であること）", appName))
	}
	for _, doc := range requiredDocs {
		if !found[doc] {
			problems = append(problems, fmt.Sprintf("同梱の手順書が無い: %q", doc))
		}
	}
	return problems
}

// universalCheckFile は実ファイルの Mach-O ヘッダから Apple Silicon・Intel の双方に
// 対応するかを判定する。
func universalCheckFile(execPath string) (ok bool, detail string, err error) {
	fat, ferr := macho.OpenFat(execPath)
	if ferr == nil {
		defer fat.Close()
		var arches []string
		hasARM64, hasAMD64 := false, false
		for _, a := range fat.Arches {
			switch a.Cpu {
			case macho.CpuArm64:
				hasARM64 = true
				arches = append(arches, "arm64")
			case macho.CpuAmd64:
				hasAMD64 = true
				arches = append(arches, "amd64")
			default:
				arches = append(arches, fmt.Sprintf("%v", a.Cpu))
			}
		}
		list := strings.Join(arches, " + ")
		if hasARM64 && hasAMD64 {
			return true, "実行ファイルは Universal Binary（" + list + "）", nil
		}
		return false, "含まれるのは " + list + " のみ", nil
	}

	// 単一アーキテクチャの Mach-O は fat ではないため、そちらとして開き直す。
	single, serr := macho.Open(execPath)
	if serr != nil {
		return false, "", fmt.Errorf("実行ファイルを読めません（%s）: %w", execPath, serr)
	}
	defer single.Close()
	return false, fmt.Sprintf("含まれるのは %v のみ", single.Cpu), nil
}

// detachRetry はマウントを切り離す。**1 回で諦めない**（実測で失敗したため）。
//
// 直後は Spotlight のインデックス等でボリュームが使用中になることがあり、
// `hdiutil detach` が exit 16（Resource busy）で落ちる。**署名・公証まで通した後に
// ここで失敗すると成果が宙に浮く**ため、間隔をあけて数回試し、最後は -force で外す。
//
// **`-quiet` を付けない**。付けると hdiutil は失敗の理由を出さず、
// 「配布物を切り離せません（…）: 」という中身の無い失敗になり、原因を追えない。
//
// それでも外せなかったときは、**device 指定で強制的に外す**（dmgPath から引く）。
// マウントを残すと次回の実行を巻き込むため、失敗を報告しつつ後始末だけは必ず行う。
func detachRetry(mountPoint, dmgPath string) error {
	var last error
	for i := 0; i < detachAttempts; i++ {
		args := []string{"detach", mountPoint}
		if i == detachAttempts-1 {
			// 最後の 1 回は強制。ここまで来たら「残す」より「外す」を優先する
			// （残ると次回の検査が別の場所を見る）。
			args = append(args, "-force")
		}
		_, err := hdiutilRun(args...)
		if err == nil {
			return nil
		}
		last = err
		time.Sleep(detachInterval)
	}

	// 後始末: マウント先からは外せなくても device からなら外せることがある。
	cleanup := "残ったマウントは外せませんでした"
	if left, ierr := waitMountsDrained(dmgPath); ierr != nil {
		cleanup = fmt.Sprintf("残ったマウントを確認できませんでした: %v", ierr)
	} else if len(left) == 0 {
		cleanup = "マウントは残っていません"
	} else {
		var failed []string
		for _, m := range left {
			if derr := detachDevices(m); derr != nil {
				failed = append(failed, derr.Error())
			}
		}
		if len(failed) == 0 {
			cleanup = "残ったマウントは device 指定で切り離しました"
		} else {
			cleanup = "残ったマウントを切り離せませんでした: " + strings.Join(failed, " / ")
		}
	}
	return fmt.Errorf("配布物を切り離せません（%s）: %w（%s）", mountPoint, last, cleanup)
}

var (
	detachAttempts = 5
	detachInterval = 2 * time.Second
)

// attachRetry は dmg を読み取り専用でマウントする。**1 回で諦めない**。
//
// 直前の操作（作成・署名・ステープル・別のマウント）の直後は
// 「リソースが使用中です」で失敗することがある。切り離し側と同じ理由で、間隔をあけて数回試す。
func attachRetry(dmgPath, mountPoint string) error {
	var last error
	for i := 0; i < detachAttempts; i++ {
		_, err := hdiutilRun("attach", "-nobrowse", "-readonly", "-noverify",
			"-mountpoint", mountPoint, dmgPath)
		if err == nil {
			return nil
		}
		last = err
		time.Sleep(detachInterval)
	}
	return fmt.Errorf("配布物をマウントできません（%s）: %w", dmgPath, last)
}
