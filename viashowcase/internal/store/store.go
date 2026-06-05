// Package store is the Postgres persistence layer (database/sql + pgx stdlib).
package store

import (
	"context"
	"crypto/rand"
	"database/sql"
	"database/sql/driver"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"

	_ "embed"

	_ "github.com/jackc/pgx/v5/stdlib"
)

//go:embed schema.sql
var schema string

// ErrNotFound is returned when a requested row does not exist.
var ErrNotFound = errors.New("store: not found")

type Store struct{ db *sql.DB }

func Open(dsn string) (*Store, error) {
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		return nil, err
	}
	return &Store{db: db}, nil
}

func (s *Store) Migrate(ctx context.Context) error {
	_, err := s.db.ExecContext(ctx, schema)
	return err
}

func (s *Store) Close() error { return s.db.Close() }

// Ping verifies the database connection is alive — used by the /healthz probe so
// the load balancer routes around a pod whose Postgres link has died.
func (s *Store) Ping(ctx context.Context) error { return s.db.PingContext(ctx) }

// SetDisplay renames a user's display name.
func (s *Store) SetDisplay(ctx context.Context, userID, display string) error {
	_, err := s.db.ExecContext(ctx, `UPDATE users SET display=$2 WHERE id=$1`, userID, display)
	return err
}

func id() string {
	var b [12]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}

type User struct{ ID, Email, Display string }

func (s *Store) CreateUser(ctx context.Context, email, passHash, display string) (User, error) {
	u := User{ID: id(), Email: email, Display: display}
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO users (id, email, pass_hash, display) VALUES ($1,$2,$3,$4)`,
		u.ID, email, passHash, display)
	if err != nil {
		return User{}, err
	}
	return u, nil
}

func (s *Store) UserByEmail(ctx context.Context, email string) (u User, passHash string, err error) {
	row := s.db.QueryRowContext(ctx,
		`SELECT id, email, display, pass_hash FROM users WHERE email=$1`, email)
	err = row.Scan(&u.ID, &u.Email, &u.Display, &passHash)
	if errors.Is(err, sql.ErrNoRows) {
		err = ErrNotFound
	}
	return
}

func (s *Store) UserByID(ctx context.Context, uid string) (User, error) {
	var u User
	err := s.db.QueryRowContext(ctx,
		`SELECT id, email, display FROM users WHERE id=$1`, uid).
		Scan(&u.ID, &u.Email, &u.Display)
	if errors.Is(err, sql.ErrNoRows) {
		return User{}, ErrNotFound
	}
	return u, err
}

func (s *Store) SetAvatar(ctx context.Context, userID, contentType string, data []byte) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO avatars (user_id, content_type, data) VALUES ($1,$2,$3)
		 ON CONFLICT (user_id) DO UPDATE SET content_type=EXCLUDED.content_type, data=EXCLUDED.data`,
		userID, contentType, data)
	return err
}

func (s *Store) Avatar(ctx context.Context, userID string) (contentType string, data []byte, err error) {
	err = s.db.QueryRowContext(ctx,
		`SELECT content_type, data FROM avatars WHERE user_id=$1`, userID).
		Scan(&contentType, &data)
	if errors.Is(err, sql.ErrNoRows) {
		err = ErrNotFound
	}
	return
}

func (s *Store) SetPref(ctx context.Context, userID, theme, mode string) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO prefs (user_id, theme, mode) VALUES ($1,$2,$3)
		 ON CONFLICT (user_id) DO UPDATE SET theme=EXCLUDED.theme, mode=EXCLUDED.mode`,
		userID, theme, mode)
	return err
}

func (s *Store) Pref(ctx context.Context, userID string) (theme, mode string, err error) {
	err = s.db.QueryRowContext(ctx,
		`SELECT theme, mode FROM prefs WHERE user_id=$1`, userID).Scan(&theme, &mode)
	if errors.Is(err, sql.ErrNoRows) {
		err = ErrNotFound
	}
	return
}

type Room struct {
	Code, HostID, Title, Kind string
	Choices                   []string
	CreatedAt                 time.Time
}

func (s *Store) CreateRoom(ctx context.Context, r Room) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO rooms (code, host_id, title, kind, choices) VALUES ($1,$2,$3,$4,$5)`,
		r.Code, r.HostID, r.Title, r.Kind, strArray(r.Choices))
	return err
}

func (s *Store) RoomByCode(ctx context.Context, code string) (Room, error) {
	var r Room
	var ch strArray
	err := s.db.QueryRowContext(ctx,
		`SELECT code, host_id, title, kind, choices, created_at FROM rooms WHERE code=$1`, code).
		Scan(&r.Code, &r.HostID, &r.Title, &r.Kind, &ch, &r.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return Room{}, ErrNotFound
	}
	r.Choices = ch
	return r, err
}

func (s *Store) RoomsByHost(ctx context.Context, hostID string) ([]Room, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT code, host_id, title, kind, choices, created_at FROM rooms
		 WHERE host_id=$1 ORDER BY created_at DESC`, hostID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Room
	for rows.Next() {
		var r Room
		var ch strArray
		if err := rows.Scan(&r.Code, &r.HostID, &r.Title, &r.Kind, &ch, &r.CreatedAt); err != nil {
			return nil, err
		}
		r.Choices = ch
		out = append(out, r)
	}
	return out, rows.Err()
}

// SaveVote durably records one vote keyed by its event-log offset. Idempotent:
// a redelivery (at-least-once consumer, multiple pods) is a no-op.
func (s *Store) SaveVote(ctx context.Context, offset int64, room, choice, by string) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO votes (offset_id, room, choice, by_nick) VALUES ($1,$2,$3,$4)
		 ON CONFLICT (offset_id) DO NOTHING`,
		offset, room, choice, by)
	return err
}

// strArray maps a Go []string to a Postgres text[] via the array literal format.
type strArray []string

func (a strArray) Value() (driver.Value, error) {
	if a == nil {
		return "{}", nil
	}
	parts := make([]string, len(a))
	for i, s := range a {
		parts[i] = `"` + strings.NewReplacer(`\`, `\\`, `"`, `\"`).Replace(s) + `"`
	}
	return "{" + strings.Join(parts, ",") + "}", nil
}

func (a *strArray) Scan(src any) error {
	var lit string
	switch v := src.(type) {
	case nil:
		*a = nil
		return nil
	case string:
		lit = v
	case []byte:
		lit = string(v)
	default:
		return fmt.Errorf("strArray: cannot scan %T", src)
	}
	*a = parseArray(lit)
	return nil
}

func parseArray(lit string) []string {
	lit = strings.TrimSpace(lit)
	if len(lit) < 2 || lit[0] != '{' || lit[len(lit)-1] != '}' {
		return nil
	}
	body := lit[1 : len(lit)-1]
	if body == "" {
		return []string{}
	}
	var out []string
	var cur strings.Builder
	inQ, esc := false, false
	flush := func() { out = append(out, cur.String()); cur.Reset() }
	for i := 0; i < len(body); i++ {
		c := body[i]
		switch {
		case esc:
			cur.WriteByte(c)
			esc = false
		case c == '\\':
			esc = true
		case c == '"':
			inQ = !inQ
		case c == ',' && !inQ:
			flush()
		default:
			cur.WriteByte(c)
		}
	}
	flush()
	return out
}
