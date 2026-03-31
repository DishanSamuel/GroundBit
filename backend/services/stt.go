package services

import (
	"context"
	"fmt"
	"os"
	"os/exec"

	speech "cloud.google.com/go/speech/apiv1"
	"cloud.google.com/go/speech/apiv1/speechpb"
	"google.golang.org/api/option"
)

type STTService struct {
	client      *speech.Client
	credentials string
}

func NewSTTService(credentialsFile string) (*STTService, error) {
	ctx := context.Background()
	client, err := speech.NewClient(ctx, option.WithCredentialsFile(credentialsFile))
	if err != nil {
		return nil, fmt.Errorf("creating stt client: %w", err)
	}
	return &STTService{client: client, credentials: credentialsFile}, nil
}

func (s *STTService) Close() { s.client.Close() }

// TranscribeAudio converts an ogg/opus audio file to text.
// It first converts to FLAC using ffmpeg, then sends to GCP STT.
// languageCode examples: "kn-IN", "hi-IN", "ta-IN", "te-IN", "en-IN"
func (s *STTService) TranscribeAudio(ctx context.Context, audioPath string) (string, string, error) {
	// Convert ogg → flac using ffmpeg
	flacPath := audioPath + ".flac"
	defer os.Remove(flacPath)

	cmd := exec.CommandContext(ctx, "ffmpeg", "-y",
		"-i", audioPath,
		"-ar", "16000", // 16kHz sample rate — required by GCP STT
		"-ac", "1", // mono
		"-f", "flac",
		flacPath,
	)
	if out, err := cmd.CombinedOutput(); err != nil {
		return "", "", fmt.Errorf("ffmpeg conversion failed: %w — %s", err, out)
	}

	// Read converted file
	audioBytes, err := os.ReadFile(flacPath)
	if err != nil {
		return "", "", fmt.Errorf("reading flac file: %w", err)
	}

	// Try multiple languages — GCP STT will pick the best match
	languageCodes := []string{"kn-IN", "hi-IN", "ta-IN", "te-IN", "en-IN"}
	alternativeCodes := languageCodes[1:]

	req := &speechpb.RecognizeRequest{
		Config: &speechpb.RecognitionConfig{
			Encoding:                   speechpb.RecognitionConfig_FLAC,
			SampleRateHertz:            16000,
			AudioChannelCount:          1,
			LanguageCode:               languageCodes[0],
			AlternativeLanguageCodes:   alternativeCodes,
			EnableAutomaticPunctuation: true,
			Model:                      "latest_long",
		},
		Audio: &speechpb.RecognitionAudio{
			AudioSource: &speechpb.RecognitionAudio_Content{
				Content: audioBytes,
			},
		},
	}

	resp, err := s.client.Recognize(ctx, req)
	if err != nil {
		return "", "", fmt.Errorf("stt recognize: %w", err)
	}

	if len(resp.Results) == 0 {
		return "", "", fmt.Errorf("no transcription results")
	}

	best := resp.Results[0].Alternatives[0]
	detectedLang := resp.Results[0].LanguageCode
	return best.Transcript, detectedLang, nil
}
