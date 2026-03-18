package db

import (
	"database/sql"
	"fmt"
	"log"
	"time"

	_ "github.com/lib/pq"

	appcfg "github.com/yourorg/whatsapp-s3-uploader/config"
)

type DB struct {
	*sql.DB
}

// Connect opens a connection pool to PostgreSQL and verifies it with a ping.
func Connect(cfg *appcfg.Config) (*DB, error) {
	dsn := fmt.Sprintf(
		"host=%s port=%s user=%s password=%s dbname=%s sslmode=%s sslrootcert=%s",
		cfg.DBHost, cfg.DBPort, cfg.DBUser, cfg.DBPassword, cfg.DBName,
		cfg.DBSSLMode, cfg.DBSSLRootCert,
	)

	sqlDB, err := sql.Open("postgres", dsn)
	if err != nil {
		return nil, fmt.Errorf("opening db: %w", err)
	}

	// Connection pool settings
	sqlDB.SetMaxOpenConns(10)
	sqlDB.SetMaxIdleConns(5)
	sqlDB.SetConnMaxLifetime(5 * time.Minute)

	if err := sqlDB.Ping(); err != nil {
		return nil, fmt.Errorf("pinging db: %w", err)
	}

	log.Println("database connected")
	return &DB{sqlDB}, nil
}

// Farmer represents a registered WhatsApp user.
type Farmer struct {
	ID        string
	Phone     string
	Name      string
	Channel   string
	CreatedAt time.Time
}

// Upload represents a single file upload event.
type Upload struct {
	ID           string
	FarmerID     string
	Phone        string
	S3Key        string
	S3URL        string
	FileType     string
	MimeType     string
	FileSize     int64
	OriginalName string
	UploadedAt   time.Time
}

// UpsertFarmer creates a new farmer or updates their name if they already exist.
// Returns the farmer's UUID.
func (db *DB) UpsertFarmer(phone, name, channel string) (string, error) {
	query := `
		INSERT INTO farmers (phone, name, channel)
		VALUES ($1, $2, $3)
		ON CONFLICT (phone) DO UPDATE
			SET name       = EXCLUDED.name,
			    updated_at = NOW()
		RETURNING id
	`
	var id string
	err := db.QueryRow(query, phone, name, channel).Scan(&id)
	if err != nil {
		return "", fmt.Errorf("upsert farmer: %w", err)
	}
	return id, nil
}

// LogUpload records a completed file upload linked to a farmer.
func (db *DB) LogUpload(u Upload) error {
	query := `
		INSERT INTO uploads
			(farmer_id, phone, s3_key, s3_url, file_type, mime_type, file_size, original_name)
		VALUES
			($1, $2, $3, $4, $5, $6, $7, $8)
	`
	_, err := db.Exec(query,
		u.FarmerID, u.Phone, u.S3Key, u.S3URL,
		u.FileType, u.MimeType, u.FileSize, u.OriginalName,
	)
	if err != nil {
		return fmt.Errorf("log upload: %w", err)
	}
	return nil
}

// GetFarmerUploads returns all uploads for a given phone number.
func (db *DB) GetFarmerUploads(phone string) ([]Upload, error) {
	query := `
		SELECT id, farmer_id, phone, s3_key, s3_url,
		       file_type, mime_type, file_size, original_name, uploaded_at
		FROM uploads
		WHERE phone = $1
		ORDER BY uploaded_at DESC
	`
	rows, err := db.Query(query, phone)
	if err != nil {
		return nil, fmt.Errorf("get farmer uploads: %w", err)
	}
	defer rows.Close()

	var uploads []Upload
	for rows.Next() {
		var u Upload
		err := rows.Scan(
			&u.ID, &u.FarmerID, &u.Phone, &u.S3Key, &u.S3URL,
			&u.FileType, &u.MimeType, &u.FileSize, &u.OriginalName, &u.UploadedAt,
		)
		if err != nil {
			return nil, err
		}
		uploads = append(uploads, u)
	}
	return uploads, rows.Err()
}
