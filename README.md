# QuackQuack

QuackQuack is a resilient Go library for managing DuckDB databases with support for periodic snapshots, restoration, and comprehensive health monitoring. It provides robust error handling, graceful degradation, and detailed status reporting for production environments.

## Features

- 🔄 **Automatic Snapshots**: Periodic database snapshots in Parquet format
- 🔧 **Resilient Extension Loading**: Multiple fallback strategies for DuckDB extensions
- 🏥 **Health Monitoring**: Comprehensive health checks and status reporting
- ⚡ **Graceful Degradation**: Continues operation even when optional features fail
- 🛡️ **Input Validation**: Early detection of configuration issues
- 📊 **Detailed Logging**: Rich logging and error reporting
- 🔍 **Extension Status Tracking**: Monitor which extensions loaded successfully

## Installation

```sh
go get github.com/OpenCHAMI/quack/quack
```

## Quick Start

### Basic Usage

```go
package main

import (
    "context"
    "log"
    "time"

    "github.com/OpenCHAMI/quack/quack"
)

func main() {
    // Create database with automatic snapshots
    storage, err := quack.NewDuckDBStorage("myapp.db", 
        quack.WithSnapshotFrequency(10*time.Minute),
        quack.WithSnapshotPath("backups/"),
        quack.WithCreateSnapshotDir(true))
    
    if err != nil {
        log.Fatalf("Failed to initialize database: %v", err)
    }
    defer storage.Shutdown(context.Background())

    // Check health status
    if !storage.IsHealthy() {
        health := storage.HealthCheck()
        for _, rec := range health.Recommendations {
            log.Printf("Recommendation: %s", rec)
        }
    }

    // Use the database
    db := storage.DB()
    _, err = db.Exec("CREATE TABLE users (id INTEGER, name TEXT)")
    if err != nil {
        log.Printf("Error creating table: %v", err)
    }
}
```

### Production Example with Monitoring

```go
package main

import (
    "context"
    "log"
    "time"

    "github.com/OpenCHAMI/quack/quack"
)

func main() {
    // Production configuration
    storage, err := quack.NewDuckDBStorage("production.db",
        quack.WithSnapshotFrequency(1*time.Hour),
        quack.WithSnapshotPath("/var/backups/myapp/"),
        quack.WithCreateSnapshotDir(true))

    if err != nil {
        // Get detailed error information
        log.Fatalf("Database initialization failed: %v", err)
    }
    defer storage.Shutdown(context.Background())

    // Check for any initialization issues
    if initErrors := storage.GetInitializationErrors(); len(initErrors) > 0 {
        for _, err := range initErrors {
            log.Printf("Initialization warning: %v", err)
        }
    }

    // Monitor extension status
    extStatus := storage.GetExtensionStatus()
    log.Printf("Extensions loaded: %v", extStatus.Loaded)
    if len(extStatus.Failed) > 0 {
        log.Printf("Extensions failed: %v", extStatus.Failed)
    }

    // Periodic health monitoring
    go func() {
        ticker := time.NewTicker(5 * time.Minute)
        defer ticker.Stop()
        
        for range ticker.C {
            health := storage.HealthCheck()
            if !health.Healthy {
                log.Printf("Health check failed: database_ok=%v", health.DatabaseOK)
                for _, rec := range health.Recommendations {
                    log.Printf("Recommendation: %s", rec)
                }
            } else {
                log.Printf("Health check passed")
            }
        }
    }()

    // Your application logic here
    db := storage.DB()
    // ... use database
}
```

## Configuration Options

All configuration is done through functional options:

### Core Options

| Option | Description | Example |
|--------|-------------|---------|
| `WithSnapshotFrequency(duration)` | Enable automatic snapshots at specified interval | `WithSnapshotFrequency(1*time.Hour)` |
| `WithSnapshotPath(path)` | Set snapshot storage directory | `WithSnapshotPath("/backups/")` |
| `WithCreateSnapshotDir(bool)` | Auto-create snapshot directory if missing | `WithCreateSnapshotDir(true)` |
| `WithRestore(path)` | Restore from snapshot on startup | `WithRestore("/backups/latest/")` |

### Extension Management

| Option | Description | Example |
|--------|-------------|---------|
| `WithSkipExtensions(bool)` | Skip all extension loading | `WithSkipExtensions(true)` |

You can also use the environment variable `DUCKDB_SKIP_EXTENSIONS=true` to disable extensions.

### Advanced Example

