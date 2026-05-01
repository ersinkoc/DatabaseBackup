package postgres

import (
	"context"
	"fmt"
	"io"
	"strings"

	"github.com/kronos/kronos/internal/drivers"
)

func pgNativeRestore(ctx context.Context, target drivers.Target, r drivers.RecordReader, opts drivers.RestoreOptions, queryer pgNativeQueryer) error {
	for {
		record, err := r.NextRecord()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
		if record.Done || !isRestorableObject(record.Object.Kind) {
			continue
		}
		if opts.DryRun {
			continue
		}
		if !opts.ReplaceExisting {
			return fmt.Errorf("postgres restore requires replace_existing=true because plain SQL restore can overwrite existing objects")
		}
		statements, err := splitPGSQLStatements(string(record.Payload))
		if err != nil {
			return err
		}
		for _, statement := range statements {
			if _, err := queryer.SimpleQuery(ctx, target, statement); err != nil {
				return err
			}
		}
	}
}

func splitPGSQLStatements(sql string) ([]string, error) {
	var statements []string
	var current strings.Builder
	var dollarTag string
	inSingle := false
	inDouble := false
	inLineComment := false
	inBlockComment := false
	for i := 0; i < len(sql); i++ {
		ch := sql[i]
		next := byte(0)
		if i+1 < len(sql) {
			next = sql[i+1]
		}
		current.WriteByte(ch)

		switch {
		case inLineComment:
			if ch == '\n' {
				inLineComment = false
			}
			continue
		case inBlockComment:
			if ch == '*' && next == '/' {
				current.WriteByte(next)
				i++
				inBlockComment = false
			}
			continue
		case dollarTag != "":
			if strings.HasPrefix(sql[i:], dollarTag) {
				for j := 1; j < len(dollarTag); j++ {
					current.WriteByte(sql[i+j])
				}
				i += len(dollarTag) - 1
				dollarTag = ""
			}
			continue
		case inSingle:
			if ch == '\'' && next == '\'' {
				current.WriteByte(next)
				i++
				continue
			}
			if ch == '\'' {
				inSingle = false
			}
			continue
		case inDouble:
			if ch == '"' && next == '"' {
				current.WriteByte(next)
				i++
				continue
			}
			if ch == '"' {
				inDouble = false
			}
			continue
		}

		if ch == '-' && next == '-' {
			current.WriteByte(next)
			i++
			inLineComment = true
			continue
		}
		if ch == '/' && next == '*' {
			current.WriteByte(next)
			i++
			inBlockComment = true
			continue
		}
		if ch == '\'' {
			inSingle = true
			continue
		}
		if ch == '"' {
			inDouble = true
			continue
		}
		if ch == '$' {
			if tag, ok := readPGDollarTag(sql[i:]); ok {
				dollarTag = tag
				for j := 1; j < len(tag); j++ {
					current.WriteByte(sql[i+j])
				}
				i += len(tag) - 1
				continue
			}
		}
		if ch == ';' {
			statement := strings.TrimSpace(current.String())
			if statement != "" {
				statements = append(statements, statement)
			}
			current.Reset()
		}
	}
	if inSingle || inDouble || inBlockComment || dollarTag != "" {
		return nil, fmt.Errorf("unterminated postgres SQL statement")
	}
	if statement := strings.TrimSpace(current.String()); statement != "" {
		statements = append(statements, statement)
	}
	return statements, nil
}

func readPGDollarTag(sql string) (string, bool) {
	if len(sql) < 2 || sql[0] != '$' {
		return "", false
	}
	for i := 1; i < len(sql); i++ {
		ch := sql[i]
		if ch == '$' {
			return sql[:i+1], true
		}
		if ch != '_' && (ch < '0' || ch > '9') && (ch < 'A' || ch > 'Z') && (ch < 'a' || ch > 'z') {
			return "", false
		}
	}
	return "", false
}
