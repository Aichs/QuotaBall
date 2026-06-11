package main

import (
	"log"

	"krill_monitor/internal/wailsui"
)

func main() {
	if err := wailsui.Run(); err != nil {
		log.Fatal(err)
	}
}
