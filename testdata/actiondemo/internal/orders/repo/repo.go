// Package repo is a subpackage of orders; it is rolled up into internal/orders.
package repo

import (
	"github.com/jackc/pgx/v5" // planted UNDOCUMENTED_EDGE: orders talks to Postgres directly
)

type Repo struct{ conn *pgx.Conn }

func New() *Repo { return &Repo{} }

func (r *Repo) Count() int { return 0 }
