package config

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/goinsane/xlog"
	"github.com/simult/simult/pkg/hc"
	"github.com/simult/simult/pkg/lb"
)

// App is an organizer of all load-balancing structures
type App struct {
	mu sync.Mutex

	listeners    map[string]*lb.Listener
	frontends    map[string]*lb.HTTPFrontend
	backends     map[string]*lb.HTTPBackend
	healthChecks map[string]interface{}
}

// NewApp creates an App from given Config
func NewApp(cfg *Config) (a *App, err error) {
	a, err = a.Fork(cfg)
	return
}

// Fork forkes an App and its own load-balancing members, and activates them
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
		an.Close(nil)
		an = nil
	}()

	if a != nil {
		a.mu.Lock()
		defer a.mu.Unlock()
	}

	for name, item := range cfg.HealthChecks {
		if name == "" || !nameRgx.MatchString(name) {
			err = fmt.Errorf("healthcheck %q has not a valid name", name)
			return
		}
		if _, ok := an.healthChecks[name]; ok {
			err = fmt.Errorf("healthcheck %q already defined", name)
			return
		}
		var h interface{}
		if item.HTTP != nil {
			if h != nil {
				err = fmt.Errorf("healthcheck %q another healthcheck defined", name)
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
				UserAgent:     "simult/0.1 healthcheck",
			}
		}
		an.healthChecks[name] = h
		xlog.V(1).Infof("healthcheck %q created", name)
	}

	for name, item := range cfg.Backends {
		if name == "" || !nameRgx.MatchString(name) {
			err = fmt.Errorf("backend %q has not a valid name", name)
			return
		}
		if _, ok := an.backends[name]; ok {
			err = fmt.Errorf("backend %q already defined", name)
			return
		}
		var opts lb.HTTPBackendOptions
		opts.Name = name
		if item.MaxConn > 0 {
			opts.MaxConn = item.MaxConn
		}
		if item.ServerMaxConn > 0 {
			opts.ServerMaxConn = item.ServerMaxConn
		}
		if item.Timeout > 0 {
			opts.Timeout = item.Timeout
		}
		if item.ConnectTimeout > 0 {
			opts.ConnectTimeout = item.ConnectTimeout
		} else {
			if cfg.Defaults.ConnectTimeout > 0 {
				opts.ConnectTimeout = cfg.Defaults.ConnectTimeout
			} else {
				opts.ConnectTimeout = 2 * time.Second
			}
		}
		opts.ReqHeader = make(http.Header, len(item.ReqHeaders))
		for k, v := range item.ReqHeaders {
			opts.ReqHeader.Set(k, v)
		}
		if item.HealthCheck != "" {
			h, ok := an.healthChecks[item.HealthCheck]
			if !ok {
				err = fmt.Errorf("backend %q healthcheck %q not found", name, item.HealthCheck)
				return
			}
			switch h.(type) {
			case *hc.HTTPCheckOptions:
				opts.HealthCheckHTTPOpts = h.(*hc.HTTPCheckOptions)
			}
		}
		if item.Mode != "" {
			switch item.Mode {
			case "roundrobin":
				opts.Mode = lb.HTTPBackendModeRoundRobin
			case "leastconn":
				opts.Mode = lb.HTTPBackendModeLeastConn
			case "affinitykey":
				opts.Mode = lb.HTTPBackendModeAffinityKey
			default:
				err = fmt.Errorf("backend %q mode %q unknown", name, item.Mode)
				return
			}
		}
		if item.AffinityKey.Source != "" {
			line := item.AffinityKey.Source
			idx := strings.IndexByte(line, ':')
			kind, key := "", ""
			if idx < 0 {
				kind = string(line)
			} else {
				kind = string(line[:idx])
				key = string(strings.TrimLeft(line[idx+1:], " "))
			}
			switch kind {
			case "remoteip":
				opts.AffinityKey.Kind = lb.HTTPBackendAffinityKeyKindRemoteIP
			case "realip":
				opts.AffinityKey.Kind = lb.HTTPBackendAffinityKeyKindRealIP
			case "httpheader":
				opts.AffinityKey.Kind = lb.HTTPBackendAffinityKeyKindHTTPHeader
			case "httpcookie":
				opts.AffinityKey.Kind = lb.HTTPBackendAffinityKeyKindHTTPCookie
			default:
				err = fmt.Errorf("backend %q affinity key kind %q unknown", name, kind)
				return
			}
			opts.AffinityKey.Key = key
			opts.AffinityKey.MaxServers = item.AffinityKey.MaxServers
			opts.AffinityKey.Threshold = item.AffinityKey.Threshold
		}
		opts.OverrideErrors = item.OverrideErrors
		opts.Servers = item.Servers

		var b, bn *lb.HTTPBackend
		if a != nil {
			b = a.backends[name]
		}
		bn, err = b.Fork(opts)
		if err != nil {
			err = fmt.Errorf("backend %q error: %w", name, err)
			return
		}
		an.backends[name] = bn
		xlog.V(1).Infof("backend %q created", name)
	}

	for name, item := range cfg.Frontends {
		if name == "" || !nameRgx.MatchString(name) {
			err = fmt.Errorf("frontend %q has not a valid name", name)
			return
		}
		if _, ok := an.frontends[name]; ok {
			err = fmt.Errorf("frontend %q already defined", name)
			return
		}
		var opts lb.HTTPFrontendOptions
		opts.Name = name
		if item.MaxConn > 0 {
			opts.MaxConn = item.MaxConn
		}
		if item.Timeout > 0 {
			opts.Timeout = item.Timeout
		}
		if item.RequestTimeout > 0 {
			opts.RequestTimeout = item.RequestTimeout
		} else {
			if cfg.Defaults.RequestTimeout > 0 {
				opts.RequestTimeout = cfg.Defaults.RequestTimeout
			} else {
				opts.RequestTimeout = 5 * time.Second
			}
		}
		if item.KeepAliveTimeout > 0 {
			opts.KeepAliveTimeout = item.KeepAliveTimeout
		} else {
			if cfg.Defaults.KeepAliveTimeout > 0 {
				opts.KeepAliveTimeout = cfg.Defaults.KeepAliveTimeout
			} else {
				opts.KeepAliveTimeout = 65 * time.Second
			}
		}
		if item.DefaultBackend != "" {
			opts.DefaultBackend = an.backends[item.DefaultBackend]
			if opts.DefaultBackend == nil {
				err = fmt.Errorf("frontend %q defaultbackend %q not found", name, item.DefaultBackend)
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
					err = fmt.Errorf("frontend %q route error: backend %q not found", name, route.Backend)
					return
				}
			}
			newRoute.Restrictions = make([]lb.HTTPFrontendRestriction, 0, len(route.Restrictions))
			for j := range route.Restrictions {
				restriction, newRestriction := &route.Restrictions[j], &lb.HTTPFrontendRestriction{}
				if restriction.Network != "" {
					_, newRestriction.Network, err = net.ParseCIDR(restriction.Network)
					if err != nil {
						err = fmt.Errorf("frontend %q route restriction network %q parse error: %w", name, restriction.Network, err)
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
			err = fmt.Errorf("frontend %q error: %w", name, err)
			return
		}
		an.frontends[name] = fn
		xlog.V(1).Infof("frontend %q created", name)

		for _, lItem := range item.Listeners {
			lName := lItem.Address
			if lName == "" {
				err = fmt.Errorf("frontend %q listener %q has not a valid address", name, lName)
				return
			}
			if _, ok := an.listeners[lName]; ok {
				err = fmt.Errorf("frontend %q listener %q already defined", name, lName)
				return
			}
			var opts lb.ListenerOptions
			opts.Name = lName
			opts.Network = "tcp"
			opts.Address = lItem.Address
			opts.Fe = fn
			if lItem.TLS {
				tlsParams := lItem.TLSParams
				if tlsParams == nil {
					tlsParams = cfg.Defaults.TLSParams
				}
				if tlsParams == nil {
					err = fmt.Errorf("frontend %q listener %q needs TLSParams", name, lName)
					return
				}
				opts.TLSConfig, err = tlsParams.Config()
				if err != nil {
					err = fmt.Errorf("frontend %q listener %q tls error: %w", name, lName, err)
					return
				}
			}

			var l, ln *lb.Listener
			if a != nil {
				l = a.listeners[lName]
			}
			ln, err = l.Fork(opts)
			if err != nil {
				err = fmt.Errorf("frontend %q listener %q error: %w", name, lName, err)
				return
			}
			an.listeners[lName] = ln
			xlog.V(1).Infof("listener %q created", lName)
		}
	}

	for name, item := range an.backends {
		item.Activate()
		xlog.V(1).Infof("backend %q activated", name)
	}
	for name, item := range an.listeners {
		item.Activate()
		xlog.V(1).Infof("listener %q activated", name)
	}

	return
}

// Close closes the App and its own load-balancing structures
func (a *App) Close(ctx context.Context) {
	a.mu.Lock()
	for _, item := range a.listeners {
		item.Close(ctx)
	}
	for _, item := range a.frontends {
		item.Close()
	}
	for _, item := range a.backends {
		item.Close()
	}
	a.mu.Unlock()
}
