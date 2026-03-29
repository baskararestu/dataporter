package migration

import "fmt"

// TableInfo describes a supported source→target table migration pair.
type TableInfo struct {
	SourceTable string
	TargetTable string
	Description string
}

// registry holds all supported source→target table migration pairs.
// To add support for a new table (e.g. dokter), register it here.
var registry = map[string]TableInfo{
	"pasien": {
		SourceTable: "pasien",
		TargetTable: "pasien",
		Description: "2M rows, INT PK → UUID PK, field merge nama_depan+nama_belakang",
	},
}

// LookupTable returns the TableInfo for the given source table name.
// Returns an error with a 422-worthy message if the table is not supported.
func LookupTable(sourceTable string) (TableInfo, error) {
	info, ok := registry[sourceTable]
	if !ok {
		return TableInfo{}, fmt.Errorf("source_table %q is not supported — add transformer and register it in registry.go", sourceTable)
	}
	return info, nil
}

// SupportedTables returns all registered table pairs.
// Used by GET /api/tables to advertise what the tool can migrate.
func SupportedTables() []TableInfo {
	tables := make([]TableInfo, 0, len(registry))
	for _, v := range registry {
		tables = append(tables, v)
	}
	return tables
}
