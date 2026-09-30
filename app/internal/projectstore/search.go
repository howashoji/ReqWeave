package projectstore

// 本ファイルは対話履歴の全文検索を担う。
//
// **索引を持たず、セッションファイルを逐次走査する**。
// 派生インデックス（index.go）は取り込みの完了時にしか更新しないため、
// 検索の網羅性を索引の鮮度へ依存させると「直前に書いた発話が検索に出ない」不整合を生む。
// 上限規模（セッション 100 × 発話 1,000）でも、逐次走査のまま応答時間の基準を満たす。
//
// 読み出しのみで、本ファイルから発話を書き換える経路は作らない（対話の記録を改変しない）。

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"gopkg.in/yaml.v3"
)

// DefaultUtteranceSearchLimit は返す該当発話の既定の上限。
//
// 上限規模では 1 語が 10 万件に当たり得る（例: 「の」）。全件を画面へ渡すと
// 描画側が応答時間の基準を守れないため、**総数は数え切ったうえで一覧は先頭 N 件に切る**。
// 切ったことは UtteranceSearchResult.Truncated で必ず呼び出し側へ伝える（黙って落とさない）。
const DefaultUtteranceSearchLimit = 200

// excerptContextRunes は抜粋で該当箇所の前後に付ける文字数（結果一覧の「該当箇所を含む抜粋」）。
const excerptContextRunes = 40

// UtteranceSearchQuery は対話履歴の全文検索の条件。
type UtteranceSearchQuery struct {
	// Text は検索する語句。空白のみは受け付けない。
	Text string
	// Phase / Type はフェーズ・セッション種別の絞り込み（セッション一覧の絞り込みと同じ。空なら全件）。
	Phase string
	Type  string
	// Limit は一覧に載せる該当発話の上限（0 で DefaultUtteranceSearchLimit）。
	Limit int
}

// UtteranceHit は該当発話 1 件（結果一覧の 1 行）。
type UtteranceHit struct {
	SessionID   string
	Phase       string
	Type        string
	UtteranceID string
	Speaker     string
	At          time.Time
	// Status は発話の状態。中断発話も検索対象に含める（途中で切れた応答も探せるように）。
	Status string
	// Excerpt は該当箇所を含む抜粋（前後 excerptContextRunes 文字。改行は空白へ畳む）。
	Excerpt string
}

// UtteranceSearchResult は検索の結果。
type UtteranceSearchResult struct {
	Hits []UtteranceHit
	// Total は該当した発話の総数（Limit で切る前）。
	Total int
	// Truncated は Total > len(Hits) のとき真。
	Truncated bool
	// ScannedSessions / ScannedUtterances は走査した件数。
	// **計測が空振りでないこと**（走査が実際に起きたこと）の確認に使う（性能の再計測）。
	ScannedSessions   int
	ScannedUtterances int
}

// SearchUtterances は全対話セッションの発話本文を語句で検索する。
//
// 絞り込み（Phase / Type）を通過したセッションだけを走査対象にする。
func (s *Store) SearchUtterances(q UtteranceSearchQuery) (UtteranceSearchResult, error) {
	needle := strings.TrimSpace(q.Text)
	if needle == "" {
		return UtteranceSearchResult{}, fmt.Errorf("検索する語句がありません。探したい語句を入力してください")
	}
	limit := q.Limit
	if limit <= 0 {
		limit = DefaultUtteranceSearchLimit
	}
	lowered := strings.ToLower(needle)
	m := newMatcher(needle)

	sessions, err := s.ListSessions()
	if err != nil {
		return UtteranceSearchResult{}, err
	}

	var out UtteranceSearchResult
	for _, sess := range sessions {
		if q.Phase != "" && sess.Phase != q.Phase {
			continue
		}
		if q.Type != "" && sess.Type != q.Type {
			continue
		}
		utterances, err := s.loadUtterances(sess.ID)
		if err != nil {
			return UtteranceSearchResult{}, err
		}
		out.ScannedSessions++
		out.ScannedUtterances += len(utterances)
		for _, u := range utterances {
			if !m.match(u.Body) {
				continue
			}
			out.Total++
			if len(out.Hits) >= limit {
				continue
			}
			out.Hits = append(out.Hits, UtteranceHit{
				SessionID:   sess.ID,
				Phase:       sess.Phase,
				Type:        sess.Type,
				UtteranceID: u.ID,
				Speaker:     u.Speaker,
				At:          u.At,
				Status:      u.Status,
				Excerpt:     excerptAround(u.Body, lowered),
			})
		}
	}
	out.Truncated = out.Total > len(out.Hits)
	return out, nil
}

