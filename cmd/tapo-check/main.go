// tapo-check prints the plug's device info. It never switches the plug.
package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"time"

	"github.com/grillermo/awh/internal/tapo"
)

func main() {
	c := &tapo.Client{Base: "http://" + os.Getenv("TAPO_IP"), Email: os.Getenv("TAPO_EMAIL"), Password: os.Getenv("TAPO_PASSWORD")}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	info, err := c.Info(ctx)
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println("model:", info["model"], "device_on:", info["device_on"])
}
