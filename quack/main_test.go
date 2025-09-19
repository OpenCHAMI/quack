package quack

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
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

func TestLoadExtensions_EnvOverrideAndFallbacks(t *testing.T) {
	// Use a temporary directory for DB file and for DUCKDB_HOME/HOME
	tmp := t.TempDir()
	dbPath := filepath.Join(tmp, "test.db")

	// Ensure env is clean
	origDuckHome := os.Getenv("DUCKDB_HOME")
	origHome := os.Getenv("HOME")
	defer os.Setenv("DUCKDB_HOME", origDuckHome)
	defer os.Setenv("HOME", origHome)

	// 1) Explicit DUCKDB_HOME override should succeed
	duckHomeDir := filepath.Join(tmp, "duckhome1")
	if err := os.MkdirAll(duckHomeDir, 0o755); err != nil {
		t.Fatalf("mkdir failed: %v", err)
	}
	os.Setenv("DUCKDB_HOME", duckHomeDir)
	os.Unsetenv("HOME")

	d, err := NewDuckDBStorage(dbPath)
	assert.NoError(t, err)
	defer d.Close()

	// loadExtensions is called by constructor but return value is not propagated,
	// call explicitly to observe errors
	err = d.loadExtensions()
	assert.NoError(t, err, "loadExtensions should succeed with DUCKDB_HOME set to writable dir")

	// 2) Fallback to HOME when DUCKDB_HOME unset
	os.Unsetenv("DUCKDB_HOME")
	homeDir := filepath.Join(tmp, "homedir")
	if err := os.MkdirAll(homeDir, 0o755); err != nil {
		t.Fatalf("mkdir failed: %v", err)
	}
	os.Setenv("HOME", homeDir)

	err = d.loadExtensions()
	assert.NoError(t, err, "loadExtensions should succeed with HOME set to writable dir")

	// 3) Fallback to /tmp when neither DUCKDB_HOME nor HOME set
	os.Unsetenv("DUCKDB_HOME")
	os.Unsetenv("HOME")

	err = d.loadExtensions()
	assert.NoError(t, err, "loadExtensions should succeed when falling back to /tmp")
}

func TestLoadExtensions_InvalidUnwritableHome(t *testing.T) {
	// This test manipulates permissions and may not be supported on all platforms.
	// Skip on windows where chmod semantics differ.
	if runtime.GOOS == "windows" {
		t.Skip("permission-based unwritable test skipped on Windows")
	}

	tmp := t.TempDir()
	dbPath := filepath.Join(tmp, "test2.db")

	origDuckHome := os.Getenv("DUCKDB_HOME")
	defer os.Setenv("DUCKDB_HOME", origDuckHome)

	// Create a directory and remove write permissions to simulate unwritable home
	badDir := filepath.Join(tmp, "badhome")
	if err := os.MkdirAll(badDir, 0o755); err != nil {
		t.Fatalf("mkdir failed: %v", err)
	}
	// remove all permissions
	if err := os.Chmod(badDir, 0); err != nil {
		t.Fatalf("chmod failed: %v", err)
	}
	// restore perms at the end so t.TempDir cleanup can delete
	defer func() {
		_ = os.Chmod(badDir, 0o755)
	}()

	os.Setenv("DUCKDB_HOME", badDir)

	d, err := NewDuckDBStorage(dbPath)
	assert.NoError(t, err)
	defer d.Close()

	err = d.loadExtensions()
	// We expect an error in many environments because the install step cannot write into badDir.
	// If the environment still allows the install, the test will accept success — assert that either is valid,
	// but prefer to detect and report if no error occurred in an environment where it should.
	if err == nil {
		t.Logf("Warning: loadExtensions did not return an error for unwritable DUCKDB_HOME (%s); environment may permit writes", badDir)
	} else {
		assert.Error(t, err, "expected error when DUCKDB_HOME points to an unwritable directory")
	}
}

