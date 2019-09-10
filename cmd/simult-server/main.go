package main

import (
	"context"
	"log"
	"os"
	"os/signal"
	"syscall"

	"github.com/simult/server/pkg/app"
)

var (
	appCtx    context.Context
	appCancel context.CancelFunc
)

func main() {
	setLoggers(
		log.New(os.Stdout, "ERROR ", log.LstdFlags),
		log.New(os.Stdout, "WARNING ", log.LstdFlags),
		log.New(os.Stdout, "INFO ", log.LstdFlags),
		log.New(os.Stdout, "DEBUG ", log.LstdFlags),
	)
	appCtx, appCancel = context.WithCancel(context.Background())
	defer appCancel()
	go func() {
		sig := make(chan os.Signal)
		signal.Notify(sig, os.Interrupt, os.Kill, syscall.SIGTERM)
		<-sig
		appCancel()
	}()

	cfg, err := app.ConfigLoadFromFile("test.yaml")
	if err != nil {
		errorLogger.Println(err)
		os.Exit(1)
	}

	_, err = app.New(cfg)
	if err != nil {
		errorLogger.Println(err)
		os.Exit(1)
	}
	<-appCtx.Done()

}
