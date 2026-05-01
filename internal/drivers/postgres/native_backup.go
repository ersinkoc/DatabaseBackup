package postgres

import (
	"bytes"
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/kronos/kronos/internal/drivers"
)

const pgNativeTablesQuery = `
select n.nspname as schema_name, c.relname as table_name
from pg_catalog.pg_class c
join pg_catalog.pg_namespace n on n.oid = c.relnamespace
where c.relkind in ('r', 'p')
  and n.nspname not in ('pg_catalog', 'information_schema')
  and n.nspname not like 'pg_toast%'
order by n.nspname, c.relname`

type pgNativeTable struct {
	Schema string
	Name   string
}

type pgNativeColumn struct {
	Name    string
	Type    string
	NotNull bool
	Default *string
}

type pgNativeConstraint struct {
	Name       string
	Definition string
}

type pgNativeIndex struct {
	Definition string
}

type pgNativeTrigger struct {
	Definition string
}

type pgNativeRoutine struct {
	Definition string
}

type pgNativeExtension struct {
	Name    string
	Schema  string
	Version string
}

type pgNativeType struct {
	Definition string
}

type pgNativeSequence struct {
	Schema      string
	Name        string
	DataType    string
	StartValue  string
	MinValue    string
	MaxValue    string
	IncrementBy string
	CacheSize   string
	Cycle       bool
	LastValue   *string
	OwnedSchema string
	OwnedTable  string
	OwnedColumn string
}

type pgNativeView struct {
	Schema       string
	Name         string
	Definition   string
	Materialized bool
}

type pgNativeTableDump struct {
	Table       pgNativeTable
	Columns     []pgNativeColumn
	Rows        [][]*string
	Constraints []pgNativeConstraint
	Indexes     []pgNativeIndex
	Triggers    []pgNativeTrigger
}

func pgNativeBackupFull(ctx context.Context, target drivers.Target, w drivers.RecordWriter, queryer pgNativeQueryer) (drivers.ResumePoint, error) {
	if includeGlobals(target) {
		return drivers.ResumePoint{}, fmt.Errorf("native postgres backup does not support include_globals yet")
	}
	tables, err := pgNativeListTables(ctx, target, queryer)
	if err != nil {
		return drivers.ResumePoint{}, err
	}
	extensions, err := pgNativeListExtensions(ctx, target, queryer)
	if err != nil {
		return drivers.ResumePoint{}, err
	}
	types, err := pgNativeListTypes(ctx, target, queryer)
	if err != nil {
		return drivers.ResumePoint{}, err
	}
	sequences, err := pgNativeListSequences(ctx, target, queryer)
	if err != nil {
		return drivers.ResumePoint{}, err
	}
	views, err := pgNativeListViews(ctx, target, queryer)
	if err != nil {
		return drivers.ResumePoint{}, err
	}
	routines, err := pgNativeListRoutines(ctx, target, queryer)
	if err != nil {
		return drivers.ResumePoint{}, err
	}
	var payload bytes.Buffer
	payload.WriteString("SET standard_conforming_strings = on;\n\n")
	dumps := make([]pgNativeTableDump, 0, len(tables))
	var totalRows int64
	for _, table := range tables {
		columns, err := pgNativeListColumns(ctx, target, queryer, table)
		if err != nil {
			return drivers.ResumePoint{}, err
		}
		rows, err := pgNativeReadTable(ctx, target, queryer, table, columns)
		if err != nil {
			return drivers.ResumePoint{}, err
		}
		totalRows += int64(len(rows))
		constraints, err := pgNativeListConstraints(ctx, target, queryer, table)
		if err != nil {
			return drivers.ResumePoint{}, err
		}
		indexes, err := pgNativeListIndexes(ctx, target, queryer, table)
		if err != nil {
			return drivers.ResumePoint{}, err
		}
		triggers, err := pgNativeListTriggers(ctx, target, queryer, table)
		if err != nil {
			return drivers.ResumePoint{}, err
		}
		dumps = append(dumps, pgNativeTableDump{
			Table:       table,
			Columns:     columns,
			Rows:        rows,
			Constraints: constraints,
			Indexes:     indexes,
			Triggers:    triggers,
		})
	}
	for _, extension := range extensions {
		writeNativeExtensionDefinition(&payload, extension)
	}
	for _, typ := range types {
		writeNativeTypeDefinition(&payload, typ)
	}
	for _, sequence := range sequences {
		writeNativeSequenceDefinition(&payload, sequence)
	}
	for _, dump := range dumps {
		writeNativeTableDefinition(&payload, dump.Table, dump.Columns)
	}
	for _, dump := range dumps {
		writeNativeTableData(&payload, dump.Table, dump.Columns, dump.Rows)
	}
	for _, routine := range routines {
		writeNativeRoutineDefinition(&payload, routine)
	}
	for _, dump := range dumps {
		writeNativeTablePostData(&payload, dump.Table, dump.Constraints, dump.Indexes, dump.Triggers)
	}
	for _, sequence := range sequences {
		writeNativeSequenceSetValue(&payload, sequence)
	}
	for _, view := range views {
		writeNativeViewDefinition(&payload, view)
	}
	obj := drivers.ObjectRef{Schema: "public", Name: databaseName(target), Kind: databaseObjectKind}
	if err := w.WriteRecord(obj, payload.Bytes()); err != nil {
		return drivers.ResumePoint{}, err
	}
	if err := w.FinishObject(obj, totalRows); err != nil {
		return drivers.ResumePoint{}, err
	}
	return drivers.ResumePoint{Driver: "postgres", Position: "pgwire:native-sql"}, nil
}