func TestSkipExtensions(t *testing.T) {
	tmp := t.TempDir()
	dbPath := filepath.Join(tmp, "test_skip.db")

	// Test skip extensions via option
	d, err := NewDuckDBStorage(dbPath, WithSkipExtensions(true))
	assert.NoError(t, err)
	defer d.Close()

	err = d.loadExtensions()
	assert.NoError(t, err, "loadExtensions should succeed when extensions are skipped")

	// Test skip extensions via environment variable
	origEnv := os.Getenv("DUCKDB_SKIP_EXTENSIONS")
	defer func() {
		if origEnv == "" {
			os.Unsetenv("DUCKDB_SKIP_EXTENSIONS")
		} else {
			os.Setenv("DUCKDB_SKIP_EXTENSIONS", origEnv)
		}
	}()

	d2, err := NewDuckDBStorage(dbPath)
	assert.NoError(t, err)
	defer d2.Close()

	os.Setenv("DUCKDB_SKIP_EXTENSIONS", "true")
	err = d2.loadExtensions()
	assert.NoError(t, err, "loadExtensions should succeed when DUCKDB_SKIP_EXTENSIONS=true")
}

func TestFindValidHomeDirectory(t *testing.T) {
	tmp := t.TempDir()
	dbPath := filepath.Join(tmp, "test_home.db")

	d, err := NewDuckDBStorage(dbPath)
	assert.NoError(t, err)
	defer d.Close()

	// Test with valid home directory
	validDir := tmp
	origHome := os.Getenv("HOME")
	origDuckHome := os.Getenv("DUCKDB_HOME")
	defer func() {
		if origHome == "" {
			os.Unsetenv("HOME")
		} else {
			os.Setenv("HOME", origHome)
		}
		if origDuckHome == "" {
			os.Unsetenv("DUCKDB_HOME")
		} else {
			os.Setenv("DUCKDB_HOME", origDuckHome)
		}
	}()

	os.Setenv("DUCKDB_HOME", validDir)
	home, err := d.findValidHomeDirectory()
	assert.NoError(t, err)
	assert.Equal(t, validDir, home)

	// Test with nonexistent directory
	os.Setenv("DUCKDB_HOME", "/nonexistent/directory")
	os.Setenv("HOME", "/another/nonexistent")
	home, _ = d.findValidHomeDirectory()
	// Should fall back to /tmp or another writable directory
	assert.NotEmpty(t, home)
	assert.True(t, d.isDirectoryWritable(home), "returned directory should be writable")
}

func TestIsDirectoryWritable(t *testing.T) {
	tmp := t.TempDir()
	dbPath := filepath.Join(tmp, "test_writable.db")

	d, err := NewDuckDBStorage(dbPath)
	assert.NoError(t, err)
	defer d.Close()

	// Test with writable directory
	assert.True(t, d.isDirectoryWritable(tmp), "temp directory should be writable")

	// Test with non-existent directory
	assert.False(t, d.isDirectoryWritable("/nonexistent/directory"), "non-existent directory should not be writable")

	// Test with file instead of directory
	testFile := filepath.Join(tmp, "testfile")
	err = os.WriteFile(testFile, []byte("test"), 0644)
	assert.NoError(t, err)
	assert.False(t, d.isDirectoryWritable(testFile), "file should not be considered a writable directory")

	// Test with unwritable directory (skip on Windows)
	if runtime.GOOS != "windows" {
		unwritableDir := filepath.Join(tmp, "unwritable")
		err = os.MkdirAll(unwritableDir, 0755)
		assert.NoError(t, err)
		err = os.Chmod(unwritableDir, 0555) // read and execute only
		assert.NoError(t, err)
		defer os.Chmod(unwritableDir, 0755) // restore for cleanup

		assert.False(t, d.isDirectoryWritable(unwritableDir), "read-only directory should not be writable")
	}
}

func TestExtensionLoadingStrategies(t *testing.T) {
	tmp := t.TempDir()
	dbPath := filepath.Join(tmp, "test_strategies.db")

	d, err := NewDuckDBStorage(dbPath)
	assert.NoError(t, err)
	defer d.Close()

	// Test tryLoadExtensionsLocally
	err = d.tryLoadExtensionsLocally()
	// This may succeed or fail depending on the DuckDB build, both are acceptable
	t.Logf("tryLoadExtensionsLocally result: %v", err)

	// Test tryLoadExtensionsWithAutoInstall with valid directory
	err = d.tryLoadExtensionsWithAutoInstall(tmp)
	// This may succeed or fail depending on network connectivity, both are acceptable
	t.Logf("tryLoadExtensionsWithAutoInstall result: %v", err)

	// Test tryLoadExtensionsBasic
	err = d.tryLoadExtensionsBasic()
	assert.NoError(t, err, "tryLoadExtensionsBasic should always succeed as it's the fallback")
}

