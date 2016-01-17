package main

import (
	"flag"
	"github.com/xlvector/lbcrawler/lb"
	"log"
	"net/http"
)

func main() {
	conf := flag.String("conf", "conf.json", "config file path")
	flag.Parse()
	srv := lb.NewLoadBalancer(*conf)
	log.Fatal(http.ListenAndServe("0.0.0.0:8070", srv))
}
