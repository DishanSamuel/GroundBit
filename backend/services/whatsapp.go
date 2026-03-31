package services

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime"
	"net/http"
	"time"

	appcfg "github.com/yourorg/whatsapp-s3-uploader/config"
	"github.com/yourorg/whatsapp-s3-uploader/models"
)

type WhatsAppService struct {
	cfg     *appcfg.Config
	client  *http.Client
	baseURL string
}

func NewWhatsAppService(cfg *appcfg.Config) *WhatsAppService {
	return &WhatsAppService{
		cfg:     cfg,
		client:  &http.Client{Timeout: 60 * time.Second},
		baseURL: fmt.Sprintf("https://graph.facebook.com/%s", cfg.WhatsAppAPIVersion),
	}
}

// GetMediaURL resolves a media_id into a download URL.
func (w *WhatsAppService) GetMediaURL(ctx context.Context, mediaID string) (*models.MediaURLResponse, error) {
	url := fmt.Sprintf("%s/%s", w.baseURL, mediaID)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+w.cfg.WhatsAppToken)

	resp, err := w.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetching media url: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("media url api error %d: %s", resp.StatusCode, body)
	}

	var result models.MediaURLResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("decoding media url response: %w", err)
	}
	return &result, nil
}

// DownloadMedia fetches the actual media bytes from Meta's CDN.
func (w *WhatsAppService) DownloadMedia(ctx context.Context, mediaURL string) (io.ReadCloser, string, int64, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, mediaURL, nil)
	if err != nil {
		return nil, "", 0, err
	}
	req.Header.Set("Authorization", "Bearer "+w.cfg.WhatsAppToken)

	resp, err := w.client.Do(req)
	if err != nil {
		return nil, "", 0, fmt.Errorf("downloading media: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		resp.Body.Close()
		return nil, "", 0, fmt.Errorf("media download error %d", resp.StatusCode)
	}

	return resp.Body, resp.Header.Get("Content-Type"), resp.ContentLength, nil
}

// SendTextMessage sends a plain-text reply to a WhatsApp user.
func (w *WhatsAppService) SendTextMessage(ctx context.Context, to, text string) error {
	payload := map[string]any{
		"messaging_product": "whatsapp",
		"to":                to,
		"type":              "text",
		"text":              map[string]string{"body": text},
	}
	return w.postMessage(ctx, payload)
}

// SendAudioMessage sends an audio file back to the user via WhatsApp.
// audioURL must be a publicly accessible URL (presigned S3 URL).
func (w *WhatsAppService) SendAudioMessage(ctx context.Context, to, audioURL string) error {
	payload := map[string]any{
		"messaging_product": "whatsapp",
		"to":                to,
		"type":              "audio",
		"audio":             map[string]string{"link": audioURL},
	}
	return w.postMessage(ctx, payload)
}

// SendTemplateMessage sends a template message.
func (w *WhatsAppService) SendTemplateMessage(ctx context.Context, to, templateName, langCode string) error {
	payload := map[string]any{
		"messaging_product": "whatsapp",
		"to":                to,
		"type":              "template",
		"template": map[string]any{
			"name":     templateName,
			"language": map[string]string{"code": langCode},
		},
	}
	return w.postMessage(ctx, payload)
}

func (w *WhatsAppService) postMessage(ctx context.Context, payload any) error {
	body, err := json.Marshal(payload)
	if err != nil {
		return err
	}

	url := fmt.Sprintf("%s/%s/messages", w.baseURL, w.cfg.WhatsAppPhoneID)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+w.cfg.WhatsAppToken)
	req.Header.Set("Content-Type", "application/json")

	resp, err := w.client.Do(req)
	if err != nil {
		return fmt.Errorf("sending message: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		respBody, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("send message api error %d: %s", resp.StatusCode, respBody)
	}
	return nil
}

// ExtensionFromMIME maps a MIME type to a file extension.
func ExtensionFromMIME(mimeType string) string {
	exts, err := mime.ExtensionsByType(mimeType)
	if err != nil || len(exts) == 0 {
		return ".bin"
	}
	return exts[0]
}
