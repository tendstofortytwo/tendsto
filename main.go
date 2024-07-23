package main

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"strings"

	"tailscale.com/tsnet"
	"tailscale.com/util/must"

	_ "github.com/mattn/go-sqlite3"
)

var errNotFound = errors.New("not found")

const (
	// what public / redirects to
	rootURL = "https://github.com/tendstofortytwo/tendsto"
)

type server struct {
	// fatal errors only
	err chan error

	db *sql.DB
}

func newServer() *server {
	db := must.Get(sql.Open("sqlite3", "./urls.db"))
	must.Get(db.Exec(`create table if not exists urls (
		shortcode string primary key,
		url string not null
	)`))

	return &server{
		db:  db,
		err: make(chan error),
	}
}

func (s *server) get(ctx context.Context, shortcode string) (string, error) {
	row, err := s.db.QueryContext(ctx, "select url from urls where shortcode=?", shortcode)
	if err != nil {
		return "", err
	}
	if !row.Next() {
		return "", errNotFound
	}
	var url string
	if err := row.Scan(&url); err != nil {
		return "", err
	}
	return url, nil
}

func (s *server) set(ctx context.Context, shortcode, url string) error {
	_, err := s.db.ExecContext(ctx, "insert into urls(shortcode, url) values(?, ?)", shortcode, url)
	return err
}

func (s *server) serveTS(w http.ResponseWriter, r *http.Request) {
	log.Printf("ts-srv: %s %s", r.Method, r.URL.Path)

	if r.URL.Path != "/" {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}

	switch r.Method {
	case "GET":
		http.ServeFile(w, r, "add.html")
	case "POST":
		shortcode := r.FormValue("shortcode")
		url := r.FormValue("url")
		if shortcode == "" || url == "" {
			http.Error(w, "missing parameter", http.StatusBadRequest)
			return
		}
		err := s.set(r.Context(), shortcode, url)
		if err != nil {
			http.Error(w, fmt.Sprintf("could not set /%s -> %s: %v", shortcode, url, err), http.StatusInternalServerError)
			log.Printf("ts-srv: ERROR %v", err)
			return
		}
		http.Redirect(w, r, "/", http.StatusSeeOther)
	default:
		http.Error(w, "bad method", http.StatusMethodNotAllowed)
	}
}

func (s *server) listenTS() {
	ts := &tsnet.Server{
		Hostname: "tendsto",
	}
	ln := must.Get(ts.ListenTLS("tcp", ":443"))
	s.err <- http.Serve(ln, http.HandlerFunc(s.serveTS))
}

func (s *server) servePublic(w http.ResponseWriter, r *http.Request) {
	log.Printf("pubsrv: %s %s", r.Method, r.URL.Path)

	path := strings.Trim(r.URL.Path, "/")
	if path == "" {
		http.Redirect(w, r, rootURL, http.StatusFound)
		return
	}

	url, err := s.get(r.Context(), path)

	if errors.Is(err, errNotFound) {
		http.Error(w, "not found", http.StatusNotFound)
		return
	} else if err != nil {
		http.Error(w, "oops", http.StatusInternalServerError)
		log.Printf("pubsrv: ERROR %v", err)
		return
	}

	http.Redirect(w, r, url, http.StatusFound)
}

func (s *server) listenPublic() {
	ln := must.Get(net.Listen("tcp", ":4242"))
	s.err <- http.Serve(ln, http.HandlerFunc(s.servePublic))
}

func (s *server) listen() {
	go s.listenTS()
	go s.listenPublic()

	log.Fatal(<-s.err)
}

func main() {
	srv := newServer()
	srv.listen()
}
