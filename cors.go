package main

import (
	"flag"
	"log"
	"net/http"
	"strings"

	"github.com/rs/cors"
)

var corsOrigins = flag.String("cors-origins", "", "CORS origins (comma-separated)")

func init() {
	middlewares = append(middlewares, &middleware{
		priority: 10,
		wrap: func(h http.Handler) http.Handler {
			if *corsOrigins == "" {
				return h
			}

			log.Printf("CORS Origins: %s", *corsOrigins)
			items := strings.Split(*corsOrigins, ",")
			return CORS(h, items...)
		},
	})
}

func CORS(h http.Handler, origins ...string) http.Handler {
	c := cors.New(cors.Options{
		AllowedOrigins: origins,
		AllowedMethods: []string{"GET", "PUT", "DELETE"},
		MaxAge:         600,
	})
	return c.Handler(h)
}
