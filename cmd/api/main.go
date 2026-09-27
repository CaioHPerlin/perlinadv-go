package main

import (
	"github.com/caiohperlin/perlinadv/internal/api"
	"log"
	"net/http"
	"os"
)

func main() {
	s, err := api.New()
	if err != nil {
		log.Fatal(err)
	}
	defer s.Close()
	addr := os.Getenv("ADDRESS")
	if addr == "" {
		addr = ":3000"
	}
	log.Printf("API listening on %s", addr)
	log.Fatal(http.ListenAndServe(addr, s.Router()))
}
