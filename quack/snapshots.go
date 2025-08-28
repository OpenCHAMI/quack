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
	// Ensure the directory exists; return error if creation fails
	if err := os.MkdirAll(escapedPath, 0755); err != nil {
		log.Error().Err(err).Str("path", escapedPath).Msg("Failed to create snapshot directory")
		return err
	}

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
	// Ensure extensions are available
	if err := d.loadExtensions(); err != nil {
		return fmt.Errorf("failed to load extensions before restore: %w", err)
	}

	// Read and execute schema.sql to set up the database schema
	schemaFile := filepath.Join(path, "schema.sql")
	if _, err := os.Stat(schemaFile); err != nil {
		return fmt.Errorf("schema.sql not found in snapshot path %q: %w", path, err)
	}
	if err := d.executeSQLFile(schemaFile); err != nil {
		return fmt.Errorf("error executing schema.sql: %w", err)
	}
	log.Info().Str("file", schemaFile).Msg("Executed schema.sql")

	// Read and execute load.sql to load Parquet files
	loadFile := filepath.Join(path, "load.sql")
	if _, err := os.Stat(loadFile); err != nil {
		return fmt.Errorf("load.sql not found in snapshot path %q: %w", path, err)
	}
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
		// Preserve line breaks so multiline statements remain valid
		if sb.Len() > 0 {
			sb.WriteString("\n")
		}
		sb.WriteString(line)

		trimmed := strings.TrimSpace(line)
		if strings.HasSuffix(trimmed, ";") {
			query := strings.TrimSpace(sb.String())
			// skip empty queries
			if query == "" || query == ";" {
				sb.Reset()
				continue
			}
			if _, err := d.db.Exec(query); err != nil {
				return fmt.Errorf("error executing query %q: %w", query, err)
			}
			sb.Reset()
		}
	}

	if err := scanner.Err(); err != nil {
		return err
	}

	// If there's leftover SQL without a trailing semicolon, attempt to execute it
	if sb.Len() > 0 {
		query := strings.TrimSpace(sb.String())
		if query != "" {
			if _, err := d.db.Exec(query); err != nil {
				return fmt.Errorf("error executing final query %q: %w", query, err)
			}
		}
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
