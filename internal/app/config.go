package app

import (
	"os"
	"strings"

	"deciscope-core-api/internal/infrastructure/database"
	"deciscope-core-api/internal/infrastructure/firebase"

	"github.com/joho/godotenv"
)

type Config struct {
	Database            database.Config
	Firebase            firebase.Config
	UploadDir           string
	FixtureDir          string
	FrontendURL         string
	AllowedOrigins      string
	SessionCookieSecure bool
}

func ConfigFromEnv() Config {
	return Config{
		Database: database.Config{URL: os.Getenv("DATABASE_URL")},
		Firebase: firebase.Config{
			CredentialsFile: firstNonEmpty(os.Getenv("FIREBASE_SERVICE_ACCOUNT_JSON"), os.Getenv("GOOGLE_APPLICATION_CREDENTIALS")),
			CredentialsJSON: os.Getenv("FIREBASE_CREDENTIALS_JSON"),
			ProjectID:       os.Getenv("FIREBASE_PROJECT_ID"),
			Enabled:         os.Getenv("AUTH_PROVIDER") == "firebase",
		},
		UploadDir:           os.Getenv("UPLOAD_DIR"),
		FixtureDir:          os.Getenv("FIXTURE_DIR"),
		FrontendURL:         os.Getenv("FRONTEND_URL"),
		AllowedOrigins:      os.Getenv("ALLOWED_ORIGINS"),
		SessionCookieSecure: strings.EqualFold(os.Getenv("SESSION_COOKIE_SECURE"), "true"),
	}
}

func LoadEnvironmentFiles() {
	_ = godotenv.Load(".env")
	_ = godotenv.Overload(".env.local")
}

func ListenAddressFromEnv() string {
	port := os.Getenv("PORT")
	if port == "" {
		port = "9090"
	}
	return ":" + port
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}
