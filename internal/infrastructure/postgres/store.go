package postgres

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/manovaspace/orbit-auth/internal/domain"
)

type Store struct {
	pool *pgxpool.Pool
}

func New(pool *pgxpool.Pool) *Store {
	return &Store{pool: pool}
}

func (s *Store) UpsertChallenge(ctx context.Context, ch domain.OTPChallenge) error {
	_, err := s.pool.Exec(ctx, `
		INSERT INTO otp_challenges (id, identifier, channel, code_hash, expires_at, attempts)
		VALUES ($1, $2, $3, $4, $5, 0)
	`, ch.ID, ch.Identifier, ch.Channel, ch.CodeHash, ch.ExpiresAt)
	return err
}

func (s *Store) GetLatestChallenge(ctx context.Context, identifier, channel string) (domain.OTPChallenge, error) {
	var ch domain.OTPChallenge
	err := s.pool.QueryRow(ctx, `
		SELECT id, identifier, channel, code_hash, expires_at, attempts
		FROM otp_challenges WHERE identifier = $1 AND channel = $2
		ORDER BY created_at DESC LIMIT 1
	`, identifier, channel).Scan(&ch.ID, &ch.Identifier, &ch.Channel, &ch.CodeHash, &ch.ExpiresAt, &ch.Attempts)
	if err != nil {
		return domain.OTPChallenge{}, err
	}
	return ch, nil
}

func (s *Store) IncrementAttempts(ctx context.Context, id string) error {
	_, err := s.pool.Exec(ctx, `UPDATE otp_challenges SET attempts = attempts + 1 WHERE id = $1`, id)
	return err
}

func (s *Store) ConsumeChallenge(ctx context.Context, id string) error {
	_, err := s.pool.Exec(ctx, `DELETE FROM otp_challenges WHERE id = $1`, id)
	return err
}

func (s *Store) DeleteExpiredChallenges(ctx context.Context) error {
	_, err := s.pool.Exec(ctx, `DELETE FROM otp_challenges WHERE expires_at < NOW()`)
	return err
}

func (s *Store) FindOrCreateByEmail(ctx context.Context, email string) (domain.User, error) {
	var u domain.User
	err := s.pool.QueryRow(ctx, `
		SELECT id, COALESCE(email, ''), COALESCE(mobile, ''), COALESCE(password_hash, ''),
		       COALESCE(display_name, ''), COALESCE(status, 'active')
		FROM users WHERE email = $1`, email).Scan(&u.ID, &u.Email, &u.Mobile, &u.PasswordHash, &u.DisplayName, &u.Status)
	if err == nil {
		return u, nil
	}
	if err != pgx.ErrNoRows {
		return domain.User{}, err
	}
	u = domain.User{ID: uuid.NewString(), Email: email, Status: domain.UserActive}
	_, err = s.pool.Exec(ctx, `INSERT INTO users (id, email, status) VALUES ($1, $2, $3)`, u.ID, u.Email, u.Status)
	return u, err
}

func (s *Store) FindByEmail(ctx context.Context, email string) (domain.User, error) {
	var u domain.User
	err := s.pool.QueryRow(ctx, `
		SELECT id, COALESCE(email, ''), COALESCE(mobile, ''), COALESCE(password_hash, ''),
		       COALESCE(display_name, ''), COALESCE(status, 'active')
		FROM users WHERE email = $1`, email).Scan(&u.ID, &u.Email, &u.Mobile, &u.PasswordHash, &u.DisplayName, &u.Status)
	return u, err
}

func (s *Store) UpdateLastLogin(ctx context.Context, userID string) error {
	_, err := s.pool.Exec(ctx, `UPDATE users SET last_login_at = NOW(), updated_at = NOW() WHERE id = $1`, userID)
	return err
}

func (s *Store) UpsertDemoUser(ctx context.Context, email, passwordHash, displayName string) error {
	_, err := s.pool.Exec(ctx, `
		INSERT INTO users (id, email, password_hash, display_name, status)
		VALUES ($1, $2, $3, $4, 'active')
		ON CONFLICT (email) DO UPDATE SET
			password_hash = EXCLUDED.password_hash,
			display_name = EXCLUDED.display_name,
			status = 'active',
			updated_at = NOW()
	`, uuid.NewString(), email, passwordHash, displayName)
	return err
}

func (s *Store) FindOrCreateByMobile(ctx context.Context, mobile string) (domain.User, error) {
	var u domain.User
	err := s.pool.QueryRow(ctx, `SELECT id, COALESCE(email, ''), mobile FROM users WHERE mobile = $1`, mobile).Scan(&u.ID, &u.Email, &u.Mobile)
	if err == nil {
		return u, nil
	}
	if err != pgx.ErrNoRows {
		return domain.User{}, err
	}
	u = domain.User{ID: uuid.NewString(), Mobile: mobile}
	_, err = s.pool.Exec(ctx, `INSERT INTO users (id, mobile) VALUES ($1, $2)`, u.ID, u.Mobile)
	return u, err
}

func (s *Store) Store(ctx context.Context, userID, tokenHash string, expiresAt time.Time) error {
	_, err := s.pool.Exec(ctx, `
		INSERT INTO refresh_tokens (id, user_id, token_hash, expires_at)
		VALUES ($1, $2, $3, $4)
	`, uuid.NewString(), userID, tokenHash, expiresAt)
	return err
}

