package identity

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// Register creates an owner and an independent workspace in one transaction.
func (s *Store) Register(ctx context.Context, email, password, name string) (User, Workspace, error) {
	email, err := canonicalEmail(email)
	if err != nil || !validPassword(password) || !validName(name) {
		return User{}, Workspace{}, ErrInvalid
	}
	hash, err := s.passwordHash(ctx, password)
	if err != nil {
		return User{}, Workspace{}, fmt.Errorf("hash password: %w", err)
	}
	userID, err := identifier("u_")
	if err != nil {
		return User{}, Workspace{}, err
	}
	workspaceID, err := identifier("w_")
	if err != nil {
		return User{}, Workspace{}, err
	}
	user, workspace := User{ID: userID, Email: email, AuthVersion: 1}, Workspace{ID: workspaceID, Name: name, Role: "OWNER"}
	if err = s.insertRegistration(ctx, user, workspace, hash); err != nil {
		return User{}, Workspace{}, err
	}
	return user, workspace, nil
}

// RegisterPending reserves an address without asking for a password before
// ownership is established. Email verification replaces this random value.
func (s *Store) RegisterPending(ctx context.Context, email, name string) (User, Workspace, error) {
	placeholder, err := randomToken(32)
	if err != nil {
		return User{}, Workspace{}, err
	}
	return s.Register(ctx, email, placeholder, name)
}

func (s *Store) insertRegistration(ctx context.Context, user User, workspace Workspace, hash []byte) error {
	ctx, cancel := context.WithTimeout(ctx, s.timeout)
	defer cancel()
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("start registration: %w", err)
	}
	defer rollback(tx)
	if _, err = tx.Exec(ctx, `INSERT INTO saas_users(id,email,password_hash) VALUES($1,$2,$3)`, user.ID, user.Email, hash); err != nil {
		var pg *pgconn.PgError
		if errors.As(err, &pg) && pg.Code == "23505" {
			return ErrConflict
		}
		return fmt.Errorf("create user: %w", err)
	}
	if err = insertWorkspace(ctx, tx, workspace, user.ID, ""); err != nil {
		return err
	}
	if err = event(ctx, tx, workspace.ID, user.ID, "", "REGISTERED", ""); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func insertWorkspace(ctx context.Context, tx pgx.Tx, workspace Workspace, owner, session string) error {
	if _, err := tx.Exec(ctx, `INSERT INTO saas_workspaces(id,name) VALUES($1,$2)`, workspace.ID, workspace.Name); err != nil {
		return fmt.Errorf("create workspace: %w", err)
	}
	if _, err := tx.Exec(ctx, `INSERT INTO saas_memberships(workspace_id,user_id,role) VALUES($1,$2,'OWNER')`, workspace.ID, owner); err != nil {
		return fmt.Errorf("create owner membership: %w", err)
	}
	return event(ctx, tx, workspace.ID, owner, session, "WORKSPACE_CREATED", "")
}

func event(ctx context.Context, tx pgx.Tx, workspace, actor, session, action, target string) error {
	_, err := tx.Exec(ctx, `INSERT INTO saas_security_events(workspace_id,actor_user_id,session_audit_id,action,target) VALUES(NULLIF($1,''),NULLIF($2,''),NULLIF($3,''),$4,$5)`, workspace, actor, session, action, target)
	if err != nil {
		return fmt.Errorf("record security event: %w", err)
	}
	return nil
}

// RecordAction records an authorized request intent before the handler can
// mutate state. The domain's own transaction remains the outcome record.
func (s *Store) RecordAction(ctx context.Context, session Session, workspace, target string) error {
	if len(target) > 256 {
		return ErrInvalid
	}
	ctx, cancel := context.WithTimeout(ctx, s.timeout)
	defer cancel()
	_, err := s.pool.Exec(ctx, `INSERT INTO saas_security_events(workspace_id,actor_user_id,session_audit_id,action,target)
	 VALUES($1,$2,$3,'AUTHORIZED_REQUEST',$4)`, workspace, session.User.ID, session.AuditID, target)
	if err != nil {
		return fmt.Errorf("record request intent: %w", err)
	}
	return nil
}

func (s *Store) Authenticate(ctx context.Context, email, password string) (User, error) {
	email, valid := canonicalEmail(email)
	if valid != nil {
		email = "invalid@example.invalid"
	}
	ctx, cancel := context.WithTimeout(ctx, s.timeout)
	defer cancel()
	var user User
	var verified bool
	hash := s.dummyHash
	err := s.pool.QueryRow(ctx, `SELECT id,email,password_hash,auth_version,email_verified_at IS NOT NULL FROM saas_users WHERE email=$1 AND disabled_at IS NULL`, email).Scan(&user.ID, &user.Email, &hash, &user.AuthVersion, &verified)
	if errors.Is(err, pgx.ErrNoRows) {
		hash = s.dummyHash
	} else if err != nil {
		return User{}, fmt.Errorf("read user: %w", err)
	}
	match := s.passwordMatches(ctx, hash, password)
	if !match || err != nil || valid != nil || !validPassword(password) || !verified {
		return User{}, ErrAuth
	}
	return user, nil
}

// AllowAuthAttempt is an atomic, persistent per-key fixed-window limit.
// Callers use the direct peer address and separately limit login identifiers.
func (s *Store) AllowAuthAttempt(ctx context.Context, bucket []byte, maximum int, window time.Duration) (bool, error) {
	if len(bucket) != 32 || maximum < 1 || window <= 0 || window > time.Hour {
		return false, ErrInvalid
	}
	ctx, cancel := context.WithTimeout(ctx, s.timeout)
	defer cancel()
	var attempts int
	err := s.pool.QueryRow(ctx, `INSERT INTO saas_auth_limits(bucket,window_started_at,attempts) VALUES($1,clock_timestamp(),1)
	 ON CONFLICT(bucket) DO UPDATE SET
	 attempts=CASE WHEN saas_auth_limits.window_started_at <= clock_timestamp()-($2 * interval '1 second') THEN 1 ELSE saas_auth_limits.attempts+1 END,
	 window_started_at=CASE WHEN saas_auth_limits.window_started_at <= clock_timestamp()-($2 * interval '1 second') THEN clock_timestamp() ELSE saas_auth_limits.window_started_at END
	 RETURNING attempts`, bucket, window.Seconds()).Scan(&attempts)
	if err != nil {
		return false, fmt.Errorf("limit identity request: %w", err)
	}
	return attempts <= maximum, nil
}
