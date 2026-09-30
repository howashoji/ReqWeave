package main

import (
	"fmt"
	"os/exec"
	"strings"
)

/*
 * 署名・公証の検査。
 *
 * macOS の配布物は **Developer ID 署名 + 公証 + ステープル**で配る。ただし署名・公証は
 * **リリース専用のターゲット（make dist-release）でだけ**行う。`make dist` は開発と検証ゲートで
 * 何度も回すため、毎回 Apple のタイムスタンプサーバ・公証サービスへ出る（= ネットワーク必須・数分）
 * 形にはしない。
 *
 * そのため本検査は 2 段構成にする（アイコンの検査と同じ方針）:
 *
 *   require=false（make dist）        … 配布用の署名が無ければ「未実施」と**明示して**緑にする。
 *                                       黙って飛ばさない。
 *   require=true （make dist-release）… 署名・hardened runtime・Developer ID・公証チケットの
 *                                       添付・OS の受理をすべて要求し、1 つでも欠けたら不合格。
 *
 * **ad-hoc 署名は「署名なし」と同じ扱い**にする。Go / Wails は macOS 向けのビルドで自動的に
 * ad-hoc 署名を付ける（`Signature=adhoc`）ため、「署名の有無」だけを見ると未署名の配布物を
 * 署名済みと誤認する。
 */

// signatureInfo は `codesign --display` の読み取り結果。
type signatureInfo struct {
	present     bool     // 署名そのものがあるか
	adhoc       bool     // ad-hoc 署名（配布用の署名ではない）
	authorities []string // Authority= の並び（先頭が署名者）
	hardened    bool     // hardened runtime（公証の必須条件）
	raw         string   // 実出力（記録用）
}

// developerID は Developer ID Application の署名者名を返す（無ければ空）。
func (s signatureInfo) developerID() string {
	for _, a := range s.authorities {
		if strings.HasPrefix(a, "Developer ID Application") {
			return a
		}
	}
	return ""
}

// parseCodesignDisplay は `codesign --display --verbose=2` の出力を読む。
func parseCodesignDisplay(out string) signatureInfo {
	info := signatureInfo{present: true, raw: out}
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		switch {
		case strings.HasPrefix(line, "Authority="):
			info.authorities = append(info.authorities, strings.TrimPrefix(line, "Authority="))
		case strings.HasPrefix(line, "Signature=adhoc"):
			info.adhoc = true
		case strings.HasPrefix(line, "CodeDirectory "):
			// 例: CodeDirectory v=20500 size=... flags=0x10000(runtime) hashes=...
			for _, field := range strings.Fields(line) {
				if !strings.HasPrefix(field, "flags=") {
					continue
				}
				if strings.Contains(field, "runtime") {
					info.hardened = true
				}
				if strings.Contains(field, "adhoc") {
					info.adhoc = true
				}
			}
		}
	}
	return info
}

// inspectCodesign は対象の署名の状態を読む。**署名が無いこと自体はエラーにしない**
// （未署名の配布物は `make dist` の正常な出力であり、判定は呼び出し側が行う）。
func inspectCodesign(path string) (signatureInfo, error) {
	out, err := exec.Command("codesign", "--display", "--verbose=2", path).CombinedOutput()
	text := string(out)
	if err != nil {
		if strings.Contains(text, "not signed at all") {
			return signatureInfo{present: false, raw: text}, nil
		}
		return signatureInfo{}, fmt.Errorf("署名を読めません（%s）: %w: %s", path, err, strings.TrimSpace(text))
	}
	return parseCodesignDisplay(text), nil
}

