package migration

import "fmt"

// TableInfo describes a supported source→target table migration pair,
// including the SQL queries needed to extract data from the source.
//
// SelectSQL must use %d as a placeholder for lastProcessedID (used in DECLARE CURSOR).
// CountSQL must use $1 as a placeholder for lastProcessedID (used in COUNT query).
type TableInfo struct {
	SourceTable string
	TargetTable string
	Description string
	// SelectSQL is the query body for DECLARE CURSOR FOR <SelectSQL>.
	// Use %d for the lastProcessedID filter — it is interpolated into the DECLARE statement
	// (server-side cursors do not support parameters in DECLARE ... FOR SELECT).
	SelectSQL string
	// CountSQL is the query to count remaining rows, must use $1 for lastProcessedID.
	CountSQL string
}

// registry holds all supported source→target table migration pairs.
// To add support for a new table (e.g. dokter), register it here.
var registry = map[string]TableInfo{
	"pasien": {
		SourceTable: "pasien",
		TargetTable: "pasien",
		Description: "2M rows, INT PK → UUID PK, field merge nama_depan+nama_belakang",
		SelectSQL: `SELECT id_pasien, nama_depan, nama_belakang, tanggal_lahir, jenis_kelamin,
		                   email, no_telepon, alamat, kota, provinsi, kode_pos,
		                   golongan_darah, kontak_darurat, no_kontak_darurat, tanggal_registrasi
		            FROM pasien
		            WHERE id_pasien > %d
		            ORDER BY id_pasien ASC`,
		CountSQL: `SELECT COUNT(id_pasien) FROM pasien WHERE id_pasien > $1`,
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
