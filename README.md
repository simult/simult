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
| frontends.`name`.keepalivetimeout | http keep-alive timeout. zero or negative means unlimited. | 0 |
