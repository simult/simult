package hc

import (
	"fmt"
	"log"
	"net/http"
	"testing"
	"time"
)

func runSimpleHTTPServer() {
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/healthcheck":
			w.WriteHeader(200)
			if (time.Now().Unix()/5)%2 == 0 {
				fmt.Fprint(w, "UP")
			} else {
				fmt.Fprint(w, "DOWN")
			}
		default:
			w.WriteHeader(404)
		}
	})
	log.Fatal(http.ListenAndServe("127.0.0.1:4040", handler))
}

func TestHTTPCheck(t *testing.T) {
	go runSimpleHTTPServer()
	opts := HTTPCheckOptions{"/healthcheck", "", 1 * time.Second, 1 * time.Second, 3, 2, []byte("UP")}
	h, _ := NewHTTPCheck("http://127.0.0.1:4040", opts)
	defer h.Close()
	for i := 0; i < 5; i++ {
		r := <-h.Check()
		log.Printf("Healthcheck: %v\n", r)
		if r != (((time.Now().Unix()-1)/5)%2 == 0) {
			t.FailNow()
		}
	}
}
