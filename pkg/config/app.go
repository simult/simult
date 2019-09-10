package config

import (
	"crypto/tls"
	"net"
	"net/http"
	"regexp"
	"sync"

	accepter "github.com/orkunkaraduman/go-accepter"
	"github.com/pkg/errors"
	"github.com/simult/server/pkg/hc"
	"github.com/simult/server/pkg/httplb"
)

type App struct {
	mu sync.Mutex

	listeners    map[string]*accepter.Accepter
	frontends    map[string]*httplb.Frontend
	backends     map[string]*httplb.Backend
	healthChecks map[string]interface{}
}

func NewApp(cfg *Config, a *App) (an *App, err error) {
	if a != nil {
		a.mu.Lock()
		defer a.mu.Unlock()
	}

	an = &App{
		listeners:    make(map[string]*accepter.Accepter),
		frontends:    make(map[string]*httplb.Frontend),
		backends:     make(map[string]*httplb.Backend),
		healthChecks: make(map[string]interface{}),
	}
	defer func() {
		if err == nil {
			return
		}
		if a != nil {
			for address := range an.listeners {
				if _, ok := a.listeners[address]; ok {
					delete(an.listeners, address)
				}
			}
		}
		an.close()
		an = nil
	}()

	type HandlerTLSConfig struct {
		Handler   accepter.Handler
		TLSConfig *tls.Config
	}
	hts := make(map[string]*HandlerTLSConfig)

	for name, item := range cfg.HealthChecks {
		if name == "" {
			continue
		}
		if _, ok := an.healthChecks[name]; ok {
			err = errors.Errorf("healthcheck %q already defined", name)
			return
		}
		var h interface{}
		if item.HTTP != nil {
			h = item.HTTP
		}
		an.healthChecks[name] = h
	}

	for name, item := range cfg.Backends {
		if name == "" {
			continue
		}
		if _, ok := an.backends[name]; ok {
			err = errors.Errorf("backend %q already defined", name)
			return
		}
		var opts httplb.BackendOptions
		if item.Timeout > 0 {
			opts.Timeout = item.Timeout
		}
		opts.ReqHeader = make(http.Header, len(item.ReqHeaders))
		for hk, hv := range item.ReqHeaders {
			opts.ReqHeader.Set(hk, hv)
		}
		if item.HealthCheck != "" {
			h, ok := an.healthChecks[item.HealthCheck]
			if !ok {
				err = errors.Errorf("backend %q healthcheck %q not found", name, item.HealthCheck)
				return
			}
			switch h.(type) {
			case *hc.HTTPCheckOptions:
				opts.HealthCheckHTTPOpts = h.(*hc.HTTPCheckOptions)
			}
		}
		opts.Servers = item.Servers

		var b, bn *httplb.Backend
		if a != nil {
			b = a.backends[name]
		}
		bn, err = b.Fork(opts)
		if err != nil {
			err = errors.Errorf("backend %q config error: %v", name, err)
			return
		}
		an.backends[name] = bn
	}

	for name, item := range cfg.Frontends {
		if _, ok := an.frontends[name]; ok {
			err = errors.Errorf("frontend %q already defined", name)
			return
		}
		var opts httplb.FrontendOptions
		if item.Timeout > 0 {
			opts.Timeout = item.Timeout
		}
		if item.DefaultBackend == "" {
			err = errors.Errorf("frontend %q defaultbackend not defined", name)
			return
		}
		b := an.backends[item.DefaultBackend]
		if b == nil {
			err = errors.Errorf("frontend %q defaultbackend %q not found", name, item.DefaultBackend)
			return
		}
		opts.DefaultBackend = b
		opts.Routes = make([]httplb.FrontendRoute, 0, len(item.Routes))
		for i := range item.Routes {
			r, t := &item.Routes[i], &httplb.FrontendRoute{}
			t.Host, err = regexp.Compile(r.Host)
			if err != nil {
				err = errors.Errorf("frontend %q route error: host %q error: %v", name, r.Host, err)
				return
			}
			t.Path, err = regexp.Compile(r.Path)
			if err != nil {
				err = errors.Errorf("frontend %q route error: path %q error: %v", name, r.Path, err)
				return
			}
			t.Backend = an.backends[r.Backend]
			if t.Backend == nil {
				err = errors.Errorf("frontend %q route error: backend %q not found", name, r.Backend)
				return
			}
			opts.Routes = append(opts.Routes, *t)
		}

		var f, fn *httplb.Frontend
		if a != nil {
			f = a.frontends[name]
		}
		fn, err = f.Fork(opts)
		if err != nil {
			err = errors.Errorf("frontend %q config error: %v", name, err)
			return
		}
		an.frontends[name] = fn

		for _, lItem := range item.Listeners {
			if lItem == nil || lItem.Address == "" {
				continue
			}
			address := lItem.Address
			if _, ok := an.listeners[address]; ok {
				err = errors.Errorf("frontend %q listener %q already defined", name, address)
				return
			}
			var l *accepter.Accepter
			if a != nil {
				l = a.listeners[address]
			}
			if l == nil {
				var lis net.Listener
				lis, err = net.Listen("tcp", address)
				if err != nil {
					err = errors.Errorf("frontend %q listener %q error: %v", name, address, err)
					return
				}
				l = &accepter.Accepter{
					Handler: &accepterHandler{},
				}
				go func() {
					if e := l.Serve(lis); e != nil {
						errorLogger.Printf("listener %q serve error: %v", address, e)
					}
				}()
			}
			an.listeners[address] = l

			var tlsConfig *tls.Config
			if lItem.TLS {
				if lItem.TLSParams == nil {
					err = errors.Errorf("frontend %q listener %q needs TLSParams", name, address)
					return
				}
				tlsConfig, err = lItem.TLSParams.Config()
				if err != nil {
					err = errors.Errorf("frontend %q listener %q tls error: %v", name, address, err)
					return
				}
			}
			hts[address] = &HandlerTLSConfig{
				Handler:   fn,
				TLSConfig: tlsConfig,
			}
		}
	}

	for address, ht := range hts {
		an.listeners[address].Handler.(*accepterHandler).Set(ht.Handler, ht.TLSConfig)
	}

	if a != nil {
		for address := range a.listeners {
			if _, ok := an.listeners[address]; ok {
				delete(a.listeners, address)
			}
		}
		a.close()
	}

	return
}

func (a *App) close() {
	for _, item := range a.listeners {
		item.Close()
	}
	for _, item := range a.frontends {
		item.Close()
	}
	for _, item := range a.backends {
		item.Close()
	}
}

func (a *App) Close() {
	a.mu.Lock()
	a.close()
	a.mu.Unlock()
}