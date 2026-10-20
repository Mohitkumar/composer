package http

import (
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/mohitkumar/composer/pkg/auth"
	"github.com/mohitkumar/composer/pkg/coord"
)

func TestWriteCoordErrorMapsSentinelsToStatusCodes(t *testing.T) {
	cases := map[error]int{
		fmt.Errorf("x: %w", coord.ErrNotFound):     http.StatusNotFound,
		fmt.Errorf("x: %w", coord.ErrInvalid):      http.StatusBadRequest,
		fmt.Errorf("x: %w", coord.ErrConflict):     http.StatusConflict,
		fmt.Errorf("x: %w", coord.ErrRunActive):    http.StatusConflict,
		fmt.Errorf("x: %w", coord.ErrRunNotActive): http.StatusConflict,
		errors.New("database down"):                http.StatusInternalServerError,
	}
	for err, want := range cases {
		rec := httptest.NewRecorder()
		writeCoordError(rec, err)
		if rec.Code != want {
			t.Errorf("%v: got %d, want %d", err, rec.Code, want)
		}
		if ct := rec.Header().Get("Content-Type"); ct != "application/json" {
			t.Errorf("content type %q", ct)
		}
	}
}

func TestJWTAuthMiddleware(t *testing.T) {
	jwt := auth.NewJWTService("secret", time.Hour)
	token, _ := jwt.GenerateToken("u1", "ada")

	var seenUser string
	handler := JWTAuthMiddleware(jwt)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seenUser = GetUserID(r) + "/" + GetUsername(r)
	}))

	for header, want := range map[string]int{
		"":                        http.StatusUnauthorized,
		"Token " + token:          http.StatusUnauthorized,
		"Bearer not-a-real-token": http.StatusUnauthorized,
		"Bearer " + token:         http.StatusOK,
	} {
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		if header != "" {
			req.Header.Set("Authorization", header)
		}
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		if rec.Code != want {
			t.Errorf("Authorization %q: got %d, want %d", header, rec.Code, want)
		}
	}
	if seenUser != "u1/ada" {
		t.Fatalf("handler must see the token's user, got %q", seenUser)
	}
}
