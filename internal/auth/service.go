package auth

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/google/uuid"

	"git.fferrando.cc/rdfilippo/kubecraft/internal/db"
	"git.fferrando.cc/rdfilippo/kubecraft/internal/db/sqlc"
)

var (
	ErrInvalidCredentials = errors.New("invalid username or password")
	ErrNoSession          = errors.New("no valid session")
	ErrWrongPassword      = errors.New("current password is wrong")
)

// RateLimitedError is returned when too many logins failed.
type RateLimitedError struct{ RetryAfter time.Duration }

func (e *RateLimitedError) Error() string {
	return fmt.Sprintf("too many login attempts, retry in %s", e.RetryAfter.Round(time.Second))
}

// Authenticator checks credentials and returns the matching user.
// Local passwords are the only provider for now, OIDC can be added as another one.
type Authenticator interface {
	Authenticate(ctx context.Context, username, password string) (sqlc.User, error)
}

// Identity is the user behind a request.
type Identity struct {
	User    sqlc.User
	Session sqlc.Session
}

func (i Identity) IsAdmin() bool { return i.User.Role == RoleAdmin }

const (
	RoleAdmin = "admin"
	RoleUser  = "user"
)

// LocalAuthenticator checks passwords stored in the users table.
type LocalAuthenticator struct{ Store *db.Store }

func (a LocalAuthenticator) Authenticate(ctx context.Context, username, password string) (sqlc.User, error) {
	u, err := a.Store.GetUserByUsername(ctx, NormalizeUsername(username))
	if db.IsNotFound(err) {
		_, _ = VerifyPassword(password, dummyHash)
		return sqlc.User{}, ErrInvalidCredentials
	}
	if err != nil {
		return sqlc.User{}, err
	}
	ok, err := VerifyPassword(password, u.PasswordHash)
	if err != nil {
		return sqlc.User{}, err
	}
	if !ok || u.Disabled {
		return sqlc.User{}, ErrInvalidCredentials
	}
	return u, nil
}

// Service manages logins and sessions.
type Service struct {
	store     *db.Store
	auth      Authenticator
	secret    []byte
	ttl       time.Duration
	userLimit *RateLimiter
	ipLimit   *RateLimiter
	now       func() time.Time
}

func NewService(store *db.Store, a Authenticator, secret []byte, ttl time.Duration) *Service {
	return &Service{
		store:     store,
		auth:      a,
		secret:    secret,
		ttl:       ttl,
		userLimit: NewRateLimiter(5, 15*time.Minute),
		ipLimit:   NewRateLimiter(20, 15*time.Minute),
		now:       time.Now,
	}
}

func (s *Service) TTL() time.Duration { return s.ttl }

// Login checks credentials and creates a session, returning the cookie token.
func (s *Service) Login(ctx context.Context, username, password, ip, userAgent string) (string, sqlc.User, error) {
	userKey := NormalizeUsername(username)
	if ok, wait := s.ipLimit.Allowed(ip); !ok {
		return "", sqlc.User{}, &RateLimitedError{wait}
	}
	if ok, wait := s.userLimit.Allowed(userKey); !ok {
		return "", sqlc.User{}, &RateLimitedError{wait}
	}
	u, err := s.auth.Authenticate(ctx, username, password)
	if errors.Is(err, ErrInvalidCredentials) {
		s.ipLimit.Fail(ip)
		s.userLimit.Fail(userKey)
		return "", sqlc.User{}, err
	}
	if err != nil {
		return "", sqlc.User{}, err
	}
	s.userLimit.Reset(userKey)

	token, err := randomToken()
	if err != nil {
		return "", sqlc.User{}, err
	}
	csrf, err := randomToken()
	if err != nil {
		return "", sqlc.User{}, err
	}
	if len(userAgent) > 512 {
		userAgent = userAgent[:512]
	}
	err = s.store.CreateSession(ctx, sqlc.CreateSessionParams{
		ID:        s.sessionID(token),
		UserID:    u.ID,
		CsrfToken: csrf,
		ExpiresAt: s.now().Add(s.ttl),
		Ip:        ip,
		UserAgent: userAgent,
	})
	if err != nil {
		return "", sqlc.User{}, fmt.Errorf("create session: %w", err)
	}
	return token, u, nil
}

// Session resolves a cookie token to an identity.
func (s *Service) Session(ctx context.Context, token string) (Identity, error) {
	if token == "" {
		return Identity{}, ErrNoSession
	}
	row, err := s.store.GetSessionUser(ctx, s.sessionID(token))
	if db.IsNotFound(err) {
		return Identity{}, ErrNoSession
	}
	if err != nil {
		return Identity{}, err
	}
	if row.User.Disabled {
		return Identity{}, ErrNoSession
	}
	return Identity{User: row.User, Session: row.Session}, nil
}

func (s *Service) Logout(ctx context.Context, token string) error {
	return s.store.DeleteSession(ctx, s.sessionID(token))
}

// ChangePassword updates the user's own password and ends their other sessions.
func (s *Service) ChangePassword(ctx context.Context, id Identity, current, next string) error {
	ok, err := VerifyPassword(current, id.User.PasswordHash)
	if err != nil {
		return err
	}
	if !ok {
		return ErrWrongPassword
	}
	if err := ValidatePassword(next); err != nil {
		return err
	}
	hash, err := HashPassword(next)
	if err != nil {
		return err
	}
	return s.store.InTx(ctx, func(q *sqlc.Queries) error {
		if err := q.UpdatePassword(ctx, sqlc.UpdatePasswordParams{ID: id.User.ID, PasswordHash: hash, MustChangePassword: false}); err != nil {
			return err
		}
		return q.DeleteOtherUserSessions(ctx, sqlc.DeleteOtherUserSessionsParams{UserID: id.User.ID, ID: id.Session.ID})
	})
}

// ResetPassword sets a new password (admin action) and ends all sessions of the user.
func (s *Service) ResetPassword(ctx context.Context, userID uuid.UUID, next string) error {
	if err := ValidatePassword(next); err != nil {
		return err
	}
	hash, err := HashPassword(next)
	if err != nil {
		return err
	}
	return s.store.InTx(ctx, func(q *sqlc.Queries) error {
		if err := q.UpdatePassword(ctx, sqlc.UpdatePasswordParams{ID: userID, PasswordHash: hash, MustChangePassword: true}); err != nil {
			return err
		}
		return q.DeleteUserSessions(ctx, userID)
	})
}

// Cleanup removes expired sessions and stale rate limit entries until ctx ends.
func (s *Service) Cleanup(ctx context.Context, every time.Duration) {
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			s.userLimit.Prune()
			s.ipLimit.Prune()
			if n, err := s.store.DeleteExpiredSessions(ctx); err != nil {
				slog.Warn("session cleanup failed", "err", err)
			} else if n > 0 {
				slog.Debug("expired sessions removed", "count", n)
			}
		}
	}
}

func (s *Service) sessionID(token string) string {
	m := hmac.New(sha256.New, s.secret)
	m.Write([]byte(token))
	return hex.EncodeToString(m.Sum(nil))
}

func randomToken() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

// NormalizeUsername trims and lowercases a username.
func NormalizeUsername(u string) string { return strings.ToLower(strings.TrimSpace(u)) }