func pgNativeListTables(ctx context.Context, target drivers.Target, queryer pgNativeQueryer) ([]pgNativeTable, error) {
	result, err := queryer.SimpleQuery(ctx, target, pgNativeTablesQuery)
	if err != nil {
		return nil, err
	}
	rows, err := pgRowsByName(result)
	if err != nil {
		return nil, err
	}
	tables := make([]pgNativeTable, 0, len(rows))
	for _, row := range rows {
		tables = append(tables, pgNativeTable{Schema: row["schema_name"], Name: row["table_name"]})
	}
	return tables, nil
}

func pgNativeListExtensions(ctx context.Context, target drivers.Target, queryer pgNativeQueryer) ([]pgNativeExtension, error) {
	query := `
select e.extname as extension_name,
       n.nspname as schema_name,
       e.extversion as extension_version
from pg_catalog.pg_extension e
join pg_catalog.pg_namespace n on n.oid = e.extnamespace
where e.extname <> 'plpgsql'
order by e.extname`
	result, err := queryer.SimpleQuery(ctx, target, query)
	if err != nil {
		return nil, err
	}
	rows, err := pgRowsByName(result)
	if err != nil {
		return nil, err
	}
	extensions := make([]pgNativeExtension, 0, len(rows))
	for _, row := range rows {
		if row["extension_name"] == "" {
			continue
		}
		extensions = append(extensions, pgNativeExtension{
			Name:    row["extension_name"],
			Schema:  row["schema_name"],
			Version: row["extension_version"],
		})
	}
	return extensions, nil
}

func pgNativeListTypes(ctx context.Context, target drivers.Target, queryer pgNativeQueryer) ([]pgNativeType, error) {
	enums, err := pgNativeListEnumTypes(ctx, target, queryer)
	if err != nil {
		return nil, err
	}
	domains, err := pgNativeListDomainTypes(ctx, target, queryer)
	if err != nil {
		return nil, err
	}
	return append(enums, domains...), nil
}

func pgNativeListEnumTypes(ctx context.Context, target drivers.Target, queryer pgNativeQueryer) ([]pgNativeType, error) {
	query := `
select n.nspname as schema_name,
       t.typname as type_name,
       string_agg(quote_literal(e.enumlabel), ', ' order by e.enumsortorder) as enum_labels
from pg_catalog.pg_type t
join pg_catalog.pg_namespace n on n.oid = t.typnamespace
join pg_catalog.pg_enum e on e.enumtypid = t.oid
where t.typtype = 'e'
  and n.nspname not in ('pg_catalog', 'information_schema')
  and n.nspname not like 'pg_toast%'
group by n.nspname, t.typname
order by n.nspname, t.typname`
	result, err := queryer.SimpleQuery(ctx, target, query)
	if err != nil {
		return nil, err
	}
	rows, err := pgRowsByName(result)
	if err != nil {
		return nil, err
	}
	types := make([]pgNativeType, 0, len(rows))
	for _, row := range rows {
		if row["schema_name"] == "" || row["type_name"] == "" || row["enum_labels"] == "" {
			continue
		}
		types = append(types, pgNativeType{
			Definition: fmt.Sprintf("CREATE TYPE %s AS ENUM (%s);", quotePGQualifiedName(row["schema_name"], row["type_name"]), row["enum_labels"]),
		})
	}
	return types, nil
}

