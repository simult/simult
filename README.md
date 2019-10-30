# simult

simult is a smart load-balancer project.

## Installing

Install to linux instance:
```sh
wget -q -O- https://raw.githubusercontent.com/simult/simult/master/install.sh | bash
```

Install from source code to `$GOPATH/bin`:
```sh
go get -u github.com/simult/simult/...
```

## Configuration

The following table lists the configurable parameters of the simult-server and their default values.

| Parameter | Description | Default |
| - | - | - |
| global.promresetonreload | reset prometheus metrics next reload | false |
| global.rlimitnofile | number of allowed open files by system | `system_default` or 1024
| default.tlsparams | default tls parameters when using tls | {} |
| default.keepalivetimeout | default frontend http keep-alive timeout. zero or negative means unlimited | 65s |
| default.connecttimeout | default backend connect timeout. zero or negative means unlimited | 2s |
| frontends | all frontends | {} |
| frontends.`name` | a frontend | {} |
| frontends.`name`.maxconn | maximum number of frontend network connections. zero or negative means unlimited | 0 |
| frontends.`name`.timeout | frontend timeout. zero or negative means unlimited | 0 |
| frontends.`name`.keepalivetimeout | http keep-alive timeout. zero or negative means unlimited | `default.keepalivetimeout` |
| frontends.`name`.defaultbackend | sets backend when no route matched | "" |
| frontends.`name`.routes | all routes  | [] |
| frontends.`name`.routes.`index` | a route  | {} |
| frontends.`name`.routes.`index`.host | wildcarded host, eg "*.example.com" | "*" |
| frontends.`name`.routes.`index`.path | wildcarded path, eg "/example/*" | "*" |
| frontends.`name`.routes.`index`.backend | backend name route to | {} |
| frontends.`name`.routes.`index`.restrictions | all restrictions | [] |
| frontends.`name`.routes.`index`.restrictions.network | network CIDR IP, eg "127.0.0.0/8" | "" |
| frontends.`name`.routes.`index`.restrictions.path | wildcarded path, eg "/example/*" | "" |
| frontends.`name`.routes.`index`.restrictions.invert | invert restriction condition | false |
| frontends.`name`.routes.`index`.restrictions.andafter | AND operation with next restriction instead of OR | false |
| frontends.`name`.listeners | all listeners | [] |
| frontends.`name`.listeners.`index` | a listener | {} |
| frontends.`name`.listeners.`index`.address | listener bind address | "" |
| frontends.`name`.listeners.`index`.tls | use tls | false |
| frontends.`name`.listeners.`index`.tlsparams | tls parameters | {} |
| frontends.`name`.listeners.`index`.tlsparams.certpath | tls certificate directory or file | "." |
| frontends.`name`.listeners.`index`.tlsparams.keypath | tls key directory or file | "." |
| backends | all backends | {} |
| backends.`name` | a backend | {} |
| backends.`name`.maxconn | maximum number of total backend connections. zero or negative means unlimited | 0 |
| backends.`name`.servermaxconn | maximum number of active connections per backend server. zero or negative means unlimited | 0 |
| backends.`name`.timeout | backend timeout. zero or negative means unlimited | 0 |
| backends.`name`.connecttimeout | connect timeout. zero or negative means unlimited | `default.connecttimeout` |
| backends.`name`.reqheaders | override request headers | {} |
| backends.`name`.healthcheck | healthcheck name | "" |
| backends.`name`.mode | backend mode: roundrobin, leastconn, affinitykey | "roundrobin" |
| backends.`name`.affinitykey.source | "kind: key". kind: remoteip, realip, httpheader, httpcookie. key is, header for httpheader, cookie for httpcookie | "remoteip" |
| backends.`name`.affinitykey.maxservers | sets maximum number of servers to distribute traffic. zero value: one server, negative values: unlimited | 1 |
| backends.`name`.affinitykey.threshold | sets threshold to distribute traffic to next server. zero or negative means no threshold | 0 |
| backends.`name`.servers | backend servers | [] |
| backends.`name`.servers.`index` | backend server at this format: "url weight", eg "http://10.5.2.2 125". elements other than `url` are optional. weight is 1 by default, and must be in [0, 255] | "" |
| healthchecks | all healthchecks | {} |
| healthchecks.`name` | a healthcheck | {} |
| healthchecks.`name`.http | http healthcheck | {} |
| healthchecks.`name`.http.path | check path | "/" |
| healthchecks.`name`.http.host | check request http host | "" |
| healthchecks.`name`.http.interval | check interval | 10 |
| healthchecks.`name`.http.timeout | fail timeout | 5 |
| healthchecks.`name`.http.fall | fall threshold | 3 |
| healthchecks.`name`.http.rise | rise threshold | 2 |
| healthchecks.`name`.http.resp | expected response body | "" |

## Prometheus

simult-server has builtin prometheus exporter. Prometheus can access metrics using management address (defined with command-line arguments) and /metrics path.

Prometheus namespace can be defined with command-line argument `-prom-namespace`. Prometheus namespace is "simult" by default.

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

Prometheus metrics are form of `namespace_subsystem_name`. The following table lists prometheus metrics of the simult-server.

| Subsystem | Name | Type | Labels | Description |
| - | - | - | - | - |
| http_frontend | read_bytes | Counter | frontend, host, path, method, backend, server, code, listener | number of bytes read from remote client |
| http_frontend | write_bytes | Counter | frontend, host, path, method, backend, server, code, listener | number of bytes written to remote client |
| http_frontend | requests_total | Counter | frontend, host, path, method, backend, server, code, listener, error | number of requests processed |
| http_frontend | request_duration_seconds | Histogram | frontend, host, path, method, backend, server, code, listener | observer of request duration. it doesn't include errored requests |
| http_frontend | connections_total | Counter | frontend, listener | number of connections received |
| http_frontend | dropped_connections_total | Counter | frontend, listener | number of connections dropped after received them. this happens when maximum connection count exceeds |
| http_frontend | active_connections | Gauge | frontend, listener | active connection count |
| http_frontend | idle_connections | Gauge | frontend, listener | idle connection count |
| http_backend | read_bytes | Counter | backend, server, code, frontend, host, path, method, listener | number of bytes read from backend server |
| http_backend | write_bytes | Counter | backend, server, code, frontend, host, path, method, listener | number of bytes written to backend server |
| http_backend | time_to_first_byte_seconds | Histogram | backend, server, code, frontend, host, path, method, listener | observer of the time to first byte of backend server |
| http_backend | active_connections | Gauge | backend, server | active connection count of backend server |
| http_backend | idle_connections | Gauge | backend, server | idle connection count of backend server |
| http_backend | server_health | Gauge | backend, server | health status(0 or 1) of backend server |
