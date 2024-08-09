package quack

import (
	"bufio"
	"context"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/rs/zerolog/log"
)

func (d *DuckDBStorage) snapshotRoutine(ctx context.Context) {
	defer d.wg.Done()
	ticker := time.NewTicker(d.snapshotFrequency)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			log.Info().Msg("Snapshot routine stopped")
			return
		case <-ticker.C:
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
			defer cancel()
			if err := d.SnapshotParquet(ctx, d.snapshotPath); err != nil {
				log.Error().Err(err).Msg("Error taking snapshot")
			}
		}
	}
}

func (d *DuckDBStorage) SnapshotParquet(ctx context.Context, path string) error {
	// Ensure the path is escaped properly
	escapedPath := strings.ReplaceAll(path, "'", "''")
	// Add a trailing slash if it is missing
	if !strings.HasSuffix(escapedPath, "/") {
		escapedPath += "/"
	}
	// Add a date and time to the path
	escapedPath += time.Now().Format("2006-01-02T15-04-05")
	if !strings.HasSuffix(escapedPath, "/") {
		escapedPath += "/"
	}
	// Ensure the directory exists
	os.MkdirAll(escapedPath, 0755)

	// Construct the SQL statement
	sql := fmt.Sprintf(`INSTALL parquet;
	LOAD parquet;
	EXPORT DATABASE '%s' (FORMAT PARQUET);`, escapedPath)

	// Execute the SQL statement with context
	_, err := d.db.ExecContext(ctx, sql)
	if err != nil {
		log.Error().Err(err).Msg("Error exporting DuckDB database to Parquet format")
		return err
	}
	log.Info().
		Str("path", escapedPath).
		Msg("SnapshotParquet")

	return nil
}

func (d *DuckDBStorage) RestoreParquet(path string) error {
	// Load the appropriate extensions for our restore to work correctly
	_, err := d.db.Exec(``)
	if err != nil {
		return err
	}
	// Read and execute schema.sql to set up the database schema
	schemaFile := filepath.Join(path, "schema.sql")
	if err := d.executeSQLFile(schemaFile); err != nil {
		return fmt.Errorf("error executing schema.sql: %w", err)
	}
	log.Info().Str("file", schemaFile).Msg("Executed schema.sql")

	// Read and execute load.sql to load Parquet files
	loadFile := filepath.Join(path, "load.sql")
	if err := d.executeSQLFile(loadFile); err != nil {
		return fmt.Errorf("error executing load.sql: %w", err)
	}
	log.Info().Str("file", loadFile).Msg("Executed load.sql")

	return nil
}

func (d *DuckDBStorage) executeSQLFile(filePath string) error {
	file, err := os.Open(filePath)
	if err != nil {
		return err
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	var sb strings.Builder
	for scanner.Scan() {
		line := scanner.Text()
		sb.WriteString(line)
		if strings.HasSuffix(strings.TrimSpace(line), ";") {
			_, err := d.db.Exec(sb.String())
			if err != nil {
				return err
			}
			sb.Reset()
		}
	}

	if err := scanner.Err(); err != nil {
		return err
	}

	return nil
}

func (d *DuckDBStorage) restore(path string) error {
	log.Info().Msg("Restoring snapshot")

	// Find the most recent snapshot directory
	snapshotDir, err := findMostRecentSnapshotDir(path)
	if err != nil {
		return err
	}

	err = d.RestoreParquet(snapshotDir)
	if err != nil {
		return err
	}
	return nil
}

// findMostRecentSnapshotDir finds the most recent directory under the given path
func findMostRecentSnapshotDir(path string) (string, error) {
	entries, err := os.ReadDir(path)
	if err != nil {
		return "", err
	}

	var dirs []fs.FileInfo
	for _, entry := range entries {
		if entry.IsDir() {
			info, err := entry.Info()
			if err != nil {
				return "", err
			}
			dirs = append(dirs, info)
		}
	}

	if len(dirs) == 0 {
		return "", fmt.Errorf("no snapshot directories found")
	}

	// Sort directories by name (assuming they are named by date)
	sort.Slice(dirs, func(i, j int) bool {
		return dirs[i].Name() > dirs[j].Name() // descending order
	})

	// Return the most recent directory
	mostRecentDir := filepath.Join(path, dirs[0].Name())
	return mostRecentDir, nil
}