func pgNativeListDomainTypes(ctx context.Context, target drivers.Target, queryer pgNativeQueryer) ([]pgNativeType, error) {
	query := `
select n.nspname as schema_name,
       t.typname as type_name,
       pg_catalog.format_type(t.typbasetype, t.typtypmod) as base_type,
       pg_catalog.pg_get_expr(t.typdefaultbin, 0) as domain_default,
       t.typnotnull::text as not_null,
       string_agg(pg_catalog.pg_get_constraintdef(con.oid, true), ' ' order by con.conname) as constraints
from pg_catalog.pg_type t
join pg_catalog.pg_namespace n on n.oid = t.typnamespace
left join pg_catalog.pg_constraint con on con.contypid = t.oid
where t.typtype = 'd'
  and n.nspname not in ('pg_catalog', 'information_schema')
  and n.nspname not like 'pg_toast%'
group by n.nspname, t.typname, t.typbasetype, t.typtypmod, t.typdefaultbin, t.typnotnull
order by n.nspname, t.typname`
	result, err := queryer.SimpleQuery(ctx, target, query)
	if err != nil {
		return nil, err
	}
	rows, err := pgRowsByName(result)
	if err != nil {
		return nil, err
	}
	types := make([]pgNativeType, 0, len(rows))
	for _, row := range rows {
		if row["schema_name"] == "" || row["type_name"] == "" || row["base_type"] == "" {
			continue
		}
		var definition strings.Builder
		fmt.Fprintf(&definition, "CREATE DOMAIN %s AS %s", quotePGQualifiedName(row["schema_name"], row["type_name"]), row["base_type"])
		if row["domain_default"] != "" {
			fmt.Fprintf(&definition, " DEFAULT %s", row["domain_default"])
		}
		if parsePGBool(row["not_null"]) {
			definition.WriteString(" NOT NULL")
		}
		if row["constraints"] != "" {
			definition.WriteByte(' ')
			definition.WriteString(row["constraints"])
		}
		definition.WriteByte(';')
		types = append(types, pgNativeType{Definition: definition.String()})
	}
	return types, nil
}

func pgNativeListSequences(ctx context.Context, target drivers.Target, queryer pgNativeQueryer) ([]pgNativeSequence, error) {
	query := `
select s.schemaname as schema_name,
       s.sequencename as sequence_name,
       s.data_type,
       s.start_value::text,
       s.min_value::text,
       s.max_value::text,
       s.increment_by::text,
       s.cache_size::text,
       s.cycle::text,
       s.last_value::text,
       tn.nspname as owned_schema,
       tc.relname as owned_table,
       ta.attname as owned_column
from pg_catalog.pg_sequences s
join pg_catalog.pg_class c on c.relkind = 'S' and c.relname = s.sequencename
join pg_catalog.pg_namespace n on n.oid = c.relnamespace and n.nspname = s.schemaname
left join pg_catalog.pg_depend d on d.objid = c.oid and d.deptype in ('a', 'i')
left join pg_catalog.pg_class tc on tc.oid = d.refobjid
left join pg_catalog.pg_namespace tn on tn.oid = tc.relnamespace
left join pg_catalog.pg_attribute ta on ta.attrelid = tc.oid and ta.attnum = d.refobjsubid
where s.schemaname not in ('pg_catalog', 'information_schema')
  and s.schemaname not like 'pg_toast%'
order by s.schemaname, s.sequencename`
	result, err := queryer.SimpleQuery(ctx, target, query)
	if err != nil {
		return nil, err
	}
	rows, err := pgRowsByName(result)
	if err != nil {
		return nil, err
	}
	sequences := make([]pgNativeSequence, 0, len(rows))
	for _, row := range rows {
		sequences = append(sequences, pgNativeSequence{
			Schema:      row["schema_name"],
			Name:        row["sequence_name"],
			DataType:    row["data_type"],
			StartValue:  row["start_value"],
			MinValue:    row["min_value"],
			MaxValue:    row["max_value"],
			IncrementBy: row["increment_by"],
			CacheSize:   row["cache_size"],
			Cycle:       parsePGBool(row["cycle"]),
			LastValue:   rowPtr(row, "last_value"),
			OwnedSchema: row["owned_schema"],
			OwnedTable:  row["owned_table"],
			OwnedColumn: row["owned_column"],
		})
	}
	return sequences, nil
}

