-- ============================================================
-- database_emr: Init Schema + Seed Data (2M pasien, 10K dokter)
-- ============================================================
-- Executed automatically by docker-entrypoint-initdb.d

-- Create Dokter Table
CREATE TABLE IF NOT EXISTS dokter (
    id_dokter INT PRIMARY KEY,
    nama_depan VARCHAR(50),
    nama_belakang VARCHAR(50),
    spesialisasi VARCHAR(100),
    email VARCHAR(100),
    no_telepon VARCHAR(20),
    no_sip VARCHAR(20),
    tahun_pengalaman INT,
    departemen VARCHAR(50),
    tanggal_bergabung DATE
);

-- Create Pasien Table
CREATE TABLE IF NOT EXISTS pasien (
    id_pasien INT PRIMARY KEY,
    nama_depan VARCHAR(50),
    nama_belakang VARCHAR(50),
    tanggal_lahir DATE,
    jenis_kelamin VARCHAR(10),
    email VARCHAR(100),
    no_telepon VARCHAR(20),
    alamat VARCHAR(200),
    kota VARCHAR(50),
    provinsi VARCHAR(50),
    kode_pos VARCHAR(10),
    golongan_darah VARCHAR(5),
    kontak_darurat VARCHAR(100),
    no_kontak_darurat VARCHAR(20),
    tanggal_registrasi DATE
);

