package main

import (
	"context"
	"log"
	"os"
	"os/signal"
	"syscall"

	"github.com/simult/server/cmd/simult-server/config"
	"github.com/simult/server/pkg/httplb"
)

var (
	appCtx    context.Context
	appCancel context.CancelFunc
)

func main() {
	config.DebugLogger = log.New(os.Stdout, "DEBUG ", log.LstdFlags)
	config.InfoLogger = log.New(os.Stdout, "INFO ", log.LstdFlags)
	config.WarningLogger = log.New(os.Stdout, "WARNING ", log.LstdFlags)
	config.ErrorLogger = log.New(os.Stdout, "ERROR ", log.LstdFlags)

	httplb.DebugLogger = config.DebugLogger
	httplb.InfoLogger = config.InfoLogger
	httplb.WarningLogger = config.WarningLogger
	httplb.ErrorLogger = config.ErrorLogger

	appCtx, appCancel = context.WithCancel(context.Background())
	defer appCancel()
	go func() {
		sig := make(chan os.Signal)
		signal.Notify(sig, os.Interrupt, os.Kill, syscall.SIGTERM)
		<-sig
		appCancel()
	}()

	cfg, err := config.LoadFromFile("test.yaml")
	if err != nil {
		config.ErrorLogger.Println(err)
		os.Exit(1)
	}

	config.NewApp(cfg, nil)
	<-appCtx.Done()

}
