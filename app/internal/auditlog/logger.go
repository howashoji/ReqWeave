// Package auditlog は監査ログ。AI送信記録・変更履歴・トークン実績を追記専用で記録する。
//
// 記録にシークレットキー・認証情報を含めない（それを持つフィールドを型に定義しない）。
package auditlog

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/howashoji/ReqWeave/app/internal/masking"
)

// 監査データの配置（プロジェクトフォルダからの相対パス）。
const (
	DirAILog   = "audit/ai-log"
	DirHistory = "audit/history"
	// DirSyncLog は同期の記録。ai-log / history と同一の追記方式。
	DirSyncLog = "audit/sync-log"
	fileExt    = ".ndjson"
)

// Sink は追記の実行先。プロジェクトストアの保存キュー（書き込みを直列化する）が実装する。
//
// 追記専用ファイルは一時ファイル + rename ではなく O_APPEND 追記とする（行単位で自己完結させる）。
type Sink interface {
	// AppendFile はプロジェクトフォルダ内の相対パスへ 1 行を追記する。
	AppendFile(rel string, line []byte) error
}

// Logger は 1 プロジェクト・1 作業者ぶんの監査記録の入口。
type Logger struct {
	sink     Sink
	authorID string
}

// New は作業者 authorID として記録する Logger を返す。
func New(sink Sink, authorID string) (*Logger, error) {
	if sink == nil {
		return nil, fmt.Errorf("監査記録の書き込み先がありません")
	}
	if authorID == "" {
		return nil, fmt.Errorf("記録する作業者のメールアドレス（利用者 ID）がありません")
	}
	return &Logger{sink: sink, authorID: authorID}, nil
}

// RecordChange は変更履歴を 1 行追記する。
func (l *Logger) RecordChange(rec ChangeRecord) error {
	if rec.Target == "" {
		return fmt.Errorf("変更履歴の対象（target）がありません")
	}
	if rec.Change == "" {
		return fmt.Errorf("変更履歴の変更種別（change）がありません")
	}
	rec.Author = l.authorID
	if rec.At.IsZero() {
		rec.At = time.Now()
	}
	rec.At = rec.At.UTC()
	return l.append(DirHistory, rec.At, rec)
}

// RecordAISend は送信行を 1 行追記し、送信 ID を返す（1 送信は送信行 + 実績行の 2 行で記録する）。
//
// 呼び出しは HTTP 送信の直前。トークン実績は後から RecordAIUsage で追記する。
// rec.ID が空のときは本メソッドが採番する。
func (l *Logger) RecordAISend(rec AISendRecord) (string, error) {
	if rec.Provider == "" || rec.Model == "" {
		return "", fmt.Errorf("AI 送信記録の宛先（provider / model）がありません")
	}
	if rec.Prompt == "" {
		return "", fmt.Errorf("AI 送信記録の送信本文がありません")
	}
	if rec.ID == "" {
		rec.ID = uuid.NewString()
	}
	rec.Author = l.authorID
	if rec.At.IsZero() {
		rec.At = time.Now()
	}
	rec.At = rec.At.UTC()
	// 実績は実績行で追記する（送信行に実績を載せない。追記専用で送信行を後から書き換えられないため）。
	rec.TokensIn, rec.TokensOut, rec.TokensReasoning = nil, nil, nil
	if err := l.append(DirAILog, rec.At, rec); err != nil {
		return "", err
	}
	return rec.ID, nil
}

// RecordAIUsage は送信 ID に紐づく実績行を追記する（応答を受け取った後）。
//
// 実績が得られなかった送信では呼ばない（欠測。推定値で代用しない）。
func (l *Logger) RecordAIUsage(sendID string, in, out, reasoning int) error {
	if sendID == "" {
		return fmt.Errorf("トークン実績に紐づける送信 ID がありません")
	}
	rec := AISendRecord{ID: sendID, At: time.Now().UTC(), Author: l.authorID}
	rec.SetTokens(in, out, reasoning)
	return l.append(DirAILog, rec.At, rec)
}

// RecordSync は同期の記録を 1 行追記する。
//
// **認証情報を含めない**。同期先の所在は資格情報部を除去してから記録する
// （同期モジュール側でも除去済みだが、記録の直前でもう一度落とす二重の防御）。
func (l *Logger) RecordSync(rec SyncRecord) error {
	if rec.Op == "" {
		return fmt.Errorf("同期の記録の操作（op）がありません")
	}
	if rec.Result == "" {
		return fmt.Errorf("同期の記録の結果（result）がありません")
	}
	rec.Author = l.authorID
	if rec.At.IsZero() {
		rec.At = time.Now()
	}
	rec.At = rec.At.UTC()
	rec.RemoteLocation = masking.StripURLCredentials(rec.RemoteLocation)
	return l.append(DirSyncLog, rec.At, rec)
}

// append は 1 レコードを NDJSON の 1 行として追記する。
// 行単位で自己完結させるため、本文中の改行は JSON エンコードで \n に畳まれる。
func (l *Logger) append(dir string, at time.Time, rec any) error {
	line, err := json.Marshal(rec)
	if err != nil {
		return fmt.Errorf("監査記録を組み立てられません: %w", err)
	}
	line = append(line, '\n')
	return l.sink.AppendFile(FileName(dir, at, l.authorID), line)
}

// FileName は月別 × 作業者別のファイル名を返す（共同作業で作業者ごとのファイルが衝突しないように）。
// 例: audit/history/2026-08.k%2esato%40example%2eco%2ejp.ndjson
func FileName(dir string, at time.Time, authorID string) string {
	return fmt.Sprintf("%s/%s.%s%s", dir, at.UTC().Format("2006-01"), SafeAuthorFileName(authorID), fileExt)
}

// SafeAuthorFileName は利用者 ID をファイル名に使える表記へ変換する。
//
// 英小文字・数字・ハイフン以外を %xx（小文字16進）へ符号化する。利用者 ID は登録時に
// 小文字化済みのため、大文字小文字を区別しないファイルシステムでも異なる ID が衝突しない。
func SafeAuthorFileName(authorID string) string {
	var b strings.Builder
	for _, c := range []byte(authorID) {
		switch {
		case c >= 'a' && c <= 'z', c >= '0' && c <= '9', c == '-':
			b.WriteByte(c)
		default:
			fmt.Fprintf(&b, "%%%02x", c)
		}
	}
	return b.String()
}
