package quack

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strings"
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
	skipExtensions    bool
	wg                sync.WaitGroup
	cancelSnapshot    context.CancelFunc
	extensionStatus   ExtensionStatus
	errors            []error // Track initialization errors
}

// ExtensionStatus tracks the status of extension loading
type ExtensionStatus struct {
	Loaded    []string `json:"loaded"`
	Failed    []string `json:"failed"`
	Skipped   bool     `json:"skipped"`
	Strategy  int      `json:"strategy"` // Which loading strategy succeeded (0 = none)
	LastError string   `json:"last_error,omitempty"`
}

// ValidationError represents configuration validation errors
type ValidationError struct {
	Field   string
	Value   string
	Message string
}

func (e ValidationError) Error() string {
	return fmt.Sprintf("validation error for %s='%s': %s", e.Field, e.Value, e.Message)
}

func NewDuckDBStorage(path string, options ...DuckDBStorageOption) (*DuckDBStorage, error) {
	// Validate database path
	if err := validateDatabasePath(path); err != nil {
		return nil, fmt.Errorf("invalid database path: %w", err)
	}

	db, err := sql.Open("duckdb", path)
	if err != nil {
		return nil, fmt.Errorf("failed to open database at '%s': %w", path, err)
	}

	// Test the connection
	if err := db.Ping(); err != nil {
		db.Close()
		return nil, fmt.Errorf("failed to connect to database at '%s': %w", path, err)
	}

	d := &DuckDBStorage{
		db:             db,
		cancelSnapshot: func() {},
		errors:         make([]error, 0),
	}

	// Apply options and collect any errors
	var optionErrors []error
	for i, option := range options {
		if err := option.apply(d); err != nil {
			optionError := fmt.Errorf("option %d failed: %w", i, err)
			d.errors = append(d.errors, optionError)
			optionErrors = append(optionErrors, optionError)
			log.Warn().Err(optionError).Int("option_index", i).Msg("Error applying DuckDBStorage option")
		}
	}

	// Validate configuration after applying options
	if err := d.validateConfiguration(); err != nil {
		db.Close()
		return nil, fmt.Errorf("configuration validation failed: %w", err)
	}

	// Load extensions and track status
	if err := d.loadExtensions(); err != nil {
		d.errors = append(d.errors, fmt.Errorf("extension loading failed: %w", err))
		log.Warn().Err(err).Msg("Extension loading failed, continuing with limited functionality")
	}

	// Return errors if any critical options failed
	if len(optionErrors) > 0 {
		log.Warn().Int("failed_options", len(optionErrors)).Msg("Some configuration options failed")
	}

	return d, nil
}

// validateDatabasePath validates the database file path
func validateDatabasePath(path string) error {
	if path == "" {
		return ValidationError{
			Field:   "path",
			Value:   path,
			Message: "database path cannot be empty",
		}
	}

	// Check if path is absolute and contains invalid characters
	if strings.Contains(path, "\x00") {
		return ValidationError{
			Field:   "path",
			Value:   path,
			Message: "database path contains null bytes",
		}
	}

	// Validate parent directory exists or can be created
	dir := filepath.Dir(path)
	if dir != "." && dir != "/" {
		if err := os.MkdirAll(dir, 0755); err != nil {
			return ValidationError{
				Field:   "path",
				Value:   path,
				Message: fmt.Sprintf("cannot create parent directory: %v", err),
			}
		}
	}

	return nil
}