func pgNativeListViews(ctx context.Context, target drivers.Target, queryer pgNativeQueryer) ([]pgNativeView, error) {
	query := `
select n.nspname as schema_name,
       c.relname as view_name,
       c.relkind,
       pg_catalog.pg_get_viewdef(c.oid, true) as view_def
from pg_catalog.pg_class c
join pg_catalog.pg_namespace n on n.oid = c.relnamespace
where c.relkind in ('v', 'm')
  and n.nspname not in ('pg_catalog', 'information_schema')
  and n.nspname not like 'pg_toast%'
order by n.nspname, c.relname`
	result, err := queryer.SimpleQuery(ctx, target, query)
	if err != nil {
		return nil, err
	}
	rows, err := pgRowsByName(result)
	if err != nil {
		return nil, err
	}
	views := make([]pgNativeView, 0, len(rows))
	for _, row := range rows {
		if row["schema_name"] == "" || row["view_name"] == "" || row["view_def"] == "" {
			continue
		}
		views = append(views, pgNativeView{
			Schema:       row["schema_name"],
			Name:         row["view_name"],
			Definition:   row["view_def"],
			Materialized: row["relkind"] == "m",
		})
	}
	return views, nil
}

func pgNativeListRoutines(ctx context.Context, target drivers.Target, queryer pgNativeQueryer) ([]pgNativeRoutine, error) {
	query := `
select pg_catalog.pg_get_functiondef(p.oid) as routine_def
from pg_catalog.pg_proc p
join pg_catalog.pg_namespace n on n.oid = p.pronamespace
where p.prokind in ('f', 'p')
  and n.nspname not in ('pg_catalog', 'information_schema')
  and n.nspname not like 'pg_toast%'
order by n.nspname, p.proname, p.oid`
	result, err := queryer.SimpleQuery(ctx, target, query)
	if err != nil {
		return nil, err
	}
	rows, err := pgRowsByName(result)
	if err != nil {
		return nil, err
	}
	routines := make([]pgNativeRoutine, 0, len(rows))
	for _, row := range rows {
		if row["routine_def"] == "" {
			continue
		}
		routines = append(routines, pgNativeRoutine{Definition: row["routine_def"]})
	}
	return routines, nil
}

func pgNativeListColumns(ctx context.Context, target drivers.Target, queryer pgNativeQueryer, table pgNativeTable) ([]pgNativeColumn, error) {
	query := fmt.Sprintf(`
select a.attname as column_name,
       pg_catalog.format_type(a.atttypid, a.atttypmod) as data_type,
       a.attnotnull::text as not_null,
       pg_catalog.pg_get_expr(ad.adbin, ad.adrelid) as column_default
from pg_catalog.pg_attribute a
join pg_catalog.pg_class c on c.oid = a.attrelid
join pg_catalog.pg_namespace n on n.oid = c.relnamespace
left join pg_catalog.pg_attrdef ad on ad.adrelid = a.attrelid and ad.adnum = a.attnum
where n.nspname = %s
  and c.relname = %s
  and a.attnum > 0
  and not a.attisdropped
order by a.attnum`, quotePGLiteral(table.Schema), quotePGLiteral(table.Name))
	result, err := queryer.SimpleQuery(ctx, target, query)
	if err != nil {
		return nil, err
	}
	rows, err := pgRowsByName(result)
	if err != nil {
		return nil, err
	}
	columns := make([]pgNativeColumn, 0, len(rows))
	for _, row := range rows {
		columns = append(columns, pgNativeColumn{
			Name:    row["column_name"],
			Type:    row["data_type"],
			NotNull: parsePGBool(row["not_null"]),
			Default: rowPtr(row, "column_default"),
		})
	}
	return columns, nil
}

