package quack

import (
	"context"
	"os"
	"testing"
	"time"

	_ "github.com/marcboeker/go-duckdb"
	"github.com/stretchr/testify/assert"
)

func TestSnapshotCreation(t *testing.T) {
	// Setup
	dbPath := "test.db"
	snapshotPath := "snapshot.parquet"
	defer os.Remove(dbPath)
	defer os.Remove(snapshotPath)

	d, err := NewDuckDBStorage(dbPath, WithSnapshotFrequency(1*time.Second), WithSnapshotPath(snapshotPath))
	assert.NoError(t, err)
	defer d.Close()

	// Verify snapshot creation at configured interval
	time.Sleep(2 * time.Second)
	_, err = os.Stat(snapshotPath)
	assert.NoError(t, err)

	// Test failure scenarios
	invalidPath := "/invalid/path/snapshot.parquet"
	d.snapshotPath = invalidPath
	err = d.SnapshotParquet(context.Background(), invalidPath)
	assert.Error(t, err)
}

func TestSnapshotStorageLocation(t *testing.T) {
	// Setup
	dbPath := "test.db"
	snapshotPath := "snapshot.parquet"
	defer os.Remove(dbPath)
	defer os.Remove(snapshotPath)

	d, err := NewDuckDBStorage(dbPath, WithSnapshotPath(snapshotPath))
	assert.NoError(t, err)
	defer d.Close()

	// Ensure snapshots are stored in the expected location
	err = d.SnapshotParquet(context.Background(), snapshotPath)
	assert.NoError(t, err)
	_, err = os.Stat(snapshotPath)
	assert.NoError(t, err)
}

func TestSnapshotRestoration(t *testing.T) {
	// Setup
	dbPath := "test.db"
	snapshotPath := "snapshot.parquet"
	restoredDBPath := "restored.db"

	defer os.Remove(dbPath)
	defer os.RemoveAll(snapshotPath)
	defer os.Remove(restoredDBPath)

	d, err := NewDuckDBStorage(dbPath, WithSnapshotPath(snapshotPath))
	assert.NoError(t, err)
	defer d.Close()

	// Create a table and insert data so the snapshot has meaningful content
	_, err = d.DB().Exec("CREATE TABLE kv (k VARCHAR, v INTEGER); INSERT INTO kv VALUES ('a', 42);")
	assert.NoError(t, err)

	// Create a snapshot
	err = d.SnapshotParquet(context.Background(), snapshotPath)
	assert.NoError(t, err)

	// Find the actual snapshot directory that was created (timestamped subdirectory)
	snapshotDir, err := latestSnapshotDir(snapshotPath)
	assert.NoError(t, err)
	assert.NotEmpty(t, snapshotDir, "no snapshot directory found")

	// Close the original DB to simulate a fresh start before restoration
	assert.NoError(t, d.Close())

	// Restore into a new DB instance
	rd, err := NewDuckDBStorage(restoredDBPath, WithSnapshotPath(snapshotPath))
	assert.NoError(t, err)
	defer rd.Close()

	err = rd.RestoreParquet(snapshotDir)
	assert.NoError(t, err)

	// Verify that the restored DB contains the data from the snapshot
	var count int
	err = rd.DB().QueryRow("SELECT COUNT(*) FROM kv").Scan(&count)
	assert.NoError(t, err)
	assert.Equal(t, 1, count)

	// Test invalid or corrupted snapshots
	invalidSnapshotPath := "invalid_snapshot.parquet"
	err = os.WriteFile(invalidSnapshotPath, []byte("invalid data"), 0644)
	assert.NoError(t, err)
	defer os.Remove(invalidSnapshotPath)

	err = rd.RestoreParquet(invalidSnapshotPath)
	assert.Error(t, err)
}

// helper to pick the latest snapshot subdirectory under the configured snapshot path
func latestSnapshotDir(root string) (string, error) {
	entries, err := os.ReadDir(root)
	if err != nil {
		// if root isn't a directory or doesn't exist, return the root (RestoreParquet may accept it)
		return root, err
	}

	var latest string
	var latestTime time.Time
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		if info.ModTime().After(latestTime) {
			latestTime = info.ModTime()
			latest = root + string(os.PathSeparator) + e.Name()
		}
	}

	if latest == "" {
		// no subdir found: return root so RestoreParquet can attempt to use it
		return root, nil
	}
	return latest, nil
}

func TestConfigurationHandling(t *testing.T) {
	// Test different configurations
	dbPath := "test.db"
	defer os.Remove(dbPath)

	d, err := NewDuckDBStorage(dbPath, WithSnapshotFrequency(2*time.Second), WithSnapshotPath("snapshot.parquet"))
	assert.NoError(t, err)
	defer d.Close()

	assert.Equal(t, 2*time.Second, d.snapshotFrequency)
	assert.Equal(t, "snapshot.parquet", d.snapshotPath)

	// Validate default settings
	d, err = NewDuckDBStorage(dbPath)
	assert.NoError(t, err)
	defer d.Close()

	assert.Equal(t, 0*time.Second, d.snapshotFrequency)
	assert.Equal(t, "", d.snapshotPath)
}
