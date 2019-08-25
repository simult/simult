package main

import (
	"log"

	accepter "github.com/orkunkaraduman/go-accepter"
	"github.com/simult/server/pkg/httplb"
)

func main() {
	b := httplb.NewBackend()
	defer b.Close()
	addr := "https://www.google.com.tr"
	//addr := "http://localhost:1236"
	if err := b.Set([]string{addr}); err != nil {
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
