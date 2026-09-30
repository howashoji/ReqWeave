// noticegen は配布物に含まれる第三者のソフトウェアのライセンス文（リポジトリ直上の NOTICE）を作る。
//
//	go run ./tools/noticegen          # NOTICE を作り直す（app/ で実行する）
//	go run ./tools/noticegen -check   # 作り直した内容と NOTICE が違えば失敗する（書き換えない）
//
// 集めるもの:
//   - アプリ本体に実際にリンクされる Go のモジュール。配布物を作る形（macOS の amd64・arm64 と Windows の amd64。
//     タグは wails build が付ける desktop・production）ごとに `go list -deps -json` を回し、その和をとる
//     （標準ライブラリと本体のモジュールは除く）。ライセンス文は Module.Dir の直下の LICENSE・COPYING・NOTICE など。
//   - フロントエンドの本番の依存（package-lock.json で dev でないもの。ビルドでバンドルされる）。
//     ライセンス文は node_modules/<名前>/ の直下の LICENSE・COPYING・NOTICE など。
//   - Go の標準ライブラリとランタイムのライセンス（$GOROOT/LICENSE）。
//
// ライセンス文が 1 つも無い依存があれば失敗する。例外は knownWithoutFile に挙げたものだけ
// （package.json が MIT と宣言しているのにパッケージにライセンス文が入っていないもの。定型文と著作者で記す）。
// 出力はパスと名前で並べ、日付など実行ごとに変わるものを入れない（依存を変えたら作り直してコミットする）。
package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

type target struct{ goos, goarch, cgo string }

// targets は配布物を作る形（Makefile の dist と dist-windows）。
var targets = []target{
	{"darwin", "amd64", "1"},
	{"darwin", "arm64", "1"},
	{"windows", "amd64", "0"},
}

const buildTags = "desktop,production"

type module struct {
	Path    string
	Version string
	Dir     string
	Main    bool
	Replace *module
}

type goPackage struct {
	Standard bool
	Module   *module
}

// section は NOTICE の 1 節（1 つの依存）。
type section struct {
	title    string // 見出し（モジュールのパスか npm の名前と版）
	source   string // 入手先
	note     string // 補足（あれば）
	licenses []licenseFile
}

type licenseFile struct {
	name string
	text string
}

// licenseName はライセンス文とみなすファイル名（大文字小文字・拡張子・LICENSE-MIT のような接尾辞の違いを許す）。
var licenseName = regexp.MustCompile(`(?i)^(licen[cs]e|copying|notice)([-._][A-Za-z0-9.-]*)?(\.(md|txt|markdown|rst))?$`)

// codeFile は名前が licenseName に合ってもライセンス文ではないもの（license-update.mjs のようなスクリプト・型定義・設定）。
var codeFile = regexp.MustCompile(`(?i)\.(go|[cm]?js|jsx|[cm]?ts|tsx|json|map|sh|py|rb|html?|css)$`)

func isLicenseFile(name string) bool {
	return licenseName.MatchString(name) && !codeFile.MatchString(name)
}

// knownWithoutFile はライセンス文を同梱していない npm の依存（名前 → 記し方）。
// package.json の license が MIT で、配布元のリポジトリにも同じ宣言がある。
var knownWithoutFile = map[string]string{
	"fastdom":   "MIT",
	"strictdom": "MIT",
}

const mitTemplate = `Copyright (c) %s

Permission is hereby granted, free of charge, to any person obtaining a copy
of this software and associated documentation files (the "Software"), to deal
in the Software without restriction, including without limitation the rights
to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
copies of the Software, and to permit persons to whom the Software is
furnished to do so, subject to the following conditions:

The above copyright notice and this permission notice shall be included in all
copies or substantial portions of the Software.

THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN THE
SOFTWARE.
`

