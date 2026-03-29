package model

import "time"

type EMRPasien struct {
	IDPasien          int        `db:"id_pasien"`
	NamaDepan         string     `db:"nama_depan"`
	NamaBelakang      string     `db:"nama_belakang"`
	TanggalLahir      *time.Time `db:"tanggal_lahir"`
	JenisKelamin      string     `db:"jenis_kelamin"`
	Email             string     `db:"email"`
	NoTelepon         string     `db:"no_telepon"`
	Alamat            string     `db:"alamat"`
	Kota              string     `db:"kota"`
	Provinsi          string     `db:"provinsi"`
	KodePos           string     `db:"kode_pos"`
	GolonganDarah     string     `db:"golongan_darah"`
	KontakDarurat     string     `db:"kontak_darurat"`
	NoKontakDarurat   string     `db:"no_kontak_darurat"`
	TanggalRegistrasi *time.Time `db:"tanggal_registrasi"`
}
