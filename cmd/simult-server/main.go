package main

import (
	"log"
	"time"

	accepter "github.com/orkunkaraduman/go-accepter"
	"github.com/simult/server/pkg/hc"
	"github.com/simult/server/pkg/httplb"
)

func main() {
	b := httplb.NewBackend()
	defer b.Close()
	hOpts := hc.HTTPOptions{"/", "", 1 * time.Second, 1 * time.Second, 3, 2, nil}
	bOpts := httplb.BackendOptions{
		HealthCheckOpts: hOpts,
		Servers:         []string{"https://www.google.com.tr", "https://www.yahoo.com"},
	}
	if err := b.SetOpts(bOpts); err != nil {
		log.Fatal(err)
	}
	l := httplb.NewLoadBalancer(httplb.LoadBalancerOptions{
		Timeout:        0,
		DefaultBackend: b,
	})
	a := &accepter.Accepter{
		Handler: l,
	}
	log.Fatal(a.TCPListenAndServe(":1234"))
}
