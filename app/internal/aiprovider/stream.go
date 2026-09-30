package aiprovider

import "encoding/json"

// 本ファイルはストリーミング対話の型と、イベントの発火経路を担う。

// Role は送信メッセージの役割。
type Role string

const (
	RoleUser      Role = "user"
	RoleAssistant Role = "assistant"
)

// Message は対話の 1 発話。送信範囲は対話エンジンが限定済み（本層は文脈を追加しない）。
type Message struct {
	Role    Role
	Content string
}

// ChatRequest はストリーミング対話の入力。
//
// 本層はこのリクエストを**そのまま送る**（文脈を追加しない）。
type ChatRequest struct {
	Model    string    // プロバイダ固有のモデル ID
	System   string    // システムプロンプト（対話エンジンが構築）
	Messages []Message // 送信範囲は対話エンジンが限定済みの文脈のみ

	// Effort はエフォート写像の結果。ReasoningLevel が空のときは推論努力パラメータを送らない
	// （非対応モデルでの縮退）。
	Effort EffortParams
	// MaxOutputTokens は実際に送る最大出力トークン。0 のときは Effort.MaxOutputTokens を用いる。
	//
	// 実装時決定: ChatRequest は MaxOutputTokens と Effort の双方を持つため、
	// 「最終値 = MaxOutputTokens、未指定なら写像値」と定める（写像値を上書きする経路を 1 つに絞る）。
	MaxOutputTokens int

	// ResponseSchema は構造化出力の JSON Schema。空 = 非構造化。
	//
	// 各アダプタが自社方式へ写す（Anthropic = output_config.format /
	// OpenAI = text.format の json_schema / Google = responseMimeType + responseJsonSchema）。
	// 専用メソッドは設けず StreamMessage の経路を共用する（プロバイダ追加時の変更範囲を
	// adapter/ 配下だけに保つ）。
	//
	// 本層はスキーマ検証をしない。応答がスキーマに適合するかの検証・失敗時の 1 回再要求・
	// 手動起票への縮退は、いずれも対話エンジンの責務である。
	ResponseSchema json.RawMessage

	// ImportRefs は取り込み分析の対象資料の識別。
	//
	// **送信記録専用のメタデータ**であり、プロバイダへ送るリクエスト本体には含めない
	// （送信記録の import_refs へそのまま記録する）。
	// 空でないリクエストは ConsentGiven が true でない限り送信されない（同意ゲート = import.go）。
	ImportRefs []ImportRef
	// ConsentGiven は担当者の同意操作済みフラグ。
	//
	// 同意 UI・送信前プレビューと同意を取る手順は上位（画面と対話エンジン）が持つ。
	// ImportRefs が空のリクエストでは参照しない（対話・生成の通常送信に影響しない）。
	ConsentGiven bool
}

// SchemaMap は ResponseSchema を map へ展開する（構造化出力を要求しないときは ok = false）。
//
// 各アダプタが自社のパラメータ型へ写すために使う。JSON として解釈できない場合は
// 入力不正（ErrClassPermanent）として扱えるようエラーを返す。
func (r ChatRequest) SchemaMap() (map[string]any, bool, error) {
	if len(r.ResponseSchema) == 0 {
		return nil, false, nil
	}
	var m map[string]any
	if err := json.Unmarshal(r.ResponseSchema, &m); err != nil {
		return nil, false, err
	}
	return m, true, nil
}

// EffectiveMaxOutputTokens は実際に送る最大出力トークンを返す。
func (r ChatRequest) EffectiveMaxOutputTokens() int {
	if r.MaxOutputTokens > 0 {
		return r.MaxOutputTokens
	}
	return r.Effort.MaxOutputTokens
}

// EventKind は StreamEvent の種別。
type EventKind int

const (
	// EventTextDelta は本文の差分（受信ごとに 1 件。本層で集約しない）。
	EventTextDelta EventKind = iota
	// EventUsage はトークン実績（EventDone / EventError の直前に最大 1 回）。
	EventUsage
	// EventDone は正常終了（中断も含む。Interrupted で区別する）。
	EventDone
	// EventError は異常終了。
	EventError
)

