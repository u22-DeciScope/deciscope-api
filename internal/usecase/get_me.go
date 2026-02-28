package usecase

import (
	"context"

	"deciscope-core-api/internal/domain"
)

type GetMeOutput struct {
	User    domain.User
	Session domain.Session
}

type GetMe struct{}

func NewGetMe() GetMe {
	return GetMe{}
}

func (GetMe) Execute(_ context.Context, authContext domain.AuthContext) (GetMeOutput, error) {
	return GetMeOutput{
		User:    authContext.User,
		Session: authContext.Session,
	}, nil
}
