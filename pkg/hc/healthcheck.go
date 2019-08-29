package hc

type HealthCheck interface {
	Healthy() bool
	Check() <-chan bool
	Close()
}
