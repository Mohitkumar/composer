package auth_test

import (
	"testing"
	"time"

	"github.com/mohitkumar/composer/pkg/auth"
	"golang.org/x/crypto/bcrypt"
)

func TestTokenRoundTrip(t *testing.T) {
	svc := auth.NewJWTService("secret", time.Hour)
	token, err := svc.GenerateToken("u1", "ada")
	if err != nil {
		t.Fatal(err)
	}
	claims, err := svc.ValidateToken(token)
	if err != nil || claims.UserID != "u1" || claims.Username != "ada" {
		t.Fatalf("claims %+v err %v", claims, err)
	}
}

func TestTokensAreRejected(t *testing.T) {
	good := auth.NewJWTService("secret", time.Hour)
	token, _ := good.GenerateToken("u1", "ada")

	if _, err := auth.NewJWTService("other", time.Hour).ValidateToken(token); err == nil {
		t.Error("a token signed with another key must be rejected")
	}
	expired, _ := auth.NewJWTService("secret", -time.Minute).GenerateToken("u1", "ada")
	if _, err := good.ValidateToken(expired); err == nil {
		t.Error("an expired token must be rejected")
	}
	if _, err := good.ValidateToken("not-a-jwt"); err == nil {
		t.Error("garbage must be rejected")
	}
}

func TestVerifyPassword(t *testing.T) {
	hash, _ := bcrypt.GenerateFromPassword([]byte("admin123"), bcrypt.MinCost)
	svc := auth.NewUserService(nil)
	if err := svc.VerifyPassword(string(hash), "admin123"); err != nil {
		t.Errorf("correct password rejected: %v", err)
	}
	if err := svc.VerifyPassword(string(hash), "wrong"); err == nil {
		t.Error("wrong password accepted")
	}
}