// matcher は発話の本文に語句が含まれるかを、英字の大文字・小文字を区別せずに判定する。
//
// 以前は発話ごとに本文全体を strings.ToLower してから探していた。上限規模（10 万発話・約 128MB）では
// 1 回の検索で本文と同じ量の文字列を作り直すことになり、検索時間の大半（約 470ms / 560ms）と
// メモリの返却（runtime.madvise）がそこに使われていた（負荷の高いときに応答時間の基準の
// 持ち分 1330ms を超えた）。本文を作り直さずに判定する。**結果は以前と同じ**（大文字・小文字の区別なし）。
type matcher struct {
	needle string
	// foldable は語句に大文字・小文字の違いを持つ文字が含まれるか。
	// 含まれない語句（日本語・数字・記号だけ）は、そのままの部分一致で判定できる。
	foldable bool
	// lowered は語句を小文字へそろえたもの（foldable のときだけ使う）。
	lowered string
}

func newMatcher(needle string) matcher {
	lowered := strings.ToLower(needle)
	return matcher{
		needle:   needle,
		foldable: lowered != needle || strings.ToUpper(needle) != needle,
		lowered:  lowered,
	}
}

func (m matcher) match(body string) bool {
	if !m.foldable {
		return strings.Contains(body, m.needle)
	}
	return containsFold(body, m.lowered)
}

// containsFold は s に sub が大文字・小文字を区別せずに含まれるかを返す（s を作り直さない）。
//
// sub は小文字へそろえ済みであること。比べ方は strings.ToLower と同じ対応（unicode.ToLower）。
// 全文字を 1 つずつ小文字へそろえると上限規模で約 500ms かかるため、まず「小文字へそろえると
// sub の先頭の文字になる文字」（例: s と S）の出現位置だけを速い探索で拾い、そこでだけ残りを比べる。
func containsFold(s, sub string) bool {
	if sub == "" {
		return true
	}
	first, _ := utf8.DecodeRuneInString(sub)
	starts := foldVariants(first)
	for i := 0; i < len(s); {
		j := strings.IndexAny(s[i:], starts)
		if j < 0 {
			return false
		}
		at := i + j
		if hasPrefixFold(s[at:], sub) {
			return true
		}
		_, size := utf8.DecodeRuneInString(s[at:])
		i = at + size
	}
	return false
}

// foldVariants は、小文字へそろえると r になる文字をすべて並べた文字列を返す。
//
// 大文字・小文字の対応の輪（unicode.SimpleFold）をたどって集める。例: k → "kK"（K はケルビン記号）。
func foldVariants(r rune) string {
	var b strings.Builder
	for v := r; ; {
		if unicode.ToLower(v) == r {
			b.WriteRune(v)
		}
		v = unicode.SimpleFold(v)
		if v == r {
			break
		}
	}
	return b.String()
}

// hasPrefixFold は s の先頭が sub（小文字へそろえ済み）と、大文字・小文字を区別せずに一致するかを返す。
func hasPrefixFold(s, sub string) bool {
	for _, want := range sub {
		if s == "" {
			return false
		}
		r, size := utf8.DecodeRuneInString(s)
		if unicode.ToLower(r) != want {
			return false
		}
		s = s[size:]
	}
	return true
}

// loadUtterances は 1 セッションの発話を読む（フロントマターは読み捨てる）。
func (s *Store) loadUtterances(id string) ([]Utterance, error) {
	path := filepath.Join(s.root, filepath.FromSlash(SessionFile(id)))
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("対話セッションを読み込めません（%s）: %w", id, err)
	}
	header, body, err := splitFrontMatter(data)
	if err != nil {
		return nil, fmt.Errorf("対話セッションのフロントマターを解釈できません（%s）: %w", id, err)
	}
	var sess Session
	if err := yaml.Unmarshal(header, &sess); err != nil {
		return nil, fmt.Errorf("対話セッションのフロントマターを解釈できません（%s）: %w", id, err)
	}
	return parseUtterances(body)
}

// excerptAround は本文から該当箇所を含む抜粋を作る。
//
// 改行は空白へ畳み（一覧が 1 行で読めるように）、前後を切った側には省略記号を付ける。
func excerptAround(body, lowered string) string {
	flat := strings.Join(strings.Fields(body), " ")
	idx := strings.Index(strings.ToLower(flat), lowered)
	if idx < 0 {
		// 改行の畳み込みで語句が空白をまたいだ場合。先頭からの抜粋に倒す（空文字を返さない）。
		idx = 0
	}
	runes := []rune(flat)
	// バイト位置 idx を文字位置へ直す。
	start := len([]rune(flat[:idx]))
	hitLen := len([]rune(lowered))

	from := start - excerptContextRunes
	if from < 0 {
		from = 0
	}
	to := start + hitLen + excerptContextRunes
	if to > len(runes) {
		to = len(runes)
	}
	var b strings.Builder
	if from > 0 {
		b.WriteString("…")
	}
	b.WriteString(string(runes[from:to]))
	if to < len(runes) {
		b.WriteString("…")
	}
	return b.String()
}

// SortHitsByID は該当発話をセッション ID・発話 ID の昇順へ並べ替える（表示順の安定化）。
func SortHitsByID(hits []UtteranceHit) {
	sort.SliceStable(hits, func(i, j int) bool {
		if hits[i].SessionID != hits[j].SessionID {
			return hits[i].SessionID < hits[j].SessionID
		}
		return hits[i].UtteranceID < hits[j].UtteranceID
	})
}