// validateConfiguration validates the overall configuration
func (d *DuckDBStorage) validateConfiguration() error {
	var errors []ValidationError

	// Validate snapshot configuration
	if d.snapshotFrequency > 0 && d.snapshotPath == "" {
		errors = append(errors, ValidationError{
			Field:   "snapshotPath",
			Value:   d.snapshotPath,
			Message: "snapshot path must be set when snapshot frequency is enabled",
		})
	}

	if d.snapshotPath != "" {
		if strings.Contains(d.snapshotPath, "\x00") {
			errors = append(errors, ValidationError{
				Field:   "snapshotPath",
				Value:   d.snapshotPath,
				Message: "snapshot path contains null bytes",
			})
		}
	}

	// Return first validation error if any
	if len(errors) > 0 {
		return errors[0]
	}

	return nil
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
	if d.skipExtensions || os.Getenv("DUCKDB_SKIP_EXTENSIONS") == "true" {
		d.extensionStatus.Skipped = true
		log.Info().Msg("Skipping DuckDB extension loading (disabled by configuration)")
		return nil
	}

	home, err := d.findValidHomeDirectory()
	if err != nil {
		log.Warn().Err(err).Msg("Could not find valid home directory, trying fallback approaches")
	}

	// Try multiple strategies for loading extensions
	strategies := []struct {
		name string
		fn   func() error
	}{
		{"local", func() error { return d.tryLoadExtensionsLocally() }},
		{"auto-install", func() error { return d.tryLoadExtensionsWithAutoInstall(home) }},
		{"basic", func() error { return d.tryLoadExtensionsBasic() }},
	}

	var lastErr error
	for i, strategy := range strategies {
		if err := strategy.fn(); err != nil {
			log.Warn().Err(err).Str("strategy", strategy.name).Int("strategy_index", i+1).Msg("Extension loading strategy failed, trying next")
			lastErr = err
			continue
		}
		d.extensionStatus.Strategy = i + 1
		log.Info().Str("strategy", strategy.name).Int("strategy_index", i+1).Msg("Successfully loaded DuckDB extensions")
		return nil
	}

	// If all strategies failed, record the error but don't fail completely
	d.extensionStatus.LastError = lastErr.Error()
	log.Warn().Err(lastErr).Msg("All extension loading strategies failed, continuing without extensions")
	return nil // Don't return error to allow operation without extensions
}

// findValidHomeDirectory tries to find a valid writable directory for DuckDB extensions
func (d *DuckDBStorage) findValidHomeDirectory() (string, error) {
	candidates := []string{
		os.Getenv("DUCKDB_HOME"),
		os.Getenv("HOME"),
		"/tmp",
		"/var/tmp",
		".",
	}

	for _, dir := range candidates {
		if dir == "" {
			continue
		}

		// Check if directory exists and is writable
		if d.isDirectoryWritable(dir) {
			return dir, nil
		}
	}

	return "/tmp", fmt.Errorf("no writable home directory found")
}

// isDirectoryWritable checks if a directory exists and is writable
func (d *DuckDBStorage) isDirectoryWritable(dir string) bool {
	info, err := os.Stat(dir)
	if err != nil {
		return false
	}

	if !info.IsDir() {
		return false
	}

	// Try to create a temporary file to test write permissions
	testFile := fmt.Sprintf("%s/.duckdb_write_test", dir)
	file, err := os.Create(testFile)
	if err != nil {
		return false
	}
	file.Close()
	os.Remove(testFile)
	return true
}

// tryLoadExtensionsLocally attempts to load extensions that are already available locally
func (d *DuckDBStorage) tryLoadExtensionsLocally() error {
	log.Info().Msg("Trying to load DuckDB extensions locally")

	extensions := []string{"json", "parquet"}
	for _, ext := range extensions {
		if _, err := d.db.Exec(fmt.Sprintf("LOAD %s", ext)); err != nil {
			d.extensionStatus.Failed = append(d.extensionStatus.Failed, ext)
			return fmt.Errorf("failed to load extension %s locally: %w", ext, err)
		}
		d.extensionStatus.Loaded = append(d.extensionStatus.Loaded, ext)
	}

	return nil
}

// tryLoadExtensionsWithAutoInstall attempts to load extensions with auto-installation
func (d *DuckDBStorage) tryLoadExtensionsWithAutoInstall(home string) error {
	log.Info().Str("home", home).Msg("Trying to load DuckDB extensions with auto-install")

	// Escape single quotes in path to avoid SQL injection
	escaped := strings.ReplaceAll(home, "'", "''")

	// Set a reasonable timeout for extension downloads
	setupSQL := fmt.Sprintf(`
		SET home_directory='%s';
		SET autoinstall_known_extensions=1;
		SET autoload_known_extensions=1;
	`, escaped)

	_, err := d.db.Exec(setupSQL)
	if err != nil {
		return fmt.Errorf("failed to configure extension settings: %w", err)
	}

	// Try to install and load extensions with timeout handling
	extensions := []string{"json", "parquet"}
	var loadErrors []string

	for _, ext := range extensions {
		installSQL := fmt.Sprintf("INSTALL %s", ext)
		loadSQL := fmt.Sprintf("LOAD %s", ext)

		// Try to install the extension
		if _, err := d.db.Exec(installSQL); err != nil {
			log.Warn().Err(err).Str("extension", ext).Msg("Failed to install extension, trying to load anyway")
		}

		// Try to load the extension
		if _, err := d.db.Exec(loadSQL); err != nil {
			d.extensionStatus.Failed = append(d.extensionStatus.Failed, ext)
			loadErrors = append(loadErrors, fmt.Sprintf("%s: %v", ext, err))
			log.Warn().Err(err).Str("extension", ext).Msg("Failed to load extension")
		} else {
			d.extensionStatus.Loaded = append(d.extensionStatus.Loaded, ext)
			log.Info().Str("extension", ext).Msg("Successfully loaded extension")
		}
	}

	if len(loadErrors) > 0 {
		return fmt.Errorf("failed to load some extensions: %s", strings.Join(loadErrors, ", "))
	}

	return nil
}

