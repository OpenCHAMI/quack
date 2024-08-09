package main

import (
	"fmt"
	"reflect"
	"strings"
)

// GenerateSQL generates a CREATE TABLE SQL statement from a Go struct
func GenerateSQL(structType interface{}) string {
	t := reflect.TypeOf(structType)
	if t.Kind() != reflect.Struct {
		panic("expected a struct")
	}

	tableName := strings.ToLower(t.Name())
	var columns []string

	for i := 0; i < t.NumField(); i++ {
		field := t.Field(i)
		columnName := getColumnName(field)
		columnType := mapGoTypeToSQLType(field.Type.Kind())
		if columnType == "" {
			continue
		}
		columns = append(columns, fmt.Sprintf("%s %s", columnName, columnType))
	}

	return fmt.Sprintf("CREATE TABLE %s (\n%s\n);", tableName, strings.Join(columns, ",\n"))
}

// getColumnName retrieves the column name from the db tag or defaults to field name
func getColumnName(field reflect.StructField) string {
	tag := field.Tag.Get("db")
	if tag != "" {
		return strings.ToLower(tag)
	}
	return strings.ToLower(field.Name)
}

// mapGoTypeToSQLType maps Go type kinds to SQL types
func mapGoTypeToSQLType(kind reflect.Kind) string {
	switch kind {
	case reflect.String:
		return "TEXT"
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return "INTEGER"
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return "INTEGER"
	case reflect.Float32, reflect.Float64:
		return "REAL"
	case reflect.Bool:
		return "BOOLEAN"
	default:
		return ""
	}
}
