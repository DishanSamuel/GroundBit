package services

import (
	"context"
	"fmt"

	"github.com/google/generative-ai-go/genai"
	"google.golang.org/api/option"
)

type GeminiService struct {
	client *genai.Client
	model  *genai.GenerativeModel
}

func NewGeminiService(apiKey string) (*GeminiService, error) {
	ctx := context.Background()
	client, err := genai.NewClient(ctx, option.WithAPIKey(apiKey))
	if err != nil {
		return nil, fmt.Errorf("creating gemini client: %w", err)
	}

	model := client.GenerativeModel("gemini-1.5-flash")
	model.SetTemperature(0.7)
	model.SetMaxOutputTokens(500) // Keep responses concise for voice

	// System instruction — farm advisor persona
	model.SystemInstruction = &genai.Content{
		Parts: []genai.Part{
			genai.Text(`You are GroundBit, an AI agricultural advisor for Indian farmers.
Your job is to give practical, actionable farming advice in simple language.
Rules:
- Keep responses under 100 words — this will be spoken aloud as a voice note
- Use simple language a farmer can understand
- Be specific and practical — give actual recommendations
- If asked about crop diseases, suggest treatment
- If asked about weather or irrigation, give specific advice
- If asked about market prices, acknowledge you need live data
- Always be warm and respectful
- Reply in the SAME language the farmer used`),
		},
	}

	return &GeminiService{client: client, model: model}, nil
}

func (g *GeminiService) Close() { g.client.Close() }

// GetFarmAdvice sends the transcribed farmer query to Gemini and returns advice text.
func (g *GeminiService) GetFarmAdvice(ctx context.Context, query string) (string, error) {
	resp, err := g.model.GenerateContent(ctx, genai.Text(query))
	if err != nil {
		return "", fmt.Errorf("gemini generate: %w", err)
	}

	if len(resp.Candidates) == 0 || len(resp.Candidates[0].Content.Parts) == 0 {
		return "", fmt.Errorf("empty gemini response")
	}

	text, ok := resp.Candidates[0].Content.Parts[0].(genai.Text)
	if !ok {
		return "", fmt.Errorf("unexpected gemini response type")
	}

	return string(text), nil
}
