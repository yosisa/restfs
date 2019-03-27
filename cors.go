package main

import (
	"flag"
	"log"
	"net/http"
	"strings"

	"github.com/rs/cors"
)

var corsOrigins = flag.String("cors-origins", "", "CORS origins (comma-separated)")

var corsHeaders []string

func addCORSHeaders(names ...string) {
	corsHeaders = append(corsHeaders, names...)
}

func init() {
	registerMiddleware(10, func(h http.Handler) http.Handler {
		if *corsOrigins == "" {
			return h
		}

		log.Printf("CORS Origins: %s", *corsOrigins)
		items := strings.Split(*corsOrigins, ",")
		return CORS(h, items...)
	})
}

func CORS(h http.Handler, origins ...string) http.Handler {
	c := cors.New(cors.Options{
		AllowedOrigins: origins,
		AllowedMethods: []string{"GET", "PUT", "DELETE"},
		AllowedHeaders: corsHeaders,
		MaxAge:         600,
	})
	return c.Handler(h)
}
