package main

import (
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/yosisa/webutil"
)

var prometheusAddr = flag.String("prometheus", "", "Listen address for prometheus")

func init() {
	middlewares = append(middlewares, &middleware{
		priority: 2,
		wrap: func(h http.Handler) http.Handler {
			if *prometheusAddr == "" {
				return h
			}

			log.Printf("Prometheus stats enabled at %s", *prometheusAddr)
			go listenAndServePrometheusHandler(*prometheusAddr)
			return withPrometheus(h)
		},
	})
}

func withPrometheus(h http.Handler) http.Handler {
	reqCnt := prometheus.NewCounterVec(prometheus.CounterOpts{
		Namespace: "restfs",
		Subsystem: "http",
		Name:      "requests_total",
		Help:      "Total number of HTTP requests made.",
	}, []string{"method", "code"})

	opts := prometheus.SummaryOpts{
		Namespace: "restfs",
		Subsystem: "http",
	}

	opts.Name = "request_duration_seconds"
	opts.Help = "The HTTP request latencies in seconds."
	reqDur := prometheus.NewSummaryVec(opts, []string{"method"})

	opts.Name = "request_size_bytes"
	opts.Help = "The HTTP request sizes in bytes."
	reqSz := prometheus.NewSummaryVec(opts, []string{"method"})

	opts.Name = "response_size_bytes"
	opts.Help = "The HTTP response sizes in bytes."
	resSz := prometheus.NewSummaryVec(opts, []string{"method"})

	prometheus.MustRegister(reqCnt)
	prometheus.MustRegister(reqDur)
	prometheus.MustRegister(reqSz)
	prometheus.MustRegister(resSz)

	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		start := time.Now()

		var body *loggedBody
		reqsz := req.ContentLength
		if reqsz == -1 {
			body = &loggedBody{ReadCloser: req.Body}
			if _, ok := req.Body.(io.WriterTo); ok {
				req.Body = &loggedBodyWithWriteTo{body}
			} else {
				req.Body = body
			}
		}
		lw := webutil.WrapResponseWriter(w)
		h.ServeHTTP(lw, req)

		elapsed := float64(time.Since(start)) / float64(time.Second)
		method := lowerMethod(req.Method)
		status := codeToStr(lw.Status)
		if reqsz == -1 {
			reqsz = body.Size
			fmt.Println(reqsz)
		}

		reqCnt.WithLabelValues(method, status).Inc()
		reqDur.WithLabelValues(method).Observe(elapsed)
		reqSz.WithLabelValues(method).Observe(float64(reqsz))
		resSz.WithLabelValues(method).Observe(float64(lw.Size))
	})
}

func listenAndServePrometheusHandler(addr string) {
	http.ListenAndServe(addr, prometheus.Handler())
}

type loggedBody struct {
	io.ReadCloser
	Size int64
}

func (l *loggedBody) Read(p []byte) (n int, err error) {
	n, err = l.ReadCloser.Read(p)
	l.Size += int64(n)
	return
}

type loggedBodyWithWriteTo struct {
	*loggedBody
}

func (l *loggedBodyWithWriteTo) WriterTo(w io.Writer) (n int64, err error) {
	n, err = l.ReadCloser.(io.WriterTo).WriteTo(w)
	l.Size += n
	return
}

func lowerMethod(method string) string {
	switch method {
	case "GET", "get":
		return "get"
	case "PUT", "put":
		return "put"
	case "DELETE", "delete":
		return "delete"
	case "POST", "post":
		return "post"
	case "HEAD", "head":
		return "head"
	case "OPTIONS", "options":
		return "options"
	}
	return strings.ToLower(method)
}

func codeToStr(code int) string {
	switch code {
	case 200:
		return "200"
	case 400:
		return "400"
	case 404:
		return "404"
	case 500:
		return "500"
	}
	return strconv.Itoa(code)
}
