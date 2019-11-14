# simult

simult is an open-source HTTP load balancer and reverse proxy that easy configurable, full-featured, production proven and provides metrics.

## Features

* Full-featured HTTP load balancer and reverse proxy
* Easy configurable by single yaml file
* Routing by host and path
* Restrictions by host, path and network
* Distributing requests to backend servers by affinity-key; remoteip, realip, httpheader, httpcookie
* Monitoring friendly; includes internal prometheus exporter to provide metrics

## Installing

Install to linux instance:
```sh
wget -q -O- https://raw.githubusercontent.com/simult/simult/master/install.sh | bash
```

Install specific version to linux instance:
```sh
wget -q -O- https://raw.githubusercontent.com/simult/simult/master/install.sh | bash -s v0.1.11
```

Install from source code:
```sh
make clean install
```

## Usage

### Command line arguments

The following lines lists the command line arguments of the simult-server.

```
Usage of simult-server:
  -c string
    	config file (default "server.yaml")
  -debug
    	debug mode
  -m string
    	management address
  -prom-namespace string
    	prometheus exporter namespace (default "simult")
  -v int
    	verbose level [0, 65535]
```

### Management address

The management address serves prometheus metrics and debug end-points.

* **/metrics** prometheus metrics
* **/debug** pprof debug

## Configuration

The following table lists the configurable parameters of the simult-server and their default values.

| Parameter | Description | Default |
| - | - | - |
| global | global configurations | {} |
| global.promresetonreload | reset prometheus metrics next reload | false |
| global.rlimitnofile | number of allowed open files by system | `system_default` or 1024
| defaults | default values | {} |
| defaults.tlsparams | default tls parameters while using tls | {} |
| defaults.requesttimeout | frontend default http request timeout. zero or negative means unlimited | 5s |
| defaults.maxkeepalivereqs | frontend default maximum http keep-alive request count. negative means unlimited | 20 |
| defaults.keepalivetimeout | frontend default http keep-alive timeout. zero or negative means unlimited | 65s |
| defaults.connecttimeout | backend default connect timeout. zero or negative means unlimited | 2s |
| frontends | configuration of frontends | {} |
| frontends.`name` | a frontend | {} |
| frontends.`name`.maxconn | maximum number of total frontend connections. zero or negative means unlimited | 0 |
| frontends.`name`.maxidleconn | maximum number of frontend idle connections. zero or negative means unlimited | 0 |
| frontends.`name`.timeout | frontend timeout. zero or negative means unlimited | 0 |
| frontends.`name`.requesttimeout | http request timeout. zero or negative means unlimited | `defaults.requesttimeout` |
| frontends.`name`.maxkeepalivereqs | maximum http keep-alive request count. negative means unlimited | `defaults.maxkeepalivereqs` |
| frontends.`name`.keepalivetimeout | http keep-alive timeout. zero or negative means unlimited | `defaults.keepalivetimeout` |
| frontends.`name`.defaultbackend | default backend name when no route matched | "" |
| frontends.`name`.defaultbackup | backup backend name of default backend | "" |
| frontends.`name`.routes | frontend routes  | [] |
| frontends.`name`.routes.`index` | a route  | {} |
| frontends.`name`.routes.`index`.host | wildcarded host, eg "*.example.com" | "*" |
| frontends.`name`.routes.`index`.path | wildcarded path, eg "/example/*" | "*" |
| frontends.`name`.routes.`index`.backend | backend name to route to | "" |
| frontends.`name`.routes.`index`.backup | backup backend of backend | "" |
| frontends.`name`.routes.`index`.restrictions | route restrictions | [] |
| frontends.`name`.routes.`index`.restrictions.network | network CIDR IP, eg "127.0.0.0/8" | "" |
| frontends.`name`.routes.`index`.restrictions.path | wildcarded path, eg "/example/*" | "" |
| frontends.`name`.routes.`index`.restrictions.invert | invert restriction condition | false |
| frontends.`name`.routes.`index`.restrictions.andafter | AND operation with next restriction instead of OR | false |
| frontends.`name`.listeners | frontend listeners | [] |
| frontends.`name`.listeners.`index` | a listener | {} |
| frontends.`name`.listeners.`index`.address | listener bind address | "" |
| frontends.`name`.listeners.`index`.tls | use tls | false |
| frontends.`name`.listeners.`index`.tlsparams | tls parameters | `defaults.tlsparams` |
| frontends.`name`.listeners.`index`.tlsparams.certpath | tls certificate directory or file | "." |
| frontends.`name`.listeners.`index`.tlsparams.keypath | tls key directory or file | "." |
| backends | configuration of backends | {} |
| backends.`name` | a backend | {} |
| backends.`name`.maxconn | maximum number of active backend connections. zero or negative means unlimited | 0 |
| backends.`name`.servermaxconn | maximum number of active connections per backend server. zero or negative means unlimited | 0 |
| backends.`name`.servermaxidleconn | maximum number of idle connections per backend server. zero or negative means unlimited | 0 |
| backends.`name`.timeout | backend timeout. zero or negative means unlimited | 0 |
| backends.`name`.connecttimeout | connect timeout. zero or negative means unlimited | `defaults.connecttimeout` |
| backends.`name`.reqheaders | override request headers | {} |
| backends.`name`.serverhashsecret | hash secret for X-Server-Name | "" |
| backends.`name`.healthcheck | healthcheck name | "" |
| backends.`name`.mode | backend mode: roundrobin, leastconn, affinitykey | "roundrobin" |
| backends.`name`.affinitykey | affinity key parameters | {} |
| backends.`name`.affinitykey.source | "kind: key". kind: remoteip, realip, httpheader, httpcookie. key is, header name for httpheader, cookie name for httpcookie | "remoteip" |
| backends.`name`.affinitykey.maxservers | sets maximum number of servers to distribute traffic. zero value: one server, negative values: unlimited | 1 |
| backends.`name`.affinitykey.threshold | sets threshold to distribute traffic to next server. zero or negative means no threshold | 0 |
| backends.`name`.overrideerrors | complete http response for overriding 502, 503, 504 errors | "" |
| backends.`name`.servers | backend servers | [] |
| backends.`name`.servers.`index` | backend server at this format: "url weight", eg "http://10.5.2.2 125". elements other than `url` are optional. weight is 1 by default, and must be in [0, 255] | "" |
| healthchecks | configuration of healthchecks | {} |
| healthchecks.`name` | a healthcheck | {} |
| healthchecks.`name`.http | http healthcheck | {} |
| healthchecks.`name`.http.path | check path | "/" |
| healthchecks.`name`.http.host | check request http host | "" |
| healthchecks.`name`.http.interval | check interval | 10s |
| healthchecks.`name`.http.timeout | fail timeout | 5s |
| healthchecks.`name`.http.fall | fall threshold | 3 |
| healthchecks.`name`.http.rise | rise threshold | 2 |
| healthchecks.`name`.http.resp | expected response body | "" |

