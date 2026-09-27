package main

import (
	"context"
	"flag"
	"github.com/caiohperlin/perlinadv/internal/worker"
	"log"
	"time"
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
	for {
		ctx, cancel := context.WithTimeout(context.Background(), 50*time.Minute)
		err = w.RunOnce(ctx)
		cancel()
		if err != nil {
			log.Printf("worker run: %v", err)
		}
		if *once {
			return
		}
		time.Sleep(time.Hour)
	}
}