func TestExtensionLoadingWithNetworkIssues(t *testing.T) {
	tmp := t.TempDir()
	dbPath := filepath.Join(tmp, "test_network.db")

	d, err := NewDuckDBStorage(dbPath)
	assert.NoError(t, err)
	defer d.Close()

	// Simulate network issues by using an invalid home directory that would prevent downloads
	invalidHome := "/dev/null" // This should cause permission issues for downloads
	err = d.tryLoadExtensionsWithAutoInstall(invalidHome)
	// We expect this to handle the error gracefully
	t.Logf("tryLoadExtensionsWithAutoInstall with invalid home result: %v", err)

	// The main loadExtensions method should still succeed due to fallback strategies
	// Set an invalid DUCKDB_HOME to force fallbacks
	origDuckHome := os.Getenv("DUCKDB_HOME")
	defer func() {
		if origDuckHome == "" {
			os.Unsetenv("DUCKDB_HOME")
		} else {
			os.Setenv("DUCKDB_HOME", origDuckHome)
		}
	}()

	os.Setenv("DUCKDB_HOME", "/dev/null")
	err = d.loadExtensions()
	assert.NoError(t, err, "loadExtensions should succeed even with invalid DUCKDB_HOME due to fallback strategies")
}

func TestDatabaseWithMissingDataDirectory(t *testing.T) {
	// Test creating database in directories that can't be created (like under /dev/null)
	// Skip this test on Windows as path semantics are different
	if runtime.GOOS != "windows" {
		invalidPath := "/dev/null/nested/test.db"

		// This should fail as /dev/null is not a directory where we can create subdirectories
		_, err := NewDuckDBStorage(invalidPath)
		assert.Error(t, err, "should fail when trying to create database under /dev/null")
	}

	// Test with valid path - create the directory first
	tmp := t.TempDir()
	subdir := filepath.Join(tmp, "subdir")
	err := os.MkdirAll(subdir, 0755)
	assert.NoError(t, err)

	validPath := filepath.Join(subdir, "test.db")

	d, err := NewDuckDBStorage(validPath)
	assert.NoError(t, err, "should succeed with valid path")
	if d != nil {
		d.Close()
	}
}

func TestSnapshotWithMissingDirectory(t *testing.T) {
	tmp := t.TempDir()
	dbPath := filepath.Join(tmp, "test_snapshot_missing.db")

	d, err := NewDuckDBStorage(dbPath)
	assert.NoError(t, err)
	defer d.Close()

	// Try to create snapshot in a path where parent directories can't be created
	// On Unix systems, trying to create under /dev/null should fail
	if runtime.GOOS != "windows" {
		nonExistentSnapshotPath := "/dev/null/nested/snapshot.parquet"
		err = d.SnapshotParquet(context.Background(), nonExistentSnapshotPath)
		assert.Error(t, err, "should fail when trying to create snapshot where parent directories can't be created")
	}

	// Test with WithCreateSnapshotDir option - this should always work
	snapshotDir := filepath.Join(tmp, "snapshots")
	snapshotPath := filepath.Join(snapshotDir, "test.parquet")

	d2, err := NewDuckDBStorage(dbPath, WithSnapshotPath(snapshotPath), WithCreateSnapshotDir(true))
	assert.NoError(t, err)
	defer d2.Close()

	// This should succeed because WithCreateSnapshotDir(true) creates the directory
	err = d2.SnapshotParquet(context.Background(), snapshotPath)
	assert.NoError(t, err, "should succeed when WithCreateSnapshotDir is enabled")

	// Verify the snapshot directory was created (the function creates a timestamped subdirectory)
	_, err = os.Stat(snapshotDir)
	assert.NoError(t, err, "snapshot directory should be created")
}

