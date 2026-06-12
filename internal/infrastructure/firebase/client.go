package firebase

import (
	"context"
	"fmt"
	"os"

	firebaseadmin "firebase.google.com/go/v4"
	firebaseauth "firebase.google.com/go/v4/auth"
	"google.golang.org/api/option"
)

// NewAuthClient creates an isolated Firebase Auth client from environment
// configuration. A nil client means Firebase auth is intentionally disabled.
func NewAuthClient(ctx context.Context) (*firebaseauth.Client, error) {
	credentialsFile := os.Getenv("FIREBASE_SERVICE_ACCOUNT_JSON")
	if credentialsFile == "" {
		credentialsFile = os.Getenv("GOOGLE_APPLICATION_CREDENTIALS")
	}
	credentialsJSON := os.Getenv("FIREBASE_CREDENTIALS_JSON")
	projectID := os.Getenv("FIREBASE_PROJECT_ID")

	if credentialsFile == "" && credentialsJSON == "" && os.Getenv("AUTH_PROVIDER") != "firebase" {
		return nil, nil
	}

	var (
		app *firebaseadmin.App
		err error
	)
	switch {
	case credentialsJSON != "":
		app, err = firebaseadmin.NewApp(ctx, nil, option.WithCredentialsJSON([]byte(credentialsJSON)))
	case credentialsFile != "":
		app, err = firebaseadmin.NewApp(ctx, nil, option.WithCredentialsFile(credentialsFile))
	case projectID != "":
		app, err = firebaseadmin.NewApp(ctx, &firebaseadmin.Config{ProjectID: projectID})
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
