package model

import (
	"time"

	"github.com/google/uuid"
)

type SIMRSPasien struct {
	PasienUUID           uuid.UUID  `db:"pasien_uuid"`
	NamaLengkap          string     `db:"nama_lengkap"`
	TanggalLahir         *time.Time `db:"tanggal_lahir"`
	Gender               string     `db:"gender"`
	Email                string     `db:"email"`
	Telepon              string     `db:"telepon"`
	AlamatLengkap        string     `db:"alamat_lengkap"`
	Kota                 string     `db:"kota"`
	Provinsi             string     `db:"provinsi"`
	KodePos              string     `db:"kode_pos"`
	GolonganDarah        string     `db:"golongan_darah"`
	NamaKontakDarurat    string     `db:"nama_kontak_darurat"`
	TeleponKontakDarurat string     `db:"telepon_kontak_darurat"`
	TanggalRegistrasi    *time.Time `db:"tanggal_registrasi"`
}

// UUIDNamespace is the fixed UUID v5 namespace for deterministic patient UUID generation.
// Using a stable namespace ensures the same id_pasien always produces the same pasien_uuid
// across all migration runs, making the process idempotent and the mapping reversible.
var UUIDNamespace = uuid.MustParse("a1b2c3d4-e5f6-7890-abcd-ef1234567890")