func main() {
	check := flag.Bool("check", false, "作り直した内容と NOTICE が違えば失敗する（書き換えない）")
	out := flag.String("o", "../NOTICE", "出力先（app/ からの相対）")
	flag.Parse()

	got, err := build()
	if err != nil {
		fmt.Fprintln(os.Stderr, "noticegen:", err)
		os.Exit(1)
	}
	if *check {
		cur, err := os.ReadFile(*out)
		if err != nil {
			fmt.Fprintf(os.Stderr, "noticegen: %s が読めません: %v（go run ./tools/noticegen で作る）\n", *out, err)
			os.Exit(1)
		}
		if !bytes.Equal(cur, got) {
			fmt.Fprintf(os.Stderr, "noticegen: %s が依存と合っていません。go run ./tools/noticegen で作り直してコミットしてください\n", *out)
			os.Exit(1)
		}
		fmt.Println("✓ NOTICE は依存と一致")
		return
	}
	if err := os.WriteFile(*out, got, 0o644); err != nil {
		fmt.Fprintln(os.Stderr, "noticegen:", err)
		os.Exit(1)
	}
	fmt.Printf("✓ %s を書きました（%d バイト）\n", *out, len(got))
}

func build() ([]byte, error) {
	goSecs, err := goSections()
	if err != nil {
		return nil, err
	}
	npmSecs, err := npmSections("frontend")
	if err != nil {
		return nil, err
	}
	std, err := goStdSection()
	if err != nil {
		return nil, err
	}
	return render(std, goSecs, npmSecs), nil
}

func render(std section, goSecs, npmSecs []section) []byte {
	var b bytes.Buffer
	b.WriteString("ReqWeave — 第三者のソフトウェアのライセンス\n\n")
	b.WriteString("ReqWeave のアプリ本体に含まれる第三者のソフトウェアとフォントのライセンス文です。\n")
	b.WriteString("このファイルは app/tools/noticegen が作ります。手で直さず、依存を変えたら作り直してください。\n\n")
	b.WriteString("macOS の配布物には OpenAI の Codex（Apache License 2.0）も同梱しています。\n")
	b.WriteString("その LICENSE と NOTICE は、アプリ本体の Contents/Resources/codex/ に置いています。\n")
	fmt.Fprintf(&b, "\n収録: Go のモジュール %d・フロントエンドのパッケージ %d\n", len(goSecs), len(npmSecs))

	writePart := func(heading string, secs []section) {
		b.WriteString("\n\n################################################################\n")
		b.WriteString(heading + "\n")
		b.WriteString("################################################################\n")
		for _, s := range secs {
			b.WriteString("\n================================================================\n")
			b.WriteString(s.title + "\n")
			if s.source != "" {
				b.WriteString("入手先: " + s.source + "\n")
			}
			if s.note != "" {
				b.WriteString(s.note + "\n")
			}
			b.WriteString("================================================================\n")
			for _, l := range s.licenses {
				fmt.Fprintf(&b, "\n--- %s ---\n\n", l.name)
				b.WriteString(strings.TrimRight(normalize(l.text), "\n") + "\n")
			}
		}
	}
	writePart("Go の標準ライブラリとランタイム", []section{std})
	writePart("Go のモジュール", goSecs)
	writePart("フロントエンド（npm のパッケージ）", npmSecs)
	return b.Bytes()
}

// normalize は改行を LF にそろえ、行末の空白を落とす（配布元の書式の揺れで差分を出さない）。
func normalize(s string) string {
	s = strings.ReplaceAll(s, "\r\n", "\n")
	s = strings.ReplaceAll(s, "\r", "\n")
	lines := strings.Split(s, "\n")
	for i, l := range lines {
		lines[i] = strings.TrimRight(l, " \t")
	}
	return strings.Join(lines, "\n")
}

