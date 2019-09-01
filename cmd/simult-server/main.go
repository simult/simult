package main

import (
	"fmt"
	"log"
	"os"
	"time"

	accepter "github.com/orkunkaraduman/go-accepter"
	"github.com/simult/server/pkg/hc"
	"github.com/simult/server/pkg/httplb"
)

func testHC() {
	hOpts := hc.HTTPCheckOptions{"/abc", "", 1 * time.Second, 1 * time.Second, 3, 2, nil}
	h, _ := hc.NewHTTPCheck("https://www.yahoo.com", hOpts)
	fmt.Println(<-h.Check())
	os.Exit(0)
}

func main() {
	httplb.DebugLogger = log.New(os.Stdout, "DEBUG ", log.LstdFlags)
	//testHC()
	b := httplb.NewBackend()
	defer b.Close()
	hOpts := hc.HTTPCheckOptions{"/", "", 1 * time.Second, 1 * time.Second, 3, 2, nil}
	bOpts := httplb.BackendOptions{
		HealthCheckOpts: hOpts,
		Servers:         []string{"https://www.google.com.tr", "https://www.yahoo.com"},
	}
	fmt.Println(1)
	if err := b.SetOpts(bOpts); err != nil {
		log.Fatal(err)
	}
	fmt.Println(2)
	l := httplb.NewLoadBalancer(httplb.LoadBalancerOptions{
		Timeout:        0,
		DefaultBackend: b,
	})
	a := &accepter.Accepter{
		Handler: l,
	}
	fmt.Println("ready")
	log.Fatal(a.TCPListenAndServe(":1234"))
}
