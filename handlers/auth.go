package handlers

import (
	"crypto/subtle"
	"fmt"
	"net/http"
	"os"
	"strings"

	"github.com/Artur-Galstyan/workcraft-stronghold/models"
	"github.com/golang-jwt/jwt/v5"
)

func AuthMiddleware(next http.HandlerFunc, hashedApiKey string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		// 1. Check for API key first (service-to-service)
		token := r.Header.Get("WORKCRAFT_API_KEY")
		if token != "" {
			// Use constant-time comparison for API key
			if subtle.ConstantTimeCompare([]byte(token), []byte(hashedApiKey)) == 1 {
				next.ServeHTTP(w, r)
				return
			}
			http.Error(w, "Unauthorized", http.StatusUnauthorized)
			return
		}

		// 2. Check for JWT cookie (browser-based)
		cookie, err := r.Cookie("workcraft_auth")
		if err != nil {
			// For API requests, return 401
			if strings.HasPrefix(r.URL.Path, "/api/") {
				http.Error(w, "Unauthorized", http.StatusUnauthorized)
				return
			}
			// For page requests, redirect to login
			http.Redirect(w, r, "/login", http.StatusSeeOther)
			return
		}

		// Validate JWT
		token = cookie.Value
		claims := &models.Claims{}
		jwtToken, err := jwt.ParseWithClaims(token, claims, func(token *jwt.Token) (interface{}, error) {
			if _, ok := token.Method.(*jwt.SigningMethodHMAC); !ok {
				return nil, fmt.Errorf("unexpected signing method: %v", token.Header["alg"])
			}
			return []byte(os.Getenv("WORKCRAFT_API_KEY")), nil
		})

		if err != nil || !jwtToken.Valid {
			// For API requests, return 401
			if strings.HasPrefix(r.URL.Path, "/api/") {
				http.Error(w, "Unauthorized", http.StatusUnauthorized)
				return
			}
			// For page requests, redirect to login
			http.Redirect(w, r, "/login", http.StatusSeeOther)
			return
		}

		// JWT is valid, set API key from claims and continue
		r.Header.Set("WORKCRAFT_API_KEY", claims.APIKey)
		next.ServeHTTP(w, r)
	}
}
