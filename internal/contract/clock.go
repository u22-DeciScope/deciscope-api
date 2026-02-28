package contract

import "time"

type Clock interface {
	Now() time.Time
}
