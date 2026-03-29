package migration

import (
	"fmt"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/baskararestu/dataporter/model"
	"github.com/google/uuid"
)

const (
	maxNamaLengkap = 100
	maxEmail       = 100
)

// TransformBatch converts a batch of EMR pasien rows into SIMRS pasien rows.
// Rows that fail validation are skipped; errors are returned alongside the results
// so the caller can log and count them without stopping the migration.
func TransformBatch(batch []model.EMRPasien) ([]model.SIMRSPasien, []TransformError) {
	results := make([]model.SIMRSPasien, 0, len(batch))
	var errs []TransformError

	for _, src := range batch {
		dst, err := transformRow(src)
		if err != nil {
			errs = append(errs, TransformError{IDPasien: src.IDPasien, Err: err})
			continue
		}
		results = append(results, dst)
	}
	return results, errs
}

// TransformError records a failed row transformation with its source ID.
type TransformError struct {
	IDPasien int
	Err      error
}

func (e TransformError) Error() string {
	return fmt.Sprintf("transform id_pasien=%d: %v", e.IDPasien, e.Err)
}

// transformRow maps a single EMR pasien to a SIMRS pasien.
// Returns an error only if the row must be skipped (e.g. empty nama_lengkap).
// Truncation warnings are embedded as non-fatal notes — caller decides logging.
func transformRow(src model.EMRPasien) (model.SIMRSPasien, error) {
	pasienUUID := uuid.NewSHA1(model.UUIDNamespace, []byte(strconv.Itoa(src.IDPasien)))

	namaLengkap := strings.TrimSpace(
		strings.TrimSpace(src.NamaDepan) + " " + strings.TrimSpace(src.NamaBelakang),
	)
	if namaLengkap == "" {
		return model.SIMRSPasien{}, fmt.Errorf("nama_lengkap is empty (both nama_depan and nama_belakang are blank)")
	}
	if utf8.RuneCountInString(namaLengkap) > maxNamaLengkap {
		namaLengkap = string([]rune(namaLengkap)[:maxNamaLengkap])
	}

	email := src.Email
	if utf8.RuneCountInString(email) > maxEmail {
		email = string([]rune(email)[:maxEmail])
	}

	return model.SIMRSPasien{
		PasienUUID:           pasienUUID,
		NamaLengkap:          namaLengkap,
		TanggalLahir:         src.TanggalLahir,
		Gender:               src.JenisKelamin,
		Email:                email,
		Telepon:              src.NoTelepon,
		AlamatLengkap:        src.Alamat,
		Kota:                 src.Kota,
		Provinsi:             src.Provinsi,
		KodePos:              src.KodePos,
		GolonganDarah:        src.GolonganDarah,
		NamaKontakDarurat:    src.KontakDarurat,
		TeleponKontakDarurat: src.NoKontakDarurat,
		TanggalRegistrasi:    src.TanggalRegistrasi,
	}, nil
}

// NowPtr returns a pointer to the current time. Convenience helper for nullable timestamps.
func NowPtr() *time.Time {
	t := time.Now()
	return &t
}
