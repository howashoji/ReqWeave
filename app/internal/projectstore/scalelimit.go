package projectstore

// 本ファイルは規模上限の警告を担う。
//
// 上限は**性能保証の前提**であり、超過しても操作を拒否しない。100% 到達で「対象項目・現在値・
// 性能保証外である旨」を通知し、80%（既定）で予告する。判定は**保存のたびに再評価**する
// 規定のため、本パッケージは値をキャッシュしない（毎回プロジェクトフォルダの実体を数える）。
//
// 「同時 50 プロジェクト」だけはプロジェクトの外側の数であり、アプリ設定の
// `recent_projects`（settings.json）から数える。判定式は共通のため EvaluateScale を使う
// （呼び出し側 = バインディング層）。

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// ScaleKind は上限の対象（8 種）。
// 画面はこの値をラベルへ写像せず、ScaleUsage.Label をそのまま出す（生のコード値を出さない）。
type ScaleKind string

const (
	ScaleSessions       ScaleKind = "sessions"
	ScaleUtterances     ScaleKind = "utterances"
	ScaleRequirements   ScaleKind = "requirements"
	ScaleDecisions      ScaleKind = "decisions"
	ScaleOpenIssues     ScaleKind = "openIssues"
	ScaleQuestionnaires ScaleKind = "questionnaires"
	ScaleTotalBytes     ScaleKind = "totalBytes"
	ScaleProjects       ScaleKind = "projects"
)

// ScaleLimitBytes は総量の上限（500MB）。
const ScaleLimitBytes int64 = 500 * 1024 * 1024

// scaleLimit は上限目安（性能を保証する規模）。
//
// 8 種すべてをここに列挙する。表を分けると「どれかを実装し忘れる」ため、
// 判定・表示・テストのすべてが本表を唯一の入力にする。
var scaleLimit = map[ScaleKind]int64{
	ScaleSessions:       100,
	ScaleUtterances:     1000,
	ScaleRequirements:   1000,
	ScaleDecisions:      500,
	ScaleOpenIssues:     500,
	ScaleQuestionnaires: 100,
	ScaleTotalBytes:     ScaleLimitBytes,
	ScaleProjects:       50,
}

// scaleLabel は利用者向けの対象名（画面へはこの文言を出す。内部キーを出さない）。
var scaleLabel = map[ScaleKind]string{
	ScaleSessions:       "対話セッション",
	ScaleUtterances:     "1 セッションの発話",
	ScaleRequirements:   "要件項目",
	ScaleDecisions:      "決定事項",
	ScaleOpenIssues:     "未決事項",
	ScaleQuestionnaires: "質問票",
	ScaleTotalBytes:     "プロジェクトの総量",
	ScaleProjects:       "同時に扱うプロジェクト",
}

// ScaleKinds は上限の対象を宣言順で返す（表示順・網羅テストの入力）。
func ScaleKinds() []ScaleKind {
	return []ScaleKind{
		ScaleSessions, ScaleUtterances, ScaleRequirements, ScaleDecisions,
		ScaleOpenIssues, ScaleQuestionnaires, ScaleTotalBytes, ScaleProjects,
	}
}

// ScaleLimitOf は対象の上限目安を返す（未知の対象は 0）。
func ScaleLimitOf(kind ScaleKind) int64 { return scaleLimit[kind] }

// ScaleLabelOf は対象の利用者向け表記を返す。
func ScaleLabelOf(kind ScaleKind) string { return scaleLabel[kind] }

// 規模の状態。トークン上限（UsageLevel*）と同じ 3 段だが、**停止はしない**点が異なる。
const (
	ScaleLevelNone     = "none"     // 予告閾値未満
	ScaleLevelWarn     = "warn"     // 予告閾値以上・上限未満
	ScaleLevelExceeded = "exceeded" // 上限到達（100% 以上）。操作は継続できる
)

// ScaleWarnRatioMin / Max は予告閾値の許容範囲（70〜90%）。
const (
	ScaleWarnRatioMin = 0.70
	ScaleWarnRatioMax = 0.90
)