func pgNativeListConstraints(ctx context.Context, target drivers.Target, queryer pgNativeQueryer, table pgNativeTable) ([]pgNativeConstraint, error) {
	query := fmt.Sprintf(`
select con.conname as constraint_name,
       pg_catalog.pg_get_constraintdef(con.oid, true) as constraint_def
from pg_catalog.pg_constraint con
join pg_catalog.pg_class c on c.oid = con.conrelid
join pg_catalog.pg_namespace n on n.oid = c.relnamespace
where n.nspname = %s
  and c.relname = %s
  and con.contype in ('p', 'u', 'c', 'f', 'x')
order by case when con.contype = 'f' then 1 else 0 end, con.conname`, quotePGLiteral(table.Schema), quotePGLiteral(table.Name))
	result, err := queryer.SimpleQuery(ctx, target, query)
	if err != nil {
		return nil, err
	}
	rows, err := pgRowsByName(result)
	if err != nil {
		return nil, err
	}
	constraints := make([]pgNativeConstraint, 0, len(rows))
	for _, row := range rows {
		if row["constraint_name"] == "" || row["constraint_def"] == "" {
			continue
		}
		constraints = append(constraints, pgNativeConstraint{Name: row["constraint_name"], Definition: row["constraint_def"]})
	}
	return constraints, nil
}

func pgNativeListIndexes(ctx context.Context, target drivers.Target, queryer pgNativeQueryer, table pgNativeTable) ([]pgNativeIndex, error) {
	query := fmt.Sprintf(`
select pg_catalog.pg_get_indexdef(i.indexrelid) as index_def
from pg_catalog.pg_index i
join pg_catalog.pg_class c on c.oid = i.indrelid
join pg_catalog.pg_namespace n on n.oid = c.relnamespace
join pg_catalog.pg_class ic on ic.oid = i.indexrelid
where n.nspname = %s
  and c.relname = %s
  and not exists (
    select 1
    from pg_catalog.pg_constraint con
    where con.conindid = i.indexrelid
  )
order by ic.relname`, quotePGLiteral(table.Schema), quotePGLiteral(table.Name))
	result, err := queryer.SimpleQuery(ctx, target, query)
	if err != nil {
		return nil, err
	}
	rows, err := pgRowsByName(result)
	if err != nil {
		return nil, err
	}
	indexes := make([]pgNativeIndex, 0, len(rows))
	for _, row := range rows {
		if row["index_def"] == "" {
			continue
		}
		indexes = append(indexes, pgNativeIndex{Definition: row["index_def"]})
	}
	return indexes, nil
}

func pgNativeListTriggers(ctx context.Context, target drivers.Target, queryer pgNativeQueryer, table pgNativeTable) ([]pgNativeTrigger, error) {
	query := fmt.Sprintf(`
select pg_catalog.pg_get_triggerdef(t.oid, true) as trigger_def
from pg_catalog.pg_trigger t
join pg_catalog.pg_class c on c.oid = t.tgrelid
join pg_catalog.pg_namespace n on n.oid = c.relnamespace
where n.nspname = %s
  and c.relname = %s
  and not t.tgisinternal
order by t.tgname`, quotePGLiteral(table.Schema), quotePGLiteral(table.Name))
	result, err := queryer.SimpleQuery(ctx, target, query)
	if err != nil {
		return nil, err
	}
	rows, err := pgRowsByName(result)
	if err != nil {
		return nil, err
	}
	triggers := make([]pgNativeTrigger, 0, len(rows))
	for _, row := range rows {
		if row["trigger_def"] == "" {
			continue
		}
		triggers = append(triggers, pgNativeTrigger{Definition: row["trigger_def"]})
	}
	return triggers, nil
}

func pgNativeReadTable(ctx context.Context, target drivers.Target, queryer pgNativeQueryer, table pgNativeTable, columns []pgNativeColumn) ([][]*string, error) {
	if len(columns) == 0 {
		query := fmt.Sprintf("select count(*)::text as row_count from %s", quotePGQualifiedName(table.Schema, table.Name))
		result, err := queryer.SimpleQuery(ctx, target, query)
		if err != nil {
			return nil, err
		}
		rows, err := pgRowsByName(result)
		if err != nil {
			return nil, err
		}
		if len(rows) == 0 {
			return nil, nil
		}
		count, err := strconv.Atoi(rows[0]["row_count"])
		if err != nil || count < 0 {
			return nil, fmt.Errorf("invalid postgres zero-column row count %q", rows[0]["row_count"])
		}
		return make([][]*string, count), nil
	}
	names := make([]string, 0, len(columns))
	for _, column := range columns {
		names = append(names, quotePGIdentifier(column.Name))
	}
	query := fmt.Sprintf("select %s from %s", strings.Join(names, ", "), quotePGQualifiedName(table.Schema, table.Name))
	result, err := queryer.SimpleQuery(ctx, target, query)
	if err != nil {
		return nil, err
	}
	return result.Rows, nil
}

