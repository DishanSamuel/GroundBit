package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"path/filepath"
	"strings"

	appcfg "github.com/yourorg/whatsapp-s3-uploader/config"
	"github.com/yourorg/whatsapp-s3-uploader/db"
	"github.com/yourorg/whatsapp-s3-uploader/models"
	"github.com/yourorg/whatsapp-s3-uploader/services"
)

type WebhookHandler struct {
	cfg *appcfg.Config
	s3  *services.S3Service
	wa  *services.WhatsAppService
	db  *db.DB
}

func NewWebhookHandler(cfg *appcfg.Config, s3 *services.S3Service, wa *services.WhatsAppService, database *db.DB) *WebhookHandler {
	return &WebhookHandler{cfg: cfg, s3: s3, wa: wa, db: database}
}

func (h *WebhookHandler) Verify(w http.ResponseWriter, r *http.Request) {
	mode := r.URL.Query().Get("hub.mode")
	token := r.URL.Query().Get("hub.verify_token")
	challenge := r.URL.Query().Get("hub.challenge")

	if mode == "subscribe" && token == h.cfg.WhatsAppVerifyToken {
		log.Println("webhook verified successfully")
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, challenge)
		return
	}
	http.Error(w, "forbidden", http.StatusForbidden)
}

func (h *WebhookHandler) Receive(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusOK)

	var payload models.WebhookPayload
	if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
		log.Printf("webhook decode error: %v", err)
		return
	}

	if payload.Object != "whatsapp_business_account" {
		return
	}

	for _, entry := range payload.Entry {
		for _, change := range entry.Changes {
			if change.Field != "messages" {
				continue
			}
			nameMap := make(map[string]string)
			for _, c := range change.Value.Contacts {
				nameMap[c.WaID] = c.Profile.Name
			}
			for _, msg := range change.Value.Messages {
				name := nameMap[msg.From]
				go h.handleMessage(msg, name)
			}
		}
	}
}

func (h *WebhookHandler) handleMessage(msg models.Message, name string) {
	ctx := context.Background()
	from := msg.From

	// ── Location message ──────────────────────────────────────────────
	if msg.Type == "location" && msg.Location != nil {
		loc := msg.Location
		log.Printf("location from %s (%s): lat=%.6f lng=%.6f name=%s",
			from, name, loc.Latitude, loc.Longitude, loc.Name)

		// Ensure farmer exists first
		if _, err := h.db.UpsertFarmer(from, name, "whatsapp"); err != nil {
			log.Printf("upsert farmer error: %v", err)
		}

		// Save location
		if err := h.db.UpdateFarmerLocation(from, loc.Latitude, loc.Longitude, loc.Name, loc.Address); err != nil {
			log.Printf("update location error: %v", err)
			_ = h.wa.SendTextMessage(ctx, from, "Sorry, I couldn't save your location. Please try again.")
			return
		}

		reply := fmt.Sprintf("📍 Location saved!\n\nLat: %.6f\nLng: %.6f", loc.Latitude, loc.Longitude)
		if loc.Name != "" {
			reply += fmt.Sprintf("\nPlace: %s", loc.Name)
		}
		if loc.Address != "" {
			reply += fmt.Sprintf("\nAddress: %s", loc.Address)
		}
		_ = h.wa.SendTextMessage(ctx, from, reply)
		return
	}

	// ── Media message ─────────────────────────────────────────────────
	media, folder, filename := extractMedia(msg)
	if media == nil {
		log.Printf("non-media message from %s (type: %s)", from, msg.Type)
		_ = h.wa.SendTextMessage(ctx, from, "Send a file or share your location to get started.")
		return
	}

	log.Printf("received %s from %s (%s): media_id=%s", msg.Type, from, name, media.ID)

	farmerID, err := h.db.UpsertFarmer(from, name, "whatsapp")
	if err != nil {
		log.Printf("upsert farmer error: %v", err)
	}

	mediaInfo, err := h.wa.GetMediaURL(ctx, media.ID)
	if err != nil {
		log.Printf("get media url error: %v", err)
		_ = h.wa.SendTextMessage(ctx, from, "Sorry, I couldn't retrieve your file. Please try again.")
		return
	}

	body, contentType, size, err := h.wa.DownloadMedia(ctx, mediaInfo.URL)
	if err != nil {
		log.Printf("download media error: %v", err)
		_ = h.wa.SendTextMessage(ctx, from, "Sorry, I couldn't download your file. Please try again.")
		return
	}
	defer body.Close()

	if filename == "" {
		ext := services.ExtensionFromMIME(contentType)
		filename = fmt.Sprintf("%s%s", media.ID, ext)
	}
	cleanName := sanitizeFilename(filename)

	result, err := h.s3.Upload(ctx, body, folder, cleanName, contentType, size)
	if err != nil {
		log.Printf("s3 upload error: %v", err)
		_ = h.wa.SendTextMessage(ctx, from, "Sorry, the upload failed. Please try again.")
		return
	}

	log.Printf("uploaded to s3: %s (farmer: %s / %s)", result.Key, name, from)

	if farmerID != "" {
		if err := h.db.LogUpload(db.Upload{
			FarmerID:     farmerID,
			Phone:        from,
			S3Key:        result.Key,
			S3URL:        result.URL,
			FileType:     folder,
			MimeType:     contentType,
			FileSize:     size,
			OriginalName: cleanName,
		}); err != nil {
			log.Printf("log upload error: %v", err)
		}
	}

	reply := fmt.Sprintf("✅ Your file has been uploaded successfully!\n\nKey: %s", result.Key)
	_ = h.wa.SendTextMessage(ctx, from, reply)
}

