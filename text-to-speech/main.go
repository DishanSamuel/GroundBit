package main

import (
	"context"
	"os"

	texttospeech "cloud.google.com/go/texttospeech/apiv1"
	texttospeechpb "cloud.google.com/go/texttospeech/apiv1/texttospeechpb"
)

func main() {

	ctx := context.Background()

	client, err := texttospeech.NewClient(ctx)
	if err != nil {
		panic(err)
	}

	req := &texttospeechpb.SynthesizeSpeechRequest{
		Input: &texttospeechpb.SynthesisInput{
			InputSource: &texttospeechpb.SynthesisInput_Text{
				Text: "ಖಂಡಿತ, ಧ್ವನಿ ಸಂದೇಶದ ಪ್ರತಿಲಿಪಿ ಇಲ್ಲಿದೆ",
			},
		},
		Voice: &texttospeechpb.VoiceSelectionParams{
			LanguageCode: "kn-IN",
		},
		AudioConfig: &texttospeechpb.AudioConfig{
			AudioEncoding: texttospeechpb.AudioEncoding_MP3,
		},
	}

	resp, err := client.SynthesizeSpeech(ctx, req)
	if err != nil {
		panic(err)
	}

	os.WriteFile("output.mp3", resp.AudioContent, 0644)
}
