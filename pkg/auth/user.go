package auth

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/mohitkumar/composer/pkg/db"
	"github.com/mohitkumar/composer/pkg/model"
	"golang.org/x/crypto/bcrypt"
)

var (
	ErrUserNotFound      = errors.New("user not found")
	ErrInvalidPassword   = errors.New("invalid password")
	ErrUserAlreadyExists = errors.New("user already exists")
)

// UserService handles user-related operations
type UserService struct {
	db db.DB
}

// NewUserService creates a new user service
func NewUserService(db db.DB) *UserService {
	return &UserService{db: db}
}

// GetUserByUsername retrieves a user by username
func (s *UserService) GetUserByUsername(ctx context.Context, username string) (*model.User, error) {
	query := `
		SELECT id, username, email, password_hash, created_at, updated_at, last_login_at
		FROM users
		WHERE username = $1
	`

	var user model.User
	var email *string
	err := s.db.QueryRow(ctx, query, username).Scan(
		&user.ID,
		&user.Username,
		&email,
		&user.PasswordHash,
		&user.CreatedAt,
		&user.UpdatedAt,
		&user.LastLoginAt,
	)
	if err != nil {
		if err == pgx.ErrNoRows {
			return nil, ErrUserNotFound
		}
		return nil, err
	}

	user.Email = email
	return &user, nil
}

// GetUserByID retrieves a user by ID
func (s *UserService) GetUserByID(ctx context.Context, id string) (*model.User, error) {
	query := `
		SELECT id, username, email, password_hash, created_at, updated_at, last_login_at
		FROM users
		WHERE id = $1
	`

	var user model.User
	var email *string
	err := s.db.QueryRow(ctx, query, id).Scan(
		&user.ID,
		&user.Username,
		&email,
		&user.PasswordHash,
		&user.CreatedAt,
		&user.UpdatedAt,
		&user.LastLoginAt,
	)
	if err != nil {
		if err == pgx.ErrNoRows {
			return nil, ErrUserNotFound
		}
		return nil, err
	}

	user.Email = email
	return &user, nil
}

// VerifyPassword verifies a password against a hash
func (s *UserService) VerifyPassword(hashedPassword, password string) error {
	return bcrypt.CompareHashAndPassword([]byte(hashedPassword), []byte(password))
}

// UpdateLastLogin updates the last login timestamp for a user
func (s *UserService) UpdateLastLogin(ctx context.Context, userID string) error {
	query := `
		UPDATE users
		SET last_login_at = NOW(), updated_at = NOW()
		WHERE id = $1
	`
	_, err := s.db.Exec(ctx, query, userID)
	return err
}

// Authenticate authenticates a user with username and password
func (s *UserService) Authenticate(ctx context.Context, username, password string) (*model.User, error) {
	user, err := s.GetUserByUsername(ctx, username)
	if err != nil {
		return nil, err
	}

	if err := s.VerifyPassword(user.PasswordHash, password); err != nil {
		return nil, ErrInvalidPassword
	}

	// Update last login
	_ = s.UpdateLastLogin(ctx, user.ID)

	// Clear password hash from returned user
	user.PasswordHash = ""
	return user, nil
}