// DefaultScaleWarnRatio は予告閾値の既定（同上 = 80%）。
const DefaultScaleWarnRatio = 0.80

// ScaleWarnRatioOrDefault は予告閾値を許容範囲へ丸めて返す。
//
// 範囲外・未設定でも判定を止めない（設定ファイルの手編集で動作を止めない。settings.json と同じ方針）。
func ScaleWarnRatioOrDefault(ratio float64) float64 {
	switch {
	case ratio < ScaleWarnRatioMin:
		return DefaultScaleWarnRatio
	case ratio > ScaleWarnRatioMax:
		return DefaultScaleWarnRatio
	default:
		return ratio
	}
}

// ScaleUsage は上限 1 種ぶんの現在値と判定。
type ScaleUsage struct {
	// Kind は対象（ScaleKind）。画面はこの値を表示に使わない。
	Kind ScaleKind `json:"kind"`
	// Label は対象の利用者向け表記。
	Label string `json:"label"`
	// Scope は対象を特定する補足（発話なら対象セッション ID。無ければ空）。
	Scope string `json:"scope"`
	// Current は現在値、Limit は上限目安。
	Current int64 `json:"current"`
	Limit   int64 `json:"limit"`
	// Ratio は上限に対する割合（上限 0 なら 0）。
	Ratio float64 `json:"ratio"`
	// Level は ScaleLevel* のいずれか。
	Level string `json:"level"`
}

// EvaluateScale は現在値から判定を組み立てる（表示文言の組み立ては呼び出し側）。
func EvaluateScale(kind ScaleKind, current int64, scope string, warnRatio float64) ScaleUsage {
	limit := ScaleLimitOf(kind)
	u := ScaleUsage{
		Kind: kind, Label: ScaleLabelOf(kind), Scope: scope,
		Current: current, Limit: limit, Level: ScaleLevelNone,
	}
	if limit <= 0 {
		return u
	}
	u.Ratio = float64(current) / float64(limit)
	switch {
	case current >= limit:
		u.Level = ScaleLevelExceeded
	case u.Ratio >= ScaleWarnRatioOrDefault(warnRatio):
		u.Level = ScaleLevelWarn
	}
	return u
}

// ScaleUsageAll はプロジェクト内の 7 種を数えて判定を返す（同時プロジェクト数は対象外）。
//
// **値をキャッシュしない**（保存のたびに再評価する）。呼ぶたびに実体を数えるため、
// 派生インデックスと実体が食い違っても実体側が勝つ。
func (s *Store) ScaleUsageAll(warnRatio float64) ([]ScaleUsage, error) {
	sessions, worstSession, worstUtterances, err := s.countSessions()
	if err != nil {
		return nil, err
	}
	requirements, err := countMarkdownRecords(s.root, dirRequirements, func(id string) bool {
		_, _, _, ok := ParseRequirementID(id)
		return ok
	})
	if err != nil {
		return nil, err
	}
	decisions, err := countMarkdownRecords(s.root, dirDecisions, func(id string) bool {
		_, ok := IDDecision.Parse(id)
		return ok
	})
	if err != nil {
		return nil, err
	}
	openIssues, err := countMarkdownRecords(s.root, dirOpenIssues, func(id string) bool {
		_, ok := IDOpenIssue.Parse(id)
		return ok
	})
	if err != nil {
		return nil, err
	}
	questionnaires, err := s.countQuestionnaires()
	if err != nil {
		return nil, err
	}
	total, err := s.totalBytes()
	if err != nil {
		return nil, err
	}
	return []ScaleUsage{
		EvaluateScale(ScaleSessions, sessions, "", warnRatio),
		EvaluateScale(ScaleUtterances, worstUtterances, worstSession, warnRatio),
		EvaluateScale(ScaleRequirements, requirements, "", warnRatio),
		EvaluateScale(ScaleDecisions, decisions, "", warnRatio),
		EvaluateScale(ScaleOpenIssues, openIssues, "", warnRatio),
		EvaluateScale(ScaleQuestionnaires, questionnaires, "", warnRatio),
		EvaluateScale(ScaleTotalBytes, total, "", warnRatio),
	}, nil
}

