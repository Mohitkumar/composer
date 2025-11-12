package coord

import (
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/rs/zerolog"
)

type Coordinator struct {
	DB  *pgxpool.Pool
	Log zerolog.Logger
}

func NewCoordinator(db *pgxpool.Pool, logger zerolog.Logger) *Coordinator {
	return &Coordinator{DB: db, Log: logger}
}
