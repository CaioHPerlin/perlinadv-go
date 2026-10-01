package main

import (
	"context"
	"flag"
	"log"
	"time"

	"github.com/caiohperlin/perlinadv/internal/worker"
)

func main() {
	once := flag.Bool("once", false, "run one full collection cycle and exit")
	tribunal := flag.String("tribunal", "", "replay one tribunal edition")
	date := flag.String("date", "", "replay edition date YYYY-MM-DD")
	flag.Parse()
	w, err := worker.New()
	if err != nil {
		log.Fatal(err)
	}
	defer w.Close()
	if *tribunal != "" || *date != "" {
		if *tribunal == "" || *date == "" {
			log.Fatal("-tribunal and -date must be used together")
		}
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
		defer cancel()
		if err = w.Replay(ctx, *tribunal, *date); err != nil {
			log.Fatal(err)
		}
		log.Printf("replayed %s %s", *tribunal, *date)
		return
	}
	runCollection := func() {
		ctx, cancel := context.WithTimeout(context.Background(), 50*time.Minute)
		runErr := w.RunOnce(ctx)
		cancel()
		if runErr != nil {
			log.Printf("worker run: %v", runErr)
		}
	}
	if *once {
		runCollection()
		return
	}
	go func() {
		runCollection()
		ticker := time.NewTicker(time.Hour)
		defer ticker.Stop()
		for range ticker.C {
			runCollection()
		}
	}()
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()
	for range ticker.C {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		if err := w.ProcessAnalysis(ctx); err != nil {
			log.Printf("analysis queue: %v", err)
		}
		if err := w.SyncGoogle(ctx); err != nil {
			log.Printf("Google sync: %v", err)
		}
		cancel()
	}
}