func writeNativeExtensionDefinition(w *bytes.Buffer, extension pgNativeExtension) {
	fmt.Fprintf(w, "CREATE EXTENSION IF NOT EXISTS %s", quotePGIdentifier(extension.Name))
	if extension.Schema != "" {
		fmt.Fprintf(w, " WITH SCHEMA %s", quotePGIdentifier(extension.Schema))
	}
	if extension.Version != "" {
		fmt.Fprintf(w, " VERSION %s", quotePGLiteral(extension.Version))
	}
	w.WriteString(";\n\n")
}

func writeNativeTypeDefinition(w *bytes.Buffer, typ pgNativeType) {
	definition := strings.TrimSpace(typ.Definition)
	if definition == "" {
		return
	}
	if !strings.HasSuffix(definition, ";") {
		definition += ";"
	}
	w.WriteString(definition)
	w.WriteString("\n\n")
}

func writeNativeSequenceDefinition(w *bytes.Buffer, sequence pgNativeSequence) {
	if sequence.Schema != "public" {
		fmt.Fprintf(w, "CREATE SCHEMA IF NOT EXISTS %s;\n", quotePGIdentifier(sequence.Schema))
	}
	fmt.Fprintf(w, "CREATE SEQUENCE %s", quotePGQualifiedName(sequence.Schema, sequence.Name))
	if sequence.DataType != "" {
		fmt.Fprintf(w, " AS %s", sequence.DataType)
	}
	writeSequenceClause(w, " START WITH ", sequence.StartValue)
	writeSequenceClause(w, " INCREMENT BY ", sequence.IncrementBy)
	writeSequenceClause(w, " MINVALUE ", sequence.MinValue)
	writeSequenceClause(w, " MAXVALUE ", sequence.MaxValue)
	writeSequenceClause(w, " CACHE ", sequence.CacheSize)
	if sequence.Cycle {
		w.WriteString(" CYCLE")
	} else {
		w.WriteString(" NO CYCLE")
	}
	w.WriteString(";\n")
	if sequence.OwnedSchema != "" && sequence.OwnedTable != "" && sequence.OwnedColumn != "" {
		fmt.Fprintf(w, "ALTER SEQUENCE %s OWNED BY %s.%s;\n", quotePGQualifiedName(sequence.Schema, sequence.Name), quotePGQualifiedName(sequence.OwnedSchema, sequence.OwnedTable), quotePGIdentifier(sequence.OwnedColumn))
	}
	w.WriteByte('\n')
}

func writeNativeSequenceSetValue(w *bytes.Buffer, sequence pgNativeSequence) {
	if sequence.LastValue == nil || *sequence.LastValue == "" {
		return
	}
	fmt.Fprintf(w, "SELECT pg_catalog.setval(%s, %s, true);\n", quotePGLiteral(quotePGQualifiedName(sequence.Schema, sequence.Name)), *sequence.LastValue)
}

func writeNativeViewDefinition(w *bytes.Buffer, view pgNativeView) {
	if view.Schema != "public" {
		fmt.Fprintf(w, "CREATE SCHEMA IF NOT EXISTS %s;\n", quotePGIdentifier(view.Schema))
	}
	if view.Materialized {
		fmt.Fprintf(w, "CREATE MATERIALIZED VIEW %s AS\n%s\nWITH NO DATA;\n\n", quotePGQualifiedName(view.Schema, view.Name), strings.TrimSpace(view.Definition))
		return
	}
	fmt.Fprintf(w, "CREATE VIEW %s AS\n%s;\n\n", quotePGQualifiedName(view.Schema, view.Name), strings.TrimSpace(view.Definition))
}

func writeNativeRoutineDefinition(w *bytes.Buffer, routine pgNativeRoutine) {
	definition := strings.TrimSpace(routine.Definition)
	if definition == "" {
		return
	}
	if !strings.HasSuffix(definition, ";") {
		definition += ";"
	}
	w.WriteString(definition)
	w.WriteString("\n\n")
}

func writeSequenceClause(w *bytes.Buffer, prefix string, value string) {
	if value == "" {
		return
	}
	w.WriteString(prefix)
	w.WriteString(value)
}