## Prometheus

simult-server has builtin prometheus exporter. Prometheus can access metrics using management address (defined with command-line arguments) and /metrics path.

Prometheus namespace can be defined with command-line argument `-prom-namespace`. The namespace is "simult" by default.

### Labels

All labels of all metrics define same thing if nothing else is specified.

| Name | Description |
| - | - |
| frontend | frontend name |
| host | matched frontend route host |
| path | matched frontend route path |
| method | request method |
| backend | backend name |
| server | backend server |
| code | response status code |
| listener | listener address |
| error | error message |

### Metrics

Prometheus metrics are form of `namespace_subsystem_name`. The namespace is "simult" by default.

The following table lists prometheus metrics of the simult.

| Subsystem | Name | Type | Labels | Description |
| - | - | - | - | - |
| http_frontend | read_bytes | Counter | frontend, host, path, method, backend, server, code, listener | number of bytes read from remote client |
| http_frontend | write_bytes | Counter | frontend, host, path, method, backend, server, code, listener | number of bytes written to remote client |
| http_frontend | requests_total | Counter | frontend, host, path, method, backend, server, code, listener, error | number of requests processed |
| http_frontend | request_duration_seconds | Histogram | frontend, host, path, method, backend, server, code, listener | observer of request duration. it doesn't include errored requests |
| http_frontend | connections_total | Counter | frontend, listener | number of connections received |
| http_frontend | active_connections | Gauge | frontend, listener | active connection count |
| http_frontend | idle_connections | Gauge | frontend, listener | idle connection count |
| http_frontend | waiting_connections | Gauge | frontend, listener | waiting connection count |
| http_backend | read_bytes | Counter | backend, server, code, frontend, host, path, method, listener | number of bytes read from backend server |
| http_backend | write_bytes | Counter | backend, server, code, frontend, host, path, method, listener | number of bytes written to backend server |
| http_backend | time_to_first_byte_seconds | Histogram | backend, server, code, frontend, host, path, method, listener | observer of the time to first byte of backend server |
| http_backend | active_connections | Gauge | backend, server | active connection count of backend server |
| http_backend | idle_connections | Gauge | backend, server | idle connection count of backend server |
| http_backend | server_health | Gauge | backend, server | health status(0 or 1) of backend server |
