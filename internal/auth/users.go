package auth

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"regexp"

	"git.fferrando.cc/rdfilippo/kubecraft/internal/db"
	"git.fferrando.cc/rdfilippo/kubecraft/internal/db/sqlc"
)

// bootstrapLockID is an arbitrary key for pg_advisory_xact_lock.
const bootstrapLockID = 0x6b7562656372616e

var usernameRe = regexp.MustCompile(`^[a-z0-9][a-z0-9_.-]{1,31}$`)

var ErrBadUsername = errors.New("username must be 2-32 characters: lowercase letters, digits, '.', '_' or '-'")

// ValidateUsername checks an already normalized username.
func ValidateUsername(u string) error {
	if !usernameRe.MatchString(u) {
		return ErrBadUsername
	}
	return nil
}

// CreateUser validates input and stores a new user with a hashed password.
func CreateUser(ctx context.Context, q *sqlc.Queries, username, password, role string, mustChange bool) (sqlc.User, error) {
	username = NormalizeUsername(username)
	if err := ValidateUsername(username); err != nil {
		return sqlc.User{}, err
	}
	if role != RoleAdmin && role != RoleUser {
		return sqlc.User{}, fmt.Errorf("unknown role %q", role)
	}
	if err := ValidatePassword(password); err != nil {
		return sqlc.User{}, err
	}
	hash, err := HashPassword(password)
	if err != nil {
		return sqlc.User{}, err
	}
	return q.CreateUser(ctx, sqlc.CreateUserParams{
		Username:           username,
		PasswordHash:       hash,
		Role:               role,
		MustChangePassword: mustChange,
	})
}

// Bootstrap creates the first admin when the users table is empty.
func Bootstrap(ctx context.Context, store *db.Store, username, password string) error {
	return store.InTx(ctx, func(q *sqlc.Queries) error {
		// Serialize concurrent replicas starting at the same time.
		if err := q.AdvisoryXactLock(ctx, bootstrapLockID); err != nil {
			return err
		}
		n, err := q.CountUsers(ctx)
		if err != nil {
			return err
		}
		if n > 0 {
			return nil
		}
		if username == "" {
			slog.Warn("no users exist and BOOTSTRAP_ADMIN_USER is not set, nobody can log in")
			return nil
		}
		u, err := CreateUser(ctx, q, username, password, RoleAdmin, true)
		if err != nil {
			return fmt.Errorf("bootstrap admin: %w", err)
		}
		slog.Info("bootstrap admin created", "username", u.Username)
		return nil
	})
}
