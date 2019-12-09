package main

import (
	"context"
	"flag"
	"io/ioutil"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"regexp"
	"sync"
	"syscall"
	"time"

	_ "net/http/pprof"

	"github.com/goinsane/accepter"
	"github.com/goinsane/xlog"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/simult/simult/pkg/config"
	"github.com/simult/simult/pkg/lb"
	"github.com/simult/simult/pkg/version"
)

var (
	app       *config.App
	appMu     sync.RWMutex
	appCtx    context.Context
	appCancel context.CancelFunc

	managementServer *http.Server
)

const (
	closeTimeout     = 30 * time.Second
	terminateTimeout = 2 * time.Second
)

var (
	promMetricNameRgx = regexp.MustCompile(`^[a-zA-Z_:]([a-zA-Z0-9_:])*$`)
	promLabelNameRgx  = regexp.MustCompile(`^[a-zA-Z_]([a-zA-Z0-9_])*$`)
)

func configGlobal(cfg *config.Config) {
	var err error

	if cfg.Global.PromResetOnReload && app != nil {
		lb.PromReset()
		xlog.Info("config global.promresetonreload: prometheus metrics have reset")
	}

	rlimitNofile := cfg.Global.RlimitNofile
	rLimit := &syscall.Rlimit{}
	if rlimitNofile <= 0 {
		rlimitNofile = 1024
		err = syscall.Getrlimit(syscall.RLIMIT_NOFILE, rLimit)
		if err == nil {
			rlimitNofile = rLimit.Cur
		}
	}
	rLimit = &syscall.Rlimit{
		Cur: rlimitNofile,
		Max: rlimitNofile,
	}
	err = syscall.Setrlimit(syscall.RLIMIT_NOFILE, rLimit)
	if err != nil {
		xlog.Warningf("config global.rlimitnofile: error setting to %d: %v", rlimitNofile, err)
	} else {
		xlog.Infof("config global.rlimitnofile: set to %d", rlimitNofile)
	}
}

func configReload(configFilename string) bool {
	xlog.Infof("loading configuration from %q", configFilename)
	f, err := os.Open(configFilename)
	if err != nil {
		xlog.Errorf("configuration file read error: %v", err)
		return false
	}
	defer f.Close()
	cfg, err := config.LoadFrom(f)
	if err != nil {
		xlog.Errorf("configuration parse error: %v", err)
		return false
	}
	appMu.Lock()
	defer appMu.Unlock()
	an, err := app.Fork(cfg)
	if err != nil {
		xlog.Errorf("configuration load error: %v", err)
		return false
	}
	xlog.Info("configuration loaded")
	if app != nil {
		xlog.Infof("closing old objects within %v", closeTimeout)
		closeCtx, closeCtxCancel := context.WithTimeout(appCtx, closeTimeout)
		defer closeCtxCancel()
		app.Close(closeCtx)
		xlog.Info("closed old objects")
	}
	configGlobal(cfg)
	app = an
	xlog.Info("configuration is active")
	return true
}

func main() {
	var configFilename string
	var managementAddress string
	var promNamespace string
	var verbose int
	var debugMode bool
	flag.StringVar(&configFilename, "c", "server.yaml", "config file")
	flag.StringVar(&managementAddress, "m", "", "management address")
	flag.StringVar(&promNamespace, "prom-namespace", "simult", "prometheus exporter namespace")
	flag.IntVar(&verbose, "v", 0, "verbose level")
	flag.BoolVar(&debugMode, "debug", false, "debug mode")
	flag.Parse()
	if !(verbose >= 0 && verbose <= 65535) {
		flag.PrintDefaults()
		os.Exit(2)
	}

	severity := xlog.SeverityInfo
	outputFlags := xlog.OutputFlagDefault | xlog.OutputFlagPadding
	if debugMode {
		severity = xlog.SeverityDebug
		outputFlags |= xlog.OutputFlagStackTrace
	}
	xlog.SetSeverity(severity)
	xlog.SetVerbose(xlog.Verbose(verbose))
	xlog.SetStackTraceSeverity(xlog.SeverityError)
	xlog.SetOutputFlags(outputFlags)
	xlog.Infof("started simult-server %s", version.Full())

	accepter.SetMaxTempDelay(5 * time.Second)

	if !promMetricNameRgx.MatchString(promNamespace) {
		xlog.Fatalf("prometheus exporter namespace %q is not a valid metric name", promNamespace)
	}
	lb.PromInitialize(promNamespace)

	if managementAddress != "" {
		managementLis, err := net.Listen("tcp", managementAddress)
		if err != nil {
			xlog.Fatalf("management address listen error: %v", err)
		}
		defer managementLis.Close()
		http.Handle("/metrics", promhttp.Handler())
		managementServer = &http.Server{
			Handler:        nil,
			ReadTimeout:    60 * time.Second,
			WriteTimeout:   60 * time.Second,
			MaxHeaderBytes: 1 << 20,
			ErrorLog:       log.New(ioutil.Discard, "", log.LstdFlags),
		}
		go managementServer.Serve(managementLis)
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

	appMu.RLock()
	defer appMu.RUnlock()
	xlog.Infof("terminating simult-server within %v", terminateTimeout)
	closeCtx, closeCtxCancel := context.WithTimeout(context.Background(), terminateTimeout)
	defer closeCtxCancel()
	app.Close(closeCtx)
	xlog.Infof("terminated simult-server %s", version.Full())
}
