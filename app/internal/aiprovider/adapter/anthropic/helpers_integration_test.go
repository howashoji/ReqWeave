//go:build integration

package anthropic

import (
	"errors"

	"github.com/howashoji/ReqWeave/app/internal/aiprovider"
)

var errKeyNotSet = errors.New("キーが未設定です")

func asProviderError(err error, target **aiprovider.ProviderError) bool {
	return errors.As(err, target)
}