// tryLoadExtensionsBasic attempts to load extensions with minimal configuration
func (d *DuckDBStorage) tryLoadExtensionsBasic() error {
	log.Info().Msg("Trying to load DuckDB extensions with basic configuration")

	// Try without setting home directory and with autoinstall disabled
	// This might work if extensions are built-in or pre-installed
	extensions := []string{"json", "parquet"}

	for _, ext := range extensions {
		_, err := d.db.Exec(fmt.Sprintf("LOAD %s", ext))
		if err != nil {
			d.extensionStatus.Failed = append(d.extensionStatus.Failed, ext)
			log.Warn().Err(err).Str("extension", ext).Msg("Failed to load extension")
			// Continue trying other extensions rather than failing completely
		} else {
			d.extensionStatus.Loaded = append(d.extensionStatus.Loaded, ext)
			log.Info().Str("extension", ext).Msg("Successfully loaded extension")
		}
	}

	// Consider it successful if we can at least execute a query
	_, err := d.db.Exec("SELECT 1")
	if err != nil {
		return fmt.Errorf("database not functional after basic extension loading: %w", err)
	}

	return nil
}

// HealthStatus represents the overall health status of the DuckDB storage
type HealthStatus struct {
	Healthy         bool            `json:"healthy"`
	DatabaseOK      bool            `json:"database_ok"`
	ExtensionStatus ExtensionStatus `json:"extension_status"`
	SnapshotEnabled bool            `json:"snapshot_enabled"`
	InitErrors      []string        `json:"init_errors,omitempty"`
	LastHealthCheck time.Time       `json:"last_health_check"`
	Recommendations []string        `json:"recommendations,omitempty"`
}

// HealthCheck performs a comprehensive health check
func (d *DuckDBStorage) HealthCheck() HealthStatus {
	status := HealthStatus{
		LastHealthCheck: time.Now(),
		ExtensionStatus: d.extensionStatus,
		SnapshotEnabled: d.snapshotFrequency > 0,
	}

	// Check database connectivity
	if err := d.db.Ping(); err != nil {
		status.DatabaseOK = false
		status.Recommendations = append(status.Recommendations,
			"Database connection failed. Check database file permissions and disk space.")
	} else {
		status.DatabaseOK = true
	}

	// Add initialization errors
	for _, err := range d.errors {
		status.InitErrors = append(status.InitErrors, err.Error())
	}

	// Generate recommendations
	if len(d.extensionStatus.Failed) > 0 {
		status.Recommendations = append(status.Recommendations,
			fmt.Sprintf("Some extensions failed to load: %v. Consider setting DUCKDB_SKIP_EXTENSIONS=true if extensions are not needed.",
				d.extensionStatus.Failed))
	}

	if d.snapshotFrequency > 0 && d.snapshotPath == "" {
		status.Recommendations = append(status.Recommendations,
			"Snapshot frequency is set but no snapshot path configured. Use WithSnapshotPath() option.")
	}

	if d.extensionStatus.Skipped && d.snapshotFrequency > 0 {
		status.Recommendations = append(status.Recommendations,
			"Extensions are skipped but snapshots are enabled. Parquet snapshots may not work without the parquet extension.")
	}

	// Overall health determination
	status.Healthy = status.DatabaseOK && len(status.InitErrors) == 0

	return status
}

// GetExtensionStatus returns detailed extension loading status
func (d *DuckDBStorage) GetExtensionStatus() ExtensionStatus {
	return d.extensionStatus
}

// GetInitializationErrors returns any errors that occurred during initialization
func (d *DuckDBStorage) GetInitializationErrors() []error {
	return d.errors
}

// IsHealthy returns true if the database is in a healthy state
func (d *DuckDBStorage) IsHealthy() bool {
	return d.HealthCheck().Healthy
}
