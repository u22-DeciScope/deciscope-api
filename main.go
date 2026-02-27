package main

import (
	"net/http"

	"github.com/go-chi/chi/v5"
)

func main() {
	r := chi.NewRouter()
	r.Get("/", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("hello chi"))
	})

	if err := http.ListenAndServe(":8080", r); err != nil {
		panic(err)
	}
}
