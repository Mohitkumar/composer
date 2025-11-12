package store

import (
	"context"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/rs/zerolog"
)

type Store struct {
	config *StoreConfig
	db     *pgxpool.Pool
	log    zerolog.Logger
}

func NewStore(config *StoreConfig, log zerolog.Logger) *Store {
	return &Store{
		config: config,
		db: func() *pgxpool.Pool {
			pg, err := pgxpool.New(context.Background(), config.URL)
			if err != nil {
				panic(err)
			}
			if err := pg.Ping(context.Background()); err != nil {
				panic(err)
			}
			return pg
		}(),
		log: log,
	}
}

func (s *Store) DB() *pgxpool.Pool {
	return s.db
}

func (s *Store) Close() {
	s.db.Close()
}
