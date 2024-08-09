package main

import (
	"context"
	"time"

	"fmt"

	q "github.com/openchami/quack/quack"
)

// Define your Go struct
type User struct {
	ID    int    `db:"id"`
	Name  string `db:"name"`
	Email string `db:"email"`
	Age   int    `db:"age"`
}

func main() {
	// Create a new Quack instance with a set of options
	newQuack, err := q.NewDuckDBStorage("tmpquack.db",
		q.WithSnapshotFrequency(5*time.Minute),
		q.WithSnapshotPath("quack-snapshots/"),
		q.WithRestore("quack-snapshots/"))
	if err != nil {
		panic(err)
	}
	defer newQuack.Close()

	// Create SQL to create a table for the User struct
	createTableSQL := GenerateSQL(User{})

	// Print the SQL statements
	fmt.Println("CREATE TABLE SQL:")
	fmt.Println(createTableSQL)

	// Execute the SQL statement
	result, err := newQuack.DB().Exec(createTableSQL)
	if err != nil {
		panic(err)
	}
	rowsAffected, err := result.RowsAffected()
	if err != nil {
		panic(err)
	}
	fmt.Println("Rows Affected: ", rowsAffected)
	newQuack.Shutdown(context.Background())
}