func (k EventKind) String() string {
	switch k {
	case EventTextDelta:
		return "text_delta"
	case EventUsage:
		return "usage"
	case EventDone:
		return "done"
	case EventError:
		return "error"
	default:
		return "unknown"
	}
}

// TokenUsage はプロバイダ応答の実績値（推定値で代用しない）。
type TokenUsage struct {
	InputTokens     int
	OutputTokens    int
	ReasoningTokens int
}

// StreamEvent はストリーミングの 1 イベント。
type StreamEvent struct {
	Kind  EventKind
	Text  string         // EventTextDelta のとき本文差分
	Usage *TokenUsage    // EventUsage のとき実績値
	Err   *ProviderError // EventError のとき
	// Interrupted は EventDone のとき、利用者の中断（ctx キャンセル）で終了したか。
	Interrupted bool
}

// streamBuffer はイベントチャネルのバッファ長。
// 受信側の一時的な遅延を吸収するためだけのもので、イベントの集約・待ち合わせは行わない。
const streamBuffer = 16

// StreamSink はアダプタがストリーミングイベントを送出する出口。
//
// EventUsage を EventDone / EventError の直前に必ず 1 回だけ流す規則を
// 構造的に担保するため、アダプタはチャネルへ直接書かず本型を通す。
//
// 呼び出し側の契約: 返されたチャネルは **EventDone または EventError が届くまで読み切る**。
// ctx をキャンセルした場合も同様（中断は EventDone{Interrupted:true} で通知される）。
type StreamSink struct {
	ch       chan StreamEvent
	usage    TokenUsage
	hasUsage bool
	closed   bool
}

// NewStream はイベントチャネルとその送出口を作る。
func NewStream() (*StreamSink, <-chan StreamEvent) {
	ch := make(chan StreamEvent, streamBuffer)
	return &StreamSink{ch: ch}, ch
}

// Text は本文差分を即座に送出する（バッファリング・集約をしない）。
func (s *StreamSink) Text(text string) {
	if s.closed || text == "" {
		return
	}
	s.ch <- StreamEvent{Kind: EventTextDelta, Text: text}
}

// SetUsage はトークン実績を記録する（送出は Done / Fail の直前）。
// 複数回呼ばれた場合、0 でない値のみを上書きする（プロバイダによって入力・出力が別イベントで届くため）。
func (s *StreamSink) SetUsage(u TokenUsage) {
	if u.InputTokens > 0 {
		s.usage.InputTokens = u.InputTokens
	}
	if u.OutputTokens > 0 {
		s.usage.OutputTokens = u.OutputTokens
	}
	if u.ReasoningTokens > 0 {
		s.usage.ReasoningTokens = u.ReasoningTokens
	}
	s.hasUsage = true
}

// currentUsage は受信済みの実績を返す。得られていなければ nil
// （推定値で代用しない。欠測は Usage=nil として上位へ伝える）。
func (s *StreamSink) currentUsage() *TokenUsage {
	if !s.hasUsage {
		return nil
	}
	u := s.usage
	return &u
}

// emitUsage は実績が得られている場合のみ EventUsage を送出する。
func (s *StreamSink) emitUsage() {
	if u := s.currentUsage(); u != nil {
		s.ch <- StreamEvent{Kind: EventUsage, Usage: u}
	}
}

// Done は正常終了（interrupted = true は利用者の中断）を通知してチャネルを閉じる。
func (s *StreamSink) Done(interrupted bool) {
	if s.closed {
		return
	}
	s.emitUsage()
	// EventDone にも実績を載せる（欠測時は Usage=nil）。
	s.ch <- StreamEvent{Kind: EventDone, Usage: s.currentUsage(), Interrupted: interrupted}
	s.closed = true
	close(s.ch)
}

// Fail は異常終了を通知してチャネルを閉じる。受信済みの実績があれば先に送出する。
func (s *StreamSink) Fail(err *ProviderError) {
	if s.closed {
		return
	}
	s.emitUsage()
	s.ch <- StreamEvent{Kind: EventError, Err: err}
	s.closed = true
	close(s.ch)
}
