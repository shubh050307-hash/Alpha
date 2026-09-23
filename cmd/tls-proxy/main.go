// tls-proxy — Tiny Go TLS terminator that proxies to Caddy on :80.
// Solves Caddy v2.11.3 TLS stack bug while preserving all Caddy features.
package main

import (
	"crypto/tls"
	"flag"
	"log"
	"net/http"
	"net/http/httputil"
	"net/url"
	"time"
)

func main() {
	listen := flag.String("listen", ":443", "TLS listen address")
	certFile := flag.String("cert", "/etc/caddy/certs/alphanetx.crt", "TLS cert")
	keyFile := flag.String("key", "/etc/caddy/certs/alphanetx.key", "TLS key")
	flag.Parse()

	cert, err := tls.LoadX509KeyPair(*certFile, *keyFile)
	if err != nil {
		log.Fatalf("load cert: %v", err)
	}

	tlsCfg := &tls.Config{
		Certificates: []tls.Certificate{cert},
		MinVersion:   tls.VersionTLS12,
	}

	backend, _ := url.Parse("http://localhost:80")
	proxy := httputil.NewSingleHostReverseProxy(backend)
	proxy.Transport = &http.Transport{
		MaxIdleConns:    100,
		IdleConnTimeout: 90 * time.Second,
	}

	server := &http.Server{
		Addr:         *listen,
		TLSConfig:    tlsCfg,
		Handler:      proxy,
		ReadTimeout:  10 * time.Second,
		WriteTimeout: 30 * time.Second,
		IdleTimeout:  120 * time.Second,
	}

	log.Printf("🔐 TLS proxy on %s → localhost:80", *listen)
	if err := server.ListenAndServeTLS("", ""); err != nil {
		log.Fatalf("serve: %v", err)
	}
}
