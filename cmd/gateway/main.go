package main

import (
	"context"
	"log"
	"os/signal"
	"syscall"

	"github.com/zltl/aiyolo/internal/app"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	if err := app.NewRootCommand().ExecuteContext(ctx); err != nil {
		log.Fatal(err)
	}
}
