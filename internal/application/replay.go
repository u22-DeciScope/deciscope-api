package application

import "context"

type ReplayStatus struct {
	MeetingID string
	Fixture   string
	Status    string
	StartedAt string
}

type FixtureInfo struct {
	Name string
	Path string
}

type ReplayController interface {
	FixtureDir() string
	ListFixtures() ([]FixtureInfo, error)
	Start(ctx context.Context, meetingID, fixtureName string) (*ReplayStatus, error)
	Pause(meetingID string) (*ReplayStatus, error)
	Resume(meetingID string) (*ReplayStatus, error)
	Reset(ctx context.Context, meetingID string) error
}