func goStdSection() (section, error) {
	root, err := exec.Command("go", "env", "GOROOT").Output()
	if err != nil {
		return section{}, fmt.Errorf("go env GOROOT: %w", err)
	}
	// Homebrew の Go は LICENSE を GOROOT（libexec）の 1 つ上へ移しているので、そこも見る
	goroot := strings.TrimSpace(string(root))
	var text []byte
	for _, p := range []string{filepath.Join(goroot, "LICENSE"), filepath.Join(goroot, "..", "LICENSE")} {
		if text, err = os.ReadFile(p); err == nil {
			break
		}
	}
	if err != nil {
		return section{}, fmt.Errorf("Go のライセンス文が読めません（%s とその 1 つ上）: %w", goroot, err)
	}
	return section{
		title:    "Go（標準ライブラリとランタイム）",
		source:   "https://go.dev/",
		licenses: []licenseFile{{name: "LICENSE", text: string(text)}},
	}, nil
}

func goSections() ([]section, error) {
	mods := map[string]module{}
	for _, t := range targets {
		cmd := exec.Command("go", "list", "-deps", "-json", "-tags", buildTags, ".")
		cmd.Env = append(os.Environ(), "GOOS="+t.goos, "GOARCH="+t.goarch, "CGO_ENABLED="+t.cgo)
		var stderr bytes.Buffer
		cmd.Stderr = &stderr
		out, err := cmd.Output()
		if err != nil {
			return nil, fmt.Errorf("go list（%s/%s）: %v: %s", t.goos, t.goarch, err, stderr.String())
		}
		dec := json.NewDecoder(bytes.NewReader(out))
		for {
			var p goPackage
			if err := dec.Decode(&p); errors.Is(err, io.EOF) {
				break
			} else if err != nil {
				return nil, fmt.Errorf("go list の出力が読めません: %w", err)
			}
			if p.Standard || p.Module == nil || p.Module.Main {
				continue
			}
			m := *p.Module
			if m.Replace != nil {
				m = *m.Replace
			}
			mods[m.Path+"@"+m.Version] = m
		}
	}
	if len(mods) == 0 {
		return nil, errors.New("Go のモジュールを 1 つも拾えていません（走査の空振り）")
	}
	var secs []section
	var missing []string
	for _, m := range mods {
		if m.Dir == "" {
			return nil, fmt.Errorf("%s@%s のソースが手元にありません（go mod download を先に）", m.Path, m.Version)
		}
		files, err := licenseFiles(m.Dir)
		if err != nil {
			return nil, err
		}
		if len(files) == 0 {
			missing = append(missing, m.Path+"@"+m.Version)
			continue
		}
		secs = append(secs, section{
			title:    m.Path + " " + m.Version,
			source:   "https://" + repoPath(m.Path),
			licenses: files,
		})
	}
	if len(missing) > 0 {
		sort.Strings(missing)
		return nil, fmt.Errorf("ライセンス文の無い Go のモジュールがあります: %s", strings.Join(missing, ", "))
	}
	sort.Slice(secs, func(i, j int) bool { return secs[i].title < secs[j].title })
	return secs, nil
}

// repoPath はモジュールのパスから /v2 のような major 版の接尾辞を外す。
func repoPath(p string) string {
	if i := strings.LastIndex(p, "/v"); i > 0 {
		rest := p[i+2:]
		if rest != "" && strings.Trim(rest, "0123456789") == "" {
			return p[:i]
		}
	}
	return p
}

type lockfile struct {
	Packages map[string]struct {
		Version string `json:"version"`
		License string `json:"license"`
		Dev     bool   `json:"dev"`
	} `json:"packages"`
}

type packageJSON struct {
	Name       string          `json:"name"`
	Version    string          `json:"version"`
	Author     json.RawMessage `json:"author"`
	Homepage   string          `json:"homepage"`
	Repository json.RawMessage `json:"repository"`
}

