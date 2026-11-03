// Package store owns all database access.
package store

import (
	"context"

	"github.com/jackc/pgx/v5"
)

type DB struct{ conn *pgx.Conn }

func Open(dsn string) *DB {
	conn, _ := pgx.Connect(context.Background(), dsn)
	return &DB{conn: conn}
}
