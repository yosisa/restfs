package main

import (
	"flag"
	"fmt"
	"io"
	"io/ioutil"
	"log"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/tylerb/graceful"
	"github.com/yosisa/sigm"
	"github.com/yosisa/webutil"
)

var (
	dataDir         = flag.String("data-dir", "./data", "Data directory")
	listen          = flag.String("listen", ":8000", "Listen address")
	gracefulTimeout = flag.Duration("graceful-timeout", 10*time.Second, "Wait until force shutdown")
	gcInterval      = flag.Duration("gc-interval", time.Hour, "GC interval for cleaning deleted files")
	accessLog       = flag.String("access-log", "-", "Path to access log file")
)

var (
	accessLogWriter = new(webutil.ConsoleLogWriter)
	middlewares     []*middleware
)

const tombstone = ".restfs-deleted"

type middleware struct {
	priority int
	wrap     func(h http.Handler) http.Handler
}

type byPriority []*middleware

func (x byPriority) Len() int           { return len(x) }
func (x byPriority) Swap(i, j int)      { x[i], x[j] = x[j], x[i] }
func (x byPriority) Less(i, j int) bool { return x[i].priority < x[j].priority }

func registerMiddleware(priority int, wrap func(http.Handler) http.Handler) {
	middlewares = append(middlewares, &middleware{priority: priority, wrap: wrap})
}

type restfs struct {
	dir string
}

func (c *restfs) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	fullpath := path.Join(c.dir, r.URL.Path)
	var (
		fi  os.FileInfo
		err error
	)
	switch r.Method {
	case "GET":
		s := stat(fullpath)
		if s == nil {
			http.Error(w, http.StatusText(http.StatusNotFound), http.StatusNotFound)
			return
		}
		if s.IsDir() {
			serveFileList(w, fullpath)
		} else {
			http.ServeFile(w, r, fullpath)
		}
		return
	case "PUT":
		fi, err = os.Stat(fullpath)
		if fi.IsDir() {
			http.Error(w, "Cannot overwrite directory", http.StatusBadRequest)
			return
		}
		err = c.saveFile(fullpath, r.Body)
		r.Body.Close()
	case "DELETE":
		fi, err = os.Stat(fullpath)
		if os.IsNotExist(err) {
			return
		}
		if fi.IsDir() {
			recursive, _ := strconv.ParseBool(r.URL.Query().Get("recursive"))
			if recursive {
				err = c.removeAll(fullpath)
			} else {
				http.Error(w, "Cannot remove directory; forgot recursive=true?", http.StatusBadRequest)
				return
			}
		} else {
			err = c.remove(fullpath)
		}
	default:
		http.Error(w, http.StatusText(http.StatusMethodNotAllowed), http.StatusMethodNotAllowed)
		return
	}
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusOK)
}

func (c *restfs) saveFile(fullpath string, r io.Reader) error {
	dir, _ := path.Split(fullpath)
	if err := os.MkdirAll(dir, 0777); err != nil {
		return err
	}
	f, err := os.OpenFile(fullpath, os.O_CREATE|os.O_RDWR|os.O_TRUNC, 0666)
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

type gc struct {
	dir    string
	invoke chan struct{}
}

func newGC(dir string) *gc {
	g := &gc{
		dir:    dir,
		invoke: make(chan struct{}, 1),
	}
	go g.loop()
	return g
}

func (g *gc) loop() {
	remove := func(s string) error {
		log.Printf("Remove %s", s)
		if err := os.Remove(s); err != nil && !os.IsNotExist(err) {
			return err
		}
		return nil
	}
	for range g.invoke {
		log.Print("GC started")
		start := time.Now()
		err := filepath.Walk(g.dir, func(name string, stat os.FileInfo, err error) error {
			if err != nil {
				return err
			}
			if stat.IsDir() || !strings.HasSuffix(name, tombstone) {
				return nil
			}
			fname := name[:len(name)-len(tombstone)]
			fstat, err := os.Stat(fname)
			if err == nil {
				if !fstat.ModTime().After(stat.ModTime()) {
					if err = remove(fname); err != nil {
						return err
					}
				}
				return remove(name)
			} else if os.IsNotExist(err) {
				return remove(name)
			}
			return err
		})
		took := time.Since(start)
		if err == nil {
			log.Printf("GC has finished in %v", took)
		} else {
			log.Printf("GC has aborted in %v with error: %v", took, err)
		}
	}
}

func (g *gc) Start() {
	select {
	case g.invoke <- struct{}{}:
	default:
	}
}

func stat(fullpath string) os.FileInfo {
	astat, err := os.Stat(fullpath)
	if err != nil {
		return nil
	}
	if astat.IsDir() {
		return astat
	}

	bstat, err := os.Stat(fullpath + tombstone)
	if err != nil {
		if os.IsNotExist(err) {
			return astat
		}
		log.Print(err)
		return nil
	}
	if astat.ModTime().After(bstat.ModTime()) {
		return astat
	}
	return nil
}

func serveFileList(w http.ResponseWriter, s string) {
	fis, err := ioutil.ReadDir(s)
	if err != nil {
		log.Print(err)
		http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
		return
	}

	tombstones := make(map[string]os.FileInfo)
	for _, fi := range fis {
		name := fi.Name()
		if strings.HasSuffix(name, tombstone) {
			name = name[:len(name)-len(tombstone)]
			tombstones[name] = fi
		}
	}

	for _, fi := range fis {
		name := fi.Name()
		if strings.HasSuffix(name, tombstone) {
			continue
		}
		if fi.IsDir() {
			name += "/"
		} else if ts := tombstones[name]; ts != nil && !fi.ModTime().After(ts.ModTime()) {
			continue
		}
		fmt.Fprintf(w, "%s\n", name)
	}
}

func openAccessLog() {
	if *accessLog == "-" {
		accessLogWriter.Swap(os.Stdout)
		return
	}
	f, err := os.OpenFile(*accessLog, os.O_CREATE|os.O_RDWR|os.O_APPEND, 0666)
	if err != nil {
		log.Print(err)
		return
	}
	if old := accessLogWriter.Swap(f); old != nil {
		if ic, ok := old.(io.Closer); ok {
			ic.Close()
		}
		log.Print("Reopen access log file")
	}
}

func main() {
	flag.Parse()

	log.Printf("Data directory: %s", *dataDir)
	var h http.Handler = &restfs{*dataDir}

	sort.Sort(sort.Reverse(byPriority(middlewares)))
	for _, m := range middlewares {
		h = m.wrap(h)
	}

	openAccessLog()
	h = webutil.Logger(h, accessLogWriter)
	sigm.Handle(syscall.SIGHUP, openAccessLog)

	g := newGC(*dataDir)
	g.Start()
	sigm.Handle(syscall.SIGUSR1, g.Start)
	if *gcInterval > 0 {
		log.Printf("GC runs every %s", *gcInterval)
		go func() {
			for range time.Tick(*gcInterval) {
				g.Start()
			}
		}()
	}

	log.Printf("Server started at %s", *listen)
	graceful.Run(*listen, *gracefulTimeout, webutil.Recoverer(h, os.Stderr))
}
