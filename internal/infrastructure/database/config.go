package database

import "os"

type Config struct {
	Driver string
	URL    string
}

func ConfigFromEnv() Config {
	driver := os.Getenv("DATABASE_DRIVER")
	if driver == "" {
		driver = "sqlite"
	}
	url := os.Getenv("DATABASE_URL")
	if url == "" {
		url = os.Getenv("SQLITE_PATH")
	}
	if url == "" {
		url = os.Getenv("AUTH_SQLITE_PATH")
	}
	if url == "" {
		url = "./db.sqlite"
	}
	return Config{Driver: driver, URL: url}
}