func npmSections(dir string) ([]section, error) {
	raw, err := os.ReadFile(filepath.Join(dir, "package-lock.json"))
	if err != nil {
		return nil, fmt.Errorf("package-lock.json が読めません: %w", err)
	}
	var lock lockfile
	if err := json.Unmarshal(raw, &lock); err != nil {
		return nil, fmt.Errorf("package-lock.json が読めません: %w", err)
	}
	var secs []section
	var missing []string
	for key, p := range lock.Packages {
		if key == "" || p.Dev {
			continue
		}
		pdir := filepath.Join(dir, filepath.FromSlash(key))
		var pj packageJSON
		pjRaw, err := os.ReadFile(filepath.Join(pdir, "package.json"))
		if err != nil {
			return nil, fmt.Errorf("%s が読めません（npm ci を先に）: %w", key, err)
		}
		if err := json.Unmarshal(pjRaw, &pj); err != nil {
			return nil, fmt.Errorf("%s/package.json が読めません: %w", key, err)
		}
		files, err := licenseFiles(pdir)
		if err != nil {
			return nil, err
		}
		s := section{title: pj.Name + " " + pj.Version, source: npmSource(pj)}
		if len(files) == 0 {
			kind, ok := knownWithoutFile[pj.Name]
			if !ok || kind != p.License {
				missing = append(missing, key)
				continue
			}
			who := authorName(pj.Author)
			if who == "" {
				return nil, fmt.Errorf("%s: ライセンス文が無く、著作者も package.json から読めません", key)
			}
			s.note = "パッケージにライセンス文が入っていないため、package.json の宣言（" + kind + "）と著作者から定型文で記す。"
			files = []licenseFile{{name: kind, text: fmt.Sprintf(mitTemplate, who)}}
		}
		s.licenses = files
		secs = append(secs, s)
	}
	if len(missing) > 0 {
		sort.Strings(missing)
		return nil, fmt.Errorf("ライセンス文の無い npm のパッケージがあります: %s", strings.Join(missing, ", "))
	}
	if len(secs) == 0 {
		return nil, errors.New("npm のパッケージを 1 つも拾えていません（走査の空振り）")
	}
	sort.Slice(secs, func(i, j int) bool { return secs[i].title < secs[j].title })
	return secs, nil
}

func npmSource(pj packageJSON) string {
	var repo string
	var obj struct {
		URL string `json:"url"`
	}
	if json.Unmarshal(pj.Repository, &repo) != nil || repo == "" {
		if json.Unmarshal(pj.Repository, &obj) == nil {
			repo = obj.URL
		}
	}
	repo = strings.TrimPrefix(repo, "git+")
	repo = strings.TrimSuffix(repo, ".git")
	repo = strings.Replace(repo, "git://", "https://", 1)
	repo = strings.Replace(repo, "ssh://git@", "https://", 1)
	if strings.HasPrefix(repo, "https://") {
		return repo
	}
	if strings.HasPrefix(repo, "github:") {
		return "https://github.com/" + strings.TrimPrefix(repo, "github:")
	}
	if repo != "" && !strings.Contains(repo, ":") && strings.Count(repo, "/") == 1 {
		return "https://github.com/" + repo
	}
	if pj.Homepage != "" {
		return pj.Homepage
	}
	return "https://www.npmjs.com/package/" + pj.Name
}

func authorName(raw json.RawMessage) string {
	var s string
	if json.Unmarshal(raw, &s) == nil {
		if i := strings.Index(s, "<"); i > 0 {
			s = s[:i]
		}
		return strings.TrimSpace(s)
	}
	var obj struct {
		Name string `json:"name"`
	}
	if json.Unmarshal(raw, &obj) == nil {
		return obj.Name
	}
	return ""
}

func licenseFiles(dir string) ([]licenseFile, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("%s が読めません: %w", dir, err)
	}
	var files []licenseFile
	for _, e := range entries {
		if e.IsDir() || !isLicenseFile(e.Name()) {
			continue
		}
		b, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			return nil, err
		}
		files = append(files, licenseFile{name: e.Name(), text: string(b)})
	}
	sort.Slice(files, func(i, j int) bool { return files[i].name < files[j].name })
	return files, nil
}
