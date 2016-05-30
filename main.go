package main

import (
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/tylerb/graceful"
	"github.com/yosisa/webutil"
)

var (
	dataDir         = flag.String("data-dir", "./data", "Data directory")
	listen          = flag.String("listen", ":8000", "Listen address")
	gracefulTimeout = flag.Duration("graceful-timeout", 10*time.Second, "Wait until force shutdown")
)

var tombstone = ".restfs-deleted"

type restfs struct {
	dir string
}

func (c *restfs) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	fullpath := path.Join(c.dir, r.URL.Path)
	var err error
	switch r.Method {
	case "GET":
		if !fileAvailable(fullpath) {
			http.Error(w, http.StatusText(http.StatusNotFound), http.StatusNotFound)
			return
		}
		http.ServeFile(w, r, fullpath)
	case "PUT":
		err = c.saveFile(fullpath, r.Body)
		r.Body.Close()
	case "DELETE":
		var stat os.FileInfo
		stat, err = os.Stat(fullpath)
		if os.IsNotExist(err) {
			return
		}
		if stat.IsDir() {
			recursive, _ := strconv.ParseBool(r.URL.Query().Get("recursive"))
			if recursive {
				err = c.removeAll(fullpath)
			} else {
				w.WriteHeader(http.StatusBadRequest)
				fmt.Fprintf(w, "Cannot remove directory; forgot recursive=true?\n")
				return
			}
		} else {
			err = c.remove(fullpath)
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

func (c *restfs) remove(fullpath string) error {
	f, err := os.Create(fullpath + tombstone)
	if err == nil {
		f.Close()
	}
	return err
}

func (c *restfs) removeAll(fullpath string) error {
	return filepath.Walk(fullpath, func(name string, stat os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if stat.IsDir() || strings.HasSuffix(name, tombstone) {
			return nil
		}
		return c.remove(name)
	})
}

func fileAvailable(fullpath string) bool {
	astat, err := os.Stat(fullpath)
	if err != nil {
		return false
	}
	bstat, err := os.Stat(fullpath + tombstone)
	if err != nil {
		return os.IsNotExist(err)
	}
	return astat.ModTime().After(bstat.ModTime())
}

func main() {
	flag.Parse()
	h := webutil.Logger(&restfs{*dataDir}, webutil.ConsoleLogWriter(os.Stdout))
	h = webutil.Recoverer(h, os.Stderr)
	graceful.Run(*listen, *gracefulTimeout, h)
}
