package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	appcfg "github.com/yourorg/whatsapp-s3-uploader/config"
	"github.com/yourorg/whatsapp-s3-uploader/db"
	"github.com/yourorg/whatsapp-s3-uploader/models"
	"github.com/yourorg/whatsapp-s3-uploader/services"
)

type WebhookHandler struct {
	cfg    *appcfg.Config
	s3     *services.S3Service
	wa     *services.WhatsAppService
	db     *db.DB
	stt    *services.STTService
	tts    *services.TTSService
	gemini *services.GeminiService
}

func NewWebhookHandler(
	cfg *appcfg.Config,
	s3 *services.S3Service,
	wa *services.WhatsAppService,
	database *db.DB,
	stt *services.STTService,
	tts *services.TTSService,
	gemini *services.GeminiService,
) *WebhookHandler {
	return &WebhookHandler{cfg: cfg, s3: s3, wa: wa, db: database, stt: stt, tts: tts, gemini: gemini}
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
				go h.handleMessage(msg, nameMap[msg.From])
			}
		}
	}
}

func (h *WebhookHandler) handleMessage(msg models.Message, name string) {
	ctx := context.Background()
	from := msg.From

	// ── Location ──────────────────────────────────────────────────────
	if msg.Type == "location" && msg.Location != nil {
		loc := msg.Location
		log.Printf("location from %s (%s): lat=%.6f lng=%.6f", from, name, loc.Latitude, loc.Longitude)
		if _, err := h.db.UpsertFarmer(from, name, "whatsapp"); err != nil {
			log.Printf("upsert farmer error: %v", err)
		}
		if err := h.db.UpdateFarmerLocation(from, loc.Latitude, loc.Longitude, loc.Name, loc.Address); err != nil {
			log.Printf("update location error: %v", err)
			_ = h.wa.SendTextMessage(ctx, from, "Sorry, I couldn't save your location.")
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

	// ── Audio — farm query pipeline ───────────────────────────────────
	if msg.Type == "audio" && msg.Audio != nil {
		log.Printf("audio query from %s (%s): media_id=%s", from, name, msg.Audio.ID)
		h.handleAudioQuery(ctx, from, name, msg.Audio)
		return
	}

	// ── Other media — upload to S3 ────────────────────────────────────
	media, folder, filename := extractMedia(msg)
	if media == nil {
		log.Printf("non-media message from %s (type: %s)", from, msg.Type)
		_ = h.wa.SendTextMessage(ctx, from, "Send a voice note to ask a farming question, a file to upload it, or share your location.")
		return
	}

	log.Printf("received %s from %s (%s): media_id=%s", msg.Type, from, name, media.ID)
	h.uploadMedia(ctx, from, name, media, folder, filename)
}

// handleAudioQuery runs the full STT → Gemini → TTS → WhatsApp pipeline.
func (h *WebhookHandler) handleAudioQuery(ctx context.Context, from, name string, audio *models.MediaInfo) {
	// Upsert farmer
	farmerID, err := h.db.UpsertFarmer(from, name, "whatsapp")
	if err != nil {
		log.Printf("upsert farmer error: %v", err)
	}

	// Step 1: Download audio from Meta
	mediaInfo, err := h.wa.GetMediaURL(ctx, audio.ID)
	if err != nil {
		log.Printf("get audio url error: %v", err)
		_ = h.wa.SendTextMessage(ctx, from, "Sorry, I couldn't receive your voice note. Please try again.")
		return
	}

	body, _, _, err := h.wa.DownloadMedia(ctx, mediaInfo.URL)
	if err != nil {
		log.Printf("download audio error: %v", err)
		_ = h.wa.SendTextMessage(ctx, from, "Sorry, I couldn't download your voice note.")
		return
	}
	defer body.Close()

	// Save audio to temp file
	tmpAudio, err := os.CreateTemp("", "audio-*.ogg")
	if err != nil {
		log.Printf("temp file error: %v", err)
		return
	}
	tmpAudioPath := tmpAudio.Name()
	defer os.Remove(tmpAudioPath)

	if _, err := io.Copy(tmpAudio, body); err != nil {
		log.Printf("write audio error: %v", err)
		tmpAudio.Close()
		return
	}
	tmpAudio.Close()

	// Step 2: STT — transcribe audio
	_ = h.wa.SendTextMessage(ctx, from, "🎙️ Got your voice note! Processing...")
	transcript, detectedLang, err := h.stt.TranscribeAudio(ctx, tmpAudioPath)
	if err != nil {
		log.Printf("stt error: %v", err)
		_ = h.wa.SendTextMessage(ctx, from, "Sorry, I couldn't understand your voice note. Please try speaking clearly.")
		return
	}
	log.Printf("transcribed (%s): %s", detectedLang, transcript)

	// Step 3: Gemini — get farm advice
	advice, err := h.gemini.GetFarmAdvice(ctx, transcript)
	if err != nil {
		log.Printf("gemini error: %v", err)
		_ = h.wa.SendTextMessage(ctx, from, "Sorry, I couldn't generate advice right now. Please try again.")
		return
	}
	log.Printf("gemini advice: %s", advice)

	// Step 4: TTS — convert advice to audio
	audioPath, err := h.tts.SynthesizeSpeech(ctx, advice, detectedLang)
	if err != nil {
		log.Printf("tts error: %v", err)
		// Fallback — send text reply if TTS fails
		_ = h.wa.SendTextMessage(ctx, from, advice)
		return
	}
	defer os.Remove(audioPath)

	// Step 5: Upload TTS audio to S3
	audioFile, err := os.Open(audioPath)
	if err != nil {
		log.Printf("open audio error: %v", err)
		_ = h.wa.SendTextMessage(ctx, from, advice)
		return
	}
	defer audioFile.Close()

	stat, _ := audioFile.Stat()
	result, err := h.s3.Upload(ctx, audioFile, "responses", fmt.Sprintf("response-%s.ogg", audio.ID), "audio/ogg", stat.Size())
	if err != nil {
		log.Printf("s3 upload tts error: %v", err)
		_ = h.wa.SendTextMessage(ctx, from, advice)
		return
	}

	// Step 6: Send audio reply back to farmer
	if err := h.wa.SendAudioMessage(ctx, from, result.URL); err != nil {
		log.Printf("send audio error: %v", err)
		// Fallback to text
		_ = h.wa.SendTextMessage(ctx, from, advice)
		return
	}

	log.Printf("audio reply sent to %s (%s)", from, name)

	// Log to DB
	if farmerID != "" {
		_ = h.db.LogUpload(db.Upload{
			FarmerID:     farmerID,
			Phone:        from,
			S3Key:        result.Key,
			S3URL:        result.URL,
			FileType:     "audio_response",
			MimeType:     "audio/ogg",
			FileSize:     stat.Size(),
			OriginalName: fmt.Sprintf("response-%s.ogg", audio.ID),
		})
	}
}

// uploadMedia handles non-audio file uploads to S3.
func (h *WebhookHandler) uploadMedia(ctx context.Context, from, name string, media *models.MediaInfo, folder, filename string) {
	farmerID, err := h.db.UpsertFarmer(from, name, "whatsapp")
	if err != nil {
		log.Printf("upsert farmer error: %v", err)
	}

	mediaInfo, err := h.wa.GetMediaURL(ctx, media.ID)
	if err != nil {
		log.Printf("get media url error: %v", err)
		_ = h.wa.SendTextMessage(ctx, from, "Sorry, I couldn't retrieve your file.")
		return
	}

	body, contentType, size, err := h.wa.DownloadMedia(ctx, mediaInfo.URL)
	if err != nil {
		log.Printf("download media error: %v", err)
		_ = h.wa.SendTextMessage(ctx, from, "Sorry, I couldn't download your file.")
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
		_ = h.wa.SendTextMessage(ctx, from, "Sorry, the upload failed.")
		return
	}

	log.Printf("uploaded to s3: %s", result.Key)

	if farmerID != "" {
		_ = h.db.LogUpload(db.Upload{
			FarmerID:     farmerID,
			Phone:        from,
			S3Key:        result.Key,
			S3URL:        result.URL,
			FileType:     folder,
			MimeType:     contentType,
			FileSize:     size,
			OriginalName: cleanName,
		})
	}

	_ = h.wa.SendTextMessage(ctx, from, fmt.Sprintf("✅ File uploaded!\n\nKey: %s", result.Key))
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
		farmerID, _ := h.db.UpsertFarmer(phone, name, "direct")
		if farmerID != "" {
			_ = h.db.LogUpload(db.Upload{
				FarmerID: farmerID, Phone: phone,
				S3Key: result.Key, S3URL: result.URL,
				FileType: "direct", MimeType: contentType,
				FileSize: header.Size, OriginalName: cleanName,
			})
		}
	}

	incrementUploadCount()
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	_ = json.NewEncoder(w).Encode(map[string]string{"key": result.Key, "url": result.URL})
}

func HealthCheck(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"status": "ok", "uploads": uploadCount})
}

func drain(r io.Reader) { _, _ = io.Copy(io.Discard, r) }
