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
	addr := flag.String("addr", envOr("LABDOC_ADDR", ":5380"), "listen address")
	dbPath := flag.String("db", envOr("LABDOC_DB", "homelab.db"), "SQLite database path")
	backup := flag.String("backup", "", "write a consistent copy of the database to this path and exit")
	showVersion := flag.Bool("version", false, "print version and exit")
	smtpHost := flag.String("smtp-host", envOr("LABDOC_SMTP_HOST", ""), "SMTP server for outgoing email (blank disables email verification / password reset)")
	smtpPort := flag.String("smtp-port", envOr("LABDOC_SMTP_PORT", "587"), "SMTP server port (587/STARTTLS; implicit-TLS 465 is not supported)")
	smtpUsername := flag.String("smtp-username", envOr("LABDOC_SMTP_USERNAME", ""), "SMTP username")
	smtpPassword := flag.String("smtp-password", envOr("LABDOC_SMTP_PASSWORD", ""), "SMTP password")
	smtpFrom := flag.String("smtp-from", envOr("LABDOC_SMTP_FROM", ""), "From address for outgoing email")
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
	srv.SetMail(web.MailConfig{
		Host: *smtpHost, Port: *smtpPort, Username: *smtpUsername, Password: *smtpPassword, From: *smtpFrom,
	})

	log.Printf("listening on %s (db: %s)", *addr, *dbPath)
	log.Fatal(http.ListenAndServe(*addr, srv.Routes()))
}

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}
