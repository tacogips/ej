package main

import (
	"log"

	"cloud.google.com/go/translate"
	"google.golang.org/api/option"
)

func main() {
	apiKey := os.Env("EJ_API_KEY")
	client, err := translate.NewClient(ctx, option.WithAPIKey(apiKey))
	if err != nil {
		log.Fatal(err)
	}
	resp, err := client.Translate(ctx, []string{""})

}
