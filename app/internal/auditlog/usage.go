package auditlog

// AI 利用量の集計（「期間別トークン消費」と「上限判定用の累計」）。
//
// すべて既存レコード（audit/ai-log）からの読み出し集計であり、集計値を永続化しない
// （実績レコードと二重に管理しない）。AI 呼び出しを行わないため、オフライン・API 障害中でも
// 成立する。

import (
	"sort"
	"time"
)

// TokenTotals はトークン実績の合計（記録の tokens_in / tokens_out / tokens_reasoning）。
type TokenTotals struct {
	In        int `json:"in"`
	Out       int `json:"out"`
	Reasoning int `json:"reasoning"`
	// Total は 3 種の合算（表示・上限判定はこの値を使う）。
	Total int `json:"total"`
}

func (t *TokenTotals) add(rec AISendRecord) {
	if rec.TokensIn != nil {
		t.In += *rec.TokensIn
	}
	if rec.TokensOut != nil {
		t.Out += *rec.TokensOut
	}
	if rec.TokensReasoning != nil {
		t.Reasoning += *rec.TokensReasoning
	}
	t.Total = t.In + t.Out + t.Reasoning
}

// UsageBucket は内訳 1 軸の 1 行（プロバイダ別・対話セッション別・作業者別）。
//
// Key は生の値（プロバイダ名・S-nnnn・利用者 ID）。**セッション文脈を持たない送信（取り込み分析）の
// Key は空文字**であり、利用者向けの表示名への変換は呼び出し側（画面）が行う
// （集計層は表示ラベルを持たない。ラベルの対応表は UI 側の責務）。
type UsageBucket struct {
	Key    string      `json:"key"`
	Tokens TokenTotals `json:"tokens"`
	// Sends は当該キーの送信件数。Missing はそのうちトークン実績が無い（欠測）件数。
	Sends   int `json:"sends"`
	Missing int `json:"missing"`
}

// UsageSummary は期間集計の結果（期間指定なし = 全期間累計はトークン上限の判定に使う）。
type UsageSummary struct {
	// From / To は集計期間（境界を含む）。ゼロ値は無制限（全期間）。
	From time.Time `json:"from"`
	To   time.Time `json:"to"`

	Tokens TokenTotals `json:"tokens"`
	Sends  int         `json:"sends"`
	// Missing はトークン実績の無い送信の件数（欠測 = API が実績を返さなかった異常時）。
	// 欠測は 0 として合算したうえで件数を別に示す（推定値で代用せず、黙って無視もしない）。
	Missing int `json:"missing"`
	// LastUsedAt は期間内で最後に AI を呼び出した日時（ゼロ値 = 期間内の送信なし）。画面の「直近の利用日時」。
	LastUsedAt time.Time `json:"lastUsedAt"`

	ByProvider []UsageBucket `json:"byProvider"`
	BySession  []UsageBucket `json:"bySession"`
	ByAuthor   []UsageBucket `json:"byAuthor"`
}

// AggregateUsage は期間内の AI 利用量を集計する。
//
// from / to は境界を含む。ゼロ値は無制限のため、両方をゼロ値にすると全期間累計になる（= TotalUsage）。
// 読み出しは ReadAISends に委ね（送信行と実績行の id マージ・期間端の月の前後 1 か月読み）、
// 本関数は合算と内訳の組立てだけを行う。
func AggregateUsage(root string, from, to time.Time) (UsageSummary, error) {
	sends, err := ReadAISends(root, from, to)
	if err != nil {
		return UsageSummary{}, err
	}
	return AggregateSends(sends, from, to), nil
}

// TotalUsage は全期間の累計を返す（トークン上限の判定が使う。判定のたびに再集計して
// 他端末の追記を取り込む）。期間集計と同一の実装から得る（二重実装しない）。
func TotalUsage(root string) (UsageSummary, error) {
	return AggregateUsage(root, time.Time{}, time.Time{})
}

// AggregateSends は読み出し済みの送信記録から集計を組み立てる（AggregateUsage の純粋部分）。
// 期間外のレコードが混じっていても from / to で除外する。
func AggregateSends(sends []AISendRecord, from, to time.Time) UsageSummary {
	out := UsageSummary{From: from, To: to}
	byProvider := map[string]*UsageBucket{}
	bySession := map[string]*UsageBucket{}
	byAuthor := map[string]*UsageBucket{}

	for _, rec := range sends {
		if !inPeriod(rec.At, from, to) {
			continue
		}
		out.Sends++
		out.Tokens.add(rec)
		if !rec.HasTokens() {
			out.Missing++
		}
		if rec.At.After(out.LastUsedAt) {
			out.LastUsedAt = rec.At
		}
		addTo(byProvider, rec.Provider, rec)
		addTo(bySession, rec.Session, rec)
		addTo(byAuthor, rec.Author, rec)
	}

	out.ByProvider = sortedBuckets(byProvider)
	out.BySession = sortedBuckets(bySession)
	out.ByAuthor = sortedBuckets(byAuthor)
	return out
}

func addTo(m map[string]*UsageBucket, key string, rec AISendRecord) {
	b, ok := m[key]
	if !ok {
		b = &UsageBucket{Key: key}
		m[key] = b
	}
	b.Sends++
	b.Tokens.add(rec)
	if !rec.HasTokens() {
		b.Missing++
	}
}

// sortedBuckets は消費の多い順（同値はキーの昇順）で並べる。並び順を固定して表示を安定させる。
func sortedBuckets(m map[string]*UsageBucket) []UsageBucket {
	out := make([]UsageBucket, 0, len(m))
	for _, b := range m {
		out = append(out, *b)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Tokens.Total != out[j].Tokens.Total {
			return out[i].Tokens.Total > out[j].Tokens.Total
		}
		return out[i].Key < out[j].Key
	})
	return out
}