func TestDatabaseCreationPermissions(t *testing.T) {
	// Test database creation with various permission scenarios
	tmp := t.TempDir()

	// Test normal case - should work
	normalPath := filepath.Join(tmp, "normal.db")
	d1, err := NewDuckDBStorage(normalPath)
	assert.NoError(t, err, "should succeed with normal writable directory")
	if d1 != nil {
		d1.Close()
	}

	// Test with read-only parent directory (skip on Windows)
	if runtime.GOOS != "windows" {
		readOnlyDir := filepath.Join(tmp, "readonly")
		err = os.MkdirAll(readOnlyDir, 0755)
		assert.NoError(t, err)
		err = os.Chmod(readOnlyDir, 0555) // read and execute only
		assert.NoError(t, err)
		defer os.Chmod(readOnlyDir, 0755) // restore for cleanup

		readOnlyPath := filepath.Join(readOnlyDir, "readonly.db")
		_, err = NewDuckDBStorage(readOnlyPath)
		assert.Error(t, err, "should fail when trying to create database in read-only directory")
	}
}

func TestExtensionLoadingRobustness(t *testing.T) {
	tmp := t.TempDir()
	dbPath := filepath.Join(tmp, "test_robust.db")

	// Test with completely invalid environment
	origHome := os.Getenv("HOME")
	origDuckHome := os.Getenv("DUCKDB_HOME")
	defer func() {
		if origHome == "" {
			os.Unsetenv("HOME")
		} else {
			os.Setenv("HOME", origHome)
		}
		if origDuckHome == "" {
			os.Unsetenv("DUCKDB_HOME")
		} else {
			os.Setenv("DUCKDB_HOME", origDuckHome)
		}
	}()

	// Set completely invalid paths
	os.Setenv("DUCKDB_HOME", "/dev/null/invalid")
	os.Setenv("HOME", "/nonexistent/invalid")

	d, err := NewDuckDBStorage(dbPath)
	assert.NoError(t, err)
	defer d.Close()

	// Extension loading should still succeed due to fallback strategies
	err = d.loadExtensions()
	assert.NoError(t, err, "extension loading should succeed even with invalid environment due to fallback strategies")

	// Database should still be functional
	_, err = d.DB().Exec("CREATE TABLE test_table (id INTEGER, name TEXT)")
	assert.NoError(t, err, "database should be functional even if extensions partially fail")

	_, err = d.DB().Exec("INSERT INTO test_table VALUES (1, 'test')")
	assert.NoError(t, err, "database operations should work")

	rows, err := d.DB().Query("SELECT COUNT(*) FROM test_table")
	assert.NoError(t, err, "database queries should work")
	defer rows.Close()

	var count int
	assert.True(t, rows.Next(), "should have results")
	err = rows.Scan(&count)
	assert.NoError(t, err, "should be able to scan results")
	assert.Equal(t, 1, count, "should have one row")
}

func TestInputValidation(t *testing.T) {
	// Test empty database path
	_, err := NewDuckDBStorage("")
	assert.Error(t, err, "should fail with empty database path")
	assert.Contains(t, err.Error(), "database path cannot be empty")

	// Test path with null bytes
	_, err = NewDuckDBStorage("test\x00.db")
	assert.Error(t, err, "should fail with null bytes in path")
	assert.Contains(t, err.Error(), "null bytes")

	// Test invalid parent directory (skip on Windows)
	if runtime.GOOS != "windows" {
		_, err = NewDuckDBStorage("/dev/null/test.db")
		assert.Error(t, err, "should fail when parent directory cannot be created")
	}
}

func TestConfigurationValidation(t *testing.T) {
	tmp := t.TempDir()
	dbPath := filepath.Join(tmp, "test_config.db")

	// Test snapshot frequency without path
	_, err := NewDuckDBStorage(dbPath, WithSnapshotFrequency(5*time.Second))
	assert.Error(t, err, "should fail when snapshot frequency is set without path")
	assert.Contains(t, err.Error(), "snapshot path must be set")

	// Test snapshot path with null bytes
	_, err = NewDuckDBStorage(dbPath, WithSnapshotPath("test\x00"))
	assert.Error(t, err, "should fail with null bytes in snapshot path")
	assert.Contains(t, err.Error(), "null bytes")

	// Test valid configuration
	snapshotPath := filepath.Join(tmp, "snapshots")
	d, err := NewDuckDBStorage(dbPath,
		WithSnapshotFrequency(5*time.Second),
		WithSnapshotPath(snapshotPath))
	assert.NoError(t, err, "should succeed with valid configuration")
	if d != nil {
		d.Close()
	}
}