-- Insert 10,000 Dokter
INSERT INTO dokter (id_dokter, nama_depan, nama_belakang, spesialisasi, email, no_telepon, no_sip, tahun_pengalaman, departemen, tanggal_bergabung)
SELECT 
    n AS id_dokter,
    CASE (n % 30)
        WHEN 0 THEN 'Ahmad' WHEN 1 THEN 'Budi' WHEN 2 THEN 'Candra' WHEN 3 THEN 'Dedi'
        WHEN 4 THEN 'Eko' WHEN 5 THEN 'Fajar' WHEN 6 THEN 'Gunawan' WHEN 7 THEN 'Hadi'
        WHEN 8 THEN 'Indra' WHEN 9 THEN 'Joko' WHEN 10 THEN 'Siti' WHEN 11 THEN 'Rina'
        WHEN 12 THEN 'Dewi' WHEN 13 THEN 'Ani' WHEN 14 THEN 'Sri' WHEN 15 THEN 'Wati'
        WHEN 16 THEN 'Fitri' WHEN 17 THEN 'Ratna' WHEN 18 THEN 'Maya' WHEN 19 THEN 'Lina'
        WHEN 20 THEN 'Rizki' WHEN 21 THEN 'Teguh' WHEN 22 THEN 'Wahyu' WHEN 23 THEN 'Bambang'
        WHEN 24 THEN 'Agus' WHEN 25 THEN 'Rudi' WHEN 26 THEN 'Andi' WHEN 27 THEN 'Yudi'
        WHEN 28 THEN 'Dina' ELSE 'Nurul'
    END AS nama_depan,
    CASE (n % 25)
        WHEN 0 THEN 'Santoso' WHEN 1 THEN 'Wijaya' WHEN 2 THEN 'Kusuma' WHEN 3 THEN 'Purnama'
        WHEN 4 THEN 'Pratama' WHEN 5 THEN 'Saputra' WHEN 6 THEN 'Wibowo' WHEN 7 THEN 'Hidayat'
        WHEN 8 THEN 'Setiawan' WHEN 9 THEN 'Firmansyah' WHEN 10 THEN 'Sutanto' WHEN 11 THEN 'Hartono'
        WHEN 12 THEN 'Nugroho' WHEN 13 THEN 'Rahman' WHEN 14 THEN 'Hakim' WHEN 15 THEN 'Anwar'
        WHEN 16 THEN 'Budiman' WHEN 17 THEN 'Susanto' WHEN 18 THEN 'Kurniawan' WHEN 19 THEN 'Gunawan'
        WHEN 20 THEN 'Utomo' WHEN 21 THEN 'Mulyadi' WHEN 22 THEN 'Suharto' WHEN 23 THEN 'Pranoto'
        ELSE 'Suryanto'
    END AS nama_belakang,
    CASE (n % 15)
        WHEN 0 THEN 'Kardiologi' WHEN 1 THEN 'Pediatri' WHEN 2 THEN 'Ortopedi'
        WHEN 3 THEN 'Neurologi' WHEN 4 THEN 'Dermatologi' WHEN 5 THEN 'Bedah Umum'
        WHEN 6 THEN 'Penyakit Dalam' WHEN 7 THEN 'Psikiatri' WHEN 8 THEN 'Radiologi'
        WHEN 9 THEN 'Onkologi' WHEN 10 THEN 'Anestesiologi' WHEN 11 THEN 'Gawat Darurat'
        WHEN 12 THEN 'Kebidanan dan Kandungan' WHEN 13 THEN 'Oftalmologi' ELSE 'THT'
    END AS spesialisasi,
    CONCAT('dr.', LOWER(CASE (n % 30)
        WHEN 0 THEN 'Ahmad' WHEN 1 THEN 'Budi' WHEN 2 THEN 'Candra' WHEN 3 THEN 'Dedi'
        WHEN 4 THEN 'Eko' WHEN 5 THEN 'Fajar' WHEN 6 THEN 'Gunawan' WHEN 7 THEN 'Hadi'
        WHEN 8 THEN 'Indra' WHEN 9 THEN 'Joko' WHEN 10 THEN 'Siti' WHEN 11 THEN 'Rina'
        WHEN 12 THEN 'Dewi' WHEN 13 THEN 'Ani' WHEN 14 THEN 'Sri' WHEN 15 THEN 'Wati'
        WHEN 16 THEN 'Fitri' WHEN 17 THEN 'Ratna' WHEN 18 THEN 'Maya' WHEN 19 THEN 'Lina'
        WHEN 20 THEN 'Rizki' WHEN 21 THEN 'Teguh' WHEN 22 THEN 'Wahyu' WHEN 23 THEN 'Bambang'
        WHEN 24 THEN 'Agus' WHEN 25 THEN 'Rudi' WHEN 26 THEN 'Andi' WHEN 27 THEN 'Yudi'
        WHEN 28 THEN 'Dina' ELSE 'Nurul'
    END), n, '@rumahsakit.co.id') AS email,
    CONCAT('08', (n % 2) + 1, LPAD((n % 9000000 + 1000000)::TEXT, 8, '0')) AS no_telepon,
    CONCAT('SIP/', LPAD(n::TEXT, 8, '0'), '/2024') AS no_sip,
    (n % 35) + 1 AS tahun_pengalaman,
    CASE (n % 10)
        WHEN 0 THEN 'Bedah' WHEN 1 THEN 'IGD' WHEN 2 THEN 'ICU'
        WHEN 3 THEN 'Poliklinik' WHEN 4 THEN 'Pediatri' WHEN 5 THEN 'Kardiologi'
        WHEN 6 THEN 'Neurologi' WHEN 7 THEN 'Onkologi' WHEN 8 THEN 'Ortopedi'
        ELSE 'Poli Umum'
    END AS departemen,
    DATE '2000-01-01' + (n % 9125) * INTERVAL '1 day' AS tanggal_bergabung
FROM generate_series(1, 10000) AS n;