// checkSignature は配布物の署名・公証を検査する。
//
// appPath はマウントした dmg の中のアプリ本体、dmgPath は配布物そのもの。
// problems は不合格の理由、notes は実出力として記録する事実（署名者・公証の状態）。
func checkSignature(appPath, dmgPath string, require bool) (problems []string, notes []string, err error) {
	info, err := inspectCodesign(appPath)
	if err != nil {
		return nil, nil, err
	}

	// 1. 配布用の署名が付いているか（ad-hoc・自己署名は配布用ではない）。
	id := info.developerID()
	if id == "" {
		state := "署名なし"
		switch {
		case info.adhoc:
			state = "ad-hoc 署名（配布用の署名ではない）"
		case info.present:
			state = "Developer ID 以外の署名"
		}
		if require {
			return []string{fmt.Sprintf(
				"アプリ本体に Developer ID 署名がありません（%s）。リリース物は make dist-release で作ること",
				state)}, nil, nil
		}
		// ad-hoc 署名でも、封（sealed resources）が壊れていないことは見る。ビルドの後に同梱物を足して
		// 署名をやり直さないと壊れる（前回のビルドの残りがあると封に入って見過ごす）。
		if info.adhoc {
			if out, verr := exec.Command("codesign", "--verify", "--strict", "--verbose=2", appPath).CombinedOutput(); verr != nil {
				problems = append(problems, "アプリ本体の ad-hoc 署名が壊れています（同梱物を入れた後に署名をやり直すこと = Makefile の reseal_adhoc）: "+
					oneLine(string(out)))
			}
		}
		return problems, []string{fmt.Sprintf(
			"署名・公証の検査は未実施（%s）。リリース物は make dist-release で作る", state)}, nil
	}
	notes = append(notes, "署名: "+id)

	// 2. 署名が壊れていないこと。**署名が付いている以上、壊れていれば require によらず不合格**
	//    （壊れた署名を配ると Gatekeeper が必ず拒否する）。
	if out, verr := exec.Command("codesign", "--verify", "--strict", "--verbose=2", appPath).CombinedOutput(); verr != nil {
		problems = append(problems, "アプリ本体の署名が壊れています: "+oneLine(string(out)))
	}

	// 3. hardened runtime（公証の必須条件 = `codesign --options runtime`）。
	if !info.hardened {
		if require {
			problems = append(problems, "hardened runtime が有効ではありません（公証の必須条件。codesign --options runtime を付けること）")
		} else {
			notes = append(notes, "hardened runtime: 無効（公証には必須）")
		}
	}

	// 4. 公証チケットの添付（ステープル）。**アプリ本体と配布物の双方**へ添付する。
	//    アプリ本体に無いと、利用者が dmg から取り出した .app をオフラインで検証できない。
	for _, target := range []struct{ label, path string }{
		{"アプリ本体", appPath},
		{"配布物", dmgPath},
	} {
		out, serr := exec.Command("xcrun", "stapler", "validate", target.path).CombinedOutput()
		switch {
		case serr == nil:
			notes = append(notes, target.label+": 公証チケット添付済み（stapler validate 成功）")
		case require:
			problems = append(problems, fmt.Sprintf("%sに公証チケットが添付されていません（xcrun stapler staple）: %s",
				target.label, oneLine(string(out))))
		default:
			notes = append(notes, target.label+": 公証チケットは未添付")
		}
	}

	// 5. OS 自身の評価（Gatekeeper が受理するか）。
	out, _ := exec.Command("spctl", "--assess", "--type", "execute", "-vv", appPath).CombinedOutput()
	assessment := oneLine(string(out))
	switch {
	case strings.Contains(string(out), "accepted"):
		notes = append(notes, "spctl --assess: "+assessment)
		if require && !strings.Contains(string(out), "source=Notarized Developer ID") {
			problems = append(problems, "OS の評価が「公証済みの Developer ID」ではありません: "+assessment)
		}
	case require:
		problems = append(problems, "OS がアプリ本体を受理しません（spctl --assess）: "+assessment)
	default:
		notes = append(notes, "spctl --assess: "+assessment)
	}
	return problems, notes, nil
}

// oneLine は複数行の実出力を 1 行へ畳む（メッセージへ埋め込むため）。
func oneLine(s string) string {
	fields := strings.Fields(strings.TrimSpace(s))
	return strings.Join(fields, " ")
}
