# QuackQuack

QuackQuack is a Go library for managing DuckDB databases with support for periodic snapshots and restoration.

## Installation

To install DuckDBStorage, use `go get`:

```sh
go get github.com/quack/quack
```

## Usage

Here's a basic example of how to use it:

```go
package main

import (
    "log"
    "time"

    "github.com/quack/quack"
)

func main() {
    storage, err := quack.NewDuckDBStorage("path/to/db", quack.WithSnapshotFrequency(10*time.Minute))
    if err != nil {
        log.Fatalf("Failed to initialize DuckDBStorage: %v", err)
    }
    defer storage.Close()

    // Your code here
}
```

For a more detailed example, check out the [example/](example/) directory.

## Configuration

DuckDBStorage supports several configuration options through functional options:
* WithSnapshotFrequency(duration time.Duration): Sets the frequency of database snapshots.
* WithSnapshotPath(path string): Sets the path where snapshots will be stored.
* WithRestoreFirst(restore bool): If set to true, the database will be restored from the latest snapshot on initialization.
Example:

```go
storage, err := quack.NewDuckDBStorage(
    "path/to/db",
    quack.WithSnapshotFrequency(10*time.Minute),
    quack.WithSnapshotPath("path/to/snapshots"),
    quack.WithRestoreFirst(true),
)
```

License
This project is licensed under the MIT License. See the [LICENSE](LICENSE) file for details.