// countSessions はセッション数と、発話が最も多いセッション（ID と件数）を返す。
// 発話の上限は「1 セッションあたり」のため、最も多い 1 件で代表させる。
func (s *Store) countSessions() (sessions int64, worstID string, worstCount int64, err error) {
	dir := filepath.Join(s.root, dirSessions)
	entries, rerr := os.ReadDir(dir)
	if os.IsNotExist(rerr) {
		return 0, "", 0, nil
	}
	if rerr != nil {
		return 0, "", 0, fmt.Errorf("対話セッションを走査できません: %w", rerr)
	}
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".md") {
			continue
		}
		id := strings.TrimSuffix(name, ".md")
		if _, ok := IDSession.Parse(id); !ok {
			continue
		}
		sessions++
		n, cerr := countUtterances(filepath.Join(dir, name))
		if cerr != nil {
			return 0, "", 0, cerr
		}
		if n > worstCount {
			worstCount, worstID = n, id
		}
	}
	return sessions, worstID, worstCount, nil
}

// countUtterances は発話ブロックの見出し（"### utt-nnnnn"）を数える。
func countUtterances(path string) (int64, error) {
	f, err := os.Open(path)
	if err != nil {
		return 0, fmt.Errorf("対話セッションを読み込めません: %w", err)
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), maxUtteranceLineBytes)
	var n int64
	for sc.Scan() {
		if _, ok := parseUtteranceHeading(sc.Text()); ok {
			n++
		}
	}
	if err := sc.Err(); err != nil {
		return 0, fmt.Errorf("対話セッションの読み出しに失敗: %w", err)
	}
	return n, nil
}

// countMarkdownRecords は `<dir>/<ID>.md` の形で置かれるレコードを数える。
func countMarkdownRecords(root, dir string, valid func(id string) bool) (int64, error) {
	entries, err := os.ReadDir(filepath.Join(root, dir))
	if os.IsNotExist(err) {
		return 0, nil
	}
	if err != nil {
		return 0, fmt.Errorf("%s を走査できません: %w", dir, err)
	}
	var n int64
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".md") {
			continue
		}
		if valid(strings.TrimSuffix(name, ".md")) {
			n++
		}
	}
	return n, nil
}

// countQuestionnaires は質問票（`questionnaires/QS-nnn/` のディレクトリ）を数える。
func (s *Store) countQuestionnaires() (int64, error) {
	entries, err := os.ReadDir(filepath.Join(s.root, dirQuestionnaires))
	if os.IsNotExist(err) {
		return 0, nil
	}
	if err != nil {
		return 0, fmt.Errorf("質問票を走査できません: %w", err)
	}
	var n int64
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		if _, ok := IDQuestionnaire.Parse(e.Name()); ok {
			n++
		}
	}
	return n, nil
}

// totalBytes はプロジェクトフォルダ全体のバイト数を返す。
//
// **既存資料 `imports/`（原本・抽出テキスト）を内数に含む**（独立の件数上限は設けない）。
// 除外は設けない: 除外リストを持つと「数えていないのに 500MB を名乗る」状態になり、
// 性能保証の前提としての意味が失われる。
func (s *Store) totalBytes() (int64, error) {
	var total int64
	err := filepath.WalkDir(s.root, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			// 走査中に消えたファイルは無視する（他のプロセスの操作で実体は動く）。
			if os.IsNotExist(err) {
				return nil
			}
			return err
		}
		if d.IsDir() {
			return nil
		}
		info, ierr := d.Info()
		if ierr != nil {
			if os.IsNotExist(ierr) {
				return nil
			}
			return ierr
		}
		total += info.Size()
		return nil
	})
	if err != nil {
		return 0, fmt.Errorf("プロジェクトの総量を数えられません: %w", err)
	}
	return total, nil
}
