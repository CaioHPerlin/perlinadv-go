package main

import (
	"database/sql"
	_ "github.com/jackc/pgx/v5/stdlib"
	"golang.org/x/crypto/bcrypt"
	"log"
	"os"
	"strings"
)

func main() {
	email := strings.ToLower(strings.TrimSpace(os.Getenv("ADMIN_EMAIL")))
	password := os.Getenv("ADMIN_PASSWORD")
	name := strings.TrimSpace(os.Getenv("ADMIN_NAME"))
	if email == "" || len(password) < 12 || name == "" {
		log.Fatal("ADMIN_EMAIL, ADMIN_NAME, and ADMIN_PASSWORD (12+ characters) required")
	}
	db, err := sql.Open("pgx", os.Getenv("DATABASE_URL"))
	if err != nil {
		log.Fatal(err)
	}
	defer db.Close()
	hashed, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		log.Fatal(err)
	}
	_, err = db.Exec(`INSERT INTO users(email,name,password_hash,role) VALUES($1,$2,$3,'ADMIN') ON CONFLICT(email) DO UPDATE SET name=excluded.name,password_hash=excluded.password_hash,role='ADMIN',active=TRUE,updated_at=now()`, email, name, string(hashed))
	if err != nil {
		log.Fatal(err)
	}
	log.Printf("administrator ready: %s", email)
}
