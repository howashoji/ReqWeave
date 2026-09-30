package anthropic

import (
	"context"
	"errors"

	sdk "github.com/anthropics/anthropic-sdk-go"

	"github.com/howashoji/ReqWeave/app/internal/aiprovider"
)

// StreamMessage はストリーミング対話。
//
// Anthropic は SSE の型付きイベント（message_start / content_block_delta / message_delta /
// message_stop）で届く。本文差分は受信ごとに即座に送出し、本層で集約しない。
func (a *Adapter) StreamMessage(ctx context.Context, req aiprovider.ChatRequest) (<-chan aiprovider.StreamEvent, error) {
	client, err := a.client(ctx)
	if err != nil {
		return nil, err
	}
	params, err := buildParams(req)
	if err != nil {
		return nil, err
	}

	sink, ch := aiprovider.NewStream()
	stream := client.Messages.NewStreaming(ctx, params)
	go func() {
		// 自前のゴルーチンのパニックは記録なしでプロセスごと落ちる。
		defer aiprovider.RecoverStreamPanic(sink, a.ID(), a.onPanic)
		defer stream.Close()
		for stream.Next() {
			ev := stream.Current()
			switch ev.Type {
			case "message_start":
				// 入力トークンの実績は message_start の usage で届く。
				sink.SetUsage(aiprovider.TokenUsage{InputTokens: int(ev.Message.Usage.InputTokens)})
			case "content_block_delta":
				// thinking_delta は本文ではないため送出しない（表示対象は text のみ）。
				if ev.Delta.Type == "text_delta" {
					sink.Text(ev.Delta.Text)
				}
			case "message_delta":
				// 出力トークンの実績は message_delta の usage で届く。
				// Anthropic は推論トークンを出力トークンに含めて返すため ReasoningTokens は 0 のままとする。
				sink.SetUsage(aiprovider.TokenUsage{OutputTokens: int(ev.Usage.OutputTokens)})
			}
		}
		if err := stream.Err(); err != nil {
			if isCanceled(ctx, err) {
				sink.Done(true) // 利用者の中断
				return
			}
			pErr, ok := normalizeError(err).(*aiprovider.ProviderError)
			if !ok {
				pErr = aiprovider.NetworkError(aiprovider.ProviderAnthropic, err)
			}
			sink.Fail(pErr)
			return
		}
		sink.Done(false)
	}()
	return ch, nil
}

// buildParams は ChatRequest を SDK のパラメータへ写す（文脈を追加しない）。
func buildParams(req aiprovider.ChatRequest) (sdk.MessageNewParams, error) {
	if req.Model == "" {
		return sdk.MessageNewParams{}, errors.New("モデルが指定されていません")
	}
	if len(req.Messages) == 0 {
		return sdk.MessageNewParams{}, errors.New("送信するメッセージがありません")
	}
	msgs := make([]sdk.MessageParam, 0, len(req.Messages))
	for _, m := range req.Messages {
		block := sdk.NewTextBlock(m.Content)
		if m.Role == aiprovider.RoleAssistant {
			msgs = append(msgs, sdk.NewAssistantMessage(block))
			continue
		}
		msgs = append(msgs, sdk.NewUserMessage(block))
	}
	params := sdk.MessageNewParams{
		Model:     sdk.Model(req.Model),
		MaxTokens: int64(req.EffectiveMaxOutputTokens()),
		Messages:  msgs,
	}
	if req.System != "" {
		params.System = []sdk.TextBlockParam{{Text: req.System}}
	}
	// 推論努力レベル。空文字は非対応モデルでの縮退 = 送信しない。
	if req.Effort.ReasoningLevel != "" {
		params.OutputConfig.Effort = sdk.OutputConfigEffort(req.Effort.ReasoningLevel)
	}
	// 構造化出力。Anthropic は output_config.format の json_schema で受ける。
	schema, ok, err := req.SchemaMap()
	if err != nil {
		return sdk.MessageNewParams{}, &aiprovider.ProviderError{
			Class:    aiprovider.ErrClassPermanent,
			Provider: aiprovider.ProviderAnthropic,
			Code:     "invalid_response_schema",
			Message:  "構造化出力のスキーマを JSON として解釈できません",
		}
	}
	if ok {
		params.OutputConfig.Format = sdk.JSONOutputFormatParam{Schema: schema}
	}
	return params, nil
}

// isCanceled は ctx のキャンセル（＝利用者の中断）に起因する終了かを判定する。
func isCanceled(ctx context.Context, err error) bool {
	return ctx.Err() != nil || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded)
}
