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
	"github.com/yourorg/whatsapp-s3-uploader/models"
	"github.com/yourorg/whatsapp-s3-uploader/services"
)

type WebhookHandler struct {
	cfg *appcfg.Config
	s3  *services.S3Service
	wa  *services.WhatsAppService
}

func NewWebhookHandler(cfg *appcfg.Config, s3 *services.S3Service, wa *services.WhatsAppService) *WebhookHandler {
	return &WebhookHandler{cfg: cfg, s3: s3, wa: wa}
}

// Verify handles GET /webhook — used by Meta to verify your endpoint during setup.
func (h *WebhookHandler) Verify(w http.ResponseWriter, r *http.Request) {
	mode      := r.URL.Query().Get("hub.mode")
	token     := r.URL.Query().Get("hub.verify_token")
	challenge := r.URL.Query().Get("hub.challenge")

	if mode == "subscribe" && token == h.cfg.WhatsAppVerifyToken {
		log.Println("webhook verified successfully")
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, challenge)
		return
	}

	log.Printf("webhook verification failed: mode=%s token=%s", mode, token)
	http.Error(w, "forbidden", http.StatusForbidden)
}

// Receive handles POST /webhook — incoming messages from Meta.
func (h *WebhookHandler) Receive(w http.ResponseWriter, r *http.Request) {
	// Always return 200 immediately; Meta will retry if it doesn't get one.
	// Do the actual work asynchronously.
	w.WriteHeader(http.StatusOK)

	var payload models.WebhookPayload
	if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
		log.Printf("webhook decode error: %v", err)
		return
	}

	// Only handle whatsapp_business_account events.
	if payload.Object != "whatsapp_business_account" {
		return
	}

	for _, entry := range payload.Entry {
		for _, change := range entry.Changes {
			if change.Field != "messages" {
				continue
			}
			for _, msg := range change.Value.Messages {
				go h.handleMessage(msg)
			}
		}
	}
}

// handleMessage processes a single incoming message in a goroutine.
func (h *WebhookHandler) handleMessage(msg models.Message) {
	ctx := context.Background()
	from := msg.From

	media, folder, filename := extractMedia(msg)
	if media == nil {
		// Not a media message — ignore or reply with help text.
		log.Printf("non-media message from %s (type: %s)", from, msg.Type)
		_ = h.wa.SendTextMessage(ctx, from, "Please send a file (image, video, audio, or document) to upload it.")
		return
	}

	log.Printf("received %s from %s: media_id=%s", msg.Type, from, media.ID)

	// Step 1: Resolve media_id → download URL.
	mediaInfo, err := h.wa.GetMediaURL(ctx, media.ID)
	if err != nil {
		log.Printf("get media url error: %v", err)
		_ = h.wa.SendTextMessage(ctx, from, "Sorry, I couldn't retrieve your file. Please try again.")
		return
	}

	// Step 2: Download the file from Meta's CDN.
	body, contentType, size, err := h.wa.DownloadMedia(ctx, mediaInfo.URL)
	if err != nil {
		log.Printf("download media error: %v", err)
		_ = h.wa.SendTextMessage(ctx, from, "Sorry, I couldn't download your file. Please try again.")
		return
	}
	defer body.Close()

	// Step 3: Determine filename.
	if filename == "" {
		ext := services.ExtensionFromMIME(contentType)
		filename = fmt.Sprintf("%s%s", media.ID, ext)
	}

	// Step 4: Upload to S3.
	result, err := h.s3.Upload(ctx, body, folder, sanitizeFilename(filename), contentType, size)
	if err != nil {
		log.Printf("s3 upload error: %v", err)
		_ = h.wa.SendTextMessage(ctx, from, "Sorry, the upload failed. Please try again.")
		return
	}

	log.Printf("uploaded to s3: %s", result.Key)

	// Step 5: Confirm to the user.
	reply := fmt.Sprintf("✅ Your file has been uploaded successfully!\n\nKey: %s", result.Key)
	if err := h.wa.SendTextMessage(ctx, from, reply); err != nil {
		log.Printf("send reply error: %v", err)
	}
}

// extractMedia pulls the media payload and categorises it.
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

// sanitizeFilename removes path separators to prevent directory traversal.
func sanitizeFilename(name string) string {
	name = filepath.Base(name)
	name = strings.ReplaceAll(name, "..", "")
	if name == "" || name == "." {
		return "file.bin"
	}
	return name
}

// UploadStats holds a lightweight in-memory counter (replace with Prometheus in prod).
var uploadCount int64

func incrementUploadCount() {
	uploadCount++
}

// DirectUpload reads a multipart file upload and pushes it straight to S3.
// Useful for REST clients outside of WhatsApp.
func (h *WebhookHandler) DirectUpload(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseMultipartForm(50 << 20); err != nil { // 50 MB limit
		http.Error(w, "file too large (max 50 MB)", http.StatusRequestEntityTooLarge)
		return
	}

	file, header, err := r.FormFile("file")
	if err != nil {
		http.Error(w, "missing 'file' field", http.StatusBadRequest)
		return
	}
	defer file.Close()

	contentType := header.Header.Get("Content-Type")
	if contentType == "" {
		contentType = "application/octet-stream"
	}

	result, err := h.s3.Upload(
		r.Context(),
		file,
		"direct",
		sanitizeFilename(header.Filename),
		contentType,
		header.Size,
	)
	if err != nil {
		log.Printf("direct upload error: %v", err)
		http.Error(w, "upload failed", http.StatusInternalServerError)
		return
	}

	incrementUploadCount()

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	_ = json.NewEncoder(w).Encode(map[string]string{
		"key": result.Key,
		"url": result.URL,
	})
}

// HealthCheck is a simple liveness probe endpoint.
func HealthCheck(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"status":  "ok",
		"uploads": uploadCount,
	})
}

// Drain cleanly reads and discards a response body to allow connection reuse.
func drain(r io.Reader) { _, _ = io.Copy(io.Discard, r) }
