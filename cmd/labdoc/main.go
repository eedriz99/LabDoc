package main

import (
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"

	"labdoc/internal/db"
	"labdoc/internal/version"
	"labdoc/internal/web"
)

func main() {
	addr := flag.String("addr", envOr("LABDOC_ADDR", ":8080"), "listen address")
	dbPath := flag.String("db", envOr("LABDOC_DB", "homelab.db"), "SQLite database path")
	backup := flag.String("backup", "", "write a consistent copy of the database to this path and exit")
	showVersion := flag.Bool("version", false, "print version and exit")
	flag.Parse()

	if *showVersion {
		fmt.Println("labdoc", version.String())
		return
	}

	d, err := db.Open(*dbPath)
	if err != nil {
		log.Fatalf("open db: %v", err)
	}
	defer func() { _ = d.Close() }()

	if *backup != "" {
		if err := db.Backup(d, *backup); err != nil {
			log.Fatalf("backup: %v", err)
		}
		fmt.Println("backup written to", *backup)
		return
	}

	srv, err := web.New(d)
	if err != nil {
		log.Fatalf("init web: %v", err)
	}

	log.Printf("listening on %s (db: %s)", *addr, *dbPath)
	log.Fatal(http.ListenAndServe(*addr, srv.Routes()))
}

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}
