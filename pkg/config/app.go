package config

import (
	"net/http"
	"regexp"
	"sync"

	"github.com/pkg/errors"
	"github.com/simult/server/pkg/hc"
	"github.com/simult/server/pkg/lb"
)

type App struct {
	mu sync.Mutex

	listeners    map[string]*lb.Listener
	frontends    map[string]*lb.HTTPFrontend
	backends     map[string]*lb.HTTPBackend
	healthChecks map[string]interface{}
}

func NewApp(cfg *Config) (a *App, err error) {
	a, err = a.Fork(cfg)
	return
}

func (a *App) Fork(cfg *Config) (an *App, err error) {
	an = &App{
		listeners:    make(map[string]*lb.Listener),
		frontends:    make(map[string]*lb.HTTPFrontend),
		backends:     make(map[string]*lb.HTTPBackend),
		healthChecks: make(map[string]interface{}),
	}
	defer func() {
		if err == nil {
			return
		}
		an.Close()
		an = nil
	}()

	if a != nil {
		a.mu.Lock()
		defer a.mu.Unlock()
	}

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
			if h != nil {
				err = errors.Errorf("healthcheck %q another healthcheck defined", name)
				return
			}
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
		var opts lb.HTTPBackendOptions
		if item.Timeout > 0 {
			opts.Timeout = item.Timeout
		}
		opts.ReqHeader = make(http.Header, len(item.ReqHeaders))
		for k, v := range item.ReqHeaders {
			opts.ReqHeader.Set(k, v)
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

		var b, bn *lb.HTTPBackend
		if a != nil {
			b = a.backends[name]
		}
		bn, err = b.Fork(opts)
		if err != nil {
			err = errors.Errorf("backend %q error: %v", name, err)
			return
		}
		an.backends[name] = bn
	}

	for name, item := range cfg.Frontends {
		if _, ok := an.frontends[name]; ok {
			err = errors.Errorf("frontend %q already defined", name)
			return
		}
		var opts lb.HTTPFrontendOptions
		opts.Name = name
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
		opts.Routes = make([]lb.HTTPFrontendRoute, 0, len(item.Routes))
		for i := range item.Routes {
			r, t := &item.Routes[i], &lb.HTTPFrontendRoute{}
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

		var f, fn *lb.HTTPFrontend
		if a != nil {
			f = a.frontends[name]
		}
		fn, err = f.Fork(opts)
		if err != nil {
			err = errors.Errorf("frontend %q error: %v", name, err)
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
			var opts lb.ListenerOptions
			opts.Network = "tcp"
			opts.Address = address
			opts.Handler = fn
			if lItem.TLS {
				if lItem.TLSParams == nil {
					err = errors.Errorf("frontend %q listener %q needs TLSParams", name, address)
					return
				}
				opts.TLSConfig, err = lItem.TLSParams.Config()
				if err != nil {
					err = errors.Errorf("frontend %q listener %q tls error: %v", name, address, err)
					return
				}
			}

			var l, ln *lb.Listener
			if a != nil {
				l = a.listeners[address]
			}
			ln, err = l.Fork(opts)
			if err != nil {
				err = errors.Errorf("frontend %q listener %q error: %v", name, address, err)
				return
			}
			an.listeners[address] = ln
		}
	}

	for _, item := range an.backends {
		item.Activate()
	}
	for _, item := range an.listeners {
		item.Activate()
	}

	return
}

func (a *App) Close() {
	a.mu.Lock()
	for _, item := range a.listeners {
		item.Close()
	}
	for _, item := range a.frontends {
		item.Close()
	}
	for _, item := range a.backends {
		item.Close()
	}
	a.mu.Unlock()
}
