package google

import (
	"context"
	"errors"
	"strings"

	sdk "google.golang.org/genai"

	"github.com/howashoji/ReqWeave/app/internal/aiprovider"
)

// StreamMessage はストリーミング対話。
//
// Google は SSE（alt=sse）の GenerateContentResponse チャンク列で届く。
// SDK は iter.Seq2 で提供するため、受信ごとに即座に StreamEvent へ変換して送出する。
func (a *Adapter) StreamMessage(ctx context.Context, req aiprovider.ChatRequest) (<-chan aiprovider.StreamEvent, error) {
	client, err := a.client(ctx)
	if err != nil {
		return nil, err
	}
	contents, config, err := buildRequest(req)
	if err != nil {
		return nil, err
	}

	sink, ch := aiprovider.NewStream()
	go func() {
		// 自前のゴルーチンのパニックは記録なしでプロセスごと落ちる。
		defer aiprovider.RecoverStreamPanic(sink, a.ID(), a.onPanic)
		for resp, err := range client.Models.GenerateContentStream(ctx, req.Model, contents, config) {
			if err != nil {
				if isCanceled(ctx, err) {
					sink.Done(true) // 利用者の中断
					return
				}
				pErr, ok := normalizeError(err).(*aiprovider.ProviderError)
				if !ok {
					pErr = aiprovider.NetworkError(aiprovider.ProviderGoogle, err)
				}
				sink.Fail(pErr)
				return
			}
			if resp == nil {
				continue
			}
			sink.Text(resp.Text())
			if u := resp.UsageMetadata; u != nil {
				sink.SetUsage(aiprovider.TokenUsage{
					InputTokens:     int(u.PromptTokenCount),
					OutputTokens:    int(u.CandidatesTokenCount),
					ReasoningTokens: int(u.ThoughtsTokenCount),
				})
			}
		}
		if ctx.Err() != nil {
			sink.Done(true)
			return
		}
		sink.Done(false)
	}()
	return ch, nil
}

// buildRequest は ChatRequest を SDK の入力へ写す（文脈を追加しない）。
func buildRequest(req aiprovider.ChatRequest) ([]*sdk.Content, *sdk.GenerateContentConfig, error) {
	if req.Model == "" {
		return nil, nil, errors.New("モデルが指定されていません")
	}
	if len(req.Messages) == 0 {
		return nil, nil, errors.New("送信するメッセージがありません")
	}
	contents := make([]*sdk.Content, 0, len(req.Messages))
	for _, m := range req.Messages {
		role := sdk.RoleUser
		if m.Role == aiprovider.RoleAssistant {
			role = sdk.RoleModel
		}
		contents = append(contents, sdk.NewContentFromText(m.Content, sdk.Role(role)))
	}
	config := &sdk.GenerateContentConfig{
		MaxOutputTokens: int32(req.EffectiveMaxOutputTokens()),
	}
	if req.System != "" {
		config.SystemInstruction = sdk.NewContentFromText(req.System, sdk.RoleUser)
	}
	// 推論努力レベル。空文字は非対応モデルでの縮退 = 送信しない。
	// Google は列挙値が大文字（LOW / MEDIUM / HIGH）のため写像値を変換する。
	if req.Effort.ReasoningLevel != "" {
		config.ThinkingConfig = &sdk.ThinkingConfig{
			ThinkingLevel: sdk.ThinkingLevel(strings.ToUpper(req.Effort.ReasoningLevel)),
		}
	}
	// 構造化出力。Google は responseMimeType と responseJsonSchema の組で受ける。
	// ResponseSchema（*genai.Schema）ではなく ResponseJsonSchema を使う: 上位が渡すのは
	// JSON Schema そのものであり、SDK 独自型へ写し替えると意味が変わりうるため。
	schema, ok, err := req.SchemaMap()
	if err != nil {
		return nil, nil, &aiprovider.ProviderError{
			Class:    aiprovider.ErrClassPermanent,
			Provider: aiprovider.ProviderGoogle,
			Code:     "invalid_response_schema",
			Message:  "構造化出力のスキーマを JSON として解釈できません",
		}
	}
	if ok {
		config.ResponseMIMEType = "application/json"
		config.ResponseJsonSchema = schema
	}
	return contents, config, nil
}

// isCanceled は ctx のキャンセル（＝利用者の中断）に起因する終了かを判定する。
func isCanceled(ctx context.Context, err error) bool {
	return ctx.Err() != nil || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded)
}
