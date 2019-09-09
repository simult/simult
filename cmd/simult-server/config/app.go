package config

import (
	"crypto/tls"
	"net"
	"net/http"
	"regexp"
	"sync"

	accepter "github.com/orkunkaraduman/go-accepter"
	"github.com/simult/server/pkg/httplb"
)

type App struct {
	mu sync.Mutex

	listeners    map[string]*accepter.Accepter
	frontends    map[string]*httplb.Frontend
	backends     map[string]*httplb.Backend
	healthChecks map[string]interface{}
}

func NewApp(cfg *Config, old *App) (app *App) {
	if old != nil {
		old.mu.Lock()
		defer old.mu.Unlock()
	}

	app = &App{
		listeners:    make(map[string]*accepter.Accepter),
		frontends:    make(map[string]*httplb.Frontend),
		backends:     make(map[string]*httplb.Backend),
		healthChecks: make(map[string]interface{}),
	}

	for name, item := range cfg.HealthChecks {
		if name == "" {
			continue
		}
		if _, ok := app.healthChecks[name]; ok {
			WarningLogger.Printf("healthcheck %s already defined", name)
			continue
		}
		if item.HTTP != nil {
			app.healthChecks[name] = *item.HTTP
			continue
		}
	}

	for name, item := range cfg.Backends {
		if name == "" {
			continue
		}
		if _, ok := app.backends[name]; ok {
			WarningLogger.Printf("backend %s already defined", name)
			continue
		}
		var opts httplb.BackendOptions
		if item.Timeout > 0 {
			opts.Timeout = item.Timeout
		}
		opts.ReqHeader = make(http.Header, len(item.ReqHeaders))
		for hk, hv := range item.ReqHeaders {
			opts.ReqHeader.Set(hk, hv)
		}
		opts.Servers = item.Servers
		var b *httplb.Backend
		if old != nil {
			b = old.backends[name]
		}
		if b == nil {
			b = httplb.NewBackend()
		}
		if e := b.SetOpts(opts); e != nil {
			WarningLogger.Printf("backend %s config error: %v", name, e)
			continue
		}
		app.backends[name] = b
	}

	for name, item := range cfg.Frontends {
		if _, ok := app.frontends[name]; ok {
			WarningLogger.Printf("frontend %s already defined", name)
			continue
		}
		var opts httplb.FrontendOptions
		if item.Timeout > 0 {
			opts.Timeout = item.Timeout
		}
		b := app.backends[item.DefaultBackend]
		if b == nil {
			WarningLogger.Printf("frontend %s default backend not found", name)
		}
		opts.DefaultBackend = b
		opts.Routes = make([]httplb.FrontendRoute, 0, len(item.Routes))
		for i := range item.Routes {
			r, t := &item.Routes[i], &httplb.FrontendRoute{}
			var e error
			t.Host, e = regexp.Compile(r.Host)
			if e != nil {
				WarningLogger.Printf("frontend %s route error: host %s error: %v", name, r.Host, e)
				continue
			}
			t.Path, e = regexp.Compile(r.Path)
			if e != nil {
				WarningLogger.Printf("frontend %s route error: path %s error: %v", name, r.Path, e)
				continue
			}
			t.Backend = app.backends[r.Backend]
			if t.Backend == nil {
				WarningLogger.Printf("frontend %s route error: backend %s not found", name, r.Backend)
				continue
			}
			opts.Routes = append(opts.Routes, *t)
		}
		var f *httplb.Frontend
		if old != nil {
			f = old.frontends[name]
		}
		if f == nil {
			f = httplb.NewFrontend()
		}
		if e := f.SetOpts(opts); e != nil {
			WarningLogger.Printf("frontend %s config error: %v", name, e)
			continue
		}
		app.frontends[name] = f

		for _, lItem := range item.Listeners {
			if lItem == nil || lItem.Address == "" {
				continue
			}
			address := lItem.Address
			if _, ok := app.listeners[address]; ok {
				WarningLogger.Printf("frontend %s listener %s error: already defined", name, address)
				continue
			}
			var l *accepter.Accepter
			if old != nil {
				l = old.listeners[address]
			}
			if l == nil {
				lis, e := net.Listen("tcp", address)
				if e != nil {
					WarningLogger.Printf("frontend %s listener %s error: %v", name, e)
					continue
				}
				l = &accepter.Accepter{
					Handler: &accepterHandler{},
				}
				go func() {
					if e := l.Serve(lis); e != nil {
						WarningLogger.Printf("listener %s serve error: %v", address, e)
					}
				}()
			}
			ah := l.Handler.(*accepterHandler)
			var tlsConfig *tls.Config
			if lItem.TLS {
				tlsConfig = &tls.Config{}
				var e error
				tlsConfig.Certificates, e = lItem.TLSParams.ToCerts()
				if e != nil {
					WarningLogger.Printf("listener %s tls error: %v", address, e)
				}
			}
			ah.Set(f, tlsConfig)
			app.listeners[address] = l
		}
	}

	// todo: old cleanup

	return
}
