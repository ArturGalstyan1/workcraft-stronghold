package handlers

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/Artur-Galstyan/workcraft-stronghold/logger"
	"github.com/Artur-Galstyan/workcraft-stronghold/models"
	"github.com/golang-jwt/jwt/v5"
)

const (
	TokenExpiration = time.Hour * 24 // 24 hours
)

func AuthMiddleware(next http.HandlerFunc, apiKey string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		jwtSecret := os.Getenv("WORKCRAFT_JWT_SECRET")
		if jwtSecret == "" {
			jwtSecret = os.Getenv("WORKCRAFT_CHIEFTAIN_PASS")
		}
		token := r.Header.Get("WORKCRAFT_API_KEY")
		if token != "" {
			if subtle.ConstantTimeCompare([]byte(token), []byte(apiKey)) == 1 {
				// Successfully authenticated with API key
				next.ServeHTTP(w, r)
				return
			}
			http.Error(w, "Unauthorized", http.StatusUnauthorized)
			return
		}

		cookie, err := r.Cookie("workcraft_auth")
		if err != nil {
			if strings.HasPrefix(r.URL.Path, "/api/") || strings.HasPrefix(r.URL.Path, "/events") {
				logger.Log.Error("Unauthorised!", "error", err.Error())
				http.Error(w, "Unauthorized", http.StatusUnauthorized)
				return
			}
			http.Redirect(w, r, "/login", http.StatusSeeOther)
			return
		}

		token = cookie.Value
		claims := &models.Claims{}
		jwtToken, err := jwt.ParseWithClaims(token, claims, func(token *jwt.Token) (interface{}, error) {
			if _, ok := token.Method.(*jwt.SigningMethodHMAC); !ok {
				return nil, fmt.Errorf("unexpected signing method: %v", token.Header["alg"])
			}
			return []byte(jwtSecret), nil
		})

		if err != nil || !jwtToken.Valid {
			if strings.HasPrefix(r.URL.Path, "/api/") {
				logger.Log.Error("Unauthorised!", "error", err.Error())
				http.Error(w, "Unauthorized", http.StatusUnauthorized)
				return
			}
			http.Redirect(w, r, "/login", http.StatusSeeOther)
			return
		}

		ctx := context.WithValue(r.Context(), "username", claims.Username)
		r.Header.Set("WORKCRAFT_API_KEY", apiKey)
		next.ServeHTTP(w, r.WithContext(ctx))
	}
}

func LoginHandler(w http.ResponseWriter, r *http.Request) {
	jwtSecret := os.Getenv("WORKCRAFT_JWT_SECRET")
	if jwtSecret == "" {
		jwtSecret = os.Getenv("WORKCRAFT_CHIEFTAIN_PASS")
	}
	var creds models.Credentials
	if err := json.NewDecoder(r.Body).Decode(&creds); err != nil {
		logger.Log.Error("Invalid request body", "error", err.Error())
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	if creds.Username != os.Getenv("WORKCRAFT_CHIEFTAIN_USER") ||
		creds.Password != os.Getenv("WORKCRAFT_CHIEFTAIN_PASS") {
		logger.Log.Error("Invalid credentials!")
		http.Error(w, "Invalid credentials", http.StatusUnauthorized)
		return
	}

	now := time.Now()
	claims := models.Claims{
		Username: creds.Username,
		Role:     "admin",
		RegisteredClaims: jwt.RegisteredClaims{
			ExpiresAt: jwt.NewNumericDate(now.Add(TokenExpiration)),
			IssuedAt:  jwt.NewNumericDate(now),
			NotBefore: jwt.NewNumericDate(now),
		},
	}

	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	signedToken, err := token.SignedString([]byte(jwtSecret))
	if err != nil {
		logger.Log.Error("Failed to sign token", "err", err)
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}

	http.SetCookie(w, &http.Cookie{
		Name:     "workcraft_auth",
		Value:    signedToken,
		HttpOnly: true,
		Secure:   r.TLS != nil,
		SameSite: http.SameSiteStrictMode,
		Path:     "/",
		MaxAge:   int(TokenExpiration.Seconds()),
	})

	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]string{"status": "success"})
}
