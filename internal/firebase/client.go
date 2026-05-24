// Firebaseクライアントの初期化と管理を行うファイル。
// - Init() で Firebase アプリと Auth クライアントを初期化する
// - AuthClient() でミドルウェアなどから Auth クライアントを取得する
// - NewClient() は必要なら個別にクライアントを作りたいとき用（オプション）
//
// 認証情報の指定方法:
//   - 環境変数 FIREBASE_SERVICE_ACCOUNT_JSON にサービスアカウントJSONのパスを指定する
//     例: FIREBASE_SERVICE_ACCOUNT_JSON=./serviceAccountKey.json
//   - それが無い場合は FIREBASE_PROJECT_ID からプロジェクトIDを使って初期化を試みる
//     （ただしサービスアカウントJSONありの方が確実）
package firebase

import (
	"context"
	"fmt"
	"log"
	"os"

	firebase "firebase.google.com/go/v4"
	"firebase.google.com/go/v4/auth"
	"google.golang.org/api/option"
)

// グローバルな Firebase App と Auth クライアント
var fbApp *firebase.App
var globalAuthClient *auth.Client

// Client 構造体: 必要なら個別に使いたいとき用
type Client struct {
	Auth *auth.Client
}

// 内部ヘルパー: Firebase App を環境変数ベースで初期化する
func newFirebaseApp(ctx context.Context) (*firebase.App, error) {
	// サービスアカウントJSONのパス（推奨）
	saPath := os.Getenv("FIREBASE_SERVICE_ACCOUNT_JSON")

	// プロジェクトID
	projectID := os.Getenv("FIREBASE_PROJECT_ID")

	var app *firebase.App
	var err error

	if saPath != "" {
		// サービスアカウントJSONを使った初期化（本番でもローカルでもこれが一番確実）
		opt := option.WithCredentialsFile(saPath)
		app, err = firebase.NewApp(ctx, nil, opt)
	} else if projectID != "" {
		// JSONがない場合はプロジェクトIDのみで初期化を試みる
		// 環境によっては動かないこともあるので、基本は JSON 推奨
		config := &firebase.Config{ProjectID: projectID}
		app, err = firebase.NewApp(ctx, config)
	} else {
		app, err = firebase.NewApp(ctx, nil)
	}

	if err != nil {
		return nil, fmt.Errorf("error initializing firebase app: %w", err)
	}

	return app, nil
}

// Init: アプリ起動時に一度だけ呼び出して、グローバルな Auth クライアントを用意する
func Init() {
	ctx := context.Background()

	saPath := os.Getenv("FIREBASE_SERVICE_ACCOUNT_JSON")
	if saPath == "" {
		saPath = os.Getenv("GOOGLE_APPLICATION_CREDENTIALS")
	}
	saJSON := os.Getenv("FIREBASE_CREDENTIALS_JSON")
	projectID := os.Getenv("FIREBASE_PROJECT_ID")

	if saPath == "" && saJSON == "" && os.Getenv("AUTH_PROVIDER") != "firebase" {
		fbApp = nil
		globalAuthClient = nil
		return
	}

	var app *firebase.App
	var err error

	if saJSON != "" {
		opt := option.WithCredentialsJSON([]byte(saJSON))
		app, err = firebase.NewApp(ctx, nil, opt)
	} else if saPath != "" {
		// JSON が指定されている場合
		opt := option.WithCredentialsFile(saPath)
		app, err = firebase.NewApp(ctx, nil, opt)
	} else if projectID != "" {
		// JSON が無い場合は ProjectID で初期化（ローカル限定）
		config := &firebase.Config{ProjectID: projectID}
		app, err = firebase.NewApp(ctx, config)
	} else {
		app, err = firebase.NewApp(ctx, nil)
	}

	if err != nil {
		log.Printf("init firebase skipped: %v", err)
		return
	}

	authClient, err := app.Auth(ctx)
	if err != nil {
		log.Printf("init firebase auth skipped: %v", err)
		return
	}

	fbApp = app
	globalAuthClient = authClient
}

// AuthClient: ミドルウェアなどから Auth クライアントを取得するための関数
func AuthClient() (*auth.Client, error) {
	if globalAuthClient == nil {
		return nil, fmt.Errorf("firebase auth client is not configured")
	}
	return globalAuthClient, nil
}

// NewClient: 必要なら個別に Firebase クライアントを作りたいとき用（オプション）
// 今回の DeciScope では基本的に Init + AuthClient() で十分なので、
// 使わなくてもOK。将来 Worker とか別プロセスで使いたくなったら便利。
func NewClient(ctx context.Context) (*Client, error) {
	app, err := newFirebaseApp(ctx)
	if err != nil {
		return nil, fmt.Errorf("error initializing firebase app: %w", err)
	}

	authClient, err := app.Auth(ctx)
	if err != nil {
		return nil, fmt.Errorf("error getting firebase auth client: %w", err)
	}

	return &Client{Auth: authClient}, nil
}
