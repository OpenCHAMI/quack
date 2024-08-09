package quack

import (
	"context"
	"database/sql"
	"sync"
	"time"

	_ "github.com/marcboeker/go-duckdb"
	"github.com/rs/zerolog/log"
)

type DuckDBStorage struct {
	db                *sql.DB
	snapshotFrequency time.Duration
	snapshotPath      string
	restoreFirst      bool
	wg                sync.WaitGroup
	cancelSnapshot    context.CancelFunc
}

func NewDuckDBStorage(path string, options ...DuckDBStorageOption) (*DuckDBStorage, error) {
	db, err := sql.Open("duckdb", path)
	if err != nil {
		return nil, err
	}

	d := &DuckDBStorage{
		db:             db,
		cancelSnapshot: func() {},
	}

	for _, option := range options {
		err := option.apply(d)
		if err != nil {
			log.Warn().Err(err).Msg("Error applying DuckDBStorage option")
		}
	}

	d.loadExtensions()

	return d, nil
}

func (d *DuckDBStorage) DB() *sql.DB {
	return d.db
}

func (d *DuckDBStorage) Close() error {
	return d.db.Close()
}

// Shutdown initiates the shutdown process
func (d *DuckDBStorage) Shutdown(ctx context.Context) {
	log.Info().Msg("Taking final snapshot before shutdown")
	if err := d.SnapshotParquet(ctx, d.snapshotPath); err != nil {
		log.Error().Err(err).Msg("Error taking final snapshot")
	}

	log.Info().Msg("Stopping snapshot routine")
	d.cancelSnapshot()

	done := make(chan struct{})
	go func() {
		d.wg.Wait()
		close(done)
	}()

	select {
	case <-done:
		log.Info().Msg("All goroutines finished cleanly")
	case <-ctx.Done():
		log.Warn().Msg("Timeout waiting for goroutines to finish")
	}

	log.Info().Msg("Closing database connection")
	if err := d.Close(); err != nil {
		log.Error().Err(err).Msg("Error closing database connection")
	}

	log.Info().Msg("DuckDB Shutdown complete")
}

func (d *DuckDBStorage) initializeDatabase() error {
	if err := d.loadExtensions(); err != nil {
		return err
	}
	return nil
}

func (d *DuckDBStorage) loadExtensions() error {
	_, err := d.db.Exec("SET autoinstall_known_extensions=1;INSTALL json;LOAD json;INSTALL parquet;LOAD parquet")
	if err != nil {
		log.Error().Err(err).Msg("Failed to load DuckDB extensions")
	}
	return err
}
