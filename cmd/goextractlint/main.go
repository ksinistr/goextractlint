package main

import (
	"context"
	"log"
	"net/http"
	"os"
	"os/signal"

	"goextractlint/internal/cli"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)

	err := cli.Run(
		ctx,
		os.Args[1:],
		os.Getenv("OPENROUTER_API_KEY"),
		&http.Client{},
		os.UserCacheDir,
		os.Stdin,
		os.Stdout,
		os.Stderr,
	)

	stop()

	if err != nil {
		log.New(os.Stderr, "", 0).Print(err)
		os.Exit(1)
	}
}
