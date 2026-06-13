package main

import (
	"log"

	"quotaball/internal/wailsui"
)

func main() {
	if err := wailsui.Run(); err != nil {
		log.Fatal(err)
	}
}
