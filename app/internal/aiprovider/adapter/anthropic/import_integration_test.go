//go:build integration

// 結合テスト（HTTP 境界）。ImportRefs は送信記録専用のメタデータであり、
// プロバイダへ送るリクエスト本体には含めない。

package anthropic

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/howashoji/ReqWeave/app/internal/aiprovider"
)

func TestImportRefsNotSentToProvider(t *testing.T) {
	var raw []byte
	a := newTestAdapter(t, func(w http.ResponseWriter, r *http.Request) {
		raw, _ = io.ReadAll(r.Body)
		sseHeader(w)
	})

	req := testRequest()
	req.ImportRefs = []aiprovider.ImportRef{{
		ID:         "IMP-001",
		SourceName: "genba-flow.docx",
		ImportedAt: time.Date(2026, 8, 31, 1, 2, 3, 0, time.UTC),
	}}
	req.ConsentGiven = true

	ch, err := a.StreamMessage(context.Background(), req)
	if err != nil {
		t.Fatalf("開始に失敗: %v", err)
	}
	collect(t, ch)

	if len(raw) == 0 {
		t.Fatal("リクエスト本体が取得できていない（検証が空振りしている）")
	}
	body := string(raw)
	for _, forbidden := range []string{"IMP-001", "genba-flow.docx", "import_refs", "importRefs", "consent"} {
		if strings.Contains(body, forbidden) {
			t.Errorf("記録専用メタデータ %q がリクエスト本体に含まれている: %s", forbidden, body)
		}
	}
	// 本来の送信内容は載っていること（検証が「空のリクエスト」で通っていないことの確認）。
	if !strings.Contains(body, "こんにちは") {
		t.Errorf("送信本文が含まれていない: %s", body)
	}
}
