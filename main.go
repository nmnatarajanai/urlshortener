package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"log"
	"net/http"
	"os"
	"strings"
	"time"

	_ "github.com/lib/pq"
)

// The 62 characters used for our short links
const base62Alphabet = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"

// App struct holds our database dependency
type App struct {
	DB *sql.DB
}

type ShortenRequest struct {
	URL string `json:"url"`
}

type ShortenResponse struct {
	ShortURL string `json:"short_url"`
}

type URLResponse struct {
	ShortURL string `json:"short_url"`
	LongURL  string `json:"long_url"`
}

func main() {
	// 1. Initialize Database from the environment
	connStr := os.Getenv("DATABASE_URL")
	if connStr == "" {
		log.Fatal("DATABASE_URL environment variable is required")
	}
	db, err := sql.Open("postgres", connStr)
	if err != nil {
		log.Fatalf("Failed to open database: %v", err)
	}
	defer db.Close()

	// 2. Production-Ready Connection Pooling
	db.SetMaxOpenConns(25)                 // Max simultaneous connections
	db.SetMaxIdleConns(25)                 // Max idle connections
	db.SetConnMaxLifetime(5 * time.Minute) // Safely recycle connections

	// Verify connection
	if err := db.Ping(); err != nil {
		log.Fatalf("Failed to connect to database: %v", err)
	}

	// 3. Create table if it doesn't exist
	// CockroachDB/Postgres schema. We use SERIAL to get an auto-incrementing integer ID.
	_, err = db.Exec(`
		CREATE TABLE IF NOT EXISTS urls (
			id SERIAL PRIMARY KEY,
			original_url TEXT NOT NULL
		);
	`)
	if err != nil {
		log.Fatalf("Failed to create table: %v", err)
	}

	app := &App{DB: db}
	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}

	// 4. Setup Routes
	mux := http.NewServeMux()
	mux.HandleFunc("/shorten", app.handleShorten)
	mux.HandleFunc("/urls", app.handleListURLs)
	mux.HandleFunc("/", app.handleRedirect)

	// 5. Production-Ready HTTP Server with Timeouts
	srv := &http.Server{
		Addr:         ":" + port,
		Handler:      mux,
		ReadTimeout:  5 * time.Second,  // Prevents slow-loris attacks
		WriteTimeout: 10 * time.Second, // Prevents hanging connections
		IdleTimeout:  120 * time.Second,
	}

	log.Printf("URL Shortener running on port %s...", port)
	if err := srv.ListenAndServe(); err != nil {
		log.Fatalf("Server failed: %v", err)
	}
}

// POST /shorten
// Takes a long URL, inserts it into the DB to get a unique ID, and encodes it.
func (a *App) handleShorten(w http.ResponseWriter, r *http.Request) {

	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req ShortenRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid JSON", http.StatusBadRequest)
		return
	}

	// Use a short context timeout for the database insert
	ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
	defer cancel()

	var id int
	// Insert and return the newly generated ID
	err := a.DB.QueryRowContext(ctx, "INSERT INTO urls (original_url) VALUES ($1) RETURNING id", req.URL).Scan(&id)
	if err != nil {
		log.Printf("DB Insert Error: %v", err)
		http.Error(w, "Database error", http.StatusInternalServerError)
		return
	}

	// Convert the DB ID to a Base62 short code
	shortCode := encodeBase62(id)

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(ShortenResponse{
		ShortURL: "http://" + r.Host + "/" + shortCode,
	})
}

// GET /urls
// Returns all stored short URLs and their original long URLs.
func (a *App) handleListURLs(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
	defer cancel()

	rows, err := a.DB.QueryContext(ctx, "SELECT id, original_url FROM urls ORDER BY id DESC")
	if err != nil {
		log.Printf("DB Query Error: %v", err)
		http.Error(w, "Database error", http.StatusInternalServerError)
		return
	}
	defer rows.Close()

	urls := make([]URLResponse, 0)
	for rows.Next() {
		var id int
		var originalURL string
		if err := rows.Scan(&id, &originalURL); err != nil {
			log.Printf("DB Row Error: %v", err)
			http.Error(w, "Database error", http.StatusInternalServerError)
			return
		}

		urls = append(urls, URLResponse{
			ShortURL: "http://" + r.Host + "/" + encodeBase62(id),
			LongURL:  originalURL,
		})
	}
	if err := rows.Err(); err != nil {
		log.Printf("DB Row Error: %v", err)
		http.Error(w, "Database error", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(urls)
}

// GET /{shortCode}
// Decodes the Base62 short code back into an ID, looks up the URL, and redirects.
func (a *App) handleRedirect(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Extract the shortcode from the path (e.g., "/abc" -> "abc")
	shortCode := strings.TrimPrefix(r.URL.Path, "/")
	if shortCode == "" {
		http.Error(w, "Missing short code", http.StatusBadRequest)
		return
	}

	// Convert Base62 back to DB ID
	id := decodeBase62(shortCode)

	ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
	defer cancel()

	var originalURL string
	err := a.DB.QueryRowContext(ctx, "SELECT original_url FROM urls WHERE id = $1", id).Scan(&originalURL)
	if err != nil {
		if err == sql.ErrNoRows {
			http.Error(w, "Link not found", http.StatusNotFound)
			return
		}
		log.Printf("DB Query Error: %v", err)
		http.Error(w, "Database error", http.StatusInternalServerError)
		return
	}

	// Perform the redirect (HTTP 302 Found)
	http.Redirect(w, r, originalURL, http.StatusFound)
}

// --- Base62 Encoding Helpers ---

// encodeBase62 converts a base-10 integer into a base-62 string
func encodeBase62(id int) string {
	if id == 0 {
		return string(base62Alphabet[0])
	}
	var encoded []byte
	for id > 0 {
		rem := id % 62
		encoded = append([]byte{base62Alphabet[rem]}, encoded...)
		id = id / 62
	}
	return string(encoded)
}

// decodeBase62 converts a base-62 string back into a base-10 integer
func decodeBase62(s string) int {
	var id int
	for _, char := range s {
		id = id*62 + strings.IndexRune(base62Alphabet, char)
	}
	return id
}