```go
storage, err := quack.NewDuckDBStorage("app.db",
    // Snapshot configuration
    quack.WithSnapshotFrequency(30*time.Minute),
    quack.WithSnapshotPath("./backups/"),
    quack.WithCreateSnapshotDir(true),
    
    // Extension configuration (useful in containerized environments)
    quack.WithSkipExtensions(false), // Default: try to load extensions
)
```

## Health Monitoring

QuackQuack provides comprehensive health monitoring:

```go
// Quick health check
if storage.IsHealthy() {
    log.Println("Database is healthy")
}

// Detailed health information
health := storage.HealthCheck()
fmt.Printf("Database OK: %v\n", health.DatabaseOK)
fmt.Printf("Extensions Loaded: %v\n", health.ExtensionStatus.Loaded)
fmt.Printf("Extensions Failed: %v\n", health.ExtensionStatus.Failed)

// Get actionable recommendations
for _, rec := range health.Recommendations {
    fmt.Printf("💡 %s\n", rec)
}
```

### Health Status Structure

```go
type HealthStatus struct {
    Healthy          bool             `json:"healthy"`
    DatabaseOK       bool             `json:"database_ok"`
    ExtensionStatus  ExtensionStatus  `json:"extension_status"`
    SnapshotEnabled  bool             `json:"snapshot_enabled"`
    InitErrors       []string         `json:"init_errors,omitempty"`
    LastHealthCheck  time.Time        `json:"last_health_check"`
    Recommendations  []string         `json:"recommendations,omitempty"`
}
```

## Error Handling

QuackQuack provides detailed error information and validation:

```go
storage, err := quack.NewDuckDBStorage("invalid\x00path.db")
if err != nil {
    // Will get: "validation error for path='invalid\x00path.db': database path contains null bytes"
    log.Printf("Validation failed: %v", err)
}

// Check for non-fatal initialization issues
if initErrors := storage.GetInitializationErrors(); len(initErrors) > 0 {
    for _, err := range initErrors {
        log.Printf("Warning: %v", err)
    }
}
```

## Extension Management

QuackQuack uses a multi-strategy approach for loading DuckDB extensions:

1. **Local Loading**: Try to load pre-installed extensions
2. **Auto-Install**: Download and install extensions automatically  
3. **Basic Fallback**: Minimal extension loading

```go
// Check extension status
extStatus := storage.GetExtensionStatus()
fmt.Printf("Strategy used: %d\n", extStatus.Strategy)
fmt.Printf("Loaded: %v\n", extStatus.Loaded)
fmt.Printf("Failed: %v\n", extStatus.Failed)
fmt.Printf("Skipped: %v\n", extStatus.Skipped)

// Disable extensions in problematic environments
storage, err := quack.NewDuckDBStorage("app.db", 
    quack.WithSkipExtensions(true))

// Or via environment variable
os.Setenv("DUCKDB_SKIP_EXTENSIONS", "true")
```

## Environment Variables

| Variable | Description | Default |
|----------|-------------|---------|
| `DUCKDB_HOME` | Directory for extension storage | `$HOME` |
| `DUCKDB_SKIP_EXTENSIONS` | Skip extension loading (`true`/`false`) | `false` |

## Snapshots and Restoration

### Manual Snapshots

```go
ctx := context.Background()
err := storage.SnapshotParquet(ctx, "./manual-backup/")
if err != nil {
    log.Printf("Snapshot failed: %v", err)
}
```

### Restoration

```go
// Restore from specific snapshot
storage, err := quack.NewDuckDBStorage("restored.db",
    quack.WithRestore("./backups/2023-01-01T12-00-00/"))
```

## Troubleshooting

### Common Issues

1. **Extension Loading Failures**:
   ```
   Set DUCKDB_SKIP_EXTENSIONS=true if extensions aren't needed
   ```

2. **Permission Errors**:
   ```
   Ensure the database directory is writable
   Check DUCKDB_HOME permissions
   ```

3. **Snapshot Directory Issues**:
   ```
   Use WithCreateSnapshotDir(true) to auto-create directories
   ```

### Getting Help

Check the health status for actionable recommendations:

```go
health := storage.HealthCheck()
for _, rec := range health.Recommendations {
    fmt.Printf("💡 %s\n", rec)
}
```
}
```

## Examples

For a complete working example, check out the [example/](example/) directory.

## License

This project is licensed under the MIT License. See the [LICENSE](LICENSE) file for details.
