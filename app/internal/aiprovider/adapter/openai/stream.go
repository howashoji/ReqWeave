package openai

import (
	"context"
	"errors"

	"github.com/openai/openai-go/v3/packages/param"
	"github.com/openai/openai-go/v3/responses"
	"github.com/openai/openai-go/v3/shared"

	"github.com/howashoji/ReqWeave/app/internal/aiprovider"
)

// StreamMessage はストリーミング対話。
//
// 実装時決定（OpenAI SDK の API 系統）: SDK 推奨の Responses 系統を用いる。
// 系統差は本メソッド内で StreamEvent へ変換して吸収し、上位への影響を持たない。
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
	stream := client.Responses.NewStreaming(ctx, params)
	go func() {
		// 自前のゴルーチンのパニックは記録なしでプロセスごと落ちる。
		defer aiprovider.RecoverStreamPanic(sink, a.ID(), a.onPanic)
		defer stream.Close()
		for stream.Next() {
			ev := stream.Current()
			switch ev.Type {
			case "response.output_text.delta":
				sink.Text(ev.Delta)
			case "response.completed", "response.incomplete":
				u := ev.Response.Usage
				sink.SetUsage(aiprovider.TokenUsage{
					InputTokens:     int(u.InputTokens),
					OutputTokens:    int(u.OutputTokens),
					ReasoningTokens: int(u.OutputTokensDetails.ReasoningTokens),
				})
			case "error", "response.failed":
				sink.Fail(&aiprovider.ProviderError{
					Class:    aiprovider.ErrClassConfig,
					Provider: aiprovider.ProviderOpenAI,
					Code:     ev.Code,
					Message:  ev.Message,
				})
				return
			}
		}
		if err := stream.Err(); err != nil {
			if isCanceled(ctx, err) {
				sink.Done(true) // 利用者の中断
				return
			}
			pErr, ok := normalizeError(err).(*aiprovider.ProviderError)
			if !ok {
				pErr = aiprovider.NetworkError(aiprovider.ProviderOpenAI, err)
			}
			sink.Fail(pErr)
			return
		}
		sink.Done(false)
	}()
	return ch, nil
}

// buildParams は ChatRequest を SDK のパラメータへ写す（文脈を追加しない）。
func buildParams(req aiprovider.ChatRequest) (responses.ResponseNewParams, error) {
	if req.Model == "" {
		return responses.ResponseNewParams{}, errors.New("モデルが指定されていません")
	}
	if len(req.Messages) == 0 {
		return responses.ResponseNewParams{}, errors.New("送信するメッセージがありません")
	}
	items := make(responses.ResponseInputParam, 0, len(req.Messages))
	for _, m := range req.Messages {
		role := responses.EasyInputMessageRoleUser
		if m.Role == aiprovider.RoleAssistant {
			role = responses.EasyInputMessageRoleAssistant
		}
		items = append(items, responses.ResponseInputItemParamOfMessage(m.Content, role))
	}
	params := responses.ResponseNewParams{
		Model:           shared.ResponsesModel(req.Model),
		Input:           responses.ResponseNewParamsInputUnion{OfInputItemList: items},
		MaxOutputTokens: param.NewOpt(int64(req.EffectiveMaxOutputTokens())),
		// 送信データをプロバイダ側に保存させない（送信範囲を限定するのと同じ方針）。
		Store: param.NewOpt(false),
	}
	if req.System != "" {
		params.Instructions = param.NewOpt(req.System)
	}
	// 推論努力レベル。空文字は非対応モデルでの縮退 = 送信しない。
	if req.Effort.ReasoningLevel != "" {
		params.Reasoning = shared.ReasoningParam{Effort: shared.ReasoningEffort(req.Effort.ReasoningLevel)}
	}
	// 構造化出力。Responses API は text.format の json_schema で受ける。
	//
	// strict は指定しない: strict モードは additionalProperties: false と全プロパティの required を
	// 要求するため、上位が渡したスキーマの意味を変えずには適合させられない場合がある。
	// 応答の検証は対話エンジン側が行う。
	schema, ok, err := req.SchemaMap()
	if err != nil {
		return responses.ResponseNewParams{}, &aiprovider.ProviderError{
			Class:    aiprovider.ErrClassPermanent,
			Provider: aiprovider.ProviderOpenAI,
			Code:     "invalid_response_schema",
			Message:  "構造化出力のスキーマを JSON として解釈できません",
		}
	}
	if ok {
		params.Text = responses.ResponseTextConfigParam{
			Format: responses.ResponseFormatTextConfigUnionParam{
				OfJSONSchema: &responses.ResponseFormatTextJSONSchemaConfigParam{
					Name:   structuredOutputName,
					Schema: schema,
				},
			},
		}
	}
	return params, nil
}

// structuredOutputName は Responses API が要求する構造化出力の名前（a-z / A-Z / 0-9 / _ / - のみ）。
// 応答の内容には影響しないため固定値でよい。
const structuredOutputName = "reqweave_structured_output"

// isCanceled は ctx のキャンセル（＝利用者の中断）に起因する終了かを判定する。
func isCanceled(ctx context.Context, err error) bool {
	return ctx.Err() != nil || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded)
}
