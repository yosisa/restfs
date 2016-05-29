package main

import (
	"flag"
	"io"
	"net/http"
	"os"
	"path"
	"strconv"
	"time"

	"github.com/tylerb/graceful"
	"github.com/yosisa/webutil"
)

var (
	dataDir         = flag.String("data-dir", "./data", "Data directory")
	listen          = flag.String("listen", ":8000", "Listen address")
	gracefulTimeout = flag.Duration("graceful-timeout", 10*time.Second, "Wait until force shutdown")
)

type restfs struct {
	dir string
}

func (c *restfs) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	fullpath := path.Join(c.dir, r.URL.Path)
	var err error
	switch r.Method {
	case "GET":
		http.ServeFile(w, r, fullpath)
	case "PUT":
		err = c.saveFile(fullpath, r.Body)
		r.Body.Close()
	case "DELETE":
		if ok, _ := strconv.ParseBool(r.URL.Query().Get("recursive")); ok {
			err = os.RemoveAll(fullpath)
		} else {
			err = os.Remove(fullpath)
		}
		if os.IsNotExist(err) {
			err = nil
		}
	}
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

func (c *restfs) saveFile(fullpath string, r io.Reader) error {
	dir, _ := path.Split(fullpath)
	if err := os.MkdirAll(dir, 0777); err != nil {
		return err
	}
	f, err := os.OpenFile(fullpath, os.O_CREATE|os.O_RDWR, 0666)
	if err != nil {
		return err
	}
	if _, err := io.Copy(f, r); err != nil {
		return err
	}
	return nil
}

func main() {
	flag.Parse()
	h := webutil.Logger(&restfs{*dataDir}, webutil.ConsoleLogWriter(os.Stdout))
	h = webutil.Recoverer(h, os.Stderr)
	graceful.Run(*listen, *gracefulTimeout, h)
}
