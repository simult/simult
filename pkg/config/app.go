package config

import (
	"net"
	"net/http"
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
		if name == "" || !nameRgx.MatchString(name) {
			err = errors.Errorf("healthcheck %q has not a valid name", name)
			return
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
			respBody := []byte(nil)
			if item.HTTP.Resp != "" {
				respBody = []byte(item.HTTP.Resp)
			}
			h = &hc.HTTPCheckOptions{
				Path:          item.HTTP.Path,
				HeaderHost:    item.HTTP.Host,
				Interval:      item.HTTP.Interval,
				Timeout:       item.HTTP.Timeout,
				FallThreshold: item.HTTP.Fall,
				RiseThreshold: item.HTTP.Rise,
				RespBody:      respBody,
				UserAgent:     "simult-server/0.1 healthcheck",
			}
		}
		an.healthChecks[name] = h
		infoLogger.Printf("healthcheck %q created", name)
	}

	for name, item := range cfg.Backends {
		if name == "" || !nameRgx.MatchString(name) {
			err = errors.Errorf("backend %q has not a valid name", name)
			return
		}
		if _, ok := an.backends[name]; ok {
			err = errors.Errorf("backend %q already defined", name)
			return
		}
		var opts lb.HTTPBackendOptions
		opts.Name = name
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
		infoLogger.Printf("backend %q created", name)
	}

	for name, item := range cfg.Frontends {
		if name == "" || !nameRgx.MatchString(name) {
			err = errors.Errorf("frontend %q has not a valid name", name)
			return
		}
		if _, ok := an.frontends[name]; ok {
			err = errors.Errorf("frontend %q already defined", name)
			return
		}
		var opts lb.HTTPFrontendOptions
		opts.Name = name
		if item.Timeout > 0 {
			opts.Timeout = item.Timeout
		}
		if item.KeepAliveTimeout > 0 {
			opts.KeepAliveTimeout = item.KeepAliveTimeout
		} else {
			opts.KeepAliveTimeout = cfg.Defaults.KeepAliveTimeout
		}
		if item.DefaultBackend != "" {
			opts.DefaultBackend = an.backends[item.DefaultBackend]
			if opts.DefaultBackend == nil {
				err = errors.Errorf("frontend %q defaultbackend %q not found", name, item.DefaultBackend)
				return
			}
		}
		opts.Routes = make([]lb.HTTPFrontendRoute, 0, len(item.Routes))
		for i := range item.Routes {
			route, newRoute := &item.Routes[i], &lb.HTTPFrontendRoute{}
			newRoute.Host = route.Host
			newRoute.Path = route.Path
			if route.Backend != "" {
				newRoute.Backend = an.backends[route.Backend]
				if newRoute.Backend == nil {
					err = errors.Errorf("frontend %q route error: backend %q not found", name, route.Backend)
					return
				}
			}
			newRoute.Restrictions = make([]lb.HTTPFrontendRestriction, 0, len(route.Restrictions))
			for j := range route.Restrictions {
				restriction, newRestriction := &route.Restrictions[j], &lb.HTTPFrontendRestriction{}
				if restriction.Network != "" {
					_, newRestriction.Network, err = net.ParseCIDR(restriction.Network)
					if err != nil {
						err = errors.Errorf("frontend %q route restriction network %q parse error: %v", name, restriction.Network, err)
						return
					}
				}
				newRestriction.Path = restriction.Path
				newRestriction.Invert = restriction.Invert
				newRestriction.AndAfter = restriction.AndAfter
				newRoute.Restrictions = append(newRoute.Restrictions, *newRestriction)
			}
			opts.Routes = append(opts.Routes, *newRoute)
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
		infoLogger.Printf("frontend %q created", name)

		for _, lItem := range item.Listeners {
			address := lItem.Address
			if address == "" {
				err = errors.Errorf("frontend %q listener %q has not a valid address", name, address)
				return
			}
			if _, ok := an.listeners[address]; ok {
				err = errors.Errorf("frontend %q listener %q already defined", name, address)
				return
			}
			var opts lb.ListenerOptions
			opts.Network = "tcp"
			opts.Address = address
			opts.Handler = fn
			if lItem.TLS {
				tlsParams := lItem.TLSParams
				if tlsParams == nil {
					tlsParams = cfg.Defaults.TLSParams
				}
				if tlsParams == nil {
					err = errors.Errorf("frontend %q listener %q needs TLSParams", name, address)
					return
				}
				opts.TLSConfig, err = tlsParams.Config()
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
			infoLogger.Printf("listener %q created", address)
		}
	}

	for name, item := range an.backends {
		item.Activate()
		infoLogger.Printf("backend %q activated", name)
	}
	for address, item := range an.listeners {
		item.Activate()
		infoLogger.Printf("listener %q activated", address)
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
