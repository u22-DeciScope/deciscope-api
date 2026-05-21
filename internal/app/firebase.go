package app

import (
	"context"
	"log"

	firebase "firebase.google.com/go/v4"
	"firebase.google.com/go/v4/auth"
	"google.golang.org/api/option"
)

var FirebaseApp *firebase.App

func InitFirebase() {
	opt := option.WithCredentialsFile("serviceAccountKey.json")

	app, err := firebase.NewApp(context.Background(), nil, opt)
	if err != nil {
		log.Fatalf("🔥 Firebase init error: %v", err)
	}

	FirebaseApp = app
	log.Println("🔥 Firebase initialized")
}

func AuthClient() (*auth.Client, error) {
	return FirebaseApp.Auth(context.Background())
}

// firebase.go
// このファイルは使われなくなりましたが、不具合起きると嫌なのでなんとなく残しています
