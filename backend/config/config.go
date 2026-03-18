package config

import (
	"fmt"
	"os"
)

type Config struct {
	// Server
	Port string

	// AWS
	AWSRegion      string
	AWSAccessKeyID string
	AWSSecretKey   string
	S3Bucket       string

	// WhatsApp / Meta
	WhatsAppToken       string // Bearer token from Meta developer console
	WhatsAppPhoneID     string // Phone Number ID from Meta
	WhatsAppVerifyToken string // Token you define for webhook verification
	WhatsAppAPIVersion  string // e.g. "v19.0"
}

func Load() (*Config, error) {
	cfg := &Config{
		Port:                getEnv("PORT", "8000"),
		AWSRegion:           getEnv("AWS_REGION", "eu-north-1"),
		AWSAccessKeyID:      getEnv("AWS_ACCESS_KEY_ID", ""),
		AWSSecretKey:        getEnv("AWS_SECRET_ACCESS_KEY", ""),
		S3Bucket:            getEnv("S3_BUCKET", ""),
		WhatsAppToken:       getEnv("WHATSAPP_TOKEN", ""),
		WhatsAppPhoneID:     getEnv("WHATSAPP_PHONE_ID", ""),
		WhatsAppVerifyToken: getEnv("WHATSAPP_VERIFY_TOKEN", ""),
		WhatsAppAPIVersion:  getEnv("WHATSAPP_API_VERSION", "v19.0"),
	}

	if err := cfg.validate(); err != nil {
		return nil, err
	}
	return cfg, nil
}

func (c *Config) validate() error {
	required := map[string]string{
		"S3_BUCKET":             c.S3Bucket,
		"WHATSAPP_TOKEN":        c.WhatsAppToken,
		"WHATSAPP_PHONE_ID":     c.WhatsAppPhoneID,
		"WHATSAPP_VERIFY_TOKEN": c.WhatsAppVerifyToken,
	}
	for name, val := range required {
		if val == "" {
			return fmt.Errorf("missing required env var: %s", name)
		}
	}
	return nil
}

func getEnv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
