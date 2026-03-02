package domain

import "time"

const (
	SessionTTL        = 30 * 24 * time.Hour
	LastSeenThrottle  = time.Minute
	SessionReasonUser = "logout"
)

type SessionSeed struct {
	UserID      string
	DeviceType  string
	DeviceName  string
	LoginMethod string
	UserAgent   string
	CreatedAt   time.Time
}

type Session struct {
	ID           string
	UserID       string
	DeviceType   string
	DeviceName   string
	LoginMethod  string
	UserAgent    string
	CreatedAt    time.Time
	LastSeenAt   time.Time
	RevokedAt    *time.Time
	RevokeReason string
}

func (s Session) IsExpired(now time.Time) bool {
	return !s.CreatedAt.Add(SessionTTL).After(now)
}

func (s Session) ShouldTouch(now time.Time) bool {
	return now.Sub(s.LastSeenAt) >= LastSeenThrottle
}
