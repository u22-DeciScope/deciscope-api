package firebase

import (
	"context"
	"fmt"
	"os"

	firebase "firebase.google.com/go/v4"
	"firebase.google.com/go/v4/auth"
	"google.golang.org/api/option"
)

type Client struct {
	Auth *auth.Client
}

func NewClient(ctx context.Context) (*Client, error) {
	// FirebaseコンソールからダウンロードしたサービスアカウントJSONのパス
	saPath := os.Getenv("FIREBASE_SERVICE_ACCOUNT_JSON")

	var app *firebase.App
	var err error

	if saPath != "" {
		opt := option.WithCredentialsFile(saPath)
		app, err = firebase.NewApp(ctx, nil, opt)
	} else {
		// JSONがない場合はプロジェクトID指定（公開鍵のみのローカル検証など、環境によっては動きますがJSONありが確実です）
		config := &firebase.Config{ProjectID: "deciscope-2733c"}
		app, err = firebase.NewApp(ctx, config)
	}

	if err != nil {
		return nil, fmt.Errorf("error initializing firebase app: %v", err)
	}

	authClient, err := app.Auth(ctx)
	if err != nil {
		return nil, fmt.Errorf("error getting firebase auth client: %v", err)
	}

	return &Client{Auth: authClient}, nil
}
