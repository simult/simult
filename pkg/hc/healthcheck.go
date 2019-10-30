package hc

// HealthCheck is general interface of all health-check implementations
type HealthCheck interface {
	Healthy() bool
	Check() <-chan bool
	Close()
}