-- Insert 2,000,000 Pasien
INSERT INTO pasien (id_pasien, nama_depan, nama_belakang, tanggal_lahir, jenis_kelamin, email, no_telepon, alamat, kota, provinsi, kode_pos, golongan_darah, kontak_darurat, no_kontak_darurat, tanggal_registrasi)
SELECT 
    n AS id_pasien,
    CASE (n % 40)
        WHEN 0 THEN 'Aditya' WHEN 1 THEN 'Bayu' WHEN 2 THEN 'Cahya' WHEN 3 THEN 'Dian'
        WHEN 4 THEN 'Eko' WHEN 5 THEN 'Fajar' WHEN 6 THEN 'Galih' WHEN 7 THEN 'Hendra'
        WHEN 8 THEN 'Indah' WHEN 9 THEN 'Jaya' WHEN 10 THEN 'Kartika' WHEN 11 THEN 'Lestari'
        WHEN 12 THEN 'Mega' WHEN 13 THEN 'Nanda' WHEN 14 THEN 'Oki' WHEN 15 THEN 'Putri'
        WHEN 16 THEN 'Reza' WHEN 17 THEN 'Sari' WHEN 18 THEN 'Tuti' WHEN 19 THEN 'Umar'
        WHEN 20 THEN 'Vina' WHEN 21 THEN 'Wulan' WHEN 22 THEN 'Yanti' WHEN 23 THEN 'Zahra'
        WHEN 24 THEN 'Andi' WHEN 25 THEN 'Bella' WHEN 26 THEN 'Citra' WHEN 27 THEN 'Dinda'
        WHEN 28 THEN 'Eka' WHEN 29 THEN 'Fina' WHEN 30 THEN 'Gita' WHEN 31 THEN 'Hani'
        WHEN 32 THEN 'Imam' WHEN 33 THEN 'Juni' WHEN 34 THEN 'Kiki' WHEN 35 THEN 'Lili'
        WHEN 36 THEN 'Mira' WHEN 37 THEN 'Nina' WHEN 38 THEN 'Omar' ELSE 'Prita'
    END AS nama_depan,
    CASE (n % 30)
        WHEN 0 THEN 'Santoso' WHEN 1 THEN 'Wijaya' WHEN 2 THEN 'Kusuma' WHEN 3 THEN 'Purnama'
        WHEN 4 THEN 'Pratama' WHEN 5 THEN 'Saputra' WHEN 6 THEN 'Wibowo' WHEN 7 THEN 'Hidayat'
        WHEN 8 THEN 'Setiawan' WHEN 9 THEN 'Firmansyah' WHEN 10 THEN 'Sutanto' WHEN 11 THEN 'Hartono'
        WHEN 12 THEN 'Nugroho' WHEN 13 THEN 'Rahman' WHEN 14 THEN 'Hakim' WHEN 15 THEN 'Anwar'
        WHEN 16 THEN 'Budiman' WHEN 17 THEN 'Susanto' WHEN 18 THEN 'Kurniawan' WHEN 19 THEN 'Gunawan'
        WHEN 20 THEN 'Utomo' WHEN 21 THEN 'Mulyadi' WHEN 22 THEN 'Suharto' WHEN 23 THEN 'Pranoto'
        WHEN 24 THEN 'Suryanto' WHEN 25 THEN 'Ramadhan' WHEN 26 THEN 'Prasetyo' WHEN 27 THEN 'Saputro'
        WHEN 28 THEN 'Permana' ELSE 'Mahendra'
    END AS nama_belakang,
    DATE '1940-01-01' + (n % 30000) * INTERVAL '1 day' AS tanggal_lahir,
    CASE (n % 2) WHEN 0 THEN 'Laki-laki' ELSE 'Perempuan' END AS jenis_kelamin,
    CONCAT(LOWER(CASE (n % 40)
        WHEN 0 THEN 'Aditya' WHEN 1 THEN 'Bayu' WHEN 2 THEN 'Cahya' WHEN 3 THEN 'Dian'
        WHEN 4 THEN 'Eko' WHEN 5 THEN 'Fajar' WHEN 6 THEN 'Galih' WHEN 7 THEN 'Hendra'
        WHEN 8 THEN 'Indah' WHEN 9 THEN 'Jaya' WHEN 10 THEN 'Kartika' WHEN 11 THEN 'Lestari'
        WHEN 12 THEN 'Mega' WHEN 13 THEN 'Nanda' WHEN 14 THEN 'Oki' WHEN 15 THEN 'Putri'
        WHEN 16 THEN 'Reza' WHEN 17 THEN 'Sari' WHEN 18 THEN 'Tuti' WHEN 19 THEN 'Umar'
        WHEN 20 THEN 'Vina' WHEN 21 THEN 'Wulan' WHEN 22 THEN 'Yanti' WHEN 23 THEN 'Zahra'
        WHEN 24 THEN 'Andi' WHEN 25 THEN 'Bella' WHEN 26 THEN 'Citra' WHEN 27 THEN 'Dinda'
        WHEN 28 THEN 'Eka' WHEN 29 THEN 'Fina' WHEN 30 THEN 'Gita' WHEN 31 THEN 'Hani'
        WHEN 32 THEN 'Imam' WHEN 33 THEN 'Juni' WHEN 34 THEN 'Kiki' WHEN 35 THEN 'Lili'
        WHEN 36 THEN 'Mira' WHEN 37 THEN 'Nina' WHEN 38 THEN 'Omar' ELSE 'Prita'
    END), '.', n, '@email.com') AS email,
    CONCAT('08', (n % 3) + 1, LPAD((n % 9000000 + 1000000)::TEXT, 8, '0')) AS no_telepon,
    CONCAT('Jl. ', 
        CASE (n % 25)
            WHEN 0 THEN 'Sudirman' WHEN 1 THEN 'Thamrin' WHEN 2 THEN 'Gatot Subroto'
            WHEN 3 THEN 'Asia Afrika' WHEN 4 THEN 'Diponegoro' WHEN 5 THEN 'Ahmad Yani'
            WHEN 6 THEN 'Merdeka' WHEN 7 THEN 'Pemuda' WHEN 8 THEN 'Veteran'
            WHEN 9 THEN 'Pahlawan' WHEN 10 THEN 'Proklamasi' WHEN 11 THEN 'Kartini'
            WHEN 12 THEN 'Gajah Mada' WHEN 13 THEN 'Imam Bonjol' WHEN 14 THEN 'Hayam Wuruk'
            WHEN 15 THEN 'Majapahit' WHEN 16 THEN 'Brawijaya' WHEN 17 THEN 'Cendrawasih'
            WHEN 18 THEN 'Garuda' WHEN 19 THEN 'Melati' WHEN 20 THEN 'Mawar'
            WHEN 21 THEN 'Anggrek' WHEN 22 THEN 'Dahlia' WHEN 23 THEN 'Kenanga'
            ELSE 'Teratai'
        END, ' No. ', (n % 200) + 1, ', RT.', LPAD(((n % 20) + 1)::TEXT, 3, '0'), '/RW.', LPAD(((n % 15) + 1)::TEXT, 3, '0')) AS alamat,
    CASE (n % 34)
        WHEN 0 THEN 'Jakarta' WHEN 1 THEN 'Surabaya' WHEN 2 THEN 'Bandung'
        WHEN 3 THEN 'Medan' WHEN 4 THEN 'Semarang' WHEN 5 THEN 'Makassar'
        WHEN 6 THEN 'Palembang' WHEN 7 THEN 'Tangerang' WHEN 8 THEN 'Depok'
        WHEN 9 THEN 'Bekasi' WHEN 10 THEN 'Bogor' WHEN 11 THEN 'Malang'
        WHEN 12 THEN 'Yogyakarta' WHEN 13 THEN 'Balikpapan' WHEN 14 THEN 'Denpasar'
        WHEN 15 THEN 'Samarinda' WHEN 16 THEN 'Banjarmasin' WHEN 17 THEN 'Pekanbaru'
        WHEN 18 THEN 'Padang' WHEN 19 THEN 'Manado' WHEN 20 THEN 'Pontianak'
        WHEN 21 THEN 'Jambi' WHEN 22 THEN 'Cirebon' WHEN 23 THEN 'Sukabumi'
        WHEN 24 THEN 'Tasikmalaya' WHEN 25 THEN 'Serang' WHEN 26 THEN 'Mataram'
        WHEN 27 THEN 'Kupang' WHEN 28 THEN 'Bandar Lampung' WHEN 29 THEN 'Batam'
        WHEN 30 THEN 'Bengkulu' WHEN 31 THEN 'Palu' WHEN 32 THEN 'Jayapura'
        ELSE 'Ambon'
    END AS kota,
    CASE (n % 34)
        WHEN 0 THEN 'DKI Jakarta' WHEN 1 THEN 'Jawa Timur' WHEN 2 THEN 'Jawa Barat'
        WHEN 3 THEN 'Sumatera Utara' WHEN 4 THEN 'Jawa Tengah' WHEN 5 THEN 'Sulawesi Selatan'
        WHEN 6 THEN 'Sumatera Selatan' WHEN 7 THEN 'Banten' WHEN 8 THEN 'Jawa Barat'
        WHEN 9 THEN 'Jawa Barat' WHEN 10 THEN 'Jawa Barat' WHEN 11 THEN 'Jawa Timur'
        WHEN 12 THEN 'DI Yogyakarta' WHEN 13 THEN 'Kalimantan Timur' WHEN 14 THEN 'Bali'
        WHEN 15 THEN 'Kalimantan Timur' WHEN 16 THEN 'Kalimantan Selatan' WHEN 17 THEN 'Riau'
        WHEN 18 THEN 'Sumatera Barat' WHEN 19 THEN 'Sulawesi Utara' WHEN 20 THEN 'Kalimantan Barat'
        WHEN 21 THEN 'Jambi' WHEN 22 THEN 'Jawa Barat' WHEN 23 THEN 'Jawa Barat'
        WHEN 24 THEN 'Jawa Barat' WHEN 25 THEN 'Banten' WHEN 26 THEN 'Nusa Tenggara Barat'
        WHEN 27 THEN 'Nusa Tenggara Timur' WHEN 28 THEN 'Lampung' WHEN 29 THEN 'Kepulauan Riau'
        WHEN 30 THEN 'Bengkulu' WHEN 31 THEN 'Sulawesi Tengah' WHEN 32 THEN 'Papua'
        ELSE 'Maluku'
    END AS provinsi,
    LPAD(((n % 99999) + 10000)::TEXT, 5, '0') AS kode_pos,
    CASE (n % 8)
        WHEN 0 THEN 'O+' WHEN 1 THEN 'A+' WHEN 2 THEN 'B+'
        WHEN 3 THEN 'AB+' WHEN 4 THEN 'O-' WHEN 5 THEN 'A-'
        WHEN 6 THEN 'B-' ELSE 'AB-'
    END AS golongan_darah,
    CONCAT(
        CASE ((n + 7) % 30)
            WHEN 0 THEN 'Budi' WHEN 1 THEN 'Siti' WHEN 2 THEN 'Ahmad' WHEN 3 THEN 'Dewi'
            WHEN 4 THEN 'Agus' WHEN 5 THEN 'Rina' WHEN 6 THEN 'Joko' WHEN 7 THEN 'Ani'
            WHEN 8 THEN 'Hadi' WHEN 9 THEN 'Maya' WHEN 10 THEN 'Eko' WHEN 11 THEN 'Fitri'
            WHEN 12 THEN 'Rizki' WHEN 13 THEN 'Lina' WHEN 14 THEN 'Wahyu' WHEN 15 THEN 'Ratna'
            WHEN 16 THEN 'Teguh' WHEN 17 THEN 'Sri' WHEN 18 THEN 'Dedi' WHEN 19 THEN 'Wati'
            WHEN 20 THEN 'Indra' WHEN 21 THEN 'Dina' WHEN 22 THEN 'Fajar' WHEN 23 THEN 'Nurul'
            WHEN 24 THEN 'Bambang' WHEN 25 THEN 'Ayu' WHEN 26 THEN 'Rudi' WHEN 27 THEN 'Sari'
            WHEN 28 THEN 'Gunawan' ELSE 'Tuti'
        END, ' ',
        CASE ((n + 13) % 20)
            WHEN 0 THEN 'Santoso' WHEN 1 THEN 'Wijaya' WHEN 2 THEN 'Kusuma' WHEN 3 THEN 'Purnama'
            WHEN 4 THEN 'Pratama' WHEN 5 THEN 'Saputra' WHEN 6 THEN 'Wibowo' WHEN 7 THEN 'Hidayat'
            WHEN 8 THEN 'Setiawan' WHEN 9 THEN 'Firmansyah' WHEN 10 THEN 'Sutanto' WHEN 11 THEN 'Hartono'
            WHEN 12 THEN 'Nugroho' WHEN 13 THEN 'Rahman' WHEN 14 THEN 'Hakim' WHEN 15 THEN 'Anwar'
            WHEN 16 THEN 'Budiman' WHEN 17 THEN 'Susanto' WHEN 18 THEN 'Kurniawan' ELSE 'Gunawan'
        END) AS kontak_darurat,
    CONCAT('08', ((n + 5000) % 3) + 1, LPAD(((n + 3000) % 9000000 + 1000000)::TEXT, 8, '0')) AS no_kontak_darurat,
    DATE '2010-01-01' + (n % 5475) * INTERVAL '1 day' AS tanggal_registrasi
FROM generate_series(1, 2000000) AS n;

-- Verification
SELECT 'Jumlah Dokter:' AS info, COUNT(*) AS jumlah FROM dokter
UNION ALL
SELECT 'Jumlah Pasien:' AS info, COUNT(*) AS jumlah FROM pasien;