func (s *Store) FindValid(ctx context.Context, tokenHash string) (string, error) {
	var userID string
	err := s.pool.QueryRow(ctx, `
		SELECT user_id FROM refresh_tokens
		WHERE token_hash = $1 AND expires_at > NOW() AND revoked_at IS NULL
	`, tokenHash).Scan(&userID)
	return userID, err
}

func (s *Store) Revoke(ctx context.Context, tokenHash string) error {
	_, err := s.pool.Exec(ctx, `UPDATE refresh_tokens SET revoked_at = NOW() WHERE token_hash = $1`, tokenHash)
	return err
}

func (s *Store) Create(ctx context.Context, t domain.ApiToken) error {
	_, err := s.pool.Exec(ctx, `
		INSERT INTO api_tokens (id, user_id, name, prefix, token_hash, scopes, expires_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7)
	`, t.ID, t.UserID, t.Name, t.Prefix, t.TokenHash, t.Scopes, t.ExpiresAt)
	return err
}

func (s *Store) ListByUser(ctx context.Context, userID string) ([]domain.ApiTokenInfo, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT id, name, prefix, scopes, expires_at, created_at
		FROM api_tokens WHERE user_id = $1 AND revoked_at IS NULL
		ORDER BY created_at DESC
	`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []domain.ApiTokenInfo
	for rows.Next() {
		var info domain.ApiTokenInfo
		var exp *time.Time
		if err := rows.Scan(&info.ID, &info.Name, &info.Prefix, &info.Scopes, &exp, &info.CreatedAt); err != nil {
			return nil, err
		}
		info.ExpiresAt = exp
		out = append(out, info)
	}
	return out, rows.Err()
}

func (s *Store) RevokeToken(ctx context.Context, userID, tokenID string) error {
	_, err := s.pool.Exec(ctx, `
		UPDATE api_tokens SET revoked_at = NOW()
		WHERE id = $1 AND user_id = $2 AND revoked_at IS NULL
	`, tokenID, userID)
	return err
}

func (s *Store) FindByPrefix(ctx context.Context, prefix string) (domain.ApiToken, error) {
	var t domain.ApiToken
	var exp, rev *time.Time
	err := s.pool.QueryRow(ctx, `
		SELECT id, user_id, name, prefix, token_hash, scopes, expires_at, revoked_at, created_at
		FROM api_tokens WHERE prefix = $1 AND revoked_at IS NULL
		ORDER BY created_at DESC LIMIT 1
	`, prefix).Scan(&t.ID, &t.UserID, &t.Name, &t.Prefix, &t.TokenHash, &t.Scopes, &exp, &rev, &t.CreatedAt)
	if err != nil {
		return domain.ApiToken{}, err
	}
	t.ExpiresAt = exp
	t.RevokedAt = rev
	return t, nil
}

func Migrate(ctx context.Context, databaseURL, migrationsDir string) error {
	pool, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		return err
	}
	defer pool.Close()

	if _, err := pool.Exec(ctx, `CREATE TABLE IF NOT EXISTS schema_migrations (
		name TEXT PRIMARY KEY,
		applied_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
	)`); err != nil {
		return err
	}

	entries, err := os.ReadDir(migrationsDir)
	if err != nil {
		return err
	}
	var files []string
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".sql") {
			files = append(files, e.Name())
		}
	}
	sort.Strings(files)

	var tracked int
	if err := pool.QueryRow(ctx, `SELECT COUNT(*) FROM schema_migrations`).Scan(&tracked); err != nil {
		return err
	}
	if tracked == 0 {
		var hasUsers bool
		if err := pool.QueryRow(ctx, `SELECT EXISTS (
			SELECT 1 FROM information_schema.tables
			WHERE table_schema = 'public' AND table_name = 'users'
		)`).Scan(&hasUsers); err != nil {
			return err
		}
		if hasUsers {
			for _, name := range files {
				if _, err := pool.Exec(ctx, `INSERT INTO schema_migrations (name) VALUES ($1) ON CONFLICT DO NOTHING`, name); err != nil {
					return err
				}
			}
			return nil
		}
	}

	for _, name := range files {
		var applied bool
		if err := pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM schema_migrations WHERE name = $1)`, name).Scan(&applied); err != nil {
			return err
		}
		if applied {
			continue
		}
		b, err := os.ReadFile(filepath.Join(migrationsDir, name))
		if err != nil {
			return err
		}
		up, err := extractGooseUp(string(b))
		if err != nil {
			return fmt.Errorf("%s: %w", name, err)
		}
		if _, err := pool.Exec(ctx, up); err != nil {
			return fmt.Errorf("%s: %w", name, err)
		}
		if _, err := pool.Exec(ctx, `INSERT INTO schema_migrations (name) VALUES ($1)`, name); err != nil {
			return err
		}
	}
	return nil
}

func extractGooseUp(sql string) (string, error) {
	const marker = "-- +goose Up"
	idx := strings.Index(sql, marker)
	if idx < 0 {
		return "", fmt.Errorf("migration marker not found")
	}
	up := sql[idx+len(marker):]
	if down := strings.Index(up, "-- +goose Down"); down >= 0 {
		up = up[:down]
	}
	return strings.TrimSpace(up), nil
}
