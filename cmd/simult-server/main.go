package main

import (
	"context"
	"flag"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	_ "net/http/pprof"

	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/simult/server/pkg/config"
	"github.com/simult/server/pkg/lb"
)

var (
	app       *config.App
	appMu     sync.RWMutex
	appCtx    context.Context
	appCancel context.CancelFunc
)

func configReload(configFilename string) bool {
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
		lb.PromReset()
	}
	app = an
	infoLogger.Print("configuration is active")
	return true
}

func main() {
	var configFilename string
	var promAddress string
	var promNamespace string
	flag.StringVar(&configFilename, "c", "config.yaml", "config file")
	flag.StringVar(&promAddress, "prom-address", "", "prometheus exporter address")
	flag.StringVar(&promNamespace, "prom-namespace", "", "prometheus exporter namespace")
	flag.Parse()

	setLoggers(
		log.New(os.Stdout, "ERROR ", log.LstdFlags),
		log.New(os.Stdout, "WARNING ", log.LstdFlags),
		log.New(os.Stdout, "INFO ", log.LstdFlags),
		log.New(os.Stdout, "DEBUG ", log.LstdFlags),
	)

	if promAddress != "" {
		promLis, err := net.Listen("tcp", promAddress)
		if err != nil {
			errorLogger.Printf("prometheus exporter listen error: %v", err)
			os.Exit(2)
		}
		defer promLis.Close()
		//promMux := http.NewServeMux()
		//promMux.Handle("/metrics", promhttp.Handler())
		http.Handle("/metrics", promhttp.Handler())
		promServer := http.Server{
			//Handler:        promMux,
			ReadTimeout:    60 * time.Second,
			WriteTimeout:   60 * time.Second,
			MaxHeaderBytes: 1 << 20,
		}
		go promServer.Serve(promLis)
		lb.PromInitialize(promNamespace)
	} else {
		lb.PromInitialize("")
	}

	if !configReload(configFilename) {
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
			configReload(configFilename)
		}
	}

	appMu.Lock()
	app.Close()
	appMu.Unlock()
}
