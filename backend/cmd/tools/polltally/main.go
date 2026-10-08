// polltally recounts a poll without writes, seeds, reconciliation or identity output.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/google/uuid"
	"ludiskus/internal/database"
	"ludiskus/internal/repository"
	"ludiskus/internal/service"
	"os"
	"time"
)

func main() {
	if len(os.Args) != 2 {
		fmt.Fprintln(os.Stderr, "usage: polltally POLL_UUID")
		os.Exit(2)
	}
	if _, e := uuid.Parse(os.Args[1]); e != nil {
		fmt.Fprintln(os.Stderr, "invalid poll UUID")
		os.Exit(2)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	pool, e := database.Connect(ctx, os.Getenv("LUDISKUS_DB_DSN"), 2)
	if e != nil {
		fmt.Fprintln(os.Stderr, "database unavailable")
		os.Exit(1)
	}
	defer pool.Close()
	r := repository.New(pool)
	p, e := r.GetPoll(ctx, os.Args[1])
	if e != nil {
		fmt.Fprintln(os.Stderr, "poll unavailable")
		os.Exit(1)
	}
	ballots, e := r.PollBallots(ctx, p)
	if e != nil {
		fmt.Fprintln(os.Stderr, "ballots unavailable")
		os.Exit(1)
	}
	json.NewEncoder(os.Stdout).Encode(service.RecountPoll(p, ballots))
}
