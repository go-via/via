package deploy

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/go-via/via"
)

var _ via.SessionStore = SQLSessions{}

// snippet:start store
// SQLSessions keeps sessions in one Postgres table:
//
//	CREATE TABLE via_sessions (
//		id      text PRIMARY KEY,
//		data    bytea NOT NULL,
//		expires timestamptz NOT NULL
//	);
type SQLSessions struct{ DB *sql.DB }

func (s SQLSessions) Load(ctx context.Context, id string) ([]byte, bool, error) {
	var data []byte
	err := s.DB.QueryRowContext(ctx, `
		SELECT data FROM via_sessions
		WHERE id = $1 AND expires > now()`, id).Scan(&data)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, false, nil
	}
	return data, err == nil, err
}

func (s SQLSessions) Save(ctx context.Context, id string, data []byte, ttl time.Duration) error {
	_, err := s.DB.ExecContext(ctx, `
		INSERT INTO via_sessions (id, data, expires) VALUES ($1, $2, $3)
		ON CONFLICT (id) DO UPDATE
		SET data = excluded.data, expires = excluded.expires`,
		id, data, time.Now().Add(ttl))
	return err
}

func (s SQLSessions) Delete(ctx context.Context, id string) error {
	_, err := s.DB.ExecContext(ctx, `DELETE FROM via_sessions WHERE id = $1`, id)
	return err
}

// snippet:end
