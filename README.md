# simult-server

## Configuration

| Parameter | Description | Default |
| - | - | - |
| global.promresetonreload | reset prometheus metrics next reload | false |
| global.rlimitnofile | number of allowed open files by system | `system_default`
| default.tlsparams | default tlsparams when using tls | {} |
| default.keepalivetimeout | default http keep-alive timeout. zero means or negative unlimited. | 0 |
| frontends | all frontends | {} |
| frontends.`name` | a frontend | {} |
| frontends.`name`.maxconn | maximum number of frontend network connections. zero or negative means unlimited. | 0 |
| frontends.`name`.timeout | http timeout. zero or negative means unlimited. | 0 |
| frontends.`name`.keepalivetimeout | http keep-alive timeout. zero or negative means unlimited. | 0 |
| frontends.`name`.defaultbackend | description. | {} |
| frontends.`name`.routes | all routes  | {} |
| frontends.`name`.routes.host | foo.bar.com | {} |
| frontends.`name`.routes.path | /example/* | {} |
| frontends.`name`.routes.backend | name of the route | {} |
| frontends.`name`.routes.restrictions | description | {} |
| frontends.`name`.routes.restrictions.network | description | {} |
| frontends.`name`.routes.restrictions.path | description | {} |
| frontends.`name`.routes.restrictions.invert | description | false |
| frontends.`name`.routes.restrictions.andafter | description | false |
| frontends.`name`.listeners.`index` | description | {} |
| frontends.`name`.listeners.address | description | {} |
| frontends.`name`.listeners.tls | description | false |
| frontends.`name`.listeners.tlsparams | description | {} |
| backends | all backends | {} |
| backends.`name` | a backend | {} |
| backends.`name`.maxconn | description | 0 |
| backends.`name`.servermaxconn | description | 0 |
| backends.`name`.timeout | description | 0 |
| backends.`name`.reqheaders | headers of incoming request |  |
| backends.`name`.healthcheck | description | {} |
| backends.`name`.mode | description | {} |
| backends.`name`.affinitykey.source | description | {} |
| backends.`name`.affinitykey.maxservers | description | 0 |
| backends.`name`.affinitykey.threshold | description | 0 |
| backends.`name`.servers.`index` | description | {} |
| healthchecks | all healthchecks | {} |
| healthchecks.`name` | a healthcheck | {} |
| healthchecks.`name`.http.path | description | {} |
| healthchecks.`name`.http.host | description | {} |
| healthchecks.`name`.http.interval | description | 0 |
| healthchecks.`name`.http.timeout | description | 0 |
| healthchecks.`name`.http.fall | description | 0 |
| healthchecks.`name`.http.rise | description | 0 |
| healthchecks.`name`.http.resp | description | {} |
