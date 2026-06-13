package firebase

import (
	"context"
	"fmt"

	firebaseadmin "firebase.google.com/go/v4"
	firebaseauth "firebase.google.com/go/v4/auth"
	"google.golang.org/api/option"
)

type Config struct {
	CredentialsFile string
	CredentialsJSON string
	ProjectID       string
	Enabled         bool
}

// NewAuthClient creates an isolated Firebase Auth client from configuration.
// A nil client means Firebase auth is intentionally disabled.
func NewAuthClient(ctx context.Context, config Config) (*firebaseauth.Client, error) {
	if config.CredentialsFile == "" && config.CredentialsJSON == "" && !config.Enabled {
		return nil, nil
	}

	var (
		app *firebaseadmin.App
		err error
	)
	switch {
	case config.CredentialsJSON != "":
		app, err = firebaseadmin.NewApp(ctx, nil, option.WithCredentialsJSON([]byte(config.CredentialsJSON)))
	case config.CredentialsFile != "":
		app, err = firebaseadmin.NewApp(ctx, nil, option.WithCredentialsFile(config.CredentialsFile))
	case config.ProjectID != "":
		app, err = firebaseadmin.NewApp(ctx, &firebaseadmin.Config{ProjectID: config.ProjectID})
	default:
		app, err = firebaseadmin.NewApp(ctx, nil)
	}
	if err != nil {
		return nil, fmt.Errorf("initialize firebase app: %w", err)
	}

	client, err := app.Auth(ctx)
	if err != nil {
		return nil, fmt.Errorf("initialize firebase auth: %w", err)
	}
	return client, nil
}
