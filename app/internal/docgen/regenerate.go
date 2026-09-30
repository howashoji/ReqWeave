package docgen

// 本ファイルは差分再生成のアルゴリズムを担う。
//
// 前回生成以降の変更 ID 集合から影響章を決め、影響章のみ再生成する。
// 非影響章は前回本文をバイト単位で再利用する（変更の無い章の本文を AI に書き直させない）。

import (
	"context"
	"strings"
	"time"

	"github.com/howashoji/ReqWeave/app/internal/auditlog"
	"github.com/howashoji/ReqWeave/app/internal/projectstore"
)

// Regenerate は差分再生成を行う。
//
// ドラフトが無い場合は全章生成（Generate と同じ）になる。
func (g *Generator) Regenerate(ctx context.Context, kind string) (<-chan Event, error) {
	tmpl, err := LoadTemplate()
	if err != nil {
		return nil, err
	}
	doc, err := tmpl.Document(kind)
	if err != nil {
		return nil, err
	}
	records, err := g.records()
	if err != nil {
		return nil, err
	}
	previous, err := g.cfg.Store.LoadDraft(kind)
	if err != nil {
		return nil, err
	}

	reusable, err := g.reusableChapters(doc, records, previous)
	if err != nil {
		return nil, err
	}

	out := make(chan Event, 16)
	go g.run(ctx, doc, tmpl, records, out, reusable)
	return out, nil
}

// reusableChapters は再生成しない章（章ファイル名 → 前回本文）を返す。
func (g *Generator) reusableChapters(doc *DocumentTemplate, records Records,
	previous []projectstore.DocumentChapter) (reuse, error) {

	if len(previous) == 0 {
		return nil, nil // 前回のドラフトが無ければ全章を生成する
	}
	since := lastGeneratedAt(previous)
	changed, changedTerms, err := g.changedIDs(since)
	if err != nil {
		return nil, err
	}

	previousByName := map[string]projectstore.DocumentChapter{}
	for _, c := range previous {
		previousByName[c.FileName] = c
	}

	assignment := Assign(doc, records)
	out := reuse{}
	for _, src := range assignment.Sources {
		prev, ok := previousByName[src.Chapter.File]
		if !ok {
			continue // 前回に無い章は生成する
		}
		if affected(src, prev, changed, changedTerms) {
			continue
		}
		out[src.Chapter.File] = strings.TrimPrefix(prev.Body, chapterHeaderOf(prev))
	}
	return out, nil
}

// affected は当該章が影響章かを返す。
//
//   - 収載ソース ID 集合 ∩ 変更 ID 集合 ≠ 空
//   - 変更された用語を本文に含む章（用語の変更は当該用語を含む全章へ波及する）
func affected(src ChapterSources, prev projectstore.DocumentChapter,
	changed map[string]bool, changedTerms map[string]bool) bool {

	for _, id := range src.SourceIDs() {
		if changed[id] {
			return true
		}
	}
	for term := range changedTerms {
		if term != "" && strings.Contains(prev.Body, term) {
			return true
		}
	}
	return false
}

// changedIDs は since 以降に変更されたレコード ID と用語名を返す。
func (g *Generator) changedIDs(since time.Time) (map[string]bool, map[string]bool, error) {
	ids := map[string]bool{}
	terms := map[string]bool{}
	changes, err := auditlog.ReadChanges(g.cfg.Store.Root(), since, time.Time{})
	if err != nil {
		return nil, nil, err
	}
	for _, c := range changes {
		if c.At.Before(since) {
			continue
		}
		if isRecordID(c.Target) {
			ids[c.Target] = true
			continue
		}
		// レコード ID の形式でない対象は用語名（用語の変更は用語名を target に記録する）。
		terms[c.Target] = true
	}
	return ids, terms, nil
}

// isRecordID は変更履歴の対象がレコード ID かを返す。
func isRecordID(target string) bool {
	for _, re := range idPatterns {
		if re.MatchString(target) {
			return true
		}
	}
	return false
}

// lastGeneratedAt は前回のドラフトの生成日時（最大値）を返す。
func lastGeneratedAt(chapters []projectstore.DocumentChapter) time.Time {
	var latest time.Time
	for _, c := range chapters {
		if c.GeneratedAt.After(latest) {
			latest = c.GeneratedAt
		}
	}
	return latest
}

// chapterHeaderOf は章本文の冒頭メタ節を返す（再利用時に本文だけを取り出すため）。
//
// 冒頭メタ節は生成のたびに日時が変わるため、再利用では本文側だけを持ち回る。
func chapterHeaderOf(c projectstore.DocumentChapter) string {
	idx := strings.Index(c.Body, "\n\n")
	if idx < 0 {
		return ""
	}
	// メタ節は「# 見出し」＋箇条書き＋空行。最後の箇条書き行の直後までを返す。
	lines := strings.Split(c.Body, "\n")
	end := 0
	for i, line := range lines {
		if strings.HasPrefix(line, "- ") {
			end = i + 1
			continue
		}
		if end > 0 && strings.TrimSpace(line) == "" {
			end = i + 1
			break
		}
	}
	if end == 0 {
		return ""
	}
	return strings.Join(lines[:end], "\n") + "\n"
}