func writeNativeTableDefinition(w *bytes.Buffer, table pgNativeTable, columns []pgNativeColumn) {
	if table.Schema != "public" {
		fmt.Fprintf(w, "CREATE SCHEMA IF NOT EXISTS %s;\n", quotePGIdentifier(table.Schema))
	}
	fmt.Fprintf(w, "CREATE TABLE %s (", quotePGQualifiedName(table.Schema, table.Name))
	if len(columns) > 0 {
		w.WriteByte('\n')
		for i, column := range columns {
			if i > 0 {
				w.WriteString(",\n")
			}
			fmt.Fprintf(w, "  %s %s", quotePGIdentifier(column.Name), column.Type)
			if column.Default != nil && *column.Default != "" {
				fmt.Fprintf(w, " DEFAULT %s", *column.Default)
			}
			if column.NotNull {
				w.WriteString(" NOT NULL")
			}
		}
		w.WriteByte('\n')
	}
	w.WriteString(");\n")
	w.WriteByte('\n')
}

func writeNativeTableData(w *bytes.Buffer, table pgNativeTable, columns []pgNativeColumn, rows [][]*string) {
	if len(columns) == 0 {
		for range rows {
			fmt.Fprintf(w, "INSERT INTO %s DEFAULT VALUES;\n", quotePGQualifiedName(table.Schema, table.Name))
		}
		w.WriteByte('\n')
		return
	}
	columnNames := make([]string, 0, len(columns))
	for _, column := range columns {
		columnNames = append(columnNames, quotePGIdentifier(column.Name))
	}
	for _, row := range rows {
		values := make([]string, 0, len(columns))
		for i := range columns {
			if i >= len(row) || row[i] == nil {
				values = append(values, "NULL")
				continue
			}
			values = append(values, quotePGLiteral(*row[i]))
		}
		fmt.Fprintf(w, "INSERT INTO %s (%s) VALUES (%s);\n", quotePGQualifiedName(table.Schema, table.Name), strings.Join(columnNames, ", "), strings.Join(values, ", "))
	}
	w.WriteByte('\n')
}

func writeNativeTablePostData(w *bytes.Buffer, table pgNativeTable, constraints []pgNativeConstraint, indexes []pgNativeIndex, triggers []pgNativeTrigger) {
	for _, constraint := range constraints {
		fmt.Fprintf(w, "ALTER TABLE %s ADD CONSTRAINT %s %s;\n", quotePGQualifiedName(table.Schema, table.Name), quotePGIdentifier(constraint.Name), constraint.Definition)
	}
	for _, index := range indexes {
		definition := strings.TrimSpace(index.Definition)
		if definition == "" {
			continue
		}
		if !strings.HasSuffix(definition, ";") {
			definition += ";"
		}
		w.WriteString(definition)
		w.WriteByte('\n')
	}
	for _, trigger := range triggers {
		definition := strings.TrimSpace(trigger.Definition)
		if definition == "" {
			continue
		}
		if !strings.HasSuffix(definition, ";") {
			definition += ";"
		}
		w.WriteString(definition)
		w.WriteByte('\n')
	}
	if len(constraints) > 0 || len(indexes) > 0 || len(triggers) > 0 {
		w.WriteByte('\n')
	}
}

func pgRowsByName(result pgQueryResult) ([]map[string]string, error) {
	out := make([]map[string]string, 0, len(result.Rows))
	for rowIndex, row := range result.Rows {
		if len(row) != len(result.Fields) {
			return nil, fmt.Errorf("postgres row %d has %d values for %d fields", rowIndex, len(row), len(result.Fields))
		}
		mapped := make(map[string]string, len(result.Fields))
		for i, field := range result.Fields {
			if row[i] == nil {
				continue
			}
			mapped[field.Name] = *row[i]
		}
		out = append(out, mapped)
	}
	return out, nil
}

func rowPtr(row map[string]string, key string) *string {
	value, ok := row[key]
	if !ok {
		return nil
	}
	return &value
}

func parsePGBool(value string) bool {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "t", "true", "1", "yes", "on":
		return true
	default:
		return false
	}
}

func quotePGIdentifier(value string) string {
	return `"` + strings.ReplaceAll(value, `"`, `""`) + `"`
}

func quotePGQualifiedName(schema string, name string) string {
	return quotePGIdentifier(schema) + "." + quotePGIdentifier(name)
}

func quotePGLiteral(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "''") + "'"
}