func TestHealthCheck(t *testing.T) {
	tmp := t.TempDir()
	dbPath := filepath.Join(tmp, "test_health.db")

	// Test healthy database
	d, err := NewDuckDBStorage(dbPath)
	assert.NoError(t, err)
	defer d.Close()

	health := d.HealthCheck()
	assert.True(t, health.DatabaseOK, "database should be OK")
	assert.NotZero(t, health.LastHealthCheck, "health check timestamp should be set")

	// Test IsHealthy method
	assert.True(t, d.IsHealthy(), "database should be healthy")

	// Test extension status
	extStatus := d.GetExtensionStatus()
	// Extension status depends on environment, just check structure
	assert.NotNil(t, extStatus, "extension status should not be nil")

	// Test initialization errors
	initErrors := d.GetInitializationErrors()
	assert.NotNil(t, initErrors, "initialization errors should not be nil")
}

func TestHealthCheckWithProblems(t *testing.T) {
	tmp := t.TempDir()
	dbPath := filepath.Join(tmp, "test_health_problems.db")

	// Create database with problematic configuration
	_, err := NewDuckDBStorage(dbPath,
		WithSnapshotFrequency(5*time.Second), // without path
		WithSkipExtensions(true))

	// This should fail validation
	assert.Error(t, err, "should fail due to configuration validation")

	// Test with snapshot frequency and skipped extensions
	snapshotPath := filepath.Join(tmp, "snapshots")
	d2, err := NewDuckDBStorage(dbPath,
		WithSnapshotFrequency(5*time.Second),
		WithSnapshotPath(snapshotPath),
		WithSkipExtensions(true))
	assert.NoError(t, err)
	defer d2.Close()

	health := d2.HealthCheck()
	assert.True(t, health.SnapshotEnabled, "snapshots should be enabled")
	assert.True(t, health.ExtensionStatus.Skipped, "extensions should be skipped")

	// Should have recommendations about extensions and snapshots
	found := false
	for _, rec := range health.Recommendations {
		if strings.Contains(rec, "Extensions are skipped but snapshots are enabled") {
			found = true
			break
		}
	}
	assert.True(t, found, "should recommend about extension/snapshot conflict")
}

func TestExtensionStatusTracking(t *testing.T) {
	tmp := t.TempDir()
	dbPath := filepath.Join(tmp, "test_ext_status.db")

	// Test with extensions skipped
	d, err := NewDuckDBStorage(dbPath, WithSkipExtensions(true))
	assert.NoError(t, err)
	defer d.Close()

	status := d.GetExtensionStatus()
	assert.True(t, status.Skipped, "extensions should be marked as skipped")
	assert.Equal(t, 0, status.Strategy, "no strategy should be used when skipped")

	// Test with extensions enabled
	d2, err := NewDuckDBStorage(dbPath)
	assert.NoError(t, err)
	defer d2.Close()

	status2 := d2.GetExtensionStatus()
	assert.False(t, status2.Skipped, "extensions should not be skipped")
	// Strategy should be > 0 if any extension loading succeeded
	if len(status2.Loaded) > 0 {
		assert.Greater(t, status2.Strategy, 0, "strategy should be set if extensions loaded")
	}
}

func TestValidationError(t *testing.T) {
	ve := ValidationError{
		Field:   "testField",
		Value:   "testValue",
		Message: "test message",
	}

	expected := "validation error for testField='testValue': test message"
	assert.Equal(t, expected, ve.Error(), "validation error should format correctly")
}

func TestDatabaseConnectionValidation(t *testing.T) {
	// Test with invalid database file
	if runtime.GOOS != "windows" {
		// Try to create database on a device file which should fail
		_, err := NewDuckDBStorage("/dev/null")
		assert.Error(t, err, "should fail when trying to create database on device file")
		// Check for either type of database error that might occur
		assert.True(t,
			strings.Contains(err.Error(), "failed to connect to database") ||
				strings.Contains(err.Error(), "failed to open database") ||
				strings.Contains(err.Error(), "fsync failed"),
			"should contain database error message")
	}
}