func extractMedia(msg models.Message) (media *models.MediaInfo, folder, filename string) {
	switch msg.Type {
	case "image":
		if msg.Image != nil {
			return msg.Image, "images", ""
		}
	case "video":
		if msg.Video != nil {
			return msg.Video, "videos", ""
		}
	case "audio":
		if msg.Audio != nil {
			return msg.Audio, "audio", ""
		}
	case "document":
		if msg.Document != nil {
			return msg.Document, "documents", msg.Document.Filename
		}
	case "sticker":
		if msg.Sticker != nil {
			return msg.Sticker, "stickers", ""
		}
	}
	return nil, "", ""
}

func sanitizeFilename(name string) string {
	name = filepath.Base(name)
	name = strings.ReplaceAll(name, "..", "")
	if name == "" || name == "." {
		return "file.bin"
	}
	return name
}

var uploadCount int64

func incrementUploadCount() { uploadCount++ }

func (h *WebhookHandler) DirectUpload(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseMultipartForm(50 << 20); err != nil {
		http.Error(w, "file too large (max 50 MB)", http.StatusRequestEntityTooLarge)
		return
	}

	file, header, err := r.FormFile("file")
	if err != nil {
		http.Error(w, "missing 'file' field", http.StatusBadRequest)
		return
	}
	defer file.Close()

	phone := r.FormValue("phone")
	name := r.FormValue("name")

	contentType := header.Header.Get("Content-Type")
	if contentType == "" {
		contentType = "application/octet-stream"
	}

	cleanName := sanitizeFilename(header.Filename)

	result, err := h.s3.Upload(r.Context(), file, "direct", cleanName, contentType, header.Size)
	if err != nil {
		log.Printf("direct upload error: %v", err)
		http.Error(w, "upload failed", http.StatusInternalServerError)
		return
	}

	if phone != "" {
		farmerID, err := h.db.UpsertFarmer(phone, name, "direct")
		if err != nil {
			log.Printf("upsert farmer error: %v", err)
		}
		if farmerID != "" {
			if err := h.db.LogUpload(db.Upload{
				FarmerID:     farmerID,
				Phone:        phone,
				S3Key:        result.Key,
				S3URL:        result.URL,
				FileType:     "direct",
				MimeType:     contentType,
				FileSize:     header.Size,
				OriginalName: cleanName,
			}); err != nil {
				log.Printf("log upload error: %v", err)
			}
		}
	}

	incrementUploadCount()
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	_ = json.NewEncoder(w).Encode(map[string]string{
		"key": result.Key,
		"url": result.URL,
	})
}

func HealthCheck(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"status":  "ok",
		"uploads": uploadCount,
	})
}

func drain(r io.Reader) { _, _ = io.Copy(io.Discard, r) }
