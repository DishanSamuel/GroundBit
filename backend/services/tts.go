package services

import (
	"context"
	"fmt"
	"os"

	texttospeech "cloud.google.com/go/texttospeech/apiv1"
	"cloud.google.com/go/texttospeech/apiv1/texttospeechpb"
	"google.golang.org/api/option"
)

type TTSService struct {
	client *texttospeech.Client
}

func NewTTSService(credentialsFile string) (*TTSService, error) {
	ctx := context.Background()
	client, err := texttospeech.NewClient(ctx, option.WithCredentialsFile(credentialsFile))
	if err != nil {
		return nil, fmt.Errorf("creating tts client: %w", err)
	}
	return &TTSService{client: client}, nil
}

func (s *TTSService) Close() { s.client.Close() }

// languageCodeToVoice maps GCP STT language codes to TTS voice names.
var languageCodeToVoice = map[string]string{
	"kn-in": "kn-IN-Standard-A",
	"hi-in": "hi-IN-Standard-A",
	"ta-in": "ta-IN-Standard-A",
	"te-in": "te-IN-Standard-A",
	"en-in": "en-IN-Standard-A",
}

// SynthesizeSpeech converts text to an OGG audio file.
// detectedLang is the BCP-47 language code returned by STT (e.g. "kn-IN").
// Returns the path to the generated .ogg file.
func (s *TTSService) SynthesizeSpeech(ctx context.Context, text, detectedLang string) (string, error) {
	// Normalize to lowercase for map lookup
	langKey := detectedLang
	if len(langKey) > 5 {
		langKey = langKey[:5]
	}

	voiceName, ok := languageCodeToVoice[langKey]
	if !ok {
		// Default to English if language not mapped
		langKey = "en-in"
		voiceName = "en-IN-Standard-A"
		detectedLang = "en-IN"
	}
	_ = langKey

	req := &texttospeechpb.SynthesizeSpeechRequest{
		Input: &texttospeechpb.SynthesisInput{
			InputSource: &texttospeechpb.SynthesisInput_Text{
				Text: text,
			},
		},
		Voice: &texttospeechpb.VoiceSelectionParams{
			LanguageCode: detectedLang,
			Name:         voiceName,
		},
		AudioConfig: &texttospeechpb.AudioConfig{
			// OGG_OPUS is natively supported by WhatsApp
			AudioEncoding: texttospeechpb.AudioEncoding_OGG_OPUS,
			SpeakingRate:  0.95, // Slightly slower — clearer for farmers
		},
	}

	resp, err := s.client.SynthesizeSpeech(ctx, req)
	if err != nil {
		return "", fmt.Errorf("synthesize speech: %w", err)
	}

	// Write to temp file
	tmpFile, err := os.CreateTemp("", "tts-*.ogg")
	if err != nil {
		return "", fmt.Errorf("creating temp file: %w", err)
	}
	defer tmpFile.Close()

	if _, err := tmpFile.Write(resp.AudioContent); err != nil {
		return "", fmt.Errorf("writing audio: %w", err)
	}

	return tmpFile.Name(), nil
}
