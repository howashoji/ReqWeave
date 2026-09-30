//go:build integration

package projectstore

import (
	"errors"

	"gopkg.in/yaml.v3"
)

func asErrTooNew(err error, target **ErrTooNew) bool { return errors.As(err, target) }

func asErrNeedsMigration(err error, target **ErrNeedsMigration) bool { return errors.As(err, target) }

func asErrLockHeld(err error, target **ErrLockHeld) bool { return errors.As(err, target) }

func marshalLockInfo(info LockInfo) ([]byte, error) { return yaml.Marshal(info) }
