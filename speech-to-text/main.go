package main

import (
	"context"
	"fmt"
	"log"
	"os"

	genai "github.com/google/generative-ai-go/genai"
	"google.golang.org/api/option"

	"github.com/joho/godotenv"
)

func main() {
	//	gemeinapikey := os.Getenv("GEMINI_API_KEY")

	err := godotenv.Load()
	if err != nil {
		log.Fatal("Error loading .env file")
	}

	apiKey := os.Getenv("GEMINI_API_KEY")
	if apiKey == "" {
		log.Fatal("GEMINI_API_KEY is empty")
	}

	ctx := context.Background()

	client, err := genai.NewClient(
		ctx,
		option.WithAPIKey(apiKey),
	)
	if err != nil {
		log.Fatal(err)
	}

	defer client.Close()

	model := client.GenerativeModel("gemini-3-flash-preview")

	audio, err := os.ReadFile("Shashank.ogg")
	if err != nil {
		panic(err)
	}

	resp, err := model.GenerateContent(
		ctx,
		genai.Blob{
			MIMEType: "audio/ogg",
			Data:     audio,
		},
		genai.Text("Transcribe this kannada voice message in kannada itself nothing much"),
	)

	if err != nil {
		panic(err)
	}

	fmt.Println(resp.Candidates[0].Content.Parts[0])
}
