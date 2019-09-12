package main

import (
	"context"
	"flag"
	"log"
	"os"
	"os/signal"
	"sync"
	"syscall"

	"github.com/simult/server/pkg/config"
)

var (
	app       *config.App
	appMu     sync.RWMutex
	appCtx    context.Context
	appCancel context.CancelFunc

	configFilename string
)

func configReload() bool {
	infoLogger.Printf("loading configuration from %q", configFilename)
	f, err := os.Open(configFilename)
	if err != nil {
		errorLogger.Printf("configuration file read error: %v", err)
		return false
	}
	defer f.Close()
	cfg, err := config.LoadFrom(f)
	if err != nil {
		errorLogger.Printf("configuration parse error: %v", err)
		return false
	}
	appMu.Lock()
	defer appMu.Unlock()
	an, err := app.Fork(cfg)
	if err != nil {
		errorLogger.Printf("configuration load error: %v", err)
		return false
	}
	if app != nil {
		app.Close()
	}
	app = an
	infoLogger.Print("configuration is active")
	return true
}

func main() {
	flag.StringVar(&configFilename, "c", "config.yaml", "config file")
	flag.Parse()

	setLoggers(
		log.New(os.Stdout, "ERROR ", log.LstdFlags),
		log.New(os.Stdout, "WARNING ", log.LstdFlags),
		log.New(os.Stdout, "INFO ", log.LstdFlags),
		log.New(os.Stdout, "DEBUG ", log.LstdFlags),
	)

	if !configReload() {
		os.Exit(2)
	}

	appCtx, appCancel = context.WithCancel(context.Background())
	defer appCancel()

	go func() {
		sigCh := make(chan os.Signal)
		signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM, syscall.SIGKILL)
		<-sigCh
		appCancel()
	}()

	configReloadSigCh := make(chan os.Signal)
	signal.Notify(configReloadSigCh, syscall.SIGHUP)
	done := false
	for !done {
		select {
		case <-appCtx.Done():
			done = true
		case <-configReloadSigCh:
			configReload()
		}
	}

	appMu.Lock()
	app.Close()
	appMu.Unlock()
}
