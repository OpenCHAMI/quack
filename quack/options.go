package quack

import (
	"context"
	"fmt"
	"os"
	"time"
)

type DuckDBStorageOption interface {
	apply(*DuckDBStorage) error
}

// snapshotFrequencyOption is an option to set the frequency of snapshots.
// when enabled, the storage will take a snapshot of the database every
// snapshotFrequency duration.  The snapshot logic is handled in a separate
// goroutine.
type snapshotFrequencyOption time.Duration

func (s snapshotFrequencyOption) apply(d *DuckDBStorage) error {
	d.snapshotFrequency = time.Duration(s)
	go d.snapshotRoutine(context.Background())
	return nil
}

func WithSnapshotFrequency(frequency time.Duration) DuckDBStorageOption {
	return snapshotFrequencyOption(frequency)
}

// snapshotPathOption is an option to set the path to store snapshots.
// when enabled, the storage will store snapshots in the specified path.
type snapshotPathOption string

func (s snapshotPathOption) apply(d *DuckDBStorage) error {
	d.snapshotPath = string(s)
	return nil
}

func WithSnapshotPath(path string) DuckDBStorageOption {
	return snapshotPathOption(path)
}

// restoreOption is an option to restore the database from a snapshot on startup.
type restoreOption string

func (r restoreOption) apply(d *DuckDBStorage) error {
	d.restoreFirst = true
	d.snapshotPath = string(r)
	return d.restore(d.snapshotPath)
}

func WithRestore(path string) DuckDBStorageOption {
	return restoreOption(path)
}

// createSnapshotDirOption is an option to create the snapshot directory if it doesn't exist.
type createSnapshotDirOption bool

func (c createSnapshotDirOption) apply(d *DuckDBStorage) error {
	if bool(c) && d.snapshotPath != "" {
		if err := os.MkdirAll(d.snapshotPath, 0755); err != nil {
			return fmt.Errorf("failed to create snapshot directory '%s': %w", d.snapshotPath, err)
		}
	}
	return nil
}

func WithCreateSnapshotDir(create bool) DuckDBStorageOption {
	return createSnapshotDirOption(create)
}

// initTablesOption is an option to initialize the tables in the database if they don't already exist.
type initTablesOption bool

func (i initTablesOption) apply(d *DuckDBStorage) error {
	if bool(i) {
		return d.initializeDatabase()
	}
	return nil
}

func WithInitTables(init bool) DuckDBStorageOption {
	return initTablesOption(init)
}

// skipExtensionsOption is an option to skip loading DuckDB extensions entirely.
// This is useful in environments where extension loading is problematic or unnecessary.
type skipExtensionsOption bool

func (s skipExtensionsOption) apply(d *DuckDBStorage) error {
	d.skipExtensions = bool(s)
	return nil
}

func WithSkipExtensions(skip bool) DuckDBStorageOption {
	return skipExtensionsOption(skip)
}